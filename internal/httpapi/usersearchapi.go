package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
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

func (p problemResponse) VisitGetUserProfileResponse(w http.ResponseWriter) error { return p.write(w) }

// GetUserProfile serves the public profile page of a user; signed-out
// visitors may call it.
func (s *v1Server) GetUserProfile(ctx context.Context, req v1gen.GetUserProfileRequestObject) (v1gen.GetUserProfileResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	pr, err := s.identity.UserProfile(ctx, req.Username)
	if errors.Is(err, identity.ErrNotFound) {
		return problemResponse{status: http.StatusNotFound, detail: "not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.GetUserProfile200JSONResponse{Username: pr.Username, DisplayName: pr.DisplayName,
		AvatarUrl: pr.AvatarURL, CreatedAt: pr.CreatedAt}, nil
}
