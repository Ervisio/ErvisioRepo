package auth

import (
	"context"

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
)

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID    uuid.UUID
	Username  string
	Role      string
	Method    string    // MethodSession; tokens will add their own
	SessionID uuid.UUID // set when Method is MethodSession
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
