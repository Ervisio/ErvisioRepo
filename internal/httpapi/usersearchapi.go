package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

func (p problemResponse) VisitSearchUsersResponse(w http.ResponseWriter) error { return p.write(w) }

// SearchUsers finds users for the assignee and reviewer pickers; the
// identity service decides who is visible (security.user_directory).
func (s *v1Server) SearchUsers(ctx context.Context, req v1gen.SearchUsersRequestObject) (v1gen.SearchUsersResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	var repoID uuid.UUID
	if r := deref(req.Params.Repo); r != "" {
		owner, name, ok := splitRepoParam(r)
		if !ok {
			return badRequest("repo must be owner/name"), nil
		}
		if s.repos == nil {
			return errReposUnavailable, nil
		}
		repo, err := s.repos.Get(ctx, p, owner, name)
		if err != nil {
			if pr, ok := repoParamProblem(err); ok {
				return pr, nil
			}
			return nil, err
		}
		repoID = repo.ID
	}
	us, err := s.identity.SearchUsers(ctx, p, req.Params.Q, repoID, deref(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	out := make(v1gen.SearchUsers200JSONResponse, 0, len(us))
	for _, u := range us {
		out = append(out, v1gen.UserSummary{Username: u.Username, DisplayName: u.DisplayName,
			AvatarUrl: u.AvatarURL})
	}
	return out, nil
}
