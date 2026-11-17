package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

// Profile is the public view of a user account.
type Profile struct {
	ID       uuid.UUID
	Username string
	// Kind is "user" or "bot"; Description is what a bot does.
	Kind          string
	Description   string
	DisplayName   string
	AvatarURL     string
	InstanceAdmin bool
	CreatedAt     time.Time
	Details       ProfileDetails
}

// UserProfile returns the public profile of the named user, or
// ErrNotFound when the name is unknown, belongs to an organization or the
// account is suspended.
func (s *Service) UserProfile(ctx context.Context, username string) (Profile, error) {
	row, err := s.q.GetUserProfileByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get user profile: %w", err)
	}
	if row.SuspendedAt.Valid {
		return Profile{}, ErrNotFound
	}
	details, err := s.ProfileDetails(ctx, row.ID)
	if err != nil {
		return Profile{}, err
	}
	return Profile{ID: row.ID, Username: row.Username, Kind: row.Kind, Description: row.Description,
		DisplayName: row.DisplayName, AvatarURL: row.AvatarUrl, InstanceAdmin: row.Role == string(auth.RoleAdmin),
		CreatedAt: row.CreatedAt.Time, Details: details}, nil
}

// OrgSummary names an organization.
type OrgSummary struct {
	Name        string
	DisplayName string
}

// VisibleOrgs lists the organizations of user that who can see: those who
// belongs to as well, or all of them for an instance admin.
func (s *Service) VisibleOrgs(ctx context.Context, who auth.Principal, user uuid.UUID) ([]OrgSummary, error) {
	if who.UserID == uuid.Nil {
		return []OrgSummary{}, nil
	}
	rows, err := s.q.ListVisibleUserOrgs(ctx, dbgen.ListVisibleUserOrgsParams{UserID: user, AllOrgs: who.IsAdmin(),
		Viewer: who.UserID})
	if err != nil {
		return nil, fmt.Errorf("list user orgs: %w", err)
	}
	out := make([]OrgSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, OrgSummary{Name: r.Name, DisplayName: r.DisplayName})
	}
	return out, nil
}
