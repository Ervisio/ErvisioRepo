package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// OAuth apps (APPS-PLAN A1): registration by users and organizations,
// client secrets and the grants users give them. The web flow and the
// token endpoint are in oauthweb.go, the device flow in device.go.

// Audit actions of the OAuth apps.
const (
	ActionOAuthAppCreated    = "oauth_app.created"
	ActionOAuthAppUpdated    = "oauth_app.updated"
	ActionOAuthAppDeleted    = "oauth_app.deleted"
	ActionOAuthSecretCreated = "oauth_app.secret_created" //nolint:gosec // audit action name, not a secret
	ActionOAuthSecretRevoked = "oauth_app.secret_revoked" //nolint:gosec // audit action name, not a secret
	ActionOAuthGrantCreated  = "oauth.grant_created"
	ActionOAuthGrantRevoked  = "oauth.grant_revoked"
)

// Limits of the OAuth apps.
const (
	MaxOAuthAppsPerOwner = 100
	MaxOAuthAppSecrets   = 5
	MaxCallbackURLs      = 10
	// maxGrantTokens is the number of live tokens kept per grant; older
	// ones are revoked when a new one is issued (GitHub keeps ten).
	maxGrantTokens = 10
	maxURLLen      = 2000
	maxDescription = 1000
)

// Owner kinds of an OAuth app.
const (
	OAuthOwnerUser   = "user"
	OAuthOwnerOrg    = "org"
	OAuthOwnerSystem = "system"
)

// ErrForbidden means the caller may not do this (registration policy,
// organization owner required).
var ErrForbidden = errors.New("not allowed")

// AppInputError is an invalid field of an OAuth app; the message is shown.
type AppInputError struct{ Msg string }

func (e *AppInputError) Error() string { return e.Msg }

func inputErr(format string, a ...any) error { return &AppInputError{Msg: fmt.Sprintf(format, a...)} }

// OAuthApp is a registered OAuth app or a built-in client.
type OAuthApp struct {
	ID           uuid.UUID
	ClientID     string
	Name         string
	Description  string
	HomepageURL  string
	LogoURL      string
	CallbackURLs []string
	DeviceFlow   bool
	// OwnerKind is OAuthOwnerUser, OAuthOwnerOrg or OAuthOwnerSystem.
	OwnerKind string
	OwnerName string
	OwnerID   uuid.UUID
	Users     int
	// Secrets is filled by GetOAuthApp only.
	Secrets   []OAuthAppSecret
	CreatedAt time.Time
	UpdatedAt time.Time

	short string
}

// System reports whether the app is a built-in client.
func (a OAuthApp) System() bool { return a.OwnerKind == OAuthOwnerSystem }

// OAuthAppSecret is a client secret as listed (never the secret).
type OAuthAppSecret struct {
	ID         uuid.UUID
	Hint       string
	CreatedBy  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// OAuthAppInput holds the fields of a new or edited app; nil fields keep
// their value on edit.
type OAuthAppInput struct {
	Name         *string
	Description  *string
	HomepageURL  *string
	LogoURL      *string
	CallbackURLs []string // nil keeps them
	DeviceFlow   *bool
}

func appFromRow(r dbgen.GetOAuthAppByClientIDRow) OAuthApp {
	kind := r.OwnerKind
	if r.IsSystem {
		kind = OAuthOwnerSystem
	}
	return OAuthApp{ID: r.ID, ClientID: r.ClientID, Name: r.Name, Description: r.Description, HomepageURL: r.HomepageUrl,
		LogoURL: r.LogoUrl, CallbackURLs: r.CallbackUrls, DeviceFlow: r.DeviceFlow, OwnerKind: kind, OwnerName: r.OwnerName,
		OwnerID: r.OwnerID.UUID, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, short: r.ShortName}
}

// lookupApp returns the app with clientID, or ErrNotFound.
func lookupApp(ctx context.Context, q *dbgen.Queries, clientID string) (OAuthApp, error) {
	if clientID == "" || len(clientID) > 100 {
		return OAuthApp{}, ErrNotFound
	}
	row, err := q.GetOAuthAppByClientID(ctx, clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthApp{}, ErrNotFound
	}
	if err != nil {
		return OAuthApp{}, fmt.Errorf("get oauth app: %w", err)
	}
	return appFromRow(row), nil
}

// isOrgOwner reports whether userID owns the organization orgID.
func isOrgOwner(ctx context.Context, q *dbgen.Queries, orgID, userID uuid.UUID) (bool, error) {
	role, err := q.GetOrgMemberRole(ctx, dbgen.GetOrgMemberRoleParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get org role: %w", err)
	}
	return role == "owner", nil
}

// canManage reports whether p manages app: its owner user, or an owner of
// its organization. Built-in clients are not managed.
func (s *Service) canManage(ctx context.Context, p auth.Principal, app OAuthApp) (bool, error) {
	switch app.OwnerKind {
	case OAuthOwnerUser:
		return app.OwnerID == p.UserID, nil
	case OAuthOwnerOrg:
		return isOrgOwner(ctx, s.q, app.OwnerID, p.UserID)
	}
	return false, nil
}

// managedApp returns the app with clientID when p manages it; otherwise
// ErrNotFound, so that other people's apps are not disclosed.
func (s *Service) managedApp(ctx context.Context, p auth.Principal, clientID string) (OAuthApp, error) {
	app, err := lookupApp(ctx, s.q, clientID)
	if err != nil {
		return OAuthApp{}, err
	}
	ok, err := s.canManage(ctx, p, app)
	if err != nil {
		return OAuthApp{}, err
	}
	if !ok {
		return OAuthApp{}, ErrNotFound
	}
	return app, nil
}

// orgFor resolves an organization that p owns.
func (s *Service) orgFor(ctx context.Context, p auth.Principal, org string) (uuid.UUID, error) {
	ns, err := s.q.GetNamespaceByName(ctx, org)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ns.Kind != "org") {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get namespace: %w", err)
	}
	ok, err := isOrgOwner(ctx, s.q, ns.ID, p.UserID)
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, ErrForbidden
	}
	return ns.ID, nil
}

// mayRegister applies security.app_registration to a new app of the
// caller (org false) or of an organization it owns (org true).
func (s *Service) mayRegister(p auth.Principal, org bool) bool {
	if p.Role == auth.RoleRestricted {
		return false
	}
	switch s.appRegistration {
	case config.AppRegistrationAdmins:
		return p.IsAdmin()
	case config.AppRegistrationOrgOwners:
		return org || p.IsAdmin()
	}
	return true
}

func listedApps(rows []dbgen.ListOAuthAppsByOwnerRow) []OAuthApp {
	out := make([]OAuthApp, 0, len(rows))
	for _, r := range rows {
		a := appFromRow(dbgen.GetOAuthAppByClientIDRow{ID: r.ID, ClientID: r.ClientID, OwnerID: r.OwnerID, IsSystem: r.IsSystem,
			Name: r.Name, ShortName: r.ShortName, Description: r.Description, HomepageUrl: r.HomepageUrl, LogoUrl: r.LogoUrl,
			CallbackUrls: r.CallbackUrls, DeviceFlow: r.DeviceFlow, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt, OwnerKind: r.OwnerKind, OwnerName: r.OwnerName})
		a.Users = int(r.Users)
		out = append(out, a)
	}
	return out
}

// ListUserOAuthApps returns the apps owned by the account userID.
func (s *Service) ListUserOAuthApps(ctx context.Context, userID uuid.UUID) ([]OAuthApp, error) {
	rows, err := s.q.ListOAuthAppsByOwner(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list oauth apps: %w", err)
	}
	return listedApps(rows), nil
}

// ListOrgOAuthApps returns the apps of an organization p owns.
func (s *Service) ListOrgOAuthApps(ctx context.Context, p auth.Principal, org string) ([]OAuthApp, error) {
	orgID, err := s.orgFor(ctx, p, org)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListOAuthAppsByOwner(ctx, uuid.NullUUID{UUID: orgID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list oauth apps: %w", err)
	}
	return listedApps(rows), nil
}

// appFields is the validated content of an app.
type appFields struct {
	name, description, homepage, logo string
	callbacks                         []string
	deviceFlow                        bool
}

// apply validates in over cur.
func (in OAuthAppInput) apply(cur appFields) (appFields, error) {
	out := cur
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || utf8.RuneCountInString(n) > MaxNameRune || !utf8.ValidString(n) || strings.ContainsFunc(n, isControl) {
			return out, inputErr("the name must be 1 to 100 characters")
		}
		out.name = n
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if utf8.RuneCountInString(d) > maxDescription || !utf8.ValidString(d) {
			return out, inputErr("the description must be at most 1000 characters")
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
		if len(cbs) == 0 || len(cbs) > MaxCallbackURLs {
			return out, inputErr("give 1 to %d callback URLs", MaxCallbackURLs)
		}
		out.callbacks = cbs
	}
	if in.DeviceFlow != nil {
		out.deviceFlow = *in.DeviceFlow
	}
	return out, nil
}

// webURL reports whether s is an absolute http(s) URL.
func webURL(s string) bool {
	if len(s) > maxURLLen || strings.ContainsFunc(s, isControl) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// blockedSchemes can run code in the browser and are never callbacks.
var blockedSchemes = []string{"javascript", "data", "vbscript", "file", "blob", "about"}

// callbackURL reports whether s can be a callback URL: http(s) with a
// host, or a custom scheme of a native app (myapp://callback).
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

// newClientID returns a client id shaped like GitHub's ("Ov23li" and 14
// alphanumeric characters).
func newClientID() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var b [14]byte
	_, _ = rand.Read(b[:])
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return "Ov23li" + string(out)
}

// CreateOAuthApp registers an app owned by p (org "") or by the
// organization org, which p must own.
func (s *Service) CreateOAuthApp(ctx context.Context, p auth.Principal, org string, in OAuthAppInput, ip string) (OAuthApp, error) {
	ownerID, kind, ownerName, orgID := p.UserID, OAuthOwnerUser, p.Username, uuid.Nil
	if org != "" {
		id, err := s.orgFor(ctx, p, org)
		if err != nil {
			return OAuthApp{}, err
		}
		ownerID, kind, ownerName, orgID = id, OAuthOwnerOrg, org, id
	}
	if !s.mayRegister(p, org != "") {
		return OAuthApp{}, ErrForbidden
	}
	if in.Name == nil || in.HomepageURL == nil || in.CallbackURLs == nil {
		return OAuthApp{}, inputErr("name, homepage URL and callback URL are required")
	}
	f, err := in.apply(appFields{})
	if err != nil {
		return OAuthApp{}, err
	}
	id, err := newID()
	if err != nil {
		return OAuthApp{}, err
	}
	now := s.now()
	app := OAuthApp{ID: id, Name: f.name, Description: f.description, HomepageURL: f.homepage, LogoURL: f.logo,
		CallbackURLs: f.callbacks, DeviceFlow: f.deviceFlow, OwnerKind: kind, OwnerName: ownerName, OwnerID: ownerID,
		CreatedAt: now, UpdatedAt: now}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if n, err := q.CountOAuthAppsByOwner(ctx, uuid.NullUUID{UUID: ownerID, Valid: true}); err != nil {
			return fmt.Errorf("count oauth apps: %w", err)
		} else if n >= MaxOAuthAppsPerOwner {
			return ErrTooMany
		}
		for range 5 {
			app.ClientID = newClientID()
			err := q.InsertOAuthApp(ctx, dbgen.InsertOAuthAppParams{ID: id, ClientID: app.ClientID,
				OwnerID: uuid.NullUUID{UUID: ownerID, Valid: true}, Name: f.name, Description: f.description,
				HomepageUrl: f.homepage, LogoUrl: f.logo, CallbackUrls: f.callbacks, DeviceFlow: f.deviceFlow,
				CreatedBy: uuid.NullUUID{UUID: p.UserID, Valid: true}})
			if isUniqueViolation(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("insert oauth app: %w", err)
			}
			return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionOAuthAppCreated, TargetType: "oauth_app",
				TargetID: id.String(), IP: ip, OrgID: orgID,
				Data: map[string]any{"client_id": app.ClientID, "name": f.name, "owner": ownerName}})
		}
		return errors.New("insert oauth app: client id collisions")
	})
	if err != nil {
		return OAuthApp{}, err
	}
	s.log.InfoContext(ctx, "oauth app created", "client_id", app.ClientID, "owner", ownerName, "user_id", p.UserID)
	return app, nil
}

func (s *Service) orgOf(app OAuthApp) uuid.UUID {
	if app.OwnerKind == OAuthOwnerOrg {
		return app.OwnerID
	}
	return uuid.Nil
}

// GetOAuthApp returns an app p manages, with its secrets.
func (s *Service) GetOAuthApp(ctx context.Context, p auth.Principal, clientID string) (OAuthApp, error) {
	app, err := s.managedApp(ctx, p, clientID)
	if err != nil {
		return OAuthApp{}, err
	}
	return s.withDetails(ctx, app)
}

func (s *Service) withDetails(ctx context.Context, app OAuthApp) (OAuthApp, error) {
	n, err := s.q.CountOAuthAppUsers(ctx, app.ID)
	if err != nil {
		return OAuthApp{}, fmt.Errorf("count oauth app users: %w", err)
	}
	app.Users = int(n)
	rows, err := s.q.ListOAuthAppSecrets(ctx, app.ID)
	if err != nil {
		return OAuthApp{}, fmt.Errorf("list oauth app secrets: %w", err)
	}
	app.Secrets = make([]OAuthAppSecret, 0, len(rows))
	for _, r := range rows {
		app.Secrets = append(app.Secrets, OAuthAppSecret{ID: r.ID, Hint: r.Hint, CreatedBy: r.CreatedByName,
			CreatedAt: r.CreatedAt.Time, LastUsedAt: ts(r.LastUsedAt)})
	}
	return app, nil
}

// UpdateOAuthApp edits an app p manages.
func (s *Service) UpdateOAuthApp(ctx context.Context, p auth.Principal, clientID string, in OAuthAppInput, ip string) (OAuthApp, error) {
	app, err := s.managedApp(ctx, p, clientID)
	if err != nil {
		return OAuthApp{}, err
	}
	f, err := in.apply(appFields{name: app.Name, description: app.Description, homepage: app.HomepageURL, logo: app.LogoURL,
		callbacks: app.CallbackURLs, deviceFlow: app.DeviceFlow})
	if err != nil {
		return OAuthApp{}, err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.UpdateOAuthApp(ctx, dbgen.UpdateOAuthAppParams{ID: app.ID, Name: f.name, Description: f.description,
			HomepageUrl: f.homepage, LogoUrl: f.logo, CallbackUrls: f.callbacks, DeviceFlow: f.deviceFlow}); err != nil {
			return fmt.Errorf("update oauth app: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionOAuthAppUpdated, TargetType: "oauth_app",
			TargetID: app.ID.String(), IP: ip, OrgID: s.orgOf(app), Data: map[string]any{"client_id": app.ClientID}})
	})
	if err != nil {
		return OAuthApp{}, err
	}
	app.Name, app.Description, app.HomepageURL, app.LogoURL = f.name, f.description, f.homepage, f.logo
	app.CallbackURLs, app.DeviceFlow, app.UpdatedAt = f.callbacks, f.deviceFlow, s.now()
	return s.withDetails(ctx, app)
}

// DeleteOAuthApp deletes an app p manages with its secrets, grants and
// tokens.
func (s *Service) DeleteOAuthApp(ctx context.Context, p auth.Principal, clientID, ip string) error {
	app, err := s.managedApp(ctx, p, clientID)
	if err != nil {
		return err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteOAuthApp(ctx, app.ID)
		if err != nil {
			return fmt.Errorf("delete oauth app: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionOAuthAppDeleted, TargetType: "oauth_app",
			TargetID: app.ID.String(), IP: ip, OrgID: s.orgOf(app),
			Data: map[string]any{"client_id": app.ClientID, "name": app.Name, "owner": app.OwnerName}})
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "oauth app deleted", "client_id", app.ClientID, "user_id", p.UserID)
	return nil
}

// newClientSecret returns a client secret shaped like GitHub's (40 hex
// characters) and its hash.
func newClientSecret() (string, []byte) {
	var b [20]byte
	_, _ = rand.Read(b[:])
	secret := hex.EncodeToString(b[:])
	return secret, auth.HashToken(secret)
}

// CreateOAuthAppSecret generates a client secret of an app p manages; the
// secret is returned once.
func (s *Service) CreateOAuthAppSecret(ctx context.Context, p auth.Principal, clientID, ip string) (OAuthAppSecret, string, error) {
	app, err := s.managedApp(ctx, p, clientID)
	if err != nil {
		return OAuthAppSecret{}, "", err
	}
	id, err := newID()
	if err != nil {
		return OAuthAppSecret{}, "", err
	}
	secret, hash := newClientSecret()
	out := OAuthAppSecret{ID: id, Hint: secret[len(secret)-8:], CreatedBy: p.Username, CreatedAt: s.now()}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.LockOAuthApp(ctx, app.ID); err != nil {
			return fmt.Errorf("lock oauth app: %w", err)
		}
		if n, err := q.CountOAuthAppSecrets(ctx, app.ID); err != nil {
			return fmt.Errorf("count oauth app secrets: %w", err)
		} else if n >= MaxOAuthAppSecrets {
			return ErrTooMany
		}
		if err := q.InsertOAuthAppSecret(ctx, dbgen.InsertOAuthAppSecretParams{ID: id, AppID: app.ID, SecretHash: hash,
			Hint: out.Hint, CreatedBy: uuid.NullUUID{UUID: p.UserID, Valid: true}}); err != nil {
			return fmt.Errorf("insert oauth app secret: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionOAuthSecretCreated, TargetType: "oauth_app",
			TargetID: app.ID.String(), IP: ip, OrgID: s.orgOf(app),
			Data: map[string]any{"client_id": app.ClientID, "secret_id": id.String(), "hint": out.Hint}})
	})
	if err != nil {
		return OAuthAppSecret{}, "", err
	}
	return out, secret, nil
}

// DeleteOAuthAppSecret revokes a client secret of an app p manages.
func (s *Service) DeleteOAuthAppSecret(ctx context.Context, p auth.Principal, clientID string, secretID uuid.UUID, ip string) error {
	app, err := s.managedApp(ctx, p, clientID)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteOAuthAppSecret(ctx, dbgen.DeleteOAuthAppSecretParams{AppID: app.ID, SecretID: secretID})
		if err != nil {
			return fmt.Errorf("delete oauth app secret: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionOAuthSecretRevoked, TargetType: "oauth_app",
			TargetID: app.ID.String(), IP: ip, OrgID: s.orgOf(app),
			Data: map[string]any{"client_id": app.ClientID, "secret_id": secretID.String()}})
	})
}

// verifyClientSecret checks a client secret of app and records its use.
func (s *Service) verifyClientSecret(ctx context.Context, q *dbgen.Queries, app OAuthApp, secret string) (bool, error) {
	if secret == "" || len(secret) > 200 {
		return false, nil
	}
	id, err := q.FindOAuthAppSecret(ctx, dbgen.FindOAuthAppSecretParams{AppID: app.ID, SecretHash: auth.HashToken(secret)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find oauth app secret: %w", err)
	}
	if err := q.TouchOAuthAppSecret(ctx, id); err != nil {
		return false, fmt.Errorf("touch oauth app secret: %w", err)
	}
	return true, nil
}

// Authorization is an app a user has authorized.
type Authorization struct {
	ID          uuid.UUID
	App         OAuthApp // summary fields only
	Scopes      []string // GitHub scopes
	TokenScopes []string
	Tokens      int
	CreatedAt   time.Time
	LastUsedAt  *time.Time
}

// ListAuthorizations returns the apps userID has authorized.
func (s *Service) ListAuthorizations(ctx context.Context, userID uuid.UUID) ([]Authorization, error) {
	rows, err := s.q.ListUserOAuthGrants(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list oauth grants: %w", err)
	}
	out := make([]Authorization, 0, len(rows))
	for _, r := range rows {
		kind := r.OwnerKind
		if r.IsSystem {
			kind = OAuthOwnerSystem
		}
		out = append(out, Authorization{ID: r.ID, App: OAuthApp{ClientID: r.ClientID, Name: r.Name, Description: r.Description,
			HomepageURL: r.HomepageUrl, LogoURL: r.LogoUrl, OwnerKind: kind, OwnerName: r.OwnerName},
			Scopes: r.Scopes, TokenScopes: auth.ScopesFromGitHub(r.Scopes), Tokens: int(r.Tokens),
			CreatedAt: r.CreatedAt.Time, LastUsedAt: ts(r.LastUsedAt)})
	}
	return out, nil
}

// RevokeAuthorization deletes a grant of userID and every token issued
// under it.
func (s *Service) RevokeAuthorization(ctx context.Context, userID, grantID uuid.UUID, ip string) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		appID, err := q.DeleteUserOAuthGrant(ctx, dbgen.DeleteUserOAuthGrantParams{UserID: userID, GrantID: grantID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("delete oauth grant: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionOAuthGrantRevoked, TargetType: "oauth_grant",
			TargetID: grantID.String(), IP: ip, Data: map[string]any{"app_id": appID.String()}})
	})
}

// upsertGrant adds scopes (GitHub scopes) to the grant of userID for app
// and records the audit event when the grant is new or wider.
func upsertGrant(ctx context.Context, q *dbgen.Queries, app OAuthApp, userID uuid.UUID, scopes []string, ip string) (uuid.UUID, error) {
	before, err := q.GetOAuthGrant(ctx, dbgen.GetOAuthGrantParams{AppID: app.ID, UserID: userID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("get oauth grant: %w", err)
	}
	id, err := newID()
	if err != nil {
		return uuid.Nil, err
	}
	row, err := q.UpsertOAuthGrant(ctx, dbgen.UpsertOAuthGrantParams{ID: id, AppID: app.ID, UserID: userID, Scopes: scopes})
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert oauth grant: %w", err)
	}
	if row.Inserted || len(row.Scopes) != len(before.Scopes) {
		if err := audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionOAuthGrantCreated, TargetType: "oauth_grant",
			TargetID: row.ID.String(), IP: ip,
			Data: map[string]any{"client_id": app.ClientID, "app": app.Name, "scopes": row.Scopes}}); err != nil {
			return uuid.Nil, err
		}
	}
	return row.ID, nil
}

// issueGrantToken creates an access token of userID under grantID with
// the token scopes of the GitHub scopes gh, keeping at most
// maxGrantTokens per grant. The caller runs it in a transaction.
func (s *Service) issueGrantToken(ctx context.Context, q *dbgen.Queries, app OAuthApp, userID, grantID uuid.UUID, gh []string, ip, grant string) (string, error) {
	ids, err := q.ListGrantTokenIDs(ctx, uuid.NullUUID{UUID: grantID, Valid: true})
	if err != nil {
		return "", fmt.Errorf("list grant tokens: %w", err)
	}
	for _, old := range ids[min(len(ids), maxGrantTokens-1):] {
		if _, err := q.DeleteToken(ctx, old); err != nil {
			return "", fmt.Errorf("delete old grant token: %w", err)
		}
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	name := app.Name
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	idText := id.String()
	name += " (OAuth " + idText[len(idText)-8:] + ")"
	ttl := s.maxTTL
	if ttl <= 0 {
		ttl = 90 * 24 * time.Hour
	}
	scopes := auth.ScopesFromGitHub(gh)
	secret, hash := auth.NewAccessToken()
	if err := q.InsertGrantToken(ctx, dbgen.InsertGrantTokenParams{ID: id, UserID: userID, Name: name, TokenHash: hash,
		Hint: auth.TokenHint(secret), Scopes: scopes, ExpiresAt: pgTime(s.now().Add(ttl)),
		GrantID: uuid.NullUUID{UUID: grantID, Valid: true}}); err != nil {
		return "", fmt.Errorf("insert grant token: %w", err)
	}
	if err := audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionTokenCreated, TargetType: "token",
		TargetID: id.String(), IP: ip, Data: map[string]any{"name": name, "scopes": scopes, "client_id": app.ClientID,
			"grant": grant}}); err != nil {
		return "", err
	}
	s.log.InfoContext(ctx, "oauth token issued", "user_id", userID, "client_id", app.ClientID, "token_id", id, "grant", grant)
	return secret, nil
}
