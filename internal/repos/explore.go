package repos

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// MaxPins is the most repositories a user can pin.
const MaxPins = 12

// ErrPinLimit is returned when a user already has MaxPins pins.
var ErrPinLimit = errors.New("you can pin at most 12 repositories")

// ExploreList is a page of public repositories with the total match count.
type ExploreList struct {
	Total int64
	Items []Repo
}

// Explore lists the public repositories whose name or description contains
// query (all of them when empty). sort is "updated" (default) or "name".
// page is 1-based. It works for anonymous callers.
func (s *Service) Explore(ctx context.Context, who auth.Principal, query, sort string, page, perPage int) (ExploreList, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if len(query) > MaxSearchQuery {
		return ExploreList{}, invalid("the search query must be at most %d characters", MaxSearchQuery)
	}
	switch sort {
	case "", "updated":
		sort = "updated"
	case "name":
	default:
		return ExploreList{}, invalid("sort must be updated or name")
	}
	pattern := likeEscape(query)
	total, err := s.q.CountExploreRepos(ctx, pattern)
	if err != nil {
		return ExploreList{}, fmt.Errorf("count explore repos: %w", err)
	}
	lim := pageLimit(perPage)
	off := (min(max(page, 1), 1<<20) - 1) * lim
	rows, err := s.q.ExploreRepos(ctx, dbgen.ExploreReposParams{Pattern: pattern, Sort: sort, Off: pageOffset(off),
		Lim: int32(lim)}) //nolint:gosec // pageLimit clamps to MaxLimit
	if err != nil {
		return ExploreList{}, fmt.Errorf("explore repos: %w", err)
	}
	out := ExploreList{Total: total, Items: make([]Repo, 0, len(rows))}
	for _, r := range rows {
		v, err := s.view(ctx, s.q, who, r.Repo, r.OwnerName, r.OwnerKind)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return ExploreList{}, err
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

// ListPins lists the repositories pinned by the caller, in pin order. Pins
// of repositories the caller can no longer read are left out.
func (s *Service) ListPins(ctx context.Context, who auth.Principal) ([]Repo, error) {
	if who.UserID == uuid.Nil {
		return nil, ErrForbidden
	}
	rows, err := s.q.ListUserPins(ctx, dbgen.ListUserPinsParams{UserID: who.UserID, AllRepos: who.IsAdmin(),
		Internal: who.Role != auth.RoleRestricted})
	if err != nil {
		return nil, fmt.Errorf("list pins: %w", err)
	}
	out := make([]Repo, 0, len(rows))
	for _, r := range rows {
		v, err := s.view(ctx, s.q, who, r.Repo, r.OwnerName, r.OwnerKind)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Pin adds owner/name after the caller's last pin; a repository that is
// already pinned stays where it is. ErrPinLimit past MaxPins.
func (s *Service) Pin(ctx context.Context, who auth.Principal, owner, name string) error {
	if who.UserID == uuid.Nil {
		return ErrForbidden
	}
	r, err := s.Authorize(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		// The count and the insert share the transaction; the per-user
		// advisory lock keeps two concurrent pins from passing the limit.
		if err := q.LockUserPins(ctx, who.UserID.String()); err != nil {
			return fmt.Errorf("lock pins: %w", err)
		}
		n, err := q.CountUserPins(ctx, who.UserID)
		if err != nil {
			return fmt.Errorf("count pins: %w", err)
		}
		if n >= MaxPins {
			return ErrPinLimit
		}
		if err := q.PinRepo(ctx, dbgen.PinRepoParams{UserID: who.UserID, RepoID: r.ID}); err != nil {
			return fmt.Errorf("pin repo: %w", err)
		}
		return nil
	})
}

// Unpin removes owner/name from the caller's pins (also when it was not
// pinned or the caller lost access to it).
func (s *Service) Unpin(ctx context.Context, who auth.Principal, owner, name string) error {
	if who.UserID == uuid.Nil {
		return ErrForbidden
	}
	row, err := s.q.GetRepoByPath(ctx, dbgen.GetRepoByPathParams{Owner: owner, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // an unknown path has nothing to unpin
	}
	if err != nil {
		return fmt.Errorf("get repo: %w", err)
	}
	if err := s.q.UnpinRepo(ctx, dbgen.UnpinRepoParams{UserID: who.UserID, RepoID: row.Repo.ID}); err != nil {
		return fmt.Errorf("unpin repo: %w", err)
	}
	return nil
}
