package repos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Stargazer is a user who starred a repository.
type Stargazer struct {
	Username    string
	DisplayName string
	AvatarURL   string
	StarredAt   time.Time
}

// StarredRepo is a repository starred by a user.
type StarredRepo struct {
	Repo
	StarredAt time.Time
}

// Star adds owner/name to the caller's stars; starring twice changes
// nothing. The caller must be signed in and able to read the repository.
func (s *Service) Star(ctx context.Context, who auth.Principal, owner, name string) error {
	if who.UserID == uuid.Nil {
		return ErrForbidden
	}
	r, err := s.Authorize(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return err
	}
	if err := s.q.StarRepo(ctx, dbgen.StarRepoParams{UserID: who.UserID, RepoID: r.ID}); err != nil {
		return fmt.Errorf("star repo: %w", err)
	}
	return nil
}

// Unstar removes owner/name from the caller's stars (also when it was not
// starred or the caller lost access to it).
func (s *Service) Unstar(ctx context.Context, who auth.Principal, owner, name string) error {
	if who.UserID == uuid.Nil {
		return ErrForbidden
	}
	row, err := s.q.GetRepoByPath(ctx, dbgen.GetRepoByPathParams{Owner: owner, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // an unknown path has nothing to unstar
	}
	if err != nil {
		return fmt.Errorf("get repo: %w", err)
	}
	if err := s.q.UnstarRepo(ctx, dbgen.UnstarRepoParams{UserID: who.UserID, RepoID: row.Repo.ID}); err != nil {
		return fmt.Errorf("unstar repo: %w", err)
	}
	return nil
}

// IsStarred tells whether the caller starred owner/name, which it must be
// able to read.
func (s *Service) IsStarred(ctx context.Context, who auth.Principal, owner, name string) (bool, error) {
	r, err := s.Get(ctx, who, owner, name)
	if err != nil {
		return false, err
	}
	if who.UserID == uuid.Nil {
		return false, nil
	}
	ok, err := s.q.IsRepoStarred(ctx, dbgen.IsRepoStarredParams{UserID: who.UserID, RepoID: r.ID})
	if err != nil {
		return false, fmt.Errorf("is starred: %w", err)
	}
	return ok, nil
}

// MarkStarred sets Starred on the repositories of list the caller starred,
// with one query. Anonymous callers star nothing.
func (s *Service) MarkStarred(ctx context.Context, who auth.Principal, list []Repo) error {
	if who.UserID == uuid.Nil || len(list) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(list))
	for i, r := range list {
		ids[i] = r.ID
	}
	starred, err := s.q.ListStarredAmong(ctx, dbgen.ListStarredAmongParams{UserID: who.UserID, RepoIds: ids})
	if err != nil {
		return fmt.Errorf("list starred: %w", err)
	}
	set := make(map[uuid.UUID]bool, len(starred))
	for _, id := range starred {
		set[id] = true
	}
	for i := range list {
		list[i].Starred = set[list[i].ID]
	}
	return nil
}

// ListStargazers lists the users who starred owner/name, latest first.
func (s *Service) ListStargazers(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]Stargazer, error) {
	r, err := s.Get(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListStargazers(ctx, dbgen.ListStargazersParams{RepoID: r.ID, Off: pageOffset(offset),
		Lim: int32(pageLimit(limit))}) //nolint:gosec // pageLimit clamps to MaxLimit
	if err != nil {
		return nil, fmt.Errorf("list stargazers: %w", err)
	}
	out := make([]Stargazer, len(rows))
	for i, g := range rows {
		out[i] = Stargazer{Username: g.Username, DisplayName: g.DisplayName, AvatarURL: g.AvatarUrl,
			StarredAt: g.StarredAt.Time}
	}
	return out, nil
}

// ListStarred lists the repositories starred by the user username that the
// caller can read, latest star first. Starred is set for the caller.
func (s *Service) ListStarred(ctx context.Context, who auth.Principal, username string, offset, limit int) ([]StarredRepo, error) {
	ns, err := s.namespace(ctx, s.q, who, username)
	if err != nil {
		return nil, err
	}
	if ns.kind != "user" {
		return nil, ErrNotFound
	}
	rows, err := s.q.ListUserStarredRepos(ctx, dbgen.ListUserStarredReposParams{UserID: ns.id, Viewer: who.UserID,
		AllRepos: who.IsAdmin(), Internal: who.UserID != uuid.Nil && who.Role != auth.RoleRestricted,
		Off: pageOffset(offset), Lim: int32(pageLimit(limit))}) //nolint:gosec // pageLimit clamps to MaxLimit
	if err != nil {
		return nil, fmt.Errorf("list starred repos: %w", err)
	}
	list := make([]Repo, 0, len(rows))
	at := make([]time.Time, 0, len(rows))
	for _, r := range rows {
		v, err := s.view(ctx, s.q, who, r.Repo, r.OwnerName, r.OwnerKind)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		list = append(list, v)
		at = append(at, r.StarredAt.Time)
	}
	if err := s.MarkStarred(ctx, who, list); err != nil {
		return nil, err
	}
	out := make([]StarredRepo, len(list))
	for i := range list {
		out[i] = StarredRepo{Repo: list[i], StarredAt: at[i]}
	}
	return out, nil
}
