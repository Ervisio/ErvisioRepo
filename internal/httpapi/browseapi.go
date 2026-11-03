package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// RepoSearcher searches the repositories visible to a caller (satisfied
// by *repos.Service). The Repos dependency serves it when it implements it.
type RepoSearcher interface {
	Search(ctx context.Context, who auth.Principal, query string, offset, limit int) ([]repos.Repo, error)
}

var _ RepoSearcher = (*repos.Service)(nil)

var errBrowseUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "code browsing is not available"}

// retryHydrating is the Retry-After of a repository being restored or busy.
const retryHydrating = 30

// browseFail maps a repository or code browsing error to a response.
func browseFail(err error, method, suffix string) (browseResponse, error) {
	switch {
	case errors.Is(err, gitbrowse.ErrNotFound), errors.Is(err, gitbrowse.ErrEmpty), errors.Is(err, gitstore.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, gitbrowse.ErrInvalid), errors.Is(err, git.ErrInvalid):
		return badRequest(err.Error()), nil
	case errors.Is(err, gitbrowse.ErrConflict), errors.Is(err, gitbrowse.ErrQuota):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, gitbrowse.ErrTooLarge):
		return problemResponse{status: http.StatusRequestEntityTooLarge, detail: err.Error()}, nil
	case errors.Is(err, gitstore.ErrBusy), errors.Is(err, gitstore.ErrCold):
		return problemResponse{status: http.StatusServiceUnavailable, detail: "repository is being prepared, retry later",
			retryAfter: retryHydrating}, nil
	case errors.Is(err, gitbrowse.ErrUnavailable), errors.Is(err, gitstore.ErrDegraded), errors.Is(err, gitstore.ErrHotMissing):
		return problemResponse{status: http.StatusServiceUnavailable, detail: "repository is temporarily unavailable"}, nil
	}
	r, err2 := reposFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(browseResponse), nil
}

// browseResponse is answered by problemResponse and movedResponse.
type browseResponse interface {
	v1gen.ResolveRefResponseObject
	v1gen.GetTreeResponseObject
	v1gen.GetBlobResponseObject
	v1gen.GetReadmeResponseObject
	v1gen.GetBlameResponseObject
	v1gen.ListCommitsResponseObject
	v1gen.GetCommitResponseObject
	v1gen.ListBranchesResponseObject
	v1gen.ListTagsResponseObject
	v1gen.CompareRefsResponseObject
	v1gen.SearchCodeResponseObject
	v1gen.EditContentsResponseObject
	v1gen.SearchReposResponseObject
}

// browseRepo loads the repository for the caller; a failure is already
// mapped to a response.
func (s *v1Server) browseRepo(ctx context.Context, owner, name, method, suffix string) (repos.Repo, browseResponse, error) {
	if s.repos == nil || s.browse == nil {
		return repos.Repo{}, errBrowseUnavailable, nil
	}
	r, err := s.repos.Get(ctx, principal(ctx), owner, name)
	if err != nil {
		resp, err := browseFail(err, method, suffix)
		return repos.Repo{}, resp, err
	}
	return r, nil, nil
}

func gitRepo(r repos.Repo) gitbrowse.Repo {
	return gitbrowse.Repo{ID: r.ID, OwnerID: r.OwnerID, Name: r.Name, DefaultBranch: r.DefaultBranch}
}

// withQuery appends the raw query of the request to a redirect suffix.
func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

func refQuery(ref *string, extra url.Values) url.Values {
	q := url.Values{}
	if ref != nil && *ref != "" {
		q.Set("ref", *ref)
	}
	for k, v := range extra {
		for _, x := range v {
			if x != "" {
				q.Add(k, x)
			}
		}
	}
	return q
}

func (s *v1Server) ResolveRef(ctx context.Context, req v1gen.ResolveRefRequestObject) (v1gen.ResolveRefResponseObject, error) {
	suffix := withQuery("/refs/resolve", refQuery(req.Params.Ref, nil))
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	r, err := s.browse.Resolve(ctx, gitRepo(repo), deref(req.Params.Ref))
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	return v1gen.ResolveRef200JSONResponse(resolvedDTO(r)), nil
}

func (s *v1Server) GetTree(ctx context.Context, req v1gen.GetTreeRequestObject) (v1gen.GetTreeResponseObject, error) {
	suffix := withQuery("/tree", refQuery(req.Params.Ref, url.Values{"path": {deref(req.Params.Path)}}))
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	t, err := s.browse.Tree(ctx, gitRepo(repo), deref(req.Params.Ref), deref(req.Params.Path), deref(req.Params.LastCommits))
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	out := v1gen.Tree{Ref: resolvedDTO(t.Ref), Path: t.Path, Truncated: t.Truncated, Entries: make([]v1gen.TreeEntry, 0, len(t.Entries))}
	if t.Readme != "" {
		out.Readme = &t.Readme
	}
	for _, e := range t.Entries {
		te := v1gen.TreeEntry{Name: e.Name, Path: e.Path, Type: v1gen.TreeEntryType(e.Type), Mode: e.Mode, Oid: e.OID, Size: e.Size}
		if e.LastCommit != nil {
			cs := summaryDTO(*e.LastCommit)
			te.LastCommit = &cs
		}
		out.Entries = append(out.Entries, te)
	}
	return v1gen.GetTree200JSONResponse(out), nil
}

func (s *v1Server) GetBlob(ctx context.Context, req v1gen.GetBlobRequestObject) (v1gen.GetBlobResponseObject, error) {
	suffix := withQuery("/blob", refQuery(req.Params.Ref, url.Values{"path": {req.Params.Path}}))
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	b, err := s.browse.Blob(ctx, gitRepo(repo), deref(req.Params.Ref), req.Params.Path)
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	return v1gen.GetBlob200JSONResponse(blobDTO(b)), nil
}

func (s *v1Server) GetReadme(ctx context.Context, req v1gen.GetReadmeRequestObject) (v1gen.GetReadmeResponseObject, error) {
	suffix := withQuery("/readme", refQuery(req.Params.Ref, url.Values{"path": {deref(req.Params.Path)}}))
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	b, err := s.browse.Readme(ctx, gitRepo(repo), deref(req.Params.Ref), deref(req.Params.Path))
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	return v1gen.GetReadme200JSONResponse(blobDTO(b)), nil
}

func (s *v1Server) GetBlame(ctx context.Context, req v1gen.GetBlameRequestObject) (v1gen.GetBlameResponseObject, error) {
	suffix := withQuery("/blame", refQuery(req.Params.Ref, url.Values{"path": {req.Params.Path}}))
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	b, err := s.browse.Blame(ctx, gitRepo(repo), deref(req.Params.Ref), req.Params.Path)
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	out := v1gen.Blame{Ref: resolvedDTO(b.Ref), Path: b.Path, Lines: b.Lines, Ranges: make([]v1gen.BlameRange, 0, len(b.Ranges))}
	if out.Lines == nil {
		out.Lines = []string{}
	}
	for _, r := range b.Ranges {
		out.Ranges = append(out.Ranges, v1gen.BlameRange{Commit: summaryDTO(r.Commit), StartLine: r.StartLine, Lines: r.Lines})
	}
	return v1gen.GetBlame200JSONResponse(out), nil
}

func (s *v1Server) ListCommits(ctx context.Context, req v1gen.ListCommitsRequestObject) (v1gen.ListCommitsResponseObject, error) {
	suffix := withQuery("/commits", refQuery(req.Params.Ref, url.Values{"path": {deref(req.Params.Path)}}))
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	p, err := s.browse.Log(ctx, gitRepo(repo), deref(req.Params.Ref), deref(req.Params.Path), offset, limit)
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	return v1gen.ListCommits200JSONResponse(v1gen.CommitPage{Ref: resolvedDTO(p.Ref), Commits: commitsDTO(p.Commits), HasMore: p.HasMore}), nil
}

func (s *v1Server) GetCommit(ctx context.Context, req v1gen.GetCommitRequestObject) (v1gen.GetCommitResponseObject, error) {
	suffix := "/commits/" + url.PathEscape(req.Sha)
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	c, err := s.browse.Commit(ctx, gitRepo(repo), req.Sha)
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	return v1gen.GetCommit200JSONResponse(v1gen.CommitDetail{Commit: commitDTO(c.Commit), Diff: diffDTO(c.Diff)}), nil
}

func (s *v1Server) ListBranches(ctx context.Context, req v1gen.ListBranchesRequestObject) (v1gen.ListBranchesResponseObject, error) {
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, "/branches")
	if fail != nil || err != nil {
		return fail, err
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, more, err := s.browse.Branches(ctx, gitRepo(repo), offset, limit)
	if err != nil {
		return browseFail(err, http.MethodGet, "/branches")
	}
	out := v1gen.BranchPage{HasMore: more, Items: make([]v1gen.Branch, 0, len(list))}
	for _, b := range list {
		out.Items = append(out.Items, v1gen.Branch{Name: b.Name, Commit: summaryDTO(b.Commit), Default: b.Default,
			Ahead: b.Ahead, Behind: b.Behind})
	}
	return v1gen.ListBranches200JSONResponse(out), nil
}

func (s *v1Server) ListTags(ctx context.Context, req v1gen.ListTagsRequestObject) (v1gen.ListTagsResponseObject, error) {
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, "/tags")
	if fail != nil || err != nil {
		return fail, err
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, more, err := s.browse.Tags(ctx, gitRepo(repo), offset, limit)
	if err != nil {
		return browseFail(err, http.MethodGet, "/tags")
	}
	out := v1gen.TagPage{HasMore: more, Items: make([]v1gen.GitTag, 0, len(list))}
	for _, t := range list {
		tag := v1gen.GitTag{Name: t.Name, Commit: summaryDTO(t.Commit), Annotated: t.Annotated, Message: t.Message}
		if t.Tagger != nil {
			p := personDTO(*t.Tagger)
			tag.Tagger = &p
		}
		out.Items = append(out.Items, tag)
	}
	return v1gen.ListTags200JSONResponse(out), nil
}

func (s *v1Server) CompareRefs(ctx context.Context, req v1gen.CompareRefsRequestObject) (v1gen.CompareRefsResponseObject, error) {
	suffix := withQuery("/compare", url.Values{"base": {req.Params.Base}, "head": {req.Params.Head}})
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	c, err := s.browse.Compare(ctx, gitRepo(repo), req.Params.Base, req.Params.Head)
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	return v1gen.CompareRefs200JSONResponse(v1gen.Comparison{Base: resolvedDTO(c.Base), Head: resolvedDTO(c.Head),
		MergeBase: c.MergeBase, Ahead: c.Ahead, Behind: c.Behind, Commits: commitsDTO(c.Commits),
		CommitsTruncated: c.CommitsTruncated, Diff: diffDTO(c.Diff)}), nil
}

func (s *v1Server) SearchCode(ctx context.Context, req v1gen.SearchCodeRequestObject) (v1gen.SearchCodeResponseObject, error) {
	p := req.Params
	suffix := withQuery("/search/code", refQuery(p.Ref, url.Values{"q": {p.Q}, "path": {deref(p.Path)}}))
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodGet, suffix)
	if fail != nil || err != nil {
		return fail, err
	}
	res, err := s.browse.Grep(ctx, gitRepo(repo), deref(p.Ref), p.Q, gitbrowse.GrepOptions{Regexp: deref(p.Regexp),
		IgnoreCase: deref(p.IgnoreCase), Path: deref(p.Path), Limit: deref(p.Limit)})
	if err != nil {
		return browseFail(err, http.MethodGet, suffix)
	}
	out := v1gen.CodeSearchResult{Ref: resolvedDTO(res.Ref), Truncated: res.Truncated, Matches: make([]v1gen.CodeMatch, 0, len(res.Matches))}
	for _, m := range res.Matches {
		out.Matches = append(out.Matches, v1gen.CodeMatch{Path: m.Path, Line: m.Line, Text: m.Text})
	}
	return v1gen.SearchCode200JSONResponse(out), nil
}

func (s *v1Server) EditContents(ctx context.Context, req v1gen.EditContentsRequestObject) (v1gen.EditContentsResponseObject, error) {
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	repo, fail, err := s.browseRepo(ctx, req.Owner, req.Name, http.MethodPost, "/contents")
	if fail != nil || err != nil {
		return fail, err
	}
	who := principal(ctx)
	if !authz.Allowed(repo.Permission, authz.ActPush) {
		return problemResponse{status: http.StatusForbidden, detail: "push permission is required"}, nil
	}
	b := req.Body
	var content []byte
	if b.Content != nil {
		if deref(b.Encoding) == v1gen.Base64 {
			if content, err = base64.StdEncoding.DecodeString(*b.Content); err != nil {
				return badRequest("content is not valid base64"), nil
			}
		} else {
			content = []byte(*b.Content)
		}
	}
	res, err := s.browse.Edit(ctx, gitRepo(repo), gitbrowse.Edit{Op: string(b.Op), Path: b.Path, Content: content,
		Branch: deref(b.Branch), NewBranch: deref(b.NewBranch), ExpectedOID: deref(b.Sha), Message: deref(b.Message),
		AuthorID: who.UserID})
	if err != nil {
		return browseFail(err, http.MethodPost, "/contents")
	}
	return v1gen.EditContents201JSONResponse(v1gen.FileEditResult{Branch: res.Branch, Commit: commitDTO(res.Commit)}), nil
}

func (s *v1Server) SearchRepos(ctx context.Context, req v1gen.SearchReposRequestObject) (v1gen.SearchReposResponseObject, error) {
	rs, ok := s.repos.(RepoSearcher)
	if !ok {
		return errReposUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := rs.Search(ctx, principal(ctx), req.Params.Q, offset, limit)
	if err != nil {
		return browseFail(err, http.MethodGet, "")
	}
	return v1gen.SearchRepos200JSONResponse(repoList(list)), nil
}

// ---- DTOs ----

func resolvedDTO(r gitbrowse.Resolved) v1gen.ResolvedRef {
	return v1gen.ResolvedRef{Ref: r.Ref, Kind: v1gen.ResolvedRefKind(r.Kind), Name: r.Name, Commit: r.Commit}
}

func personDTO(p gitbrowse.Person) v1gen.GitPerson {
	return v1gen.GitPerson{Name: p.Name, Email: p.Email, Date: p.Date}
}

func summaryDTO(c gitbrowse.CommitSummary) v1gen.GitCommitSummary {
	return v1gen.GitCommitSummary{Sha: c.SHA, Summary: c.Summary, Author: c.Author, Date: c.Date}
}

func commitDTO(c gitbrowse.Commit) v1gen.GitCommit {
	parents := c.Parents
	if parents == nil {
		parents = []string{}
	}
	return v1gen.GitCommit{Sha: c.SHA, Parents: parents, Author: personDTO(c.Author), Committer: personDTO(c.Committer),
		Summary: c.Summary, Message: c.Message}
}

func commitsDTO(list []gitbrowse.Commit) []v1gen.GitCommit {
	out := make([]v1gen.GitCommit, 0, len(list))
	for _, c := range list {
		out = append(out, commitDTO(c))
	}
	return out
}

func blobDTO(b gitbrowse.Blob) v1gen.BlobFile {
	out := v1gen.BlobFile{Ref: resolvedDTO(b.Ref), Path: b.Path, Oid: b.OID, Mode: b.Mode, Size: b.Size, Binary: b.Binary,
		Truncated: b.Truncated, ContentType: b.ContentType}
	if b.Content != nil {
		c := string(b.Content)
		out.Content = &c
	}
	if b.LFS != nil {
		out.Lfs = &v1gen.LfsPointer{Oid: b.LFS.OID, Size: b.LFS.Size}
	}
	return out
}

func diffDTO(d gitbrowse.Diff) v1gen.Diff {
	out := v1gen.Diff{Additions: d.Additions, Deletions: d.Deletions, Truncated: d.Truncated, Files: make([]v1gen.FileDiff, 0, len(d.Files))}
	for _, f := range d.Files {
		fd := v1gen.FileDiff{Status: v1gen.FileDiffStatus(f.Status), OldPath: f.OldPath, NewPath: f.NewPath,
			OldMode: f.OldMode, NewMode: f.NewMode, OldOid: f.OldOID, NewOid: f.NewOID, Similarity: f.Similarity,
			Binary: f.Binary, Additions: f.Additions, Deletions: f.Deletions, Truncated: f.Truncated,
			Hunks: make([]v1gen.DiffHunk, 0, len(f.Hunks))}
		for _, h := range f.Hunks {
			dh := v1gen.DiffHunk{Header: h.Header, OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart,
				NewLines: h.NewLines, Lines: make([]v1gen.DiffLine, 0, len(h.Lines))}
			for _, l := range h.Lines {
				dh.Lines = append(dh.Lines, v1gen.DiffLine{Type: v1gen.DiffLineType(l.Type), OldLine: l.OldLine, NewLine: l.NewLine, Text: l.Text})
			}
			fd.Hunks = append(fd.Hunks, dh)
		}
		out.Files = append(out.Files, fd)
	}
	return out
}

// ---- streaming routes (outside OpenAPI and the request timeout) ----

const (
	rawPath     = "/api/v1/repos/{owner}/{name}/raw"
	archivePath = "/api/v1/repos/{owner}/{name}/archive"
)

// mountBrowse mounts the raw file (?ref=&path=) and archive
// (?ref=&format=zip|tar.gz) downloads. They stream, so they sit outside the
// request timeout and the generated router.
func mountBrowse(r chi.Router, d Deps) {
	r.With(d.Auth.LoadSession).Get(rawPath, func(w http.ResponseWriter, req *http.Request) {
		serveRaw(w, req, d)
	})
	r.With(d.Auth.LoadSession).Get(archivePath, func(w http.ResponseWriter, req *http.Request) {
		serveArchive(w, req, d)
	})
}

// browseStreamRepo loads the repository of a streaming route; ok is false
// when the answer was already written.
func browseStreamRepo(w http.ResponseWriter, req *http.Request, d Deps, suffix string) (gitbrowse.Repo, bool) {
	if d.Repos == nil || d.Browse == nil {
		writeProblem(w, http.StatusServiceUnavailable, "code browsing is not available")
		return gitbrowse.Repo{}, false
	}
	p, _ := auth.PrincipalFrom(req.Context())
	if p.Method == auth.MethodToken && !p.HasScope("repo:read") {
		writeProblem(w, http.StatusForbidden, "the token lacks the repo:read scope")
		return gitbrowse.Repo{}, false
	}
	repo, err := d.Repos.Get(req.Context(), p, chi.URLParam(req, "owner"), chi.URLParam(req, "name"))
	if err != nil {
		browseStreamFail(w, req, d, err, suffix)
		return gitbrowse.Repo{}, false
	}
	return gitRepo(repo), true
}

// browseStreamFail answers an error of a streaming route before the
// response started; after it, the connection is just cut.
func browseStreamFail(w http.ResponseWriter, req *http.Request, d Deps, err error, suffix string) {
	if gitbrowse.IsStreaming(err) {
		if !errors.Is(err, context.Canceled) {
			d.Log.WarnContext(req.Context(), "code download interrupted", "error", err)
		}
		return
	}
	resp, ferr := browseFail(err, req.Method, suffix)
	if ferr != nil {
		d.Log.ErrorContext(req.Context(), "code download failed", "error", ferr)
		writeProblem(w, http.StatusInternalServerError, "")
		return
	}
	switch v := resp.(type) {
	case problemResponse:
		_ = v.write(w)
	case movedResponse:
		_ = v.write(w)
	default:
		writeProblem(w, http.StatusInternalServerError, "")
	}
}

func serveRaw(w http.ResponseWriter, req *http.Request, d Deps) {
	q := req.URL.Query()
	suffix := withQuery("/raw", url.Values{"ref": {q.Get("ref")}, "path": {q.Get("path")}})
	repo, ok := browseStreamRepo(w, req, d, suffix)
	if !ok {
		return
	}
	err := d.Browse.Raw(req.Context(), repo, q.Get("ref"), q.Get("path"), func(b gitbrowse.Blob) (io.Writer, error) {
		h := w.Header()
		h.Set("Content-Type", b.ContentType)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		h.Set("Cache-Control", "private, no-cache")
		h.Set("ETag", `"`+b.OID+`"`)
		disposition := "attachment"
		if !b.Binary || strings.HasPrefix(b.ContentType, "image/") {
			disposition = "inline"
		}
		name := b.Path[strings.LastIndexByte(b.Path, '/')+1:]
		h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
		if match := req.Header.Get("If-None-Match"); match != "" && match == `"`+b.OID+`"` {
			w.WriteHeader(http.StatusNotModified)
			return io.Discard, nil
		}
		w.WriteHeader(http.StatusOK)
		return w, nil
	})
	if err != nil {
		browseStreamFail(w, req, d, err, suffix)
	}
}

func serveArchive(w http.ResponseWriter, req *http.Request, d Deps) {
	q := req.URL.Query()
	format := q.Get("format")
	if format == "" {
		format = gitbrowse.FormatZip
	}
	suffix := withQuery("/archive", url.Values{"ref": {q.Get("ref")}, "format": {q.Get("format")}})
	repo, ok := browseStreamRepo(w, req, d, suffix)
	if !ok {
		return
	}
	err := d.Browse.Archive(req.Context(), repo, q.Get("ref"), format, func(_ gitbrowse.Resolved, filename string) (io.Writer, error) {
		h := w.Header()
		ct := "application/zip"
		if format == gitbrowse.FormatTarGz {
			ct = "application/gzip"
		}
		h.Set("Content-Type", ct)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "private, no-cache")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.WriteHeader(http.StatusOK)
		return w, nil
	})
	if err != nil {
		browseStreamFail(w, req, d, err, suffix)
	}
}

// Visit methods let problemResponse and movedResponse answer every code
// browsing operation.
func (p problemResponse) VisitResolveRefResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitGetTreeResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitGetBlobResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitGetReadmeResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitGetBlameResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitListCommitsResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitGetCommitResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitListBranchesResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListTagsResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitCompareRefsResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitSearchCodeResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitEditContentsResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitSearchReposResponse(w http.ResponseWriter) error  { return p.write(w) }

func (m movedResponse) VisitResolveRefResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitGetTreeResponse(w http.ResponseWriter) error      { return m.write(w) }
func (m movedResponse) VisitGetBlobResponse(w http.ResponseWriter) error      { return m.write(w) }
func (m movedResponse) VisitGetReadmeResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitGetBlameResponse(w http.ResponseWriter) error     { return m.write(w) }
func (m movedResponse) VisitListCommitsResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitGetCommitResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitListBranchesResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitListTagsResponse(w http.ResponseWriter) error     { return m.write(w) }
func (m movedResponse) VisitCompareRefsResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitSearchCodeResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitEditContentsResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitSearchReposResponse(w http.ResponseWriter) error  { return m.write(w) }
