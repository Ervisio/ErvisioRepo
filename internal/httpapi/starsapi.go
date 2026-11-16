package httpapi

import (
	"context"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

func (s *v1Server) StarRepo(ctx context.Context, req v1gen.StarRepoRequestObject) (v1gen.StarRepoResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if !signedIn(ctx) {
		return unauthenticated, nil
	}
	if err := s.repos.Star(ctx, principal(ctx), req.Owner, req.Name); err != nil {
		return reposFail(err, http.MethodPut, "/star")
	}
	return v1gen.StarRepo204Response{}, nil
}

func (s *v1Server) UnstarRepo(ctx context.Context, req v1gen.UnstarRepoRequestObject) (v1gen.UnstarRepoResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if !signedIn(ctx) {
		return unauthenticated, nil
	}
	if err := s.repos.Unstar(ctx, principal(ctx), req.Owner, req.Name); err != nil {
		return reposFail(err, http.MethodDelete, "/star")
	}
	return v1gen.UnstarRepo204Response{}, nil
}

func (s *v1Server) ListStargazers(ctx context.Context, req v1gen.ListStargazersRequestObject) (v1gen.ListStargazersResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	o, l := page(req.Params.Offset, req.Params.Limit)
	list, err := s.repos.ListStargazers(ctx, principal(ctx), req.Owner, req.Name, o, l)
	if err != nil {
		return reposFail(err, http.MethodGet, "/stargazers")
	}
	out := make(v1gen.ListStargazers200JSONResponse, 0, len(list))
	for _, g := range list {
		out = append(out, v1gen.Stargazer{Username: g.Username, DisplayName: g.DisplayName, AvatarUrl: g.AvatarURL,
			StarredAt: g.StarredAt})
	}
	return out, nil
}

func (s *v1Server) ListUserStarred(ctx context.Context, req v1gen.ListUserStarredRequestObject) (v1gen.ListUserStarredResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	o, l := page(req.Params.Offset, req.Params.Limit)
	list, err := s.repos.ListStarred(ctx, principal(ctx), req.Username, o, l)
	if err != nil {
		return reposFail(err, http.MethodGet, "")
	}
	out := make(v1gen.ListUserStarred200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, v1gen.StarredRepo{Repo: repoDTO(ctx, r.Repo), StarredAt: r.StarredAt})
	}
	return out, nil
}

func (p problemResponse) VisitStarRepoResponse(w http.ResponseWriter) error       { return p.write(w) }
func (p problemResponse) VisitUnstarRepoResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitListStargazersResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListUserStarredResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitStarRepoResponse(w http.ResponseWriter) error       { return m.write(w) }
func (m movedResponse) VisitUnstarRepoResponse(w http.ResponseWriter) error     { return m.write(w) }
func (m movedResponse) VisitListStargazersResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitListUserStarredResponse(w http.ResponseWriter) error {
	return m.write(w)
}
