package identity

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// UserSummary is the public view of a user found by SearchUsers.
type UserSummary struct {
	ID          uuid.UUID
	Username    string
	DisplayName string
	AvatarURL   string
}

// maxSearchQuery bounds the length of a user search, in runes.
const maxSearchQuery = 100

// SearchUsers finds the active users whose username or display name
// starts with query (case-insensitive, a leading @ is ignored), the exact
// username first. Unless security.user_directory is instance or the caller
// is an administrator, only the people the caller shares an organization
// or a repository with are found; restricted users always get that view.
// A non-nil repoID adds the people of that repository: the caller must
// already be allowed to read it. An anonymous caller finds nobody.
func (s *Service) SearchUsers(ctx context.Context, who auth.Principal, query string, repoID uuid.UUID, limit int) ([]UserSummary, error) {
	if who.UserID == uuid.Nil {
		return []UserSummary{}, nil
	}
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "@"))
	if utf8.RuneCountInString(q) > maxSearchQuery {
		return []UserSummary{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, 50)
	instance := (s.userDir == config.UserDirectoryInstance || who.Role == auth.RoleAdmin) &&
		who.Role != auth.RoleRestricted
	rows, err := s.q.SearchUsers(ctx, dbgen.SearchUsersParams{
		Instance: instance,
		Pattern:  likePrefix(q),
		Exact:    q,
		Lim:      int32(limit),
		Caller:   who.UserID,
		RepoID:   uuid.NullUUID{UUID: repoID, Valid: repoID != uuid.Nil},
	})
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}
	out := make([]UserSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, UserSummary{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName,
			AvatarURL: r.AvatarUrl})
	}
	return out, nil
}

// likePrefix is a LIKE pattern matching the strings that start with p.
func likePrefix(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(p) + "%"
}
