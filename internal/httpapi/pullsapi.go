package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Pulls is the pull request service used by the REST API.
type Pulls interface {
	Get(ctx context.Context, who auth.Principal, owner, name string, number int64) (pulls.Pull, error)
	List(ctx context.Context, who auth.Principal, owner, name string, o pulls.ListOptions) ([]pulls.Pull, error)
	UpdateBranch(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.UpdateBranchInput) (string, error)
	LinkedPulls(ctx context.Context, who auth.Principal, owner, name string, number int64) ([]pulls.LinkedPull, error)
	Create(ctx context.Context, who auth.Principal, owner, name string, in pulls.CreateInput) (pulls.Pull, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.UpdateInput) (pulls.Pull, error)
	ListCommits(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]gitbrowse.Commit, bool, error)
	ListFiles(ctx context.Context, who auth.Principal, owner, name string, number int64) ([]gitbrowse.FileChange, error)
	Diff(ctx context.Context, who auth.Principal, owner, name string, number int64, paths []string) (gitbrowse.Diff, error)
	Mergeability(ctx context.Context, who auth.Principal, owner, name string, number int64) (pulls.Mergeability, error)

	Merge(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.MergeInput) (string, error)
	EnableAutoMerge(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.AutoMergeInput) (pulls.Pull, error)
	DisableAutoMerge(ctx context.Context, who auth.Principal, owner, name string, number int64) (pulls.Pull, error)

	ListReviews(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]pulls.Review, error)
	GetReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID) (pulls.Review, error)
	CreateReview(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.ReviewInput) (pulls.Review, error)
	SubmitReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID, event, body string) (pulls.Review, error)
	DeletePendingReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID) error
	DismissReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID, message string) (pulls.Review, error)
	RequestReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string) (pulls.Pull, error)
	RemoveReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string) (pulls.Pull, error)

	ListComments(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewID uuid.UUID, offset, limit int) ([]pulls.Comment, error)
	CreateComment(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.CommentInput) (pulls.Comment, error)
	UpdateComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, body string) (pulls.Comment, error)
	DeleteComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) error
	ResolveThread(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, resolved bool) (pulls.Comment, error)
	ApplySuggestion(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, message string) (string, error)

	CreateFork(ctx context.Context, who auth.Principal, owner, name string, in pulls.ForkInput, c identity.Client) (repos.Repo, error)
	ListForks(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]repos.Repo, error)
}

var _ Pulls = (*pulls.Service)(nil)

var errPullsUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "pull requests are not available"}

// pullsFail maps a pull request service error to a response.
func pullsFail(err error, method, suffix string) (pullResponse, error) {
	var ve *pulls.ValidationError
	var ive *issues.ValidationError
	switch {
	case errors.As(err, &ve), errors.As(err, &ive):
		return badRequest(err.Error()), nil
	case errors.Is(err, pulls.ErrNotFound), errors.Is(err, pulls.ErrReviewNotFound), errors.Is(err, pulls.ErrCommentNotFound),
		errors.Is(err, issues.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, pulls.ErrLocked):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, nil
	case errors.Is(err, pulls.ErrNotMergeable):
		return problemResponse{status: http.StatusMethodNotAllowed, detail: err.Error()}, nil
	case errors.Is(err, pulls.ErrConflict):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, pulls.ErrUnavailable):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error(), retryAfter: retryHydrating}, nil
	}
	r, err2 := browseFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(pullResponse), nil
}

func pullSuffix(n int64, tail string) string {
	return "/pulls/" + strconv.FormatInt(n, 10) + tail
}

func pullCommentSuffix(id uuid.UUID, tail string) string {
	return "/pulls/comments/" + id.String() + tail
}

func reviewSuffix(n int64, id uuid.UUID, tail string) string {
	return pullSuffix(n, "/reviews/"+id.String()+tail)
}

// ---- pull requests ----

func (s *v1Server) ListPulls(ctx context.Context, req v1gen.ListPullsRequestObject) (v1gen.ListPullsResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	p := req.Params
	o := pulls.ListOptions{State: string(deref(p.State)), Base: deref(p.Base), Author: deref(p.Author), Sort: string(deref(p.Sort)),
		ReviewRequested: deref(p.ReviewRequested), Q: deref(p.Q)}
	if h := deref(p.Head); h != "" {
		// "owner:branch" selects a head branch of the owner's fork.
		if owner, branch, ok := strings.Cut(h, ":"); ok {
			o.HeadRepo, o.Head = owner+"/"+req.Name, branch
		} else {
			o.Head = h
		}
	}
	o.Offset, o.Limit = page(p.Offset, p.Limit)
	list, err := s.pulls.List(ctx, principal(ctx), req.Owner, req.Name, o)
	if err != nil {
		return pullsFail(err, http.MethodGet, "/pulls")
	}
	out := make(v1gen.ListPulls200JSONResponse, 0, len(list))
	for _, pr := range list {
		out = append(out, pullDTO(pr))
	}
	return out, nil
}

func (s *v1Server) CreatePull(ctx context.Context, req v1gen.CreatePullRequestObject) (v1gen.CreatePullResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	b := req.Body
	if b == nil {
		return badRequest("missing body"), nil
	}
	in := pulls.CreateInput{Title: b.Title, Body: deref(b.Body), Head: b.Head, HeadRepo: deref(b.HeadRepo), Base: b.Base,
		Draft: deref(b.Draft)}
	pr, err := s.pulls.Create(ctx, principal(ctx), req.Owner, req.Name, in)
	if err != nil {
		return pullsFail(err, http.MethodPost, "/pulls")
	}
	return v1gen.CreatePull201JSONResponse(pullDTO(pr)), nil
}

func (s *v1Server) GetPull(ctx context.Context, req v1gen.GetPullRequestObject) (v1gen.GetPullResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	pr, err := s.pulls.Get(ctx, principal(ctx), req.Owner, req.Name, req.Number)
	if err != nil {
		return pullsFail(err, http.MethodGet, pullSuffix(req.Number, ""))
	}
	return v1gen.GetPull200JSONResponse(pullDTO(pr)), nil
}

func (s *v1Server) UpdatePull(ctx context.Context, req v1gen.UpdatePullRequestObject) (v1gen.UpdatePullResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	b := req.Body
	if b == nil {
		return badRequest("missing body"), nil
	}
	in := pulls.UpdateInput{Title: b.Title, Body: b.Body, Base: b.Base, Draft: b.Draft}
	if b.State != nil {
		st := string(*b.State)
		in.State = &st
	}
	pr, err := s.pulls.Update(ctx, principal(ctx), req.Owner, req.Name, req.Number, in)
	if err != nil {
		return pullsFail(err, http.MethodPatch, pullSuffix(req.Number, ""))
	}
	return v1gen.UpdatePull200JSONResponse(pullDTO(pr)), nil
}

func (s *v1Server) ListPullCommits(ctx context.Context, req v1gen.ListPullCommitsRequestObject) (v1gen.ListPullCommitsResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	off, lim := page(req.Params.Offset, req.Params.Limit)
	list, more, err := s.pulls.ListCommits(ctx, principal(ctx), req.Owner, req.Name, req.Number, off, lim)
	if err != nil {
		return pullsFail(err, http.MethodGet, pullSuffix(req.Number, "/commits"))
	}
	return v1gen.ListPullCommits200JSONResponse{HasMore: more, Items: commitsDTO(list)}, nil
}

func (s *v1Server) ListPullFiles(ctx context.Context, req v1gen.ListPullFilesRequestObject) (v1gen.ListPullFilesResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	list, err := s.pulls.ListFiles(ctx, principal(ctx), req.Owner, req.Name, req.Number)
	if err != nil {
		return pullsFail(err, http.MethodGet, pullSuffix(req.Number, "/files"))
	}
	out := make(v1gen.ListPullFiles200JSONResponse, 0, len(list))
	for _, f := range list {
		out = append(out, v1gen.PullFile{Status: v1gen.PullFileStatus(f.Status), OldPath: f.OldPath, NewPath: f.NewPath,
			Additions: f.Additions, Deletions: f.Deletions, Binary: f.Binary})
	}
	return out, nil
}

func (s *v1Server) GetPullDiff(ctx context.Context, req v1gen.GetPullDiffRequestObject) (v1gen.GetPullDiffResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	d, err := s.pulls.Diff(ctx, principal(ctx), req.Owner, req.Name, req.Number, deref(req.Params.Path))
	if err != nil {
		return pullsFail(err, http.MethodGet, pullSuffix(req.Number, "/diff"))
	}
	return v1gen.GetPullDiff200JSONResponse(diffDTO(d)), nil
}

func (s *v1Server) GetPullMergeability(ctx context.Context, req v1gen.GetPullMergeabilityRequestObject) (v1gen.GetPullMergeabilityResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	m, err := s.pulls.Mergeability(ctx, principal(ctx), req.Owner, req.Name, req.Number)
	if err != nil {
		return pullsFail(err, http.MethodGet, pullSuffix(req.Number, "/mergeability"))
	}
	return v1gen.GetPullMergeability200JSONResponse(mergeabilityDTO(m)), nil
}

// ---- merge ----

func (s *v1Server) MergePull(ctx context.Context, req v1gen.MergePullRequestObject) (v1gen.MergePullResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	in := pulls.MergeInput{}
	if b := req.Body; b != nil {
		in = pulls.MergeInput{Method: string(deref(b.Method)), Title: deref(b.Title), Message: deref(b.Message),
			SHA: deref(b.Sha), DeleteBranch: deref(b.DeleteBranch)}
	}
	sha, err := s.pulls.Merge(ctx, principal(ctx), req.Owner, req.Name, req.Number, in)
	if err != nil {
		return pullsFail(err, http.MethodPost, pullSuffix(req.Number, "/merge"))
	}
	return v1gen.MergePull200JSONResponse{Sha: sha}, nil
}

func (s *v1Server) UpdatePullBranch(ctx context.Context, req v1gen.UpdatePullBranchRequestObject) (v1gen.UpdatePullBranchResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	in := pulls.UpdateBranchInput{}
	if b := req.Body; b != nil {
		in = pulls.UpdateBranchInput{Method: string(deref(b.Method)), ExpectedHeadSHA: deref(b.ExpectedHeadSha)}
	}
	sha, err := s.pulls.UpdateBranch(ctx, principal(ctx), req.Owner, req.Name, req.Number, in)
	if err != nil {
		return pullsFail(err, http.MethodPut, pullSuffix(req.Number, "/update-branch"))
	}
	return v1gen.UpdatePullBranch200JSONResponse{Sha: sha}, nil
}

func (s *v1Server) ListIssueLinkedPulls(ctx context.Context, req v1gen.ListIssueLinkedPullsRequestObject) (v1gen.ListIssueLinkedPullsResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	list, err := s.pulls.LinkedPulls(ctx, principal(ctx), req.Owner, req.Name, req.Number)
	if err != nil {
		return pullsFail(err, http.MethodGet, "/issues/"+strconv.FormatInt(req.Number, 10)+"/linked-pulls")
	}
	out := make(v1gen.ListIssueLinkedPulls200JSONResponse, 0, len(list))
	for _, lp := range list {
		out = append(out, v1gen.LinkedPull{Repo: lp.Repo, Number: lp.Number, Title: lp.Title,
			State: v1gen.LinkedPullState(lp.State), Draft: lp.Draft, Author: lp.Author})
	}
	return out, nil
}

func (s *v1Server) EnablePullAutoMerge(ctx context.Context, req v1gen.EnablePullAutoMergeRequestObject) (v1gen.EnablePullAutoMergeResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	in := pulls.AutoMergeInput{}
	if b := req.Body; b != nil {
		in = pulls.AutoMergeInput{Method: string(deref(b.Method)), Title: deref(b.Title), Message: deref(b.Message),
			DeleteBranch: deref(b.DeleteBranch)}
	}
	pr, err := s.pulls.EnableAutoMerge(ctx, principal(ctx), req.Owner, req.Name, req.Number, in)
	if err != nil {
		return pullsFail(err, http.MethodPut, pullSuffix(req.Number, "/auto-merge"))
	}
	return v1gen.EnablePullAutoMerge200JSONResponse(pullDTO(pr)), nil
}

func (s *v1Server) DisablePullAutoMerge(ctx context.Context, req v1gen.DisablePullAutoMergeRequestObject) (v1gen.DisablePullAutoMergeResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	pr, err := s.pulls.DisableAutoMerge(ctx, principal(ctx), req.Owner, req.Name, req.Number)
	if err != nil {
		return pullsFail(err, http.MethodDelete, pullSuffix(req.Number, "/auto-merge"))
	}
	return v1gen.DisablePullAutoMerge200JSONResponse(pullDTO(pr)), nil
}

// ---- reviewers ----

func (s *v1Server) RequestPullReviewers(ctx context.Context, req v1gen.RequestPullReviewersRequestObject) (v1gen.RequestPullReviewersResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	pr, err := s.pulls.RequestReviewers(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.Body.Reviewers)
	if err != nil {
		return pullsFail(err, http.MethodPost, pullSuffix(req.Number, "/requested-reviewers"))
	}
	return v1gen.RequestPullReviewers200JSONResponse(pullDTO(pr)), nil
}

func (s *v1Server) RemovePullReviewers(ctx context.Context, req v1gen.RemovePullReviewersRequestObject) (v1gen.RemovePullReviewersResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	var names []string
	for _, n := range strings.Split(req.Params.Reviewers, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	pr, err := s.pulls.RemoveReviewers(ctx, principal(ctx), req.Owner, req.Name, req.Number, names)
	if err != nil {
		return pullsFail(err, http.MethodDelete, pullSuffix(req.Number, "/requested-reviewers"))
	}
	return v1gen.RemovePullReviewers200JSONResponse(pullDTO(pr)), nil
}

// ---- reviews ----

func (s *v1Server) ListPullReviews(ctx context.Context, req v1gen.ListPullReviewsRequestObject) (v1gen.ListPullReviewsResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	off, lim := page(req.Params.Offset, req.Params.Limit)
	list, err := s.pulls.ListReviews(ctx, principal(ctx), req.Owner, req.Name, req.Number, off, lim)
	if err != nil {
		return pullsFail(err, http.MethodGet, pullSuffix(req.Number, "/reviews"))
	}
	out := make(v1gen.ListPullReviews200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, pullReviewDTO(r))
	}
	return out, nil
}

func (s *v1Server) CreatePullReview(ctx context.Context, req v1gen.CreatePullReviewRequestObject) (v1gen.CreatePullReviewResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	in := pulls.ReviewInput{}
	if b := req.Body; b != nil {
		in = pulls.ReviewInput{Event: string(deref(b.Event)), Body: deref(b.Body), CommitSHA: deref(b.CommitSha)}
		for _, c := range deref(b.Comments) {
			in.Comments = append(in.Comments, pulls.DraftComment{Path: c.Path, Side: string(deref(c.Side)), Line: c.Line,
				StartLine: deref(c.StartLine), Body: c.Body})
		}
	}
	r, err := s.pulls.CreateReview(ctx, principal(ctx), req.Owner, req.Name, req.Number, in)
	if err != nil {
		return pullsFail(err, http.MethodPost, pullSuffix(req.Number, "/reviews"))
	}
	return v1gen.CreatePullReview201JSONResponse(pullReviewDTO(r)), nil
}

func (s *v1Server) GetPullReview(ctx context.Context, req v1gen.GetPullReviewRequestObject) (v1gen.GetPullReviewResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	r, err := s.pulls.GetReview(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.ReviewId)
	if err != nil {
		return pullsFail(err, http.MethodGet, reviewSuffix(req.Number, req.ReviewId, ""))
	}
	return v1gen.GetPullReview200JSONResponse(pullReviewDTO(r)), nil
}

func (s *v1Server) DeletePullReview(ctx context.Context, req v1gen.DeletePullReviewRequestObject) (v1gen.DeletePullReviewResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	if err := s.pulls.DeletePendingReview(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.ReviewId); err != nil {
		return pullsFail(err, http.MethodDelete, reviewSuffix(req.Number, req.ReviewId, ""))
	}
	return v1gen.DeletePullReview204Response{}, nil
}

func (s *v1Server) SubmitPullReview(ctx context.Context, req v1gen.SubmitPullReviewRequestObject) (v1gen.SubmitPullReviewResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	r, err := s.pulls.SubmitReview(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.ReviewId,
		string(req.Body.Event), deref(req.Body.Body))
	if err != nil {
		return pullsFail(err, http.MethodPost, reviewSuffix(req.Number, req.ReviewId, "/submit"))
	}
	return v1gen.SubmitPullReview200JSONResponse(pullReviewDTO(r)), nil
}

func (s *v1Server) DismissPullReview(ctx context.Context, req v1gen.DismissPullReviewRequestObject) (v1gen.DismissPullReviewResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	r, err := s.pulls.DismissReview(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.ReviewId, req.Body.Message)
	if err != nil {
		return pullsFail(err, http.MethodPost, reviewSuffix(req.Number, req.ReviewId, "/dismiss"))
	}
	return v1gen.DismissPullReview200JSONResponse(pullReviewDTO(r)), nil
}

func (s *v1Server) ListPullReviewCommentsOfReview(ctx context.Context, req v1gen.ListPullReviewCommentsOfReviewRequestObject) (v1gen.ListPullReviewCommentsOfReviewResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	off, lim := page(req.Params.Offset, req.Params.Limit)
	list, err := s.pulls.ListComments(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.ReviewId, off, lim)
	if err != nil {
		return pullsFail(err, http.MethodGet, reviewSuffix(req.Number, req.ReviewId, "/comments"))
	}
	return v1gen.ListPullReviewCommentsOfReview200JSONResponse(pullCommentsDTO(list)), nil
}

// ---- review comments ----

func (s *v1Server) ListPullReviewComments(ctx context.Context, req v1gen.ListPullReviewCommentsRequestObject) (v1gen.ListPullReviewCommentsResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	off, lim := page(req.Params.Offset, req.Params.Limit)
	list, err := s.pulls.ListComments(ctx, principal(ctx), req.Owner, req.Name, req.Number, uuid.Nil, off, lim)
	if err != nil {
		return pullsFail(err, http.MethodGet, pullSuffix(req.Number, "/comments"))
	}
	return v1gen.ListPullReviewComments200JSONResponse(pullCommentsDTO(list)), nil
}

func (s *v1Server) CreatePullReviewComment(ctx context.Context, req v1gen.CreatePullReviewCommentRequestObject) (v1gen.CreatePullReviewCommentResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	b := req.Body
	if b == nil {
		return badRequest("missing body"), nil
	}
	in := pulls.CommentInput{Body: b.Body, InReplyTo: deref(b.InReplyTo), Path: deref(b.Path), Side: string(deref(b.Side)),
		Line: deref(b.Line), StartLine: deref(b.StartLine), CommitSHA: deref(b.CommitSha)}
	c, err := s.pulls.CreateComment(ctx, principal(ctx), req.Owner, req.Name, req.Number, in)
	if err != nil {
		return pullsFail(err, http.MethodPost, pullSuffix(req.Number, "/comments"))
	}
	return v1gen.CreatePullReviewComment201JSONResponse(pullCommentDTO(c)), nil
}

func (s *v1Server) UpdatePullReviewComment(ctx context.Context, req v1gen.UpdatePullReviewCommentRequestObject) (v1gen.UpdatePullReviewCommentResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	c, err := s.pulls.UpdateComment(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, req.Body.Body)
	if err != nil {
		return pullsFail(err, http.MethodPatch, pullCommentSuffix(req.CommentId, ""))
	}
	return v1gen.UpdatePullReviewComment200JSONResponse(pullCommentDTO(c)), nil
}

func (s *v1Server) DeletePullReviewComment(ctx context.Context, req v1gen.DeletePullReviewCommentRequestObject) (v1gen.DeletePullReviewCommentResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	if err := s.pulls.DeleteComment(ctx, principal(ctx), req.Owner, req.Name, req.CommentId); err != nil {
		return pullsFail(err, http.MethodDelete, pullCommentSuffix(req.CommentId, ""))
	}
	return v1gen.DeletePullReviewComment204Response{}, nil
}

func (s *v1Server) ResolvePullThread(ctx context.Context, req v1gen.ResolvePullThreadRequestObject) (v1gen.ResolvePullThreadResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	c, err := s.pulls.ResolveThread(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, true)
	if err != nil {
		return pullsFail(err, http.MethodPut, pullCommentSuffix(req.CommentId, "/resolve"))
	}
	return v1gen.ResolvePullThread200JSONResponse(pullCommentDTO(c)), nil
}

func (s *v1Server) UnresolvePullThread(ctx context.Context, req v1gen.UnresolvePullThreadRequestObject) (v1gen.UnresolvePullThreadResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	c, err := s.pulls.ResolveThread(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, false)
	if err != nil {
		return pullsFail(err, http.MethodDelete, pullCommentSuffix(req.CommentId, "/resolve"))
	}
	return v1gen.UnresolvePullThread200JSONResponse(pullCommentDTO(c)), nil
}

func (s *v1Server) ApplyPullSuggestion(ctx context.Context, req v1gen.ApplyPullSuggestionRequestObject) (v1gen.ApplyPullSuggestionResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	msg := ""
	if req.Body != nil {
		msg = deref(req.Body.Message)
	}
	sha, err := s.pulls.ApplySuggestion(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, msg)
	if err != nil {
		return pullsFail(err, http.MethodPost, pullCommentSuffix(req.CommentId, "/apply-suggestion"))
	}
	return v1gen.ApplyPullSuggestion200JSONResponse{Sha: sha}, nil
}

// ---- forks ----

func (s *v1Server) ListForks(ctx context.Context, req v1gen.ListForksRequestObject) (v1gen.ListForksResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	off, lim := page(req.Params.Offset, req.Params.Limit)
	list, err := s.pulls.ListForks(ctx, principal(ctx), req.Owner, req.Name, off, lim)
	if err != nil {
		return pullsFail(err, http.MethodGet, "/forks")
	}
	return v1gen.ListForks200JSONResponse(repoList(list)), nil
}

func (s *v1Server) CreateFork(ctx context.Context, req v1gen.CreateForkRequestObject) (v1gen.CreateForkResponseObject, error) {
	if s.pulls == nil {
		return errPullsUnavailable, nil
	}
	in := pulls.ForkInput{}
	if b := req.Body; b != nil {
		in = pulls.ForkInput{Owner: deref(b.Owner), Name: deref(b.Name)}
	}
	r, err := s.pulls.CreateFork(ctx, principal(ctx), req.Owner, req.Name, in, clientOf(ctx))
	if err != nil {
		return pullsFail(err, http.MethodPost, "/forks")
	}
	return v1gen.CreateFork201JSONResponse(repoDTO(r)), nil
}

// ---- DTOs ----

func pullRefDTO(r pulls.Ref) v1gen.PullRef {
	return v1gen.PullRef{Repo: r.Repo, Ref: r.Ref, Sha: r.SHA}
}

func pullDTO(p pulls.Pull) v1gen.PullRequest {
	out := v1gen.PullRequest{Issue: issueDTO(p.Issue), State: v1gen.PullRequestState(p.State), Draft: p.Draft,
		Head: pullRefDTO(p.Head), Base: pullRefDTO(p.Base), MergeBase: p.MergeBase,
		Mergeable: v1gen.PullRequestMergeable(p.Mergeable), ConflictFiles: nonNilStrings(p.ConflictFiles),
		MergedAt: p.MergedAt, MergedBy: p.MergedBy, MergeCommit: p.MergeCommit, MergeMethod: p.MergeMethod,
		RequestedReviewers: nonNilStrings(p.RequestedReviewers)}
	if a := p.AutoMerge; a != nil {
		out.AutoMerge = &v1gen.PullAutoMerge{Method: v1gen.PullAutoMergeMethod(a.Method), EnabledBy: a.EnabledBy,
			Title: a.Title, Message: a.Message, DeleteBranch: a.DeleteBranch}
	}
	return out
}

func pullReviewDTO(r pulls.Review) v1gen.PullReview {
	return v1gen.PullReview{Id: r.ID, Reviewer: r.Reviewer, State: v1gen.PullReviewState(r.State), Body: r.Body,
		CommitSha: r.CommitSHA, SubmittedAt: r.SubmittedAt, DismissedAt: r.DismissedAt, DismissedBy: optStr(r.DismissedBy),
		DismissMessage: optStr(r.DismissMessage), CreatedAt: r.CreatedAt}
}

func pullCommentDTO(c pulls.Comment) v1gen.PullReviewComment {
	out := v1gen.PullReviewComment{Id: c.ID, Author: c.Author, Path: c.Path, Side: v1gen.PullReviewCommentSide(c.Side),
		Line: c.Line, StartLine: c.StartLine, OriginalLine: c.OriginalLine, OriginalStartLine: c.OriginalStartLine,
		OriginalCommitSha: c.OriginalCommitSHA, CommitSha: c.CommitSHA, DiffHunk: c.DiffHunk, Body: c.Body,
		Outdated: c.Outdated, Resolved: c.Resolved, ResolvedBy: optStr(c.ResolvedBy), CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt}
	if c.ReviewID != uuid.Nil {
		out.ReviewId = c.ReviewID.String()
	}
	if c.InReplyTo != uuid.Nil {
		out.InReplyTo = optStr(c.InReplyTo.String())
	}
	if c.Pending {
		t := true
		out.Pending = &t
	}
	return out
}

func pullCommentsDTO(list []pulls.Comment) []v1gen.PullReviewComment {
	out := make([]v1gen.PullReviewComment, 0, len(list))
	for _, c := range list {
		out = append(out, pullCommentDTO(c))
	}
	return out
}

func mergeabilityDTO(m pulls.Mergeability) v1gen.PullMergeability {
	out := v1gen.PullMergeability{State: v1gen.PullMergeabilityState(m.State),
		Mergeable: v1gen.PullMergeabilityMergeable(m.Mergeable), CanMerge: m.CanMerge,
		ConflictFiles: nonNilStrings(m.ConflictFiles), HeadSha: m.HeadSHA, BaseSha: m.BaseSHA, MergeBase: m.MergeBase,
		Rule: m.Rule, RequiredApprovals: m.RequiredApprove, Approvals: m.Approvals,
		MissingChecks: nonNilStrings(m.MissingChecks), Behind: m.Behind, CodeOwnersFile: optStr(m.CodeOwnersFile),
		CodeOwnersErrors: nonNilStrings(m.CodeOwnersErrors),
		CodeOwners:       make([]v1gen.PullCodeOwners, 0, len(m.CodeOwners)),
		Violations:       make([]v1gen.ProtectionViolation, 0, len(m.Violations))}
	for _, g := range m.CodeOwners {
		out.CodeOwners = append(out.CodeOwners, v1gen.PullCodeOwners{Pattern: g.Pattern, Owners: nonNilStrings(g.Owners),
			Paths: nonNilStrings(g.Paths), Approved: g.Approved})
	}
	for _, v := range m.Violations {
		out.Violations = append(out.Violations, v1gen.ProtectionViolation{Code: v.Code, Message: v.Message,
			Commit: optStr(v.Commit)})
	}
	return out
}

// ---- problem and redirect responses ----

// pullResponse is answered by problemResponse and movedResponse.
type pullResponse interface {
	v1gen.ListForksResponseObject
	v1gen.CreateForkResponseObject
	v1gen.ListPullsResponseObject
	v1gen.CreatePullResponseObject
	v1gen.DeletePullReviewCommentResponseObject
	v1gen.UpdatePullReviewCommentResponseObject
	v1gen.ApplyPullSuggestionResponseObject
	v1gen.UnresolvePullThreadResponseObject
	v1gen.ResolvePullThreadResponseObject
	v1gen.GetPullResponseObject
	v1gen.UpdatePullResponseObject
	v1gen.DisablePullAutoMergeResponseObject
	v1gen.EnablePullAutoMergeResponseObject
	v1gen.ListPullReviewCommentsResponseObject
	v1gen.CreatePullReviewCommentResponseObject
	v1gen.ListPullCommitsResponseObject
	v1gen.GetPullDiffResponseObject
	v1gen.ListPullFilesResponseObject
	v1gen.MergePullResponseObject
	v1gen.UpdatePullBranchResponseObject
	v1gen.ListIssueLinkedPullsResponseObject
	v1gen.GetPullMergeabilityResponseObject
	v1gen.RemovePullReviewersResponseObject
	v1gen.RequestPullReviewersResponseObject
	v1gen.ListPullReviewsResponseObject
	v1gen.CreatePullReviewResponseObject
	v1gen.DeletePullReviewResponseObject
	v1gen.GetPullReviewResponseObject
	v1gen.ListPullReviewCommentsOfReviewResponseObject
	v1gen.DismissPullReviewResponseObject
	v1gen.SubmitPullReviewResponseObject
}

func (p problemResponse) VisitListForksResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitCreateForkResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListPullsResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitCreatePullResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDeletePullReviewCommentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitUpdatePullReviewCommentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitApplyPullSuggestionResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitUnresolvePullThreadResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitResolvePullThreadResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetPullResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitUpdatePullResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDisablePullAutoMergeResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitEnablePullAutoMergeResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListPullReviewCommentsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitCreatePullReviewCommentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListPullCommitsResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetPullDiffResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitListPullFilesResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitMergePullResponse(w http.ResponseWriter) error       { return p.write(w) }
func (p problemResponse) VisitUpdatePullBranchResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListIssueLinkedPullsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetPullMergeabilityResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRemovePullReviewersResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRequestPullReviewersResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListPullReviewsResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitCreatePullReviewResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeletePullReviewResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetPullReviewResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListPullReviewCommentsOfReviewResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDismissPullReviewResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitSubmitPullReviewResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitListForksResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitCreateForkResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitListPullsResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitCreatePullResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitDeletePullReviewCommentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitUpdatePullReviewCommentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitApplyPullSuggestionResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitUnresolvePullThreadResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitResolvePullThreadResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetPullResponse(w http.ResponseWriter) error           { return m.write(w) }
func (m movedResponse) VisitUpdatePullResponse(w http.ResponseWriter) error        { return m.write(w) }
func (m movedResponse) VisitDisablePullAutoMergeResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitEnablePullAutoMergeResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListPullReviewCommentsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitCreatePullReviewCommentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListPullCommitsResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetPullDiffResponse(w http.ResponseWriter) error     { return m.write(w) }
func (m movedResponse) VisitListPullFilesResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitMergePullResponse(w http.ResponseWriter) error       { return m.write(w) }
func (m movedResponse) VisitUpdatePullBranchResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListIssueLinkedPullsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitGetPullMergeabilityResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitRemovePullReviewersResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitRequestPullReviewersResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListPullReviewsResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitCreatePullReviewResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitDeletePullReviewResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetPullReviewResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitListPullReviewCommentsOfReviewResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDismissPullReviewResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitSubmitPullReviewResponse(w http.ResponseWriter) error  { return m.write(w) }
