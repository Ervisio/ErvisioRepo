package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// TokenPrefix starts every personal access token, so that leaked tokens
// are easy to recognize (secret scanners, logs).
const TokenPrefix = "erp_"

// Token scopes (PLAN §8.1). A write scope implies the matching read scope.
const (
	ScopeReadUser      = "user:read"
	ScopeWriteUser     = "user:write"
	ScopeReadRepo      = "repo:read"
	ScopeWriteRepo     = "repo:write"
	ScopeReadPackages  = "packages:read"
	ScopeWritePackages = "packages:write"
	ScopeWriteActions  = "actions:write"
	// ScopeAdmin lets an instance admin use the admin API with the token.
	ScopeAdmin = "admin"
)

// AllScopes lists the valid token scopes.
var AllScopes = []string{
	ScopeReadUser, ScopeWriteUser, ScopeReadRepo, ScopeWriteRepo,
	ScopeReadPackages, ScopeWritePackages, ScopeWriteActions, ScopeAdmin,
}

// ValidScope reports whether s is a known token scope.
func ValidScope(s string) bool { return slices.Contains(AllScopes, s) }

// ErrNoToken means the access token is unknown, expired or belongs to a
// suspended user.
var ErrNoToken = errors.New("invalid access token")

// NewAccessToken returns a new personal access token and its stored hash.
func NewAccessToken() (token string, hash []byte) {
	t, _ := NewToken()
	token = TokenPrefix + t
	return token, HashToken(token)
}

// ValidAccessTokenFormat reports whether s looks like a token from
// NewAccessToken.
func ValidAccessTokenFormat(s string) bool {
	rest, ok := strings.CutPrefix(s, TokenPrefix)
	if !ok || len(rest) != base64.RawURLEncoding.EncodedLen(TokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(rest)
	return err == nil
}

// JobTokenPrefix starts every Ervisio Actions job token (GITHUB_TOKEN of a
// workflow job).
const JobTokenPrefix = "erj_"

// ValidCredentialFormat reports whether s looks like a personal access
// token or an Ervisio Actions job token.
func ValidCredentialFormat(s string) bool {
	if ValidAccessTokenFormat(s) {
		return true
	}
	rest, ok := strings.CutPrefix(s, JobTokenPrefix)
	if !ok || len(rest) != base64.RawURLEncoding.EncodedLen(TokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(rest)
	return err == nil
}

// JobTokenLookup authenticates Ervisio Actions job tokens. It returns
// ErrNoToken for an unknown or expired token.
type JobTokenLookup interface {
	LookupJobToken(ctx context.Context, token string) (Principal, error)
}

// TokenHint is the part of a token shown in lists to recognize it.
func TokenHint(token string) string {
	if len(token) < len(TokenPrefix)+4 {
		return TokenPrefix
	}
	return token[:len(TokenPrefix)+4]
}

// TokenStore is the subset of dbgen.Querier used by Tokens.
type TokenStore interface {
	GetTokenByHash(ctx context.Context, tokenHash []byte) (dbgen.GetTokenByHashRow, error)
	TouchToken(ctx context.Context, id uuid.UUID) error
}

// Tokens authenticates personal access tokens.
type Tokens struct {
	Store TokenStore
	Now   func() time.Time // defaults to time.Now
	// Jobs authenticates job tokens (erj_…); nil refuses them.
	Jobs JobTokenLookup
}

// Lookup returns the caller authenticated by token.
func (t *Tokens) Lookup(ctx context.Context, token string) (Principal, error) {
	if strings.HasPrefix(token, JobTokenPrefix) {
		if t.Jobs == nil || !ValidCredentialFormat(token) {
			return Principal{}, ErrNoToken
		}
		return t.Jobs.LookupJobToken(ctx, token)
	}
	if !ValidAccessTokenFormat(token) {
		return Principal{}, ErrNoToken
	}
	row, err := t.Store.GetTokenByHash(ctx, HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrNoToken
	}
	if err != nil {
		return Principal{}, fmt.Errorf("get token: %w", err)
	}
	now := time.Now()
	if t.Now != nil {
		now = t.Now()
	}
	if row.SuspendedAt.Valid || (row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(now)) {
		return Principal{}, ErrNoToken
	}
	if err := t.Store.TouchToken(ctx, row.ID); err != nil {
		return Principal{}, fmt.Errorf("touch token: %w", err)
	}
	return Principal{UserID: row.UserID, Username: row.Username, Role: row.Role, Method: MethodToken,
		TokenID: row.ID, Scopes: slices.Clone(row.Scopes)}, nil
}
