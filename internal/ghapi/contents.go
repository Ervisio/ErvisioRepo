package ghapi

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// ---- URL helpers ----

func (c *call) blobHTML(ref, p string) string {
	return c.repoHTML() + "/blob?" + url.Values{"ref": {ref}, "path": {p}}.Encode()
}

func (c *call) treeHTML(ref, p string) string {
	return c.repoHTML() + "?" + url.Values{"ref": {ref}, "path": {p}}.Encode()
}

func (c *call) rawURL(ref, p string) string {
	return c.s.base + "/api/v1/repos/" + esc(c.owner) + "/" + esc(c.repo) + "/raw?" +
		url.Values{"ref": {ref}, "path": {p}}.Encode()
}

func (c *call) contentsURL(ref, p string) string {
	u := c.repoAPI() + "/contents/" + escPath(p)
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	return u
}

func (c *call) commitAPI(sha string) string  { return c.repoAPI() + "/commits/" + sha }
func (c *call) commitHTML(sha string) string { return c.repoHTML() + "/commit/" + sha }

// ---- contents ----

type ghLinks struct {
	Self string `json:"self"`
	Git  string `json:"git"`
	HTML string `json:"html"`
}

type ghContent struct {
	Type        string   `json:"type"`
	Encoding    *string  `json:"encoding,omitempty"`
	Size        int64    `json:"size"`
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Content     *string  `json:"content,omitempty"`
	Target      *string  `json:"target,omitempty"`
	SHA         string   `json:"sha"`
	URL         string   `json:"url"`
	GitURL      *string  `json:"git_url"`
	HTMLURL     *string  `json:"html_url"`
	DownloadURL *string  `json:"download_url"`
	Links       *ghLinks `json:"_links"`
}

func (c *call) contentEntry(ref, typ, p, oid string, size int64) ghContent {
	gitURL := c.repoAPI() + "/git/blobs/" + oid
	html := c.blobHTML(ref, p)
	if typ == "dir" {
		gitURL = c.repoAPI() + "/git/trees/" + oid
		html = c.treeHTML(ref, p)
	}
	self := c.contentsURL(ref, p)
	e := ghContent{Type: typ, Size: max(size, 0), Name: path.Base(p), Path: p, SHA: oid, URL: self,
		GitURL: &gitURL, HTMLURL: &html, Links: &ghLinks{Self: self, Git: gitURL, HTML: html}}
	if typ == "file" {
		d := c.rawURL(ref, p)
		e.DownloadURL = &d
	}
	if typ == "submodule" {
		e.GitURL, e.HTMLURL, e.Links.Git, e.Links.HTML = nil, nil, "", ""
	}
	return e
}

// wrap64 is base64 with a line break every 60 characters, as GitHub sends.
func wrap64(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var sb strings.Builder
	for len(s) > 60 {
		sb.WriteString(s[:60])
		sb.WriteByte('\n')
		s = s[60:]
	}
	sb.WriteString(s)
	sb.WriteByte('\n')
	return sb.String()
}

func (c *call) fileContent(ref string, b gitbrowse.Blob) ghContent {
	typ := "file"
	if b.Mode == "120000" {
		typ = "symlink"
	}
	e := c.contentEntry(ref, typ, b.Path, b.OID, b.Size)
	enc, content := "base64", ""
	if b.Content == nil {
		// Binary or above the inline limit: GitHub answers encoding "none"
		// and an empty content; download_url serves the bytes.
		enc = "none"
	} else {
		content = wrap64(b.Content)
	}
	if typ == "symlink" && b.Content != nil {
		t := string(b.Content)
		e.Target = &t
	}
	e.Encoding, e.Content = &enc, &content
	return e
}

func entryType(t string) string {
	switch t {
	case gitbrowse.TypeTree:
		return "dir"
	case gitbrowse.TypeSymlink:
		return "symlink"
	case gitbrowse.TypeSubmodule:
		return "submodule"
	}
	return "file"
}

func (c *call) getContents() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	ref, p := c.query("ref"), c.wildcard()
	repo := browseRepo(r)
	if strings.Contains(c.r.Header.Get("Accept"), ".raw") && p != "" {
		return c.rawContents(repo, ref, p)
	}
	t, err := c.s.d.Browse.Tree(c.ctx, repo, ref, p, false)
	if err == nil {
		out := make([]ghContent, 0, len(t.Entries))
		for _, e := range t.Entries {
			out = append(out, c.contentEntry(ref, entryType(e.Type), e.Path, e.OID, e.Size))
		}
		return c.ok(http.StatusOK, out)
	}
	if !errors.Is(err, gitbrowse.ErrInvalid) || p == "" {
		return err
	}
	b, err := c.s.d.Browse.Blob(c.ctx, repo, ref, p)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.fileContent(ref, b))
}

// rawContents streams a file for the application/vnd.github.raw media type.
func (c *call) rawContents(repo gitbrowse.Repo, ref, p string) error {
	err := c.s.d.Browse.Raw(c.ctx, repo, ref, p, func(b gitbrowse.Blob) (io.Writer, error) {
		h := c.w.Header()
		h.Set("Content-Type", "application/vnd.github.raw")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("ETag", `"`+b.OID+`"`)
		if b.Size >= 0 {
			h.Set("Content-Length", strconv.FormatInt(b.Size, 10))
		}
		c.w.WriteHeader(http.StatusOK)
		return c.w, nil
	})
	if err != nil && gitbrowse.IsStreaming(err) {
		if !errors.Is(err, context.Canceled) {
			c.s.d.Log.WarnContext(c.ctx, "ghapi: raw download interrupted", "error", err)
		}
		return nil
	}
	return err
}

// gitIdentity is the author or committer of a contents write. The server
// always commits as the token owner; the body fields are ignored.
type gitIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type contentsBody struct {
	Message   string       `json:"message"`
	Content   *string      `json:"content"`
	SHA       string       `json:"sha"`
	Branch    string       `json:"branch"`
	Committer *gitIdentity `json:"committer"`
	Author    *gitIdentity `json:"author"`
}

// pushRepo loads the repository for a write to its code.
func (c *call) pushRepo() (repos.Repo, error) {
	r, err := c.loadRepoWrite()
	if err != nil {
		return r, err
	}
	if !authz.Allowed(r.Permission, authz.ActPush) {
		return r, errf(http.StatusForbidden, "Resource not accessible: push permission is required")
	}
	return r, nil
}

func (c *call) putContents() error {
	r, err := c.pushRepo()
	if err != nil {
		return err
	}
	var b contentsBody
	if err := c.decode(&b); err != nil {
		return err
	}
	p := c.wildcard()
	if b.Content == nil {
		return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"content\" wasn't supplied.")
	}
	content, err := base64.StdEncoding.DecodeString(strings.Map(dropSpace, *b.Content))
	if err != nil {
		return errf(http.StatusUnprocessableEntity, "content is not valid Base64")
	}
	op, status := gitbrowse.OpCreate, http.StatusCreated
	if b.SHA != "" {
		op, status = gitbrowse.OpUpdate, http.StatusOK
	}
	repo := browseRepo(r)
	res, err := c.s.d.Browse.Edit(c.ctx, repo, gitbrowse.Edit{Op: op, Path: p, Content: content, Branch: b.Branch,
		ExpectedOID: b.SHA, Message: b.Message, AuthorID: c.who.UserID})
	if err != nil {
		if op == gitbrowse.OpCreate && errors.Is(err, gitbrowse.ErrConflict) {
			return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"sha\" wasn't supplied.")
		}
		return err
	}
	blob, err := c.s.d.Browse.Blob(c.ctx, repo, res.Commit.SHA, p)
	if err != nil {
		return err
	}
	e := c.contentEntry(res.Branch, "file", p, blob.OID, blob.Size)
	return c.ok(status, map[string]any{"content": e, "commit": c.gitCommit(res.Commit)})
}

func dropSpace(r rune) rune {
	if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
		return -1
	}
	return r
}

func (c *call) deleteContents() error {
	r, err := c.pushRepo()
	if err != nil {
		return err
	}
	var b contentsBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.SHA == "" {
		return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"sha\" wasn't supplied.")
	}
	res, err := c.s.d.Browse.Edit(c.ctx, browseRepo(r), gitbrowse.Edit{Op: gitbrowse.OpDelete, Path: c.wildcard(),
		Branch: b.Branch, ExpectedOID: b.SHA, Message: b.Message, AuthorID: c.who.UserID})
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, map[string]any{"content": nil, "commit": c.gitCommit(res.Commit)})
}

// ---- commits ----

type ghPerson struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

func person(p gitbrowse.Person) ghPerson {
	return ghPerson{Name: p.Name, Email: p.Email, Date: ts(p.Date)}
}

type ghParent struct {
	SHA     string `json:"sha"`
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
}

func (c *call) parents(list []string) []ghParent {
	out := make([]ghParent, len(list))
	for i, p := range list {
		out[i] = ghParent{SHA: p, URL: c.commitAPI(p), HTMLURL: c.commitHTML(p)}
	}
	return out
}

type ghVerification struct {
	Verified  bool    `json:"verified"`
	Reason    string  `json:"reason"`
	Signature *string `json:"signature"`
	Payload   *string `json:"payload"`
}

var unsigned = ghVerification{Reason: "unsigned"}

// gitCommit is the git commit object of the contents API.
func (c *call) gitCommit(cm gitbrowse.Commit) map[string]any {
	return map[string]any{
		"sha": cm.SHA, "node_id": nodeIDString("Commit", c.owner+"/"+c.repo+"@"+cm.SHA),
		"url": c.repoAPI() + "/git/commits/" + cm.SHA, "html_url": c.commitHTML(cm.SHA),
		"author": person(cm.Author), "committer": person(cm.Committer), "message": cm.Message,
		"tree": map[string]any{"sha": nil, "url": nil}, "parents": c.parents(cm.Parents),
		"verification": unsigned,
	}
}

// nodeIDString is nodeID for objects without a UUID (commits, refs).
func nodeIDString(kind, key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + key))
}

type ghCommitData struct {
	Author       ghPerson       `json:"author"`
	Committer    ghPerson       `json:"committer"`
	Message      string         `json:"message"`
	Tree         map[string]any `json:"tree"`
	URL          string         `json:"url"`
	CommentCount int            `json:"comment_count"`
	Verification ghVerification `json:"verification"`
}

type ghStats struct {
	Total     int `json:"total"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type ghCommit struct {
	SHA         string       `json:"sha"`
	NodeID      string       `json:"node_id"`
	Commit      ghCommitData `json:"commit"`
	URL         string       `json:"url"`
	HTMLURL     string       `json:"html_url"`
	CommentsURL string       `json:"comments_url"`
	// Author and Committer would be accounts matched by email; the API
	// does not resolve them.
	Author    *ghUser    `json:"author"`
	Committer *ghUser    `json:"committer"`
	Parents   []ghParent `json:"parents"`
	Stats     *ghStats   `json:"stats,omitempty"`
	Files     []ghFile   `json:"files,omitempty"`
}

func (c *call) commitObj(cm gitbrowse.Commit) ghCommit {
	return ghCommit{
		SHA: cm.SHA, NodeID: nodeIDString("Commit", c.owner+"/"+c.repo+"@"+cm.SHA),
		Commit: ghCommitData{Author: person(cm.Author), Committer: person(cm.Committer), Message: cm.Message,
			Tree: map[string]any{"sha": nil, "url": nil}, URL: c.repoAPI() + "/git/commits/" + cm.SHA,
			Verification: unsigned},
		URL: c.commitAPI(cm.SHA), HTMLURL: c.commitHTML(cm.SHA), CommentsURL: c.commitAPI(cm.SHA) + "/comments",
		Parents: c.parents(cm.Parents),
	}
}

type ghFile struct {
	SHA              string  `json:"sha"`
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	Additions        int     `json:"additions"`
	Deletions        int     `json:"deletions"`
	Changes          int     `json:"changes"`
	BlobURL          string  `json:"blob_url"`
	RawURL           string  `json:"raw_url"`
	ContentsURL      string  `json:"contents_url"`
	PreviousFilename *string `json:"previous_filename,omitempty"`
}

func fileStatus(s string) string {
	if s == gitbrowse.StatusDeleted {
		return "removed"
	}
	return s
}

func (c *call) fileObj(ref, sha string, f gitbrowse.FileChange) ghFile {
	name := f.NewPath
	if name == "" {
		name = f.OldPath
	}
	out := ghFile{SHA: sha, Filename: name, Status: fileStatus(f.Status), Additions: f.Additions,
		Deletions: f.Deletions, Changes: f.Additions + f.Deletions, BlobURL: c.blobHTML(ref, name),
		RawURL: c.rawURL(ref, name), ContentsURL: c.contentsURL(ref, name)}
	if (f.Status == gitbrowse.StatusRenamed || f.Status == gitbrowse.StatusCopied) && f.OldPath != "" {
		out.PreviousFilename = &f.OldPath
	}
	return out
}

func (c *call) listCommits() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	p := c.pager()
	page, err := c.s.d.Browse.Log(c.ctx, browseRepo(r), c.query("sha"), c.query("path"), p.offset(), p.per)
	if err != nil {
		return err
	}
	out := make([]ghCommit, len(page.Commits))
	for i, cm := range page.Commits {
		out[i] = c.commitObj(cm)
	}
	c.link(p, page.HasMore)
	return c.ok(http.StatusOK, out)
}

func (c *call) getCommit() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	d, err := c.s.d.Browse.Commit(c.ctx, browseRepo(r), chi.URLParam(c.r, "ref"))
	if err != nil {
		return err
	}
	out := c.commitObj(d.Commit)
	out.Stats = &ghStats{Total: d.Diff.Additions + d.Diff.Deletions, Additions: d.Diff.Additions,
		Deletions: d.Diff.Deletions}
	out.Files = make([]ghFile, 0, len(d.Diff.Files))
	for _, f := range d.Diff.Files {
		sha := f.NewOID
		if sha == "" {
			sha = f.OldOID
		}
		out.Files = append(out.Files, c.fileObj(d.Commit.SHA, sha, gitbrowse.FileChange{Status: f.Status,
			OldPath: f.OldPath, NewPath: f.NewPath, Additions: f.Additions, Deletions: f.Deletions, Binary: f.Binary}))
	}
	return c.ok(http.StatusOK, out)
}

// ---- branches, tags and refs ----

type ghCommitRef struct {
	SHA string `json:"sha"`
	URL string `json:"url"`
}

type ghBranch struct {
	Name      string      `json:"name"`
	Commit    ghCommitRef `json:"commit"`
	Protected bool        `json:"protected"`
}

func (c *call) listBranches() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	p := c.pager()
	list, more, err := c.s.d.Browse.Branches(c.ctx, browseRepo(r), p.offset(), p.per)
	if err != nil {
		return err
	}
	out := make([]ghBranch, len(list))
	for i, b := range list {
		// Protected is not computed: branch protection is read through
		// /api/v1/repos/{owner}/{name}/branch-protections.
		out[i] = ghBranch{Name: b.Name, Commit: ghCommitRef{SHA: b.Commit.SHA, URL: c.commitAPI(b.Commit.SHA)}}
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) getBranch() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	name := c.wildcard()
	repo := browseRepo(r)
	res, err := c.s.d.Browse.Resolve(c.ctx, repo, "refs/heads/"+name)
	if err != nil {
		return err
	}
	if res.Kind != gitbrowse.KindBranch || res.Name != "refs/heads/"+name {
		return errf(http.StatusNotFound, "Branch not found")
	}
	page, err := c.s.d.Browse.Log(c.ctx, repo, res.Commit, "", 0, 1)
	if err != nil {
		return err
	}
	if len(page.Commits) == 0 {
		return errf(http.StatusNotFound, "Branch not found")
	}
	self := c.repoAPI() + "/branches/" + escPath(name)
	return c.ok(http.StatusOK, map[string]any{
		"name": name, "commit": c.commitObj(page.Commits[0]), "protected": false,
		"protection":     map[string]any{"enabled": false, "required_status_checks": map[string]any{"enforcement_level": "off", "contexts": []string{}, "checks": []string{}}},
		"protection_url": self + "/protection",
		"_links":         map[string]string{"self": self, "html": c.treeHTML(name, "")},
	})
}

type ghTag struct {
	Name       string      `json:"name"`
	Commit     ghCommitRef `json:"commit"`
	ZipballURL string      `json:"zipball_url"`
	TarballURL string      `json:"tarball_url"`
	NodeID     string      `json:"node_id"`
}

func (c *call) archiveURL(ref, format string) string {
	return c.s.base + "/api/v1/repos/" + esc(c.owner) + "/" + esc(c.repo) + "/archive?" +
		url.Values{"ref": {ref}, "format": {format}}.Encode()
}

func (c *call) listTags() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	p := c.pager()
	list, more, err := c.s.d.Browse.Tags(c.ctx, browseRepo(r), p.offset(), p.per)
	if err != nil {
		return err
	}
	out := make([]ghTag, len(list))
	for i, t := range list {
		out[i] = ghTag{Name: t.Name, Commit: ghCommitRef{SHA: t.Commit.SHA, URL: c.commitAPI(t.Commit.SHA)},
			ZipballURL: c.archiveURL("refs/tags/"+t.Name, "zip"), TarballURL: c.archiveURL("refs/tags/"+t.Name, "tar.gz"),
			NodeID: nodeIDString("Ref", c.owner+"/"+c.repo+":refs/tags/"+t.Name)}
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

type ghRefObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
	URL  string `json:"url"`
}

type ghRef struct {
	Ref    string      `json:"ref"`
	NodeID string      `json:"node_id"`
	URL    string      `json:"url"`
	Object ghRefObject `json:"object"`
}

func (c *call) refObj(name, sha string) ghRef {
	return ghRef{Ref: name, NodeID: nodeIDString("Ref", c.owner+"/"+c.repo+":"+name),
		URL:    c.repoAPI() + "/git/" + escPath(name),
		Object: ghRefObject{Type: "commit", SHA: sha, URL: c.repoAPI() + "/git/commits/" + sha}}
}

func (c *call) getRef() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	name := "refs/" + c.wildcard()
	res, err := c.s.d.Browse.Resolve(c.ctx, browseRepo(r), name)
	if err != nil {
		return err
	}
	if res.Kind == gitbrowse.KindCommit || res.Name != name {
		return errNotFound
	}
	return c.ok(http.StatusOK, c.refObj(res.Name, res.Commit))
}

// maxRefPages bounds the branch and tag pages read to match refs.
const maxRefPages = 10

// allRefs lists the branches and tags (at most maxRefPages pages of 100
// each) whose full name starts with prefix.
func (c *call) allRefs(repo gitbrowse.Repo, prefix string) ([]ghRef, error) {
	var out []ghRef
	if strings.HasPrefix("refs/heads/", prefix) || strings.HasPrefix(prefix, "refs/heads/") {
		for i := range maxRefPages {
			list, more, err := c.s.d.Browse.Branches(c.ctx, repo, i*maxPerPage, maxPerPage)
			if err != nil {
				return nil, err
			}
			for _, b := range list {
				if n := "refs/heads/" + b.Name; strings.HasPrefix(n, prefix) {
					out = append(out, c.refObj(n, b.Commit.SHA))
				}
			}
			if !more {
				break
			}
		}
	}
	if strings.HasPrefix("refs/tags/", prefix) || strings.HasPrefix(prefix, "refs/tags/") {
		for i := range maxRefPages {
			list, more, err := c.s.d.Browse.Tags(c.ctx, repo, i*maxPerPage, maxPerPage)
			if err != nil {
				return nil, err
			}
			for _, t := range list {
				if n := "refs/tags/" + t.Name; strings.HasPrefix(n, prefix) {
					out = append(out, c.refObj(n, t.Commit.SHA))
				}
			}
			if !more {
				break
			}
		}
	}
	return out, nil
}

// matchingRefs serves git/refs and git/matching-refs: the refs starting
// with the given prefix. git/refs/<full ref> answers the single ref.
func (c *call) matchingRefs() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	rest := c.wildcard()
	prefix := "refs/"
	if rest != "" {
		prefix += rest
	}
	exact := !strings.Contains(c.r.URL.Path, "/git/matching-refs/")
	list, err := c.allRefs(browseRepo(r), prefix)
	if err != nil {
		return err
	}
	if exact && rest != "" {
		for _, ref := range list {
			if ref.Ref == prefix {
				return c.ok(http.StatusOK, ref)
			}
		}
		// GitHub answers the refs under the prefix as a directory.
		var under []ghRef
		for _, ref := range list {
			if strings.HasPrefix(ref.Ref, prefix+"/") {
				under = append(under, ref)
			}
		}
		if len(under) == 0 {
			return errNotFound
		}
		list = under
	}
	p := c.pager()
	page, more := slice(p, list)
	if page == nil {
		page = []ghRef{}
	}
	c.link(p, more)
	return c.ok(http.StatusOK, page)
}
