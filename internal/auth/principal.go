package auth

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/bots"
)

// Instance roles (PLAN §5.3).
const (
	RoleAdmin      = "admin"
	RoleUser       = "user"
	RoleRestricted = "restricted"
)

// Authentication methods of a Principal.
const (
	MethodSession = "session"
	MethodToken   = "token" // personal access token (Authorization: Bearer)
	// MethodApp is an App authenticated with its JWT (ghapi /app routes).
	MethodApp = "app"
)

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID    uuid.UUID
	Username  string
	Role      string
	Method    string    // MethodSession or MethodToken
	SessionID uuid.UUID // set when Method is MethodSession
	TokenID   uuid.UUID // set when Method is MethodToken
	// Scopes are the scopes of the access token (MethodToken only).
	Scopes []string
	// Grant is set for an Ervisio Actions job token (erj_…): the caller
	// acts on Grant.RepoID only, with Grant.Permission, and is anonymous
	// everywhere else. UserID is uuid.Nil for such callers.
	Grant *RepoGrant
	// Installation is set for an App installation access token (erps_…):
	// the caller acts as the App's bot on the repositories the installation
	// covers, with its permission set, and is anonymous everywhere else.
	// UserID is uuid.Nil for such callers.
	Installation *InstallationGrant
	// App is set for an App authenticated with its JWT (MethodApp); it can
	// only manage its installations.
	App *AppIdentity
	// UserApp is set for a user-to-server token of an App: the user's
	// access is bounded by the App's permissions and limited to the
	// repositories where the App is installed.
	UserApp *UserAppGrant
}

// AppIdentity is an App authenticated with its JWT.
type AppIdentity struct {
	ID    uuid.UUID
	NumID int64
	Slug  string
}

// UserAppGrant is the App behind a user-to-server token.
type UserAppGrant struct {
	AppID       uuid.UUID
	Permissions authz.AppPermissions
}

// InstallationGrant is the access of an installation access token.
type InstallationGrant struct {
	ID       uuid.UUID
	NumID    int64
	AppID    uuid.UUID
	AppNumID int64
	AppSlug  string
	// BotID is the App's bot account, the author of the changes.
	BotID uuid.UUID
	// AccountID is the user or organization the App is installed on.
	AccountID uuid.UUID
	// AllRepos covers every repository of the account; otherwise RepoIDs.
	AllRepos bool
	RepoIDs  []uuid.UUID
	// Permissions are the effective permissions of the token.
	Permissions authz.AppPermissions
	// TokenID is the access token row.
	TokenID uuid.UUID
}

// Covers reports whether the installation grants access to repository
// repoID owned by ownerID.
func (g *InstallationGrant) Covers(repoID, ownerID uuid.UUID) bool {
	if g == nil || ownerID != g.AccountID {
		return false
	}
	return g.AllRepos || slices.Contains(g.RepoIDs, repoID)
}

// AppPermissions returns the App permission set bounding the caller, if
// any: an installation token or a user-to-server token.
func (p Principal) AppPermissions() (authz.AppPermissions, bool) {
	switch {
	case p.Installation != nil:
		return p.Installation.Permissions, true
	case p.UserApp != nil:
		return p.UserApp.Permissions, true
	}
	return nil, false
}

// Automated reports whether the caller is a token acting for an
// automation (an Actions job or an App installation) rather than a user.
func (p Principal) Automated() bool { return p.Grant != nil || p.Installation != nil }

// RepoGrant is the repository access of an Ervisio Actions job token.
type RepoGrant struct {
	RepoID uuid.UUID
	// Permission is "read" or "write" (an authz permission name).
	Permission string
	// JobID is the job the token was issued to.
	JobID uuid.UUID
}

// HasScope reports whether a token caller holds scope. A write scope
// implies the matching read scope. Session callers hold no token scopes.
func (p Principal) HasScope(scope string) bool {
	if p.Method != MethodToken {
		return false
	}
	if slices.Contains(p.Scopes, scope) {
		return true
	}
	if res, ok := strings.CutSuffix(scope, ":read"); ok {
		return slices.Contains(p.Scopes, res+":write")
	}
	return false
}

// Actor is the account a change made by the caller is attributed to: the
// caller itself, or ervisio-actions[bot] for an Actions job token (whose
// UserID stays uuid.Nil, so that it is anonymous outside its repository).
func (p Principal) Actor() uuid.UUID {
	switch {
	case p.Grant != nil:
		return bots.ActionsID
	case p.Installation != nil:
		return p.Installation.BotID
	}
	return p.UserID
}

// WritesTo reports whether the caller may make signed-in changes (issues,
// comments, pull requests) on repository repoID owned by ownerID: any
// signed-in user, an Actions job token on its own repository, or an
// installation on a repository it covers. The repository permission is
// checked separately.
func (p Principal) WritesTo(repoID, ownerID uuid.UUID) bool {
	switch {
	case p.Grant != nil:
		return p.Grant.RepoID == repoID && p.Grant.Permission != "none"
	case p.Installation != nil:
		return p.Installation.Covers(repoID, ownerID)
	}
	return p.UserID != uuid.Nil
}

// IsAdmin reports whether the caller is an instance admin.
func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

type principalKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the caller of the request, if authenticated.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
