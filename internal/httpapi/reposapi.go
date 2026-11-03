package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Repos is the repository service used by the /repos endpoints; it is
// implemented by *repos.Service.
type Repos interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
	GetByID(ctx context.Context, who auth.Principal, id uuid.UUID) (repos.Repo, error)
	Create(ctx context.Context, who auth.Principal, in repos.CreateInput, c identity.Client) (repos.Repo, error)
	ListOwner(ctx context.Context, who auth.Principal, owner string, offset, limit int) ([]repos.Repo, error)
	ListMine(ctx context.Context, who auth.Principal, offset, limit int) ([]repos.Repo, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, in repos.UpdateInput, c identity.Client) (repos.Repo, error)
	Transfer(ctx context.Context, who auth.Principal, owner, name string, in repos.TransferInput, c identity.Client) (repos.Repo, error)
	Delete(ctx context.Context, who auth.Principal, owner, name string, c identity.Client) error
	ListCollaborators(ctx context.Context, who auth.Principal, owner, name string) ([]repos.Collaborator, error)
	SetCollaborator(ctx context.Context, who auth.Principal, owner, name, username, permission string, c identity.Client) error
	RemoveCollaborator(ctx context.Context, who auth.Principal, owner, name, username string, c identity.Client) error
}

var _ Repos = (*repos.Service)(nil)

var errReposUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "repositories are not available"}

// movedResponse answers a request to the old path of a renamed or
// transferred repository: 301 for GET, 307 otherwise (the method and body
// must be repeated at Location).
type movedResponse struct {
	status   int
	location string
}

func (m movedResponse) write(w http.ResponseWriter) error {
	w.Header().Set("Location", m.location)
	writeProblem(w, m.status, "repository moved")
	return nil
}

// reposFail maps a service error to a response, or passes it on as a 500.
// suffix is the part of the request path after /repos/{owner}/{name}, kept
// in the Location of a redirect.
func reposFail(err error, method, suffix string) (repoResponse, error) {
	var mv *repos.MovedError
	if errors.As(err, &mv) {
		status := http.StatusTemporaryRedirect
		if method == http.MethodGet {
			status = http.StatusMovedPermanently
		}
		return movedResponse{status: status, location: "/api/v1/repos/" + url.PathEscape(mv.Owner) + "/" +
			url.PathEscape(mv.Name) + suffix}, nil
	}
	var ve *repos.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), nil
	case errors.Is(err, repos.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, repos.ErrForbidden):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, nil
	case errors.Is(err, repos.ErrNameTaken), quota.IsExceeded(err):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	}
	return nil, err
}

// repoResponse is answered by problemResponse and movedResponse.
type repoResponse interface {
	v1gen.GetRepoResponseObject
	v1gen.UpdateRepoResponseObject
	v1gen.DeleteRepoResponseObject
	v1gen.TransferRepoResponseObject
	v1gen.ListRepoCollaboratorsResponseObject
	v1gen.SetRepoCollaboratorResponseObject
	v1gen.RemoveRepoCollaboratorResponseObject
	v1gen.ListMyReposResponseObject
	v1gen.CreateUserRepoResponseObject
	v1gen.ListUserReposResponseObject
	v1gen.ListOrgReposResponseObject
	v1gen.CreateOrgRepoResponseObject
}

func repoDTO(r repos.Repo) v1gen.Repo {
	return v1gen.Repo{Id: r.ID, Owner: r.Owner, OwnerKind: v1gen.RepoOwnerKind(r.OwnerKind), Name: r.Name,
		FullName: r.FullName(), Description: r.Description, Visibility: v1gen.Visibility(r.Visibility),
		DefaultBranch: r.DefaultBranch, StorageState: v1gen.RepoStorageState(r.StorageState), SizeBytes: r.SizeBytes,
		Permission: v1gen.Permission(r.Permission.String()), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func repoList(list []repos.Repo) []v1gen.Repo {
	out := make([]v1gen.Repo, 0, len(list))
	for _, r := range list {
		out = append(out, repoDTO(r))
	}
	return out
}

func page(offset, limit *int) (int, int) {
	o, l := 0, 0
	if offset != nil {
		o = *offset
	}
	if limit != nil {
		l = *limit
	}
	return o, l
}

func createInput(owner string, b *v1gen.RepoCreate) repos.CreateInput {
	in := repos.CreateInput{Owner: owner, Name: b.Name}
	if b.Description != nil {
		in.Description = *b.Description
	}
	if b.Visibility != nil {
		in.Visibility = string(*b.Visibility)
	}
	if b.DefaultBranch != nil {
		in.DefaultBranch = *b.DefaultBranch
	}
	return in
}

func (s *v1Server) ListMyRepos(ctx context.Context, req v1gen.ListMyReposRequestObject) (v1gen.ListMyReposResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	o, l := page(req.Params.Offset, req.Params.Limit)
	list, err := s.repos.ListMine(ctx, principal(ctx), o, l)
	if err != nil {
		return reposFail(err, http.MethodGet, "")
	}
	return v1gen.ListMyRepos200JSONResponse(repoList(list)), nil
}

func (s *v1Server) CreateUserRepo(ctx context.Context, req v1gen.CreateUserRepoRequestObject) (v1gen.CreateUserRepoResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	p := principal(ctx)
	r, err := s.repos.Create(ctx, p, createInput(p.Username, req.Body), clientOf(ctx))
	if err != nil {
		return reposFail(err, http.MethodPost, "")
	}
	return v1gen.CreateUserRepo201JSONResponse(repoDTO(r)), nil
}

func (s *v1Server) ListUserRepos(ctx context.Context, req v1gen.ListUserReposRequestObject) (v1gen.ListUserReposResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	o, l := page(req.Params.Offset, req.Params.Limit)
	list, err := s.repos.ListOwner(ctx, principal(ctx), req.Username, o, l)
	if err != nil {
		return reposFail(err, http.MethodGet, "")
	}
	return v1gen.ListUserRepos200JSONResponse(repoList(list)), nil
}

func (s *v1Server) ListOrgRepos(ctx context.Context, req v1gen.ListOrgReposRequestObject) (v1gen.ListOrgReposResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	o, l := page(req.Params.Offset, req.Params.Limit)
	list, err := s.repos.ListOwner(ctx, principal(ctx), req.Org, o, l)
	if err != nil {
		return reposFail(err, http.MethodGet, "")
	}
	return v1gen.ListOrgRepos200JSONResponse(repoList(list)), nil
}

func (s *v1Server) CreateOrgRepo(ctx context.Context, req v1gen.CreateOrgRepoRequestObject) (v1gen.CreateOrgRepoResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	r, err := s.repos.Create(ctx, principal(ctx), createInput(req.Org, req.Body), clientOf(ctx))
	if err != nil {
		return reposFail(err, http.MethodPost, "")
	}
	return v1gen.CreateOrgRepo201JSONResponse(repoDTO(r)), nil
}

func (s *v1Server) GetRepo(ctx context.Context, req v1gen.GetRepoRequestObject) (v1gen.GetRepoResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	r, err := s.repos.Get(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return reposFail(err, http.MethodGet, "")
	}
	return v1gen.GetRepo200JSONResponse(repoDTO(r)), nil
}

func (s *v1Server) UpdateRepo(ctx context.Context, req v1gen.UpdateRepoRequestObject) (v1gen.UpdateRepoResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	in := repos.UpdateInput{Name: req.Body.Name, Description: req.Body.Description, DefaultBranch: req.Body.DefaultBranch}
	if req.Body.Visibility != nil {
		v := string(*req.Body.Visibility)
		in.Visibility = &v
	}
	r, err := s.repos.Update(ctx, principal(ctx), req.Owner, req.Name, in, clientOf(ctx))
	if err != nil {
		return reposFail(err, http.MethodPatch, "")
	}
	return v1gen.UpdateRepo200JSONResponse(repoDTO(r)), nil
}

func (s *v1Server) DeleteRepo(ctx context.Context, req v1gen.DeleteRepoRequestObject) (v1gen.DeleteRepoResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if err := s.repos.Delete(ctx, principal(ctx), req.Owner, req.Name, clientOf(ctx)); err != nil {
		return reposFail(err, http.MethodDelete, "")
	}
	return v1gen.DeleteRepo204Response{}, nil
}

func (s *v1Server) TransferRepo(ctx context.Context, req v1gen.TransferRepoRequestObject) (v1gen.TransferRepoResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	in := repos.TransferInput{NewOwner: req.Body.NewOwner}
	if req.Body.NewName != nil {
		in.NewName = *req.Body.NewName
	}
	r, err := s.repos.Transfer(ctx, principal(ctx), req.Owner, req.Name, in, clientOf(ctx))
	if err != nil {
		return reposFail(err, http.MethodPost, "/transfer")
	}
	return v1gen.TransferRepo200JSONResponse(repoDTO(r)), nil
}

func (s *v1Server) ListRepoCollaborators(ctx context.Context, req v1gen.ListRepoCollaboratorsRequestObject) (v1gen.ListRepoCollaboratorsResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	list, err := s.repos.ListCollaborators(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return reposFail(err, http.MethodGet, "/collaborators")
	}
	out := make(v1gen.ListRepoCollaborators200JSONResponse, 0, len(list))
	for _, c := range list {
		out = append(out, v1gen.RepoCollaborator{Username: c.Username, DisplayName: c.DisplayName,
			Permission: v1gen.Permission(c.Permission.String()), Since: c.Since})
	}
	return out, nil
}

func (s *v1Server) SetRepoCollaborator(ctx context.Context, req v1gen.SetRepoCollaboratorRequestObject) (v1gen.SetRepoCollaboratorResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	if err := s.repos.SetCollaborator(ctx, principal(ctx), req.Owner, req.Name, req.Username,
		string(req.Body.Permission), clientOf(ctx)); err != nil {
		return reposFail(err, http.MethodPut, "/collaborators/"+url.PathEscape(req.Username))
	}
	return v1gen.SetRepoCollaborator204Response{}, nil
}

func (s *v1Server) RemoveRepoCollaborator(ctx context.Context, req v1gen.RemoveRepoCollaboratorRequestObject) (v1gen.RemoveRepoCollaboratorResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if err := s.repos.RemoveCollaborator(ctx, principal(ctx), req.Owner, req.Name, req.Username, clientOf(ctx)); err != nil {
		return reposFail(err, http.MethodDelete, "/collaborators/"+url.PathEscape(req.Username))
	}
	return v1gen.RemoveRepoCollaborator204Response{}, nil
}

// Visit methods let problemResponse and movedResponse answer every
// repository operation.
func (p problemResponse) VisitGetRepoResponse(w http.ResponseWriter) error        { return p.write(w) }
func (p problemResponse) VisitUpdateRepoResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitDeleteRepoResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitTransferRepoResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitListMyReposResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitCreateUserRepoResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListUserReposResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitListOrgReposResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitCreateOrgRepoResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitSetRepoCollaboratorResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRemoveRepoCollaboratorResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListRepoCollaboratorsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitGetRepoResponse(w http.ResponseWriter) error        { return m.write(w) }
func (m movedResponse) VisitUpdateRepoResponse(w http.ResponseWriter) error     { return m.write(w) }
func (m movedResponse) VisitDeleteRepoResponse(w http.ResponseWriter) error     { return m.write(w) }
func (m movedResponse) VisitTransferRepoResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitListMyReposResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitCreateUserRepoResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitListUserReposResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitListOrgReposResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitCreateOrgRepoResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitSetRepoCollaboratorResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitRemoveRepoCollaboratorResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListRepoCollaboratorsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
