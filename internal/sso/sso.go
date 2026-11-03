// Package sso signs users in with external OpenID Connect providers
// ([[auth.oidc]], PLAN §8.1). It uses the authorization code flow with
// PKCE (S256), a state bound to a browser cookie and an ID token nonce.
//
// An external identity is the pair issuer#subject. It is attached to an
// account only by a signed-in user (linking), or when it creates a new
// account (auto-registration); a matching email address never links an
// identity to an existing account.
package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/mfa"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// Audit actions of this package.
const (
	ActionLogin    = "auth.oidc_login"
	ActionRegister = "auth.oidc_register"
	ActionLinked   = "auth.oidc_linked"
	ActionUnlinked = "auth.oidc_unlinked"
)

// Flow modes.
const (
	ModeLogin = "login"
	ModeLink  = "link"
)

// StateCookie is the base name of the cookie that binds a flow to the
// browser that started it.
const StateCookie = "erepo_oidc"

// FlowTTL bounds the time between the redirect to the provider and the
// callback.
const FlowTTL = 10 * time.Minute

// Errors. Each has a Code, used in the redirect to the login page.
var (
	ErrUnknownProvider = &Error{"unknown_provider", "unknown identity provider"}
	ErrInvalidState    = &Error{"invalid_state", "the sign-in request is invalid or expired; try again"}
	ErrProvider        = &Error{"provider_error", "the identity provider refused or failed the sign-in"}
	ErrNotLinked       = &Error{"not_linked", "this identity is not linked to an account; sign in and link it from the settings"}
	ErrEmailTaken      = &Error{"email_taken", identity.ErrExternalEmailTaken.Error()}
	ErrEmailUnverified = &Error{"email_unverified", "the identity provider did not confirm a verified email address"}
	ErrLinkedElsewhere = &Error{"linked_elsewhere", "this identity is already linked to another account"}
	ErrAlreadyLinked   = &Error{"already_linked", "an identity of this provider is already linked to the account"}
	ErrLinkSession     = &Error{"link_session", "linking requires the same signed-in session that started it"}
	ErrNotFound        = errors.New("not found")
	ErrLastMethod      = errors.New("this is the last way to sign in to the account; set a password or link another provider first")
)

// Error is a failure of a flow that can be shown to the user.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Options configures a Manager.
type Options struct {
	Pool      *pgxpool.Pool
	Identity  *identity.Service
	Providers []config.OIDCProvider
	// Secrets maps provider names to client secrets.
	Secrets   map[string]string
	PublicURL string
	Cookies   auth.Cookies
	// HTTPClient talks to the providers; nil uses a client with a timeout.
	HTTPClient *http.Client
	Log        *slog.Logger
	Now        func() time.Time
	// Limiter bounds how often a client may start a flow; nil disables it.
	Limiter *auth.Limiter
	// Gate runs the local second-factor check for providers with
	// require_local_2fa (mfa.Service.Gate); nil skips it.
	Gate GateFunc
}

// GateFunc is the second step of a first-factor login, see mfa.Service.Gate.
type GateFunc func(ctx context.Context, p auth.Principal, oldToken string, c identity.Client, method string) (mfa.Outcome, error)

// Manager runs the OIDC flows of the configured providers.
type Manager struct {
	pool      *pgxpool.Pool
	q         *dbgen.Queries
	ids       *identity.Service
	publicURL string
	cookies   auth.Cookies
	client    *http.Client
	log       *slog.Logger
	now       func() time.Time
	limiter   *auth.Limiter
	gate      GateFunc
	order     []string
	providers map[string]*provider
}

type provider struct {
	cfg    config.OIDCProvider
	secret string

	mu       sync.Mutex
	oidc     *oidc.Provider
	verifier *oidc.IDTokenVerifier
}

// New returns a Manager. Provider discovery is lazy: an unreachable
// provider does not prevent the server from starting.
func New(o Options) *Manager {
	m := &Manager{pool: o.Pool, ids: o.Identity, publicURL: strings.TrimRight(o.PublicURL, "/"), cookies: o.Cookies,
		client: o.HTTPClient, log: o.Log, now: o.Now, providers: map[string]*provider{}, limiter: o.Limiter, gate: o.Gate}
	if o.Pool != nil {
		m.q = dbgen.New(o.Pool)
	}
	if m.client == nil {
		m.client = &http.Client{Timeout: 15 * time.Second}
	}
	if m.log == nil {
		m.log = slog.New(slog.DiscardHandler)
	}
	if m.now == nil {
		m.now = time.Now
	}
	for _, p := range o.Providers {
		m.order = append(m.order, p.Name)
		m.providers[p.Name] = &provider{cfg: p, secret: o.Secrets[p.Name]}
	}
	return m
}

// ProviderInfo is a provider as shown on the login page.
type ProviderInfo struct {
	Name  string
	Label string
}

// Providers lists the configured providers in configuration order.
func (m *Manager) Providers() []ProviderInfo {
	if m == nil {
		return nil
	}
	out := make([]ProviderInfo, 0, len(m.order))
	for _, n := range m.order {
		out = append(out, ProviderInfo{Name: n, Label: m.providers[n].cfg.Label()})
	}
	return out
}

// Has reports whether name is a configured provider.
func (m *Manager) Has(name string) bool {
	if m == nil {
		return false
	}
	_, ok := m.providers[name]
	return ok
}

func (m *Manager) ctx(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, m.client)
}

func (m *Manager) discover(ctx context.Context, p *provider) (*oidc.Provider, *oidc.IDTokenVerifier, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.oidc == nil {
		op, err := oidc.NewProvider(m.ctx(ctx), p.cfg.Issuer)
		if err != nil {
			m.log.WarnContext(ctx, "oidc discovery failed", "provider", p.cfg.Name, "err", err)
			return nil, nil, ErrProvider
		}
		p.oidc = op
		p.verifier = op.Verifier(&oidc.Config{ClientID: p.cfg.ClientID, Now: m.now})
	}
	return p.oidc, p.verifier, nil
}

func (m *Manager) oauth(p *provider, op *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID: p.cfg.ClientID, ClientSecret: p.secret, Endpoint: op.Endpoint(), Scopes: p.cfg.Scopes,
		RedirectURL: m.CallbackURL(p.cfg.Name),
	}
}

// CallbackURL is the redirect URI to register at the provider.
func (m *Manager) CallbackURL(name string) string {
	return m.publicURL + "/auth/oidc/" + name + "/callback"
}

func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashState(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// SafeReturnTo returns s if it is a local path, "/" otherwise.
func SafeReturnTo(s string) string {
	if s == "" || len(s) > 512 || s[0] != '/' || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") ||
		strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' }) {
		return "/"
	}
	return s
}

// Begin starts a flow and returns the provider URL to redirect to and the
// state to set with SetStateCookie. A link flow needs cur, the signed-in
// session principal.
func (m *Manager) Begin(ctx context.Context, name, mode string, cur *auth.Principal, returnTo string) (redirect, state string, err error) {
	if m == nil {
		return "", "", ErrUnknownProvider
	}
	p, ok := m.providers[name]
	if !ok {
		return "", "", ErrUnknownProvider
	}
	params := dbgen.InsertOIDCFlowParams{Provider: name, Mode: mode, ReturnTo: SafeReturnTo(returnTo)}
	switch mode {
	case ModeLogin:
	case ModeLink:
		if cur == nil || cur.Method != auth.MethodSession {
			return "", "", ErrLinkSession
		}
		params.UserID = uuid.NullUUID{UUID: cur.UserID, Valid: true}
		params.SessionID = uuid.NullUUID{UUID: cur.SessionID, Valid: true}
	default:
		return "", "", fmt.Errorf("unknown oidc mode %q", mode)
	}
	op, _, err := m.discover(ctx, p)
	if err != nil {
		return "", "", err
	}
	state = randomString()
	params.StateHash = hashState(state)
	params.Nonce = randomString()
	params.PkceVerifier = oauth2.GenerateVerifier()
	params.ExpiresAt = pgtype.Timestamptz{Time: m.now().Add(FlowTTL), Valid: true}
	if _, err := m.q.DeleteExpiredOIDCFlows(ctx); err != nil {
		return "", "", fmt.Errorf("delete expired oidc flows: %w", err)
	}
	if err := m.q.InsertOIDCFlow(ctx, params); err != nil {
		return "", "", fmt.Errorf("insert oidc flow: %w", err)
	}
	redirect = m.oauth(p, op).AuthCodeURL(state, oidc.Nonce(params.Nonce), oauth2.S256ChallengeOption(params.PkceVerifier))
	return redirect, state, nil
}

func (m *Manager) stateCookieName() string {
	if m.cookies.Secure {
		return "__Host-" + StateCookie
	}
	return StateCookie
}

// SetStateCookie binds a flow to the browser. SameSite=Lax lets the
// browser send it on the top-level redirect back from the provider.
func (m *Manager) SetStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public URL scheme
		Name: m.stateCookieName(), Value: state, Path: "/", MaxAge: int(FlowTTL.Seconds()),
		Secure: m.cookies.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// StateFromRequest returns the state cookie of r.
func (m *Manager) StateFromRequest(r *http.Request) string {
	c, err := r.Cookie(m.stateCookieName())
	if err != nil {
		return ""
	}
	return c.Value
}

// ClearStateCookie removes the state cookie.
func (m *Manager) ClearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public URL scheme
		Name: m.stateCookieName(), Value: "", Path: "/", MaxAge: -1, Secure: m.cookies.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// Callback is the provider redirect back to the server.
type Callback struct {
	Provider string
	// State and Code are the query parameters; Cookie is the state cookie.
	State, Code, Cookie string
	// ProviderError is the error query parameter, if any.
	ProviderError string
	// Current is the signed-in session principal, if any; OldToken the
	// session cookie as sent.
	Current  *auth.Principal
	OldToken string
	Client   identity.Client
}

// Result is a completed flow.
type Result struct {
	Mode     string
	ReturnTo string
	// Login is set for ModeLogin when the session was started.
	Login *identity.LoginResult
	// Pending is set instead of Login when the local second factor is due.
	Pending *mfa.Pending
}

type claims struct {
	subject, issuer, username, email, name string
	emailVerified                          bool
}

// Complete finishes a flow: it checks the state against the cookie, takes
// the stored flow, exchanges the code with the PKCE verifier, verifies the
// ID token and its nonce, then signs in (or registers) or links.
func (m *Manager) Complete(ctx context.Context, cb Callback) (Result, error) {
	p, ok := m.providers[cb.Provider]
	if !ok {
		return Result{}, ErrUnknownProvider
	}
	if cb.State == "" || cb.Cookie == "" || subtle.ConstantTimeCompare([]byte(cb.State), []byte(cb.Cookie)) != 1 {
		return Result{}, ErrInvalidState
	}
	flow, err := m.q.TakeOIDCFlow(ctx, hashState(cb.State))
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrInvalidState
	}
	if err != nil {
		return Result{}, fmt.Errorf("take oidc flow: %w", err)
	}
	if flow.Provider != cb.Provider || !m.now().Before(flow.ExpiresAt.Time) {
		return Result{}, ErrInvalidState
	}
	if cb.ProviderError != "" || cb.Code == "" {
		return Result{}, ErrProvider
	}
	// A link flow must come back to the same session that started it.
	if flow.Mode == ModeLink && (cb.Current == nil || cb.Current.Method != auth.MethodSession ||
		cb.Current.UserID != flow.UserID.UUID || cb.Current.SessionID != flow.SessionID.UUID) {
		return Result{}, ErrLinkSession
	}
	cl, err := m.exchange(ctx, p, cb.Code, flow)
	if err != nil {
		return Result{}, err
	}
	res := Result{Mode: flow.Mode, ReturnTo: SafeReturnTo(flow.ReturnTo)}
	if flow.Mode == ModeLink {
		return res, m.link(ctx, p, cl, flow.UserID.UUID, cb.Client)
	}
	lr, pending, err := m.login(ctx, p, cl, cb)
	if err != nil {
		return Result{}, err
	}
	res.Login, res.Pending = lr, pending
	return res, nil
}

func (m *Manager) exchange(ctx context.Context, p *provider, code string, flow dbgen.OidcFlow) (claims, error) {
	op, verifier, err := m.discover(ctx, p)
	if err != nil {
		return claims{}, err
	}
	tok, err := m.oauth(p, op).Exchange(m.ctx(ctx), code, oauth2.VerifierOption(flow.PkceVerifier))
	if err != nil {
		m.log.WarnContext(ctx, "oidc code exchange failed", "provider", p.cfg.Name, "err", err)
		return claims{}, ErrProvider
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return claims{}, ErrProvider
	}
	idt, err := verifier.Verify(m.ctx(ctx), raw)
	if err != nil {
		m.log.WarnContext(ctx, "oidc id token rejected", "provider", p.cfg.Name, "err", err)
		return claims{}, ErrProvider
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(flow.Nonce)) != 1 {
		m.log.WarnContext(ctx, "oidc nonce mismatch", "provider", p.cfg.Name)
		return claims{}, ErrProvider
	}
	var all map[string]any
	if err := idt.Claims(&all); err != nil {
		return claims{}, ErrProvider
	}
	c := claims{subject: idt.Subject, issuer: idt.Issuer, username: str(all[p.cfg.UsernameClaim]),
		email: str(all[p.cfg.EmailClaim]), name: str(all[p.cfg.NameClaim])}
	switch v := all["email_verified"].(type) {
	case bool:
		c.emailVerified = v
	case string:
		c.emailVerified = v == "true"
	}
	if c.subject == "" || len(c.subject) > 255 {
		return claims{}, ErrProvider
	}
	if len(c.email) > identity.MaxEmailLength {
		c.email, c.emailVerified = "", false
	}
	return c, nil
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func (m *Manager) login(ctx context.Context, p *provider, c claims, cb Callback) (*identity.LoginResult, *mfa.Pending, error) {
	row, err := m.q.GetOIDCLinkBySubject(ctx, dbgen.GetOIDCLinkBySubjectParams{Issuer: c.issuer, Subject: c.subject})
	var userID uuid.UUID
	switch {
	case err == nil:
		userID = row.UserID
		if err := m.q.TouchOIDCLink(ctx, dbgen.TouchOIDCLinkParams{ID: row.ID, Email: infoEmail(c)}); err != nil {
			return nil, nil, fmt.Errorf("touch oidc link: %w", err)
		}
	case errors.Is(err, pgx.ErrNoRows):
		if !p.cfg.AutoRegister {
			return nil, nil, ErrNotLinked
		}
		if m.ids.VerificationRequired() && (c.email == "" || !c.emailVerified) {
			return nil, nil, ErrEmailUnverified
		}
		username := c.username
		if username == "" {
			username = c.email
		}
		userID, err = m.ids.RegisterExternal(ctx, identity.ExternalAccount{Username: username, DisplayName: c.name,
			Email: c.email, EmailVerified: c.emailVerified}, func(q *dbgen.Queries, uid uuid.UUID) error {
			if err := m.insertLink(ctx, q, p, c, uid); err != nil {
				return err
			}
			return audit.Record(ctx, q, audit.Event{Actor: uid, Action: ActionRegister, TargetType: "user", TargetID: uid.String(),
				IP: cb.Client.IP, Data: map[string]any{"provider": p.cfg.Name}})
		}, cb.Client)
		if errors.Is(err, identity.ErrExternalEmailTaken) {
			m.log.WarnContext(ctx, "oidc auto-registration refused: email already in use", "provider", p.cfg.Name)
			return nil, nil, ErrEmailTaken
		}
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("get oidc link: %w", err)
	}
	var lr identity.LoginResult
	method := "oidc:" + p.cfg.Name
	if p.cfg.RequireLocal2FA && m.gate != nil {
		u, err := m.ids.User(ctx, userID)
		if err != nil {
			return nil, nil, err
		}
		out, err := m.gate(ctx, auth.Principal{UserID: u.ID, Username: u.Username, Role: u.Role, Method: auth.MethodSession},
			cb.OldToken, cb.Client, method)
		if err != nil {
			return nil, nil, err
		}
		if out.Pending != nil {
			return nil, out.Pending, nil
		}
		lr = *out.Session
	} else {
		lr, err = m.ids.StartSession(ctx, userID, cb.OldToken, cb.Client, method)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := audit.Record(ctx, m.q, audit.Event{Actor: userID, Action: ActionLogin, TargetType: "session",
		TargetID: lr.Session.ID.String(), IP: cb.Client.IP, Data: map[string]any{"provider": p.cfg.Name}}); err != nil {
		m.log.WarnContext(ctx, "audit oidc login", "err", err)
	}
	return &lr, nil, nil
}

// Allow counts one flow start from ip against the hourly action budget of
// the limiter and reports whether it may proceed, or how long to wait.
func (m *Manager) Allow(ip string) (bool, time.Duration) {
	if m == nil || m.limiter == nil {
		return true, 0
	}
	return m.limiter.AllowAction("oidc:" + ip)
}

// infoEmail is the address stored on the link for display.
func infoEmail(c claims) string {
	if len(c.email) > identity.MaxEmailLength {
		return ""
	}
	return c.email
}

func (m *Manager) insertLink(ctx context.Context, q *dbgen.Queries, p *provider, c claims, userID uuid.UUID) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("link id: %w", err)
	}
	err = q.InsertOIDCLink(ctx, dbgen.InsertOIDCLinkParams{ID: id, UserID: userID, Provider: p.cfg.Name, Issuer: c.issuer,
		Subject: c.subject, Email: infoEmail(c)})
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		if pg.ConstraintName == "oidc_links_user_id_provider_key" {
			return ErrAlreadyLinked
		}
		return ErrLinkedElsewhere
	}
	if err != nil {
		return fmt.Errorf("insert oidc link: %w", err)
	}
	return nil
}

func (m *Manager) link(ctx context.Context, p *provider, c claims, userID uuid.UUID, cl identity.Client) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := m.q.WithTx(tx)
	if err := m.insertLink(ctx, q, p, c, userID); err != nil {
		return err
	}
	if err := audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionLinked, TargetType: "user", TargetID: userID.String(),
		IP: cl.IP, Data: map[string]any{"provider": p.cfg.Name}}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Link is an external identity attached to an account.
type Link struct {
	ID          uuid.UUID
	Provider    string
	Label       string
	Email       string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// Links lists the identities linked to userID.
func (m *Manager) Links(ctx context.Context, userID uuid.UUID) ([]Link, error) {
	rows, err := m.q.ListUserOIDCLinks(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list oidc links: %w", err)
	}
	out := make([]Link, 0, len(rows))
	for _, r := range rows {
		l := Link{ID: r.ID, Provider: r.Provider, Label: r.Provider, Email: r.Email, CreatedAt: r.CreatedAt.Time}
		if p, ok := m.providers[r.Provider]; ok {
			l.Label = p.cfg.Label()
		}
		if r.LastLoginAt.Valid {
			t := r.LastLoginAt.Time
			l.LastLoginAt = &t
		}
		out = append(out, l)
	}
	return out, nil
}

// Unlink removes an identity of userID, unless it is the last way to sign
// in (no password and no other identity).
func (m *Manager) Unlink(ctx context.Context, userID, linkID uuid.UUID, ip string) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := m.q.WithTx(tx)
	if _, err := q.LockUserForUpdate(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	l, err := q.GetUserOIDCLink(ctx, dbgen.GetUserOIDCLinkParams{UserID: userID, LinkID: linkID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get oidc link: %w", err)
	}
	hasPw, err := q.UserHasPassword(ctx, userID)
	if err != nil {
		return fmt.Errorf("user has password: %w", err)
	}
	n, err := q.CountUserOIDCLinks(ctx, userID)
	if err != nil {
		return fmt.Errorf("count oidc links: %w", err)
	}
	if !hasPw && n <= 1 {
		return ErrLastMethod
	}
	if _, err := q.DeleteUserOIDCLink(ctx, dbgen.DeleteUserOIDCLinkParams{UserID: userID, LinkID: linkID}); err != nil {
		return fmt.Errorf("delete oidc link: %w", err)
	}
	if err := audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionUnlinked, TargetType: "user", TargetID: userID.String(),
		IP: ip, Data: map[string]any{"provider": l.Provider}}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
