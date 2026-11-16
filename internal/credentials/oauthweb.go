package credentials

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// OAuth web application flow, shaped like GitHub's: the SPA consent page
// at /login/oauth/authorize checks the request (CheckAuthorize), the user
// decides (Authorize), the browser returns to the app with a single-use
// code, and the app trades it at POST /login/oauth/access_token
// (ExchangeCode) for a token of the user's grant.

// OAuthCodeTTL is the lifetime of an authorization code, as on GitHub.
const OAuthCodeTTL = 10 * time.Minute

// Errors of the token endpoint for the web flow, with GitHub's codes.
var (
	ErrBadVerificationCode = deviceErr("bad_verification_code", "The code passed is incorrect or expired.")
	ErrClientCredentials   = deviceErr("incorrect_client_credentials", "The client_id and/or client_secret passed are incorrect.")
	ErrRedirectMismatch    = deviceErr("redirect_uri_mismatch", "The redirect_uri MUST match the registered callback URL for this application.")
)

// AuthorizeError is a request the consent page refuses without redirecting
// (unknown client, bad redirect_uri or PKCE parameters).
type AuthorizeError struct {
	NotFound bool
	Msg      string
}

func (e *AuthorizeError) Error() string { return e.Msg }

// AuthorizeRequest holds the parameters of GET /login/oauth/authorize.
type AuthorizeRequest struct {
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// Consent describes a valid request for the consent page.
type Consent struct {
	App         OAuthApp
	RedirectURI string
	// RequestedScopes are the known GitHub scopes asked for.
	RequestedScopes []string
	Scopes          []string // token scopes
	// Granted means the user already authorized these scopes.
	Granted bool
}

// CheckAuthorize validates a web flow request of userID.
func (s *Service) CheckAuthorize(ctx context.Context, userID uuid.UUID, req AuthorizeRequest) (Consent, error) {
	app, redirect, err := s.checkAuthorize(ctx, req)
	if err != nil {
		return Consent{}, err
	}
	scopes := auth.ParseGitHubScopes(req.Scope)
	c := Consent{App: app, RedirectURI: redirect, RequestedScopes: scopes, Scopes: auth.ScopesFromGitHub(scopes)}
	g, err := s.q.GetOAuthGrant(ctx, dbgen.GetOAuthGrantParams{AppID: app.ID, UserID: userID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return Consent{}, fmt.Errorf("get oauth grant: %w", err)
	default:
		c.Granted = !slices.ContainsFunc(scopes, func(sc string) bool { return !slices.Contains(g.Scopes, sc) })
	}
	return c, nil
}

func (s *Service) checkAuthorize(ctx context.Context, req AuthorizeRequest) (OAuthApp, string, error) {
	app, err := lookupApp(ctx, s.q, req.ClientID)
	if errors.Is(err, ErrNotFound) {
		return OAuthApp{}, "", &AuthorizeError{NotFound: true, Msg: "unknown client_id"}
	}
	if err != nil {
		return OAuthApp{}, "", err
	}
	if len(app.CallbackURLs) == 0 {
		return OAuthApp{}, "", &AuthorizeError{Msg: "this application does not use the web flow"}
	}
	redirect := app.CallbackURLs[0]
	if req.RedirectURI != "" {
		if !redirectAllowed(req.RedirectURI, app.CallbackURLs) {
			return OAuthApp{}, "", &AuthorizeError{Msg: "the redirect_uri is not associated with this application"}
		}
		redirect = req.RedirectURI
	}
	if req.CodeChallenge != "" || req.CodeChallengeMethod != "" {
		if req.CodeChallengeMethod != "S256" {
			return OAuthApp{}, "", &AuthorizeError{Msg: "code_challenge_method must be S256"}
		}
		if !validChallenge(req.CodeChallenge) {
			return OAuthApp{}, "", &AuthorizeError{Msg: "code_challenge must be a base64url SHA-256 digest"}
		}
	}
	if len(req.State) > 2000 {
		return OAuthApp{}, "", &AuthorizeError{Msg: "state is too long"}
	}
	return app, redirect, nil
}

func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}

// redirectAllowed applies GitHub's rules: scheme, host and port equal to
// a callback URL (any port for a loopback host), and a path equal to the
// callback path or below it.
func redirectAllowed(raw string, callbacks []string) bool {
	if !callbackURL(raw) {
		return false
	}
	r, err := url.Parse(raw)
	if err != nil || strings.Contains(r.Path, "/../") || strings.HasSuffix(r.Path, "/..") {
		return false
	}
	for _, cb := range callbacks {
		c, err := url.Parse(cb)
		if err != nil || !strings.EqualFold(c.Scheme, r.Scheme) || !strings.EqualFold(c.Hostname(), r.Hostname()) {
			continue
		}
		if c.Port() != r.Port() && !isLoopback(c.Hostname()) {
			continue
		}
		cp := c.EscapedPath()
		rp := r.EscapedPath()
		if rp == cp || strings.HasPrefix(rp, strings.TrimSuffix(cp, "/")+"/") {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Authorize records the decision of userID on a web flow request and
// returns the URL the browser goes to: the redirect URI with code and
// state, or with error=access_denied.
func (s *Service) Authorize(ctx context.Context, userID uuid.UUID, req AuthorizeRequest, approve bool, ip string) (string, error) {
	app, redirect, err := s.checkAuthorize(ctx, req)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(redirect)
	if err != nil {
		return "", &AuthorizeError{Msg: "invalid redirect_uri"}
	}
	q := u.Query()
	if !approve {
		q.Set("error", "access_denied")
		q.Set("error_description", "The user has denied your application access.")
		q.Set("error_uri", "https://docs.github.com/apps/managing-oauth-apps/troubleshooting-authorization-request-errors/#access-denied")
		if req.State != "" {
			q.Set("state", req.State)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	scopes := auth.ParseGitHubScopes(req.Scope)
	raw, codeHash := newClientSecret() // 40 hex characters, like a GitHub code
	err = s.tx(ctx, func(tq *dbgen.Queries) error {
		if _, err := upsertGrant(ctx, tq, app, userID, scopes, ip); err != nil {
			return err
		}
		if err := tq.InsertOAuthCode(ctx, dbgen.InsertOAuthCodeParams{CodeHash: codeHash, AppID: app.ID, UserID: userID,
			RedirectUri: redirect, RedirectGiven: req.RedirectURI != "", Scopes: scopes, CodeChallenge: req.CodeChallenge,
			ExpiresAt: pgTime(s.now().Add(OAuthCodeTTL))}); err != nil {
			return fmt.Errorf("insert oauth code: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	q.Set("code", raw)
	if req.State != "" {
		q.Set("state", req.State)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// TokenRequest is a web flow request of the token endpoint.
type TokenRequest struct {
	ClientID     string
	ClientSecret string
	Code         string
	RedirectURI  string
	CodeVerifier string
}

// ExchangeCode trades an authorization code for an access token. Errors
// are *DeviceError values for the OAuth error codes, others are internal
// failures. A client without a secret must prove the code with PKCE.
func (s *Service) ExchangeCode(ctx context.Context, req TokenRequest, ip string) (DeviceToken, error) {
	var out DeviceToken
	var result error
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		app, err := lookupApp(ctx, q, req.ClientID)
		if errors.Is(err, ErrNotFound) {
			result = ErrClientCredentials
			return nil
		}
		if err != nil {
			return err
		}
		authed := false
		if req.ClientSecret != "" {
			ok, err := s.verifyClientSecret(ctx, q, app, req.ClientSecret)
			if err != nil {
				return err
			}
			if !ok {
				result = ErrClientCredentials
				return nil
			}
			authed = true
		}
		if req.Code == "" || len(req.Code) > 200 {
			result = ErrBadVerificationCode
			return nil
		}
		row, err := q.GetOAuthCodeForUpdate(ctx, auth.HashToken(req.Code))
		if errors.Is(err, pgx.ErrNoRows) {
			result = ErrBadVerificationCode
			return nil
		}
		if err != nil {
			return fmt.Errorf("get oauth code: %w", err)
		}
		if row.AppID != app.ID || row.UsedAt.Valid || !row.ExpiresAt.Time.After(s.now()) {
			result = ErrBadVerificationCode
			return nil
		}
		if (row.RedirectGiven || req.RedirectURI != "") && req.RedirectURI != row.RedirectUri {
			result = ErrRedirectMismatch
			return nil
		}
		if row.CodeChallenge != "" {
			sum := sha256.Sum256([]byte(req.CodeVerifier))
			got := base64.RawURLEncoding.EncodeToString(sum[:])
			if req.CodeVerifier == "" || subtle.ConstantTimeCompare([]byte(got), []byte(row.CodeChallenge)) != 1 {
				result = deviceErr("bad_verification_code", "The code_verifier does not match the code_challenge.")
				return nil
			}
			authed = true
		}
		if !authed {
			result = ErrClientCredentials
			return nil
		}
		if err := q.UseOAuthCode(ctx, row.CodeHash); err != nil {
			return fmt.Errorf("use oauth code: %w", err)
		}
		if err := lockUser(ctx, q, row.UserID); err != nil {
			return err
		}
		grantID, err := upsertGrant(ctx, q, app, row.UserID, row.Scopes, ip)
		if err != nil {
			return err
		}
		secret, err := s.issueGrantToken(ctx, q, app, row.UserID, grantID, row.Scopes, ip, "authorization_code")
		if err != nil {
			return err
		}
		out = DeviceToken{AccessToken: secret, Scope: strings.Join(row.Scopes, ",")}
		return nil
	})
	if err != nil {
		return DeviceToken{}, err
	}
	if result != nil {
		return DeviceToken{}, result
	}
	return out, nil
}

// AppToken describes a token of an OAuth app for the GitHub API
// /applications/{client_id}/token.
type AppToken struct {
	ID        uuid.UUID
	App       OAuthApp
	UserID    uuid.UUID
	Username  string
	Token     string // set when the token was just created (reset)
	Hint      string
	Scopes    []string // GitHub scopes
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt *time.Time
}

// ErrAppCredentials means the client id and secret of an /applications
// request are wrong.
var ErrAppCredentials = errors.New("bad client credentials")

// appToken authenticates the app (clientID, secret) and finds token among
// the tokens it received.
func (s *Service) appToken(ctx context.Context, q *dbgen.Queries, clientID, secret, token string) (AppToken, error) {
	app, err := lookupApp(ctx, q, clientID)
	if errors.Is(err, ErrNotFound) {
		return AppToken{}, ErrAppCredentials
	}
	if err != nil {
		return AppToken{}, err
	}
	ok, err := s.verifyClientSecret(ctx, q, app, secret)
	if err != nil {
		return AppToken{}, err
	}
	if !ok {
		return AppToken{}, ErrAppCredentials
	}
	if !auth.ValidAccessTokenFormat(token) {
		return AppToken{}, ErrNotFound
	}
	row, err := q.GetAppTokenByHash(ctx, dbgen.GetAppTokenByHashParams{TokenHash: auth.HashToken(token), AppID: app.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return AppToken{}, ErrNotFound
	}
	if err != nil {
		return AppToken{}, fmt.Errorf("get app token: %w", err)
	}
	if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(s.now()) {
		return AppToken{}, ErrNotFound
	}
	return AppToken{ID: row.ID, App: app, UserID: row.UserID, Username: row.Username, Hint: token[len(token)-8:],
		Scopes: row.GrantScopes, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.GrantUpdatedAt.Time,
		ExpiresAt: ts(row.ExpiresAt)}, nil
}

// CheckAppToken returns the token of an app, authenticated by its client
// credentials (POST /applications/{client_id}/token).
func (s *Service) CheckAppToken(ctx context.Context, clientID, secret, token string) (AppToken, error) {
	return s.appToken(ctx, s.q, clientID, secret, token)
}

// ResetAppToken replaces a token of an app with a new one with the same
// grant (PATCH /applications/{client_id}/token).
func (s *Service) ResetAppToken(ctx context.Context, clientID, secret, token, ip string) (AppToken, error) {
	var out AppToken
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		t, err := s.appToken(ctx, q, clientID, secret, token)
		if err != nil {
			return err
		}
		if _, err := q.DeleteToken(ctx, t.ID); err != nil {
			return fmt.Errorf("delete token: %w", err)
		}
		g, err := q.GetOAuthGrant(ctx, dbgen.GetOAuthGrantParams{AppID: t.App.ID, UserID: t.UserID})
		if err != nil {
			return fmt.Errorf("get oauth grant: %w", err)
		}
		fresh, err := s.issueGrantToken(ctx, q, t.App, t.UserID, g.ID, g.Scopes, ip, "reset")
		if err != nil {
			return err
		}
		t.Token, t.Hint, t.CreatedAt = fresh, fresh[len(fresh)-8:], s.now()
		out = t
		return nil
	})
	return out, err
}

// DeleteAppToken revokes a token of an app (DELETE
// /applications/{client_id}/token).
func (s *Service) DeleteAppToken(ctx context.Context, clientID, secret, token, ip string) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		t, err := s.appToken(ctx, q, clientID, secret, token)
		if err != nil {
			return err
		}
		if _, err := q.DeleteToken(ctx, t.ID); err != nil {
			return fmt.Errorf("delete token: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: t.UserID, Action: ActionTokenRevoked, TargetType: "token",
			TargetID: t.ID.String(), IP: ip, Data: map[string]any{"client_id": t.App.ClientID, "by": "app"}})
	})
}

// DeleteAppGrant revokes the grant behind a token of an app, with every
// token of the grant (DELETE /applications/{client_id}/grant).
func (s *Service) DeleteAppGrant(ctx context.Context, clientID, secret, token, ip string) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		t, err := s.appToken(ctx, q, clientID, secret, token)
		if err != nil {
			return err
		}
		g, err := q.GetOAuthGrant(ctx, dbgen.GetOAuthGrantParams{AppID: t.App.ID, UserID: t.UserID})
		if err != nil {
			return fmt.Errorf("get oauth grant: %w", err)
		}
		if _, err := q.DeleteOAuthGrant(ctx, dbgen.DeleteOAuthGrantParams{AppID: t.App.ID, UserID: t.UserID}); err != nil {
			return fmt.Errorf("delete oauth grant: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: t.UserID, Action: ActionOAuthGrantRevoked, TargetType: "oauth_grant",
			TargetID: g.ID.String(), IP: ip, Data: map[string]any{"client_id": t.App.ClientID, "by": "app"}})
	})
}
