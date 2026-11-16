// Package apps implements Apps in the style of GitHub Apps (APPS-PLAN A2):
// registration by users and organizations, the bot account slug[bot] each
// App acts as, RS256 private keys and the App JWT, installations on user
// and organization accounts, installation access tokens (erps_…) and the
// installation webhook events. The permission rules are in authz/app.go;
// the GitHub-compatible endpoints are in ghapi, the management API in
// httpapi.
package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/bots"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
)

// Audit actions.
const (
	ActionAppCreated            = "app.created"
	ActionAppUpdated            = "app.updated"
	ActionAppDeleted            = "app.deleted"
	ActionAppKeyCreated         = "app.key_created"
	ActionAppKeyRevoked         = "app.key_revoked"
	ActionInstallationCreated   = "app_installation.created"
	ActionInstallationDeleted   = "app_installation.deleted"
	ActionInstallationRepos     = "app_installation.repositories_changed"
	ActionInstallationSuspended = "app_installation.suspended"
	ActionInstallationResumed   = "app_installation.unsuspended"
	ActionInstallationAccepted  = "app_installation.permissions_accepted"
	ActionInstallationToken     = "app_installation.token_created" //nolint:gosec // audit action name, not a secret
)

// Limits.
const (
	MaxAppsPerOwner = 100
	MaxKeys         = 10
	MaxCallbackURLs = 10
	maxURLLen       = 2000
	maxDescription  = 1000
	maxNameRunes    = 100
	maxSlugLen      = 34
	// TokenTTL is the lifetime of an installation access token.
	TokenTTL = time.Hour
	// jwtMaxTTL bounds the lifetime of an App JWT, jwtSkew the clock drift
	// tolerated on iat and exp.
	jwtMaxTTL = 10 * time.Minute
	jwtSkew   = 60 * time.Second
)

// Errors.
var (
	ErrNotFound  = errors.New("app not found")
	ErrForbidden = errors.New("not allowed")
	ErrConflict  = errors.New("conflict")
	ErrTooMany   = errors.New("limit reached")
	// ErrSuspended means the installation is suspended.
	ErrSuspended = errors.New("this installation has been suspended")
	// ErrBadJWT means the App JWT is malformed, expired or not signed by a
	// key of the App.
	ErrBadJWT = errors.New("invalid app JWT")
)

// InputError is an invalid field; the message is shown.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func inputErr(format string, a ...any) error { return &InputError{Msg: fmt.Sprintf(format, a...)} }

// Owner kinds.
const (
	OwnerUser = "user"
	OwnerOrg  = "org"
)

// Repository selections of an installation.
const (
	SelectionAll      = "all"
	SelectionSelected = "selected"
)

// Options configure the service.
type Options struct {
	Pool *pgxpool.Pool
	// Webhooks stores the App hooks and delivers the installation events;
	// nil disables both.
	Webhooks *webhooks.Service
	// AppRegistration is security.app_registration.
	AppRegistration string
	Log             *slog.Logger
	Now             func() time.Time
}

// Service manages Apps and their installations.
type Service struct {
	pool  *pgxpool.Pool
	q     *dbgen.Queries
	hooks *webhooks.Service
	reg   string
	log   *slog.Logger
	now   func() time.Time
}

// New builds the service.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{pool: o.Pool, q: dbgen.New(o.Pool), hooks: o.Webhooks, reg: o.AppRegistration, log: o.Log, now: o.Now}
}

func (s *Service) tx(ctx context.Context, fn func(*dbgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("new id: %w", err)
	}
	return id, nil
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func pgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func decodePerms(b []byte) authz.AppPermissions {
	p := authz.AppPermissions{}
	_ = json.Unmarshal(b, &p)
	p[authz.AppMetadata] = authz.AppRead
	return p
}

func encodePerms(p authz.AppPermissions) []byte {
	b, err := json.Marshal(p)
	if err != nil {
		return []byte(`{"metadata":"read"}`)
	}
	return b
}

// App is a registered App.
type App struct {
	ID           uuid.UUID
	NumID        int64
	Slug         string
	Name         string
	Description  string
	HomepageURL  string
	LogoURL      string
	Public       bool
	OwnerID      uuid.UUID
	OwnerKind    string
	OwnerName    string
	BotID        uuid.UUID
	BotLogin     string
	OAuthAppID   uuid.UUID
	ClientID     string
	CallbackURLs []string
	Permissions  authz.AppPermissions
	Events       []string
	SetupURL     string
	// Installations counts the installations (lists and Get only).
	Installations int
	CreatedAt     time.Time
	UpdatedAt     time.Time

	// Filled for the managers of the App by Get.
	Manageable    bool
	Keys          []Key
	Webhook       *webhooks.Hook
	ClientSecrets []ClientSecret
}

// Key is a private key of an App, as listed (never the key).
type Key struct {
	ID          uuid.UUID
	Fingerprint string
	CreatedBy   string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
}

// ClientSecret is a client secret of the App's OAuth client, as listed.
type ClientSecret struct {
	ID         uuid.UUID
	Hint       string
	CreatedBy  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

func appFrom(r dbgen.GetAppByIDRow) App {
	return App{ID: r.ID, NumID: r.NumID.Int64, Slug: r.Slug, Name: r.Name, Description: r.Description,
		HomepageURL: r.HomepageUrl, LogoURL: r.LogoUrl, Public: r.Public, OwnerID: r.OwnerID, OwnerKind: r.OwnerKind,
		OwnerName: r.OwnerName, BotID: r.BotID, BotLogin: r.BotLogin, OAuthAppID: r.OauthAppID, ClientID: r.ClientID,
		CallbackURLs: nonNil(r.CallbackUrls), Permissions: decodePerms(r.Permissions), Events: nonNil(r.Events),
		SetupURL: r.SetupUrl, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Service) appByID(ctx context.Context, q *dbgen.Queries, id uuid.UUID) (App, error) {
	r, err := q.GetAppByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, fmt.Errorf("get app: %w", err)
	}
	return appFrom(r), nil
}

func (s *Service) appBySlug(ctx context.Context, slug string) (App, error) {
	if slug == "" || len(slug) > 100 {
		return App{}, ErrNotFound
	}
	r, err := s.q.GetAppBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, fmt.Errorf("get app: %w", err)
	}
	return appFrom(dbgen.GetAppByIDRow(r)), nil
}

// orgRole returns the role of userID in the organization orgID ("" when
// not a member).
func orgRole(ctx context.Context, q *dbgen.Queries, orgID, userID uuid.UUID) (string, error) {
	role, err := q.GetOrgMemberRole(ctx, dbgen.GetOrgMemberRoleParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get org role: %w", err)
	}
	return role, nil
}

// managesAccount reports whether p administers the account id of kind:
// the user itself, or an owner of the organization. Instance admins
// manage every account.
func (s *Service) managesAccount(ctx context.Context, p auth.Principal, id uuid.UUID, kind string) (bool, error) {
	if p.UserID == uuid.Nil || p.Automated() {
		return false, nil
	}
	if p.IsAdmin() {
		return true, nil
	}
	switch kind {
	case OwnerUser:
		return id == p.UserID, nil
	case OwnerOrg:
		role, err := orgRole(ctx, s.q, id, p.UserID)
		return role == string(authz.OrgOwner), err
	}
	return false, nil
}

// manages reports whether p manages app.
func (s *Service) manages(ctx context.Context, p auth.Principal, app App) (bool, error) {
	if p.UserID == uuid.Nil || p.Automated() {
		return false, nil
	}
	switch app.OwnerKind {
	case OwnerUser:
		return app.OwnerID == p.UserID, nil
	case OwnerOrg:
		role, err := orgRole(ctx, s.q, app.OwnerID, p.UserID)
		return role == string(authz.OrgOwner), err
	}
	return false, nil
}

// managed returns the App slug when p manages it, ErrNotFound otherwise.
func (s *Service) managed(ctx context.Context, p auth.Principal, slug string) (App, error) {
	app, err := s.appBySlug(ctx, slug)
	if err != nil {
		return App{}, err
	}
	ok, err := s.manages(ctx, p, app)
	if err != nil {
		return App{}, err
	}
	if !ok {
		return App{}, ErrNotFound
	}
	return app, nil
}

// account resolves an account name to its id and kind (user or org).
func (s *Service) account(ctx context.Context, name string) (uuid.UUID, string, string, error) {
	ns, err := s.q.GetNamespaceByName(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ns.Kind != OwnerUser && ns.Kind != OwnerOrg) {
		return uuid.Nil, "", "", ErrNotFound
	}
	if err != nil {
		return uuid.Nil, "", "", fmt.Errorf("get namespace: %w", err)
	}
	return ns.ID, ns.Kind, ns.Name, nil
}

// orgOwned resolves an organization p owns.
func (s *Service) orgOwned(ctx context.Context, p auth.Principal, org string) (uuid.UUID, string, error) {
	id, kind, name, err := s.account(ctx, org)
	if err != nil {
		return uuid.Nil, "", err
	}
	if kind != OwnerOrg {
		return uuid.Nil, "", ErrNotFound
	}
	role, err := orgRole(ctx, s.q, id, p.UserID)
	if err != nil {
		return uuid.Nil, "", err
	}
	if role == "" {
		return uuid.Nil, "", ErrNotFound
	}
	if role != string(authz.OrgOwner) {
		return uuid.Nil, "", ErrForbidden
	}
	return id, name, nil
}

// mayRegister applies security.app_registration.
func (s *Service) mayRegister(p auth.Principal, org bool) bool {
	if p.Role == auth.RoleRestricted || p.Automated() || p.UserID == uuid.Nil {
		return false
	}
	switch s.reg {
	case config.AppRegistrationAdmins:
		return p.IsAdmin()
	case config.AppRegistrationOrgOwners:
		return org || p.IsAdmin()
	}
	return true
}

// Input holds the fields of a new or edited App; nil fields keep their
// value on edit.
type Input struct {
	Name         *string
	Slug         *string // create only; derived from the name when nil
	Description  *string
	HomepageURL  *string
	LogoURL      *string
	Public       *bool
	Permissions  authz.AppPermissions // nil keeps them
	Events       []string             // nil keeps them
	SetupURL     *string
	CallbackURLs []string // nil keeps them
	// WebhookURL "" removes the hook; WebhookSecret "" removes the secret.
	WebhookURL    *string
	WebhookSecret *string
	WebhookActive *bool
}

type fields struct {
	name, description, homepage, logo, setup string
	public                                   bool
	perms                                    authz.AppPermissions
	events, callbacks                        []string
}

var slugRe = regexp.MustCompile(`^[a-z0-9](-?[a-z0-9])*$`)

// Slugify derives a slug from an App name: lower case, runs of other
// characters become a dash.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	out := b.String()
	if len(out) > maxSlugLen {
		out = strings.TrimRight(out[:maxSlugLen], "-")
	}
	return out
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func webURL(s string) bool {
	if len(s) > maxURLLen || strings.ContainsFunc(s, isControl) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

var blockedSchemes = []string{"javascript", "data", "vbscript", "file", "blob", "about"}

func callbackURL(s string) bool {
	if len(s) > maxURLLen || strings.ContainsFunc(s, isControl) || strings.Contains(s, "#") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.User != nil || slices.Contains(blockedSchemes, strings.ToLower(u.Scheme)) {
		return false
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.Host != ""
	}
	return u.Host != "" || u.Path != ""
}

// apply validates in over cur.
func (in Input) apply(cur fields) (fields, error) {
	out := cur
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || utf8.RuneCountInString(n) > maxNameRunes || !utf8.ValidString(n) || strings.ContainsFunc(n, isControl) {
			return out, inputErr("the name must be 1 to %d characters", maxNameRunes)
		}
		out.name = n
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if utf8.RuneCountInString(d) > maxDescription || !utf8.ValidString(d) {
			return out, inputErr("the description must be at most %d characters", maxDescription)
		}
		out.description = d
	}
	if in.HomepageURL != nil {
		h := strings.TrimSpace(*in.HomepageURL)
		if !webURL(h) {
			return out, inputErr("the homepage URL must be an absolute http or https URL")
		}
		out.homepage = h
	}
	if in.LogoURL != nil {
		l := strings.TrimSpace(*in.LogoURL)
		if l != "" && !webURL(l) {
			return out, inputErr("the logo URL must be an absolute http or https URL")
		}
		out.logo = l
	}
	if in.SetupURL != nil {
		u := strings.TrimSpace(*in.SetupURL)
		if u != "" && !webURL(u) {
			return out, inputErr("the setup URL must be an absolute http or https URL")
		}
		out.setup = u
	}
	if in.Public != nil {
		out.public = *in.Public
	}
	if in.CallbackURLs != nil {
		var cbs []string
		for _, c := range in.CallbackURLs {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if !callbackURL(c) {
				return out, inputErr("%q is not a valid callback URL: use an absolute URL without a fragment", c)
			}
			if !slices.Contains(cbs, c) {
				cbs = append(cbs, c)
			}
		}
		if len(cbs) > MaxCallbackURLs {
			return out, inputErr("at most %d callback URLs", MaxCallbackURLs)
		}
		out.callbacks = nonNil(cbs)
	}
	if in.Permissions != nil {
		p, err := in.Permissions.Normalize()
		if err != nil {
			return out, inputErr("%s", err.Error())
		}
		out.perms = p
	}
	if in.Events != nil {
		var evs []string
		for _, e := range in.Events {
			e = strings.TrimSpace(e)
			if !slices.Contains(authz.AppEvents, e) {
				return out, inputErr("unknown event %q", e)
			}
			if !slices.Contains(evs, e) {
				evs = append(evs, e)
			}
		}
		out.events = nonNil(evs)
	}
	// Events are limited by the permissions.
	for _, e := range out.events {
		if !authz.AppEventAllowed(out.perms, e) {
			return out, inputErr("the event %q needs the %s permission", e, authz.AppEventPermission(e))
		}
	}
	return out, nil
}

// ListUserApps returns the Apps owned by the account of p.
func (s *Service) ListUserApps(ctx context.Context, p auth.Principal) ([]App, error) {
	return s.listByOwner(ctx, p.UserID)
}

// ListOrgApps returns the Apps of an organization p owns.
func (s *Service) ListOrgApps(ctx context.Context, p auth.Principal, org string) ([]App, error) {
	id, _, err := s.orgOwned(ctx, p, org)
	if err != nil {
		return nil, err
	}
	return s.listByOwner(ctx, id)
}

func (s *Service) listByOwner(ctx context.Context, owner uuid.UUID) ([]App, error) {
	rows, err := s.q.ListAppsByOwner(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	out := make([]App, 0, len(rows))
	for _, r := range rows {
		a := appFrom(dbgen.GetAppByIDRow{ID: r.ID, NumID: r.NumID, Slug: r.Slug, OwnerID: r.OwnerID, OauthAppID: r.OauthAppID,
			BotID: r.BotID, Name: r.Name, NameLower: r.NameLower, Description: r.Description, HomepageUrl: r.HomepageUrl,
			LogoUrl: r.LogoUrl, Public: r.Public, Permissions: r.Permissions, Events: r.Events, SetupUrl: r.SetupUrl,
			CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, OwnerKind: r.OwnerKind,
			OwnerName: r.OwnerName, ClientID: r.ClientID, CallbackUrls: r.CallbackUrls, BotLogin: r.BotLogin})
		a.Installations = int(r.Installations)
		a.Manageable = true
		out = append(out, a)
	}
	return out, nil
}

// Create registers an App owned by p (org "") or by the organization org,
// which p must own. It creates the bot account slug[bot] and the App's
// OAuth client.
func (s *Service) Create(ctx context.Context, p auth.Principal, org string, in Input, ip string) (App, error) {
	ownerID, kind, ownerName, auditOrg := p.UserID, OwnerUser, p.Username, uuid.Nil
	if org != "" {
		id, name, err := s.orgOwned(ctx, p, org)
		if err != nil {
			return App{}, err
		}
		ownerID, kind, ownerName, auditOrg = id, OwnerOrg, name, id
	}
	if !s.mayRegister(p, org != "") {
		return App{}, ErrForbidden
	}
	if in.Name == nil || in.HomepageURL == nil {
		return App{}, inputErr("name and homepage URL are required")
	}
	f, err := in.apply(fields{perms: authz.AppPermissions{authz.AppMetadata: authz.AppRead}, events: []string{},
		callbacks: []string{}})
	if err != nil {
		return App{}, err
	}
	slug := Slugify(f.name)
	if in.Slug != nil && strings.TrimSpace(*in.Slug) != "" {
		slug = strings.ToLower(strings.TrimSpace(*in.Slug))
	}
	if len(slug) > maxSlugLen || !slugRe.MatchString(slug) {
		return App{}, inputErr("the slug must be 1 to %d lower case letters, digits and single dashes", maxSlugLen)
	}
	appID, err := newID()
	if err != nil {
		return App{}, err
	}
	botID, err := newID()
	if err != nil {
		return App{}, err
	}
	oauthID, err := newID()
	if err != nil {
		return App{}, err
	}
	clientID := newClientID()
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if n, err := q.CountAppsByOwner(ctx, ownerID); err != nil {
			return fmt.Errorf("count apps: %w", err)
		} else if n >= MaxAppsPerOwner {
			return ErrTooMany
		}
		login := slug + bots.Suffix
		if err := q.InsertBotNamespace(ctx, dbgen.InsertBotNamespaceParams{ID: botID, Name: login}); err != nil {
			if isUniqueViolation(err) {
				return inputErr("the slug %q is taken", slug)
			}
			return fmt.Errorf("insert bot namespace: %w", err)
		}
		if err := q.InsertBotUser(ctx, dbgen.InsertBotUserParams{ID: botID, DisplayName: f.name}); err != nil {
			return fmt.Errorf("insert bot user: %w", err)
		}
		if err := q.InsertOAuthApp(ctx, dbgen.InsertOAuthAppParams{ID: oauthID, ClientID: clientID,
			OwnerID: uuid.NullUUID{UUID: ownerID, Valid: true}, Name: f.name, Description: f.description,
			HomepageUrl: f.homepage, LogoUrl: f.logo, CallbackUrls: f.callbacks,
			CreatedBy: uuid.NullUUID{UUID: p.UserID, Valid: true}}); err != nil {
			return fmt.Errorf("insert app oauth client: %w", err)
		}
		_, err := q.InsertApp(ctx, dbgen.InsertAppParams{ID: appID, Slug: slug, OwnerID: ownerID, OauthAppID: oauthID,
			BotID: botID, Name: f.name, Description: f.description, HomepageUrl: f.homepage, LogoUrl: f.logo,
			Public: f.public, Permissions: encodePerms(f.perms), Events: f.events, SetupUrl: f.setup,
			CreatedBy: uuid.NullUUID{UUID: p.UserID, Valid: true}})
		if isUniqueViolation(err) {
			return inputErr("an App with this name or slug exists")
		}
		if err != nil {
			return fmt.Errorf("insert app: %w", err)
		}
		if err := q.InsertAppBot(ctx, dbgen.InsertAppBotParams{UserID: botID, AppID: uuid.NullUUID{UUID: appID, Valid: true},
			Description: botDescription(f.name)}); err != nil {
			return fmt.Errorf("insert app bot: %w", err)
		}
		if err := s.saveHook(ctx, q, appID, p.UserID, in, nil); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionAppCreated, TargetType: "app",
			TargetID: appID.String(), IP: ip, OrgID: auditOrg,
			Data: map[string]any{"slug": slug, "name": f.name, "owner": ownerName, "permissions": f.perms}})
	})
	if err != nil {
		return App{}, err
	}
	s.log.InfoContext(ctx, "app created", "slug", slug, "owner", ownerName, "owner_kind", kind, "user_id", p.UserID)
	return s.Get(ctx, p, slug)
}

func botDescription(name string) string {
	d := "Bot account of the App " + name + "."
	if r := []rune(d); len(r) > 1000 {
		d = string(r[:1000])
	}
	return d
}

// saveHook applies the webhook fields of in; cur is the current hook.
func (s *Service) saveHook(ctx context.Context, q *dbgen.Queries, appID, by uuid.UUID, in Input, cur *webhooks.Hook) error {
	if s.hooks == nil || (in.WebhookURL == nil && in.WebhookSecret == nil && in.WebhookActive == nil) {
		return nil
	}
	u, active := "", true
	if cur != nil {
		u, active = cur.URL, cur.Active
	}
	if in.WebhookURL != nil {
		u = strings.TrimSpace(*in.WebhookURL)
	}
	if in.WebhookActive != nil {
		active = *in.WebhookActive
	}
	err := s.hooks.SetAppHook(ctx, q, appID, by, u, in.WebhookSecret, active)
	var ve *webhooks.ValidationError
	if errors.As(err, &ve) {
		return inputErr("webhook: %s", ve.Error())
	}
	if errors.Is(err, webhooks.ErrNoMasterKey) {
		return inputErr("webhook secrets need security.master_key_file on this instance")
	}
	return err
}

// Get returns an App: public Apps to everyone, private ones to their
// managers only. Managers also get the keys, the webhook and the client
// secrets.
func (s *Service) Get(ctx context.Context, p auth.Principal, slug string) (App, error) {
	app, err := s.appBySlug(ctx, slug)
	if err != nil {
		return App{}, err
	}
	ok, err := s.manages(ctx, p, app)
	if err != nil {
		return App{}, err
	}
	if !ok && !app.Public {
		return App{}, ErrNotFound
	}
	n, err := s.q.CountAppInstallations(ctx, app.ID)
	if err != nil {
		return App{}, fmt.Errorf("count installations: %w", err)
	}
	app.Installations = int(n)
	if !ok {
		return app, nil
	}
	app.Manageable = true
	keys, err := s.q.ListAppKeys(ctx, app.ID)
	if err != nil {
		return App{}, fmt.Errorf("list app keys: %w", err)
	}
	app.Keys = make([]Key, 0, len(keys))
	for _, k := range keys {
		app.Keys = append(app.Keys, Key{ID: k.ID, Fingerprint: k.Fingerprint, CreatedBy: k.CreatedByName,
			CreatedAt: k.CreatedAt.Time, LastUsedAt: tsPtr(k.LastUsedAt)})
	}
	secrets, err := s.q.ListOAuthAppSecrets(ctx, app.OAuthAppID)
	if err != nil {
		return App{}, fmt.Errorf("list client secrets: %w", err)
	}
	app.ClientSecrets = make([]ClientSecret, 0, len(secrets))
	for _, c := range secrets {
		app.ClientSecrets = append(app.ClientSecrets, ClientSecret{ID: c.ID, Hint: c.Hint, CreatedBy: c.CreatedByName,
			CreatedAt: c.CreatedAt.Time, LastUsedAt: tsPtr(c.LastUsedAt)})
	}
	if s.hooks != nil {
		if app.Webhook, err = s.hooks.AppHook(ctx, app.ID); err != nil {
			return App{}, err
		}
	}
	return app, nil
}

func orgOf(app App) uuid.UUID {
	if app.OwnerKind == OwnerOrg {
		return app.OwnerID
	}
	return uuid.Nil
}

// Update edits an App p manages. Permissions that the App drops are taken
// away from its installations at once; new ones wait for the approval of
// each installation's owner.
func (s *Service) Update(ctx context.Context, p auth.Principal, slug string, in Input, ip string) (App, error) {
	app, err := s.managed(ctx, p, slug)
	if err != nil {
		return App{}, err
	}
	f, err := in.apply(fields{name: app.Name, description: app.Description, homepage: app.HomepageURL, logo: app.LogoURL,
		setup: app.SetupURL, public: app.Public, perms: app.Permissions, events: app.Events, callbacks: app.CallbackURLs})
	if err != nil {
		return App{}, err
	}
	var cur *webhooks.Hook
	if s.hooks != nil {
		if cur, err = s.hooks.AppHook(ctx, app.ID); err != nil {
			return App{}, err
		}
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.LockApp(ctx, app.ID); err != nil {
			return fmt.Errorf("lock app: %w", err)
		}
		err := q.UpdateApp(ctx, dbgen.UpdateAppParams{ID: app.ID, Name: f.name, Description: f.description,
			HomepageUrl: f.homepage, LogoUrl: f.logo, Public: f.public, Permissions: encodePerms(f.perms),
			Events: f.events, SetupUrl: f.setup})
		if isUniqueViolation(err) {
			return inputErr("an App with this name exists")
		}
		if err != nil {
			return fmt.Errorf("update app: %w", err)
		}
		if err := q.UpdateAppOAuthClient(ctx, dbgen.UpdateAppOAuthClientParams{ID: app.OAuthAppID, Name: f.name,
			Description: f.description, HomepageUrl: f.homepage, LogoUrl: f.logo, CallbackUrls: f.callbacks}); err != nil {
			return fmt.Errorf("update app oauth client: %w", err)
		}
		if err := q.UpdateBotDisplayName(ctx, dbgen.UpdateBotDisplayNameParams{ID: app.BotID, DisplayName: f.name}); err != nil {
			return fmt.Errorf("update bot: %w", err)
		}
		if err := q.UpdateBotDescription(ctx, dbgen.UpdateBotDescriptionParams{UserID: app.BotID,
			Description: botDescription(f.name)}); err != nil {
			return fmt.Errorf("update bot: %w", err)
		}
		if err := narrowInstallations(ctx, q, app.ID, f.perms, f.events); err != nil {
			return err
		}
		if err := s.saveHook(ctx, q, app.ID, p.UserID, in, cur); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionAppUpdated, TargetType: "app",
			TargetID: app.ID.String(), IP: ip, OrgID: orgOf(app),
			Data: map[string]any{"slug": app.Slug, "permissions": f.perms, "events": f.events}})
	})
	if err != nil {
		return App{}, err
	}
	return s.Get(ctx, p, slug)
}

// narrowInstallations drops from every installation of an App the
// permissions and events the App no longer has.
func narrowInstallations(ctx context.Context, q *dbgen.Queries, appID uuid.UUID, perms authz.AppPermissions, events []string) error {
	rows, err := q.ListInstallationGrants(ctx, appID)
	if err != nil {
		return fmt.Errorf("list installation grants: %w", err)
	}
	for _, r := range rows {
		cur := decodePerms(r.Permissions)
		np := cur.Intersect(perms)
		var ne []string
		for _, e := range r.Events {
			if slices.Contains(events, e) && authz.AppEventAllowed(np, e) {
				ne = append(ne, e)
			}
		}
		if np.Equal(cur) && len(ne) == len(r.Events) {
			continue
		}
		if err := q.SetInstallationGrants(ctx, dbgen.SetInstallationGrantsParams{ID: r.ID, Permissions: encodePerms(np),
			Events: nonNil(ne)}); err != nil {
			return fmt.Errorf("narrow installation: %w", err)
		}
	}
	return nil
}

// Delete deletes an App p manages, with its installations, keys, tokens,
// hook and OAuth client. Its bot account stays, so that what it wrote
// keeps its author; the slug stays taken by it.
func (s *Service) Delete(ctx context.Context, p auth.Principal, slug, ip string) error {
	app, err := s.managed(ctx, p, slug)
	if err != nil {
		return err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		oauthID, err := q.DeleteApp(ctx, app.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("delete app: %w", err)
		}
		if err := q.DeleteOAuthAppOfApp(ctx, oauthID); err != nil {
			return fmt.Errorf("delete app oauth client: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionAppDeleted, TargetType: "app",
			TargetID: app.ID.String(), IP: ip, OrgID: orgOf(app), Data: map[string]any{"slug": app.Slug, "name": app.Name}})
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "app deleted", "slug", app.Slug, "user_id", p.UserID)
	return nil
}

// HookScope returns the webhook scope of an App p manages and the id of
// its hook (uuid.Nil without one), for the recent deliveries.
func (s *Service) HookScope(ctx context.Context, p auth.Principal, slug string) (webhooks.Scope, uuid.UUID, error) {
	app, err := s.managed(ctx, p, slug)
	if err != nil {
		return webhooks.Scope{}, uuid.Nil, err
	}
	if s.hooks == nil {
		return webhooks.AppScope(app.ID), uuid.Nil, nil
	}
	h, err := s.hooks.AppHook(ctx, app.ID)
	if err != nil {
		return webhooks.Scope{}, uuid.Nil, err
	}
	if h == nil {
		return webhooks.AppScope(app.ID), uuid.Nil, nil
	}
	return webhooks.AppScope(app.ID), h.ID, nil
}

// UserAppGrant implements auth.AppTokenLookup: the App behind a
// user-to-server token.
func (s *Service) UserAppGrant(ctx context.Context, appID uuid.UUID) (*auth.UserAppGrant, error) {
	b, err := s.q.GetAppPermissions(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrNoToken
	}
	if err != nil {
		return nil, fmt.Errorf("get app permissions: %w", err)
	}
	return &auth.UserAppGrant{AppID: appID, Permissions: decodePerms(b)}, nil
}
