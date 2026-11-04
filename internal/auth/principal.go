package auth

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"
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
}

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
