package repos

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// MaxSearchQuery bounds the length of a repository search.
const MaxSearchQuery = 100

// Search lists the repositories whose name contains query and that the
// caller can read, exact and prefix matches first.
func (s *Service) Search(ctx context.Context, who auth.Principal, query string, offset, limit int) ([]Repo, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || len(query) > MaxSearchQuery {
		return nil, invalid("the search query must be 1 to %d characters", MaxSearchQuery)
	}
	rows, err := s.q.SearchRepos(ctx, dbgen.SearchReposParams{Pattern: likeEscape(query), AllRepos: who.IsAdmin(),
		Internal: who.UserID != uuid.Nil && who.Role != auth.RoleRestricted, Viewer: who.UserID,
		Off: pageOffset(offset), Lim: int32(pageLimit(limit))}) //nolint:gosec // pageLimit clamps to MaxLimit
	if err != nil {
		return nil, fmt.Errorf("search repos: %w", err)
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

// likeEscape escapes the LIKE metacharacters of s (ESCAPE '\').
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
