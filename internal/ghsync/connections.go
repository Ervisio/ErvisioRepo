package ghsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Connection is a GitHub connection without its credentials.
type Connection struct {
	ID             uuid.UUID
	OwnerID        uuid.UUID
	Owner          string
	OwnerKind      string
	Name           string
	Kind           string
	BaseURL        string
	AppID          int64
	InstallationID int64
	WebhookURL     string
	// WebhookSecret is set only by the calls that created or rotated it.
	WebhookSecret string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ConnectionInput creates a connection.
type ConnectionInput struct {
	Owner          string // the caller when empty
	Name           string
	Kind           string // app or token
	BaseURL        string // DefaultBaseURL when empty
	AppID          int64
	InstallationID int64
	PrivateKey     string // app
	Token          string // token
	WebhookSecret  string // generated when empty
}

// ConnectionUpdate changes a connection; nil fields are kept.
type ConnectionUpdate struct {
	Name                *string
	AppID               *int64
	InstallationID      *int64
	PrivateKey          *string
	Token               *string
	RotateWebhookSecret bool
}

func (s *Service) connection(row dbgen.GithubConnection, owner, ownerKind string) Connection {
	return Connection{ID: row.ID, OwnerID: row.OwnerID, Owner: owner, OwnerKind: ownerKind, Name: row.Name,
		Kind: row.Kind, BaseURL: row.BaseUrl, AppID: row.AppID, InstallationID: row.InstallationID,
		WebhookURL: s.o.PublicURL + "/api/v1/github/webhook/" + row.ID.String(),
		CreatedAt:  row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func checkName(name string) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(name)); n < 1 || n > 100 {
		return invalidf("name must be 1 to 100 characters")
	}
	return nil
}

// checkToken refuses anything that could break out of the HTTP header the
// token is sent in.
func checkToken(tok string) error {
	if tok == "" || len(tok) > 1024 {
		return invalidf("token must be 1 to 1024 characters")
	}
	for i := 0; i < len(tok); i++ {
		if c := tok[i]; c <= ' ' || c > '~' {
			return invalidf("token contains invalid characters")
		}
	}
	return nil
}

func checkApp(appID, installationID int64, key string) error {
	if appID <= 0 || installationID <= 0 {
		return invalidf("appId and installationId are required for a GitHub App")
	}
	if key != "" {
		if _, err := parsePrivateKey([]byte(key)); err != nil {
			return err
		}
	}
	return nil
}

func newWebhookSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("webhook secret: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ownerNamespace resolves the owner of connections for who.
func (s *Service) ownerNamespace(ctx context.Context, who auth.Principal, owner string) (dbgen.GetNamespaceByNameRow, error) {
	if owner == "" {
		owner = who.Username
	}
	ns, err := s.q.GetNamespaceByName(ctx, owner)
	if noRows(err) {
		return ns, ErrNotFound
	}
	if err != nil {
		return ns, fmt.Errorf("get namespace: %w", err)
	}
	ok, err := s.canManage(ctx, who, ns.ID, ns.Kind)
	if err != nil {
		return ns, err
	}
	if !ok {
		return ns, ErrNotFound
	}
	return ns, nil
}

// ListConnections returns the connections of owner (the caller when empty).
func (s *Service) ListConnections(ctx context.Context, who auth.Principal, owner string) ([]Connection, error) {
	ns, err := s.ownerNamespace(ctx, who, owner)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListGitHubConnections(ctx, ns.ID)
	if err != nil {
		return nil, fmt.Errorf("list GitHub connections: %w", err)
	}
	out := make([]Connection, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.connection(r.GithubConnection, r.OwnerName, r.OwnerKind))
	}
	return out, nil
}

// CreateConnection stores a new connection with sealed credentials.
func (s *Service) CreateConnection(ctx context.Context, who auth.Principal, in ConnectionInput) (Connection, error) {
	if !s.seal.ok() {
		return Connection{}, ErrNoMasterKey
	}
	ns, err := s.ownerNamespace(ctx, who, in.Owner)
	if err != nil {
		return Connection{}, err
	}
	if err := checkName(in.Name); err != nil {
		return Connection{}, err
	}
	base := DefaultBaseURL
	if in.BaseURL != "" {
		if base, err = checkBaseURL(in.BaseURL); err != nil {
			return Connection{}, err
		}
	}
	// A custom server makes this process send requests to any host: only
	// an instance admin may point a connection elsewhere.
	if base != DefaultBaseURL && !who.IsAdmin() {
		return Connection{}, ErrForbidden
	}
	var secret []byte
	switch in.Kind {
	case kindApp:
		if in.PrivateKey == "" {
			return Connection{}, invalidf("privateKey is required for a GitHub App")
		}
		if err := checkApp(in.AppID, in.InstallationID, in.PrivateKey); err != nil {
			return Connection{}, err
		}
		secret = []byte(in.PrivateKey)
	case kindToken:
		if err := checkToken(in.Token); err != nil {
			return Connection{}, err
		}
		secret, in.AppID, in.InstallationID = []byte(in.Token), 0, 0
	default:
		return Connection{}, invalidf("kind must be app or token")
	}
	hook := in.WebhookSecret
	if hook == "" {
		if hook, err = newWebhookSecret(); err != nil {
			return Connection{}, err
		}
	} else if len(hook) < 16 || len(hook) > 256 {
		return Connection{}, invalidf("webhookSecret must be 16 to 256 characters")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Connection{}, fmt.Errorf("connection id: %w", err)
	}
	sealed, err := s.seal.seal(secret, id, purposeSecret)
	if err != nil {
		return Connection{}, err
	}
	sealedHook, err := s.seal.seal([]byte(hook), id, purposeWebhook)
	if err != nil {
		return Connection{}, err
	}
	row, err := s.q.InsertGitHubConnection(ctx, dbgen.InsertGitHubConnectionParams{ID: id, OwnerID: ns.ID,
		Name: strings.TrimSpace(in.Name), Kind: in.Kind, BaseUrl: base, AppID: in.AppID, InstallationID: in.InstallationID,
		Secret: sealed, WebhookSecret: sealedHook, CreatedBy: nullUser(who.UserID)})
	if isUnique(err) {
		return Connection{}, ErrNameTaken
	}
	if err != nil {
		return Connection{}, fmt.Errorf("insert GitHub connection: %w", err)
	}
	c := s.connection(row, ns.Name, ns.Kind)
	c.WebhookSecret = hook
	s.o.Log.InfoContext(ctx, "github connection created", "connection", id, "owner", ns.Name, "kind", in.Kind)
	return c, nil
}

// managed loads a connection the caller manages (ErrNotFound otherwise).
func (s *Service) managed(ctx context.Context, who auth.Principal, id uuid.UUID) (dbgen.GetGitHubConnectionRow, error) {
	row, err := s.q.GetGitHubConnection(ctx, id)
	if noRows(err) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get GitHub connection: %w", err)
	}
	ok, err := s.canManage(ctx, who, row.GithubConnection.OwnerID, row.OwnerKind)
	if err != nil {
		return row, err
	}
	if !ok {
		return row, ErrNotFound
	}
	return row, nil
}

// GetConnection returns a connection the caller manages.
func (s *Service) GetConnection(ctx context.Context, who auth.Principal, id uuid.UUID) (Connection, error) {
	row, err := s.managed(ctx, who, id)
	if err != nil {
		return Connection{}, err
	}
	return s.connection(row.GithubConnection, row.OwnerName, row.OwnerKind), nil
}

// UpdateConnection renames a connection, replaces its credentials or
// rotates its webhook secret.
func (s *Service) UpdateConnection(ctx context.Context, who auth.Principal, id uuid.UUID, in ConnectionUpdate) (Connection, error) {
	if !s.seal.ok() {
		return Connection{}, ErrNoMasterKey
	}
	row, err := s.managed(ctx, who, id)
	if err != nil {
		return Connection{}, err
	}
	c := row.GithubConnection
	p := dbgen.UpdateGitHubConnectionParams{ID: id, Name: c.Name, AppID: c.AppID, InstallationID: c.InstallationID,
		Secret: c.Secret, WebhookSecret: c.WebhookSecret}
	if in.Name != nil {
		if err := checkName(*in.Name); err != nil {
			return Connection{}, err
		}
		p.Name = strings.TrimSpace(*in.Name)
	}
	switch c.Kind {
	case kindApp:
		if in.Token != nil {
			return Connection{}, invalidf("token does not apply to a GitHub App connection")
		}
		if in.AppID != nil {
			p.AppID = *in.AppID
		}
		if in.InstallationID != nil {
			p.InstallationID = *in.InstallationID
		}
		key := ""
		if in.PrivateKey != nil {
			key = *in.PrivateKey
			if key == "" {
				return Connection{}, invalidf("privateKey cannot be empty")
			}
		}
		if err := checkApp(p.AppID, p.InstallationID, key); err != nil {
			return Connection{}, err
		}
		if key != "" {
			if p.Secret, err = s.seal.seal([]byte(key), id, purposeSecret); err != nil {
				return Connection{}, err
			}
		}
	case kindToken:
		if in.PrivateKey != nil || in.AppID != nil || in.InstallationID != nil {
			return Connection{}, invalidf("privateKey, appId and installationId apply to GitHub App connections")
		}
		if in.Token != nil {
			if err := checkToken(*in.Token); err != nil {
				return Connection{}, err
			}
			if p.Secret, err = s.seal.seal([]byte(*in.Token), id, purposeSecret); err != nil {
				return Connection{}, err
			}
		}
	}
	hook := ""
	if in.RotateWebhookSecret {
		if hook, err = newWebhookSecret(); err != nil {
			return Connection{}, err
		}
		if p.WebhookSecret, err = s.seal.seal([]byte(hook), id, purposeWebhook); err != nil {
			return Connection{}, err
		}
	}
	upd, err := s.q.UpdateGitHubConnection(ctx, p)
	if isUnique(err) {
		return Connection{}, ErrNameTaken
	}
	if err != nil {
		return Connection{}, fmt.Errorf("update GitHub connection: %w", err)
	}
	s.tokens.forget(id)
	out := s.connection(upd, row.OwnerName, row.OwnerKind)
	out.WebhookSecret = hook
	s.o.Log.InfoContext(ctx, "github connection updated", "connection", id, "rotated_webhook", in.RotateWebhookSecret)
	return out, nil
}

// DeleteConnection deletes a connection no link uses (ErrInUse otherwise).
func (s *Service) DeleteConnection(ctx context.Context, who auth.Principal, id uuid.UUID) error {
	if _, err := s.managed(ctx, who, id); err != nil {
		return err
	}
	n, err := s.q.CountGitHubConnectionLinks(ctx, id)
	if err != nil {
		return fmt.Errorf("count GitHub sync links: %w", err)
	}
	if n > 0 {
		return ErrInUse
	}
	if _, err := s.q.DeleteGitHubConnection(ctx, id); err != nil {
		return fmt.Errorf("delete GitHub connection: %w", err)
	}
	s.tokens.forget(id)
	return nil
}

// CheckConnection asks GitHub whether the credentials work. A failure of
// GitHub is an answer (ok false, detail), not an error.
func (s *Service) CheckConnection(ctx context.Context, who auth.Principal, id uuid.UUID) (bool, string, error) {
	row, err := s.managed(ctx, who, id)
	if err != nil {
		return false, "", err
	}
	cred, err := s.credential(row.GithubConnection)
	if err != nil {
		return false, "", err
	}
	api, err := s.tokens.api(ctx, cred)
	if err == nil {
		err = check(ctx, api, cred.kind)
	}
	if err != nil {
		return false, safeError(err), nil
	}
	return true, "", nil
}

// credential opens the sealed secret of a connection.
func (s *Service) credential(c dbgen.GithubConnection) (credential, error) {
	if !s.seal.ok() {
		return credential{}, ErrNoMasterKey
	}
	secret, err := s.seal.open(c.Secret, c.ID, purposeSecret)
	if err != nil {
		return credential{}, err
	}
	return credential{id: c.ID, version: c.UpdatedAt.Time, kind: c.Kind, baseURL: c.BaseUrl,
		appID: c.AppID, installationID: c.InstallationID, secret: secret}, nil
}

// usable loads a connection who may attach to a repository of the
// namespace ownerID: one of that namespace, one of the caller, or one the
// caller manages.
func (s *Service) usable(ctx context.Context, who auth.Principal, id, ownerID uuid.UUID) (dbgen.GithubConnection, error) {
	row, err := s.q.GetGitHubConnection(ctx, id)
	if noRows(err) {
		return dbgen.GithubConnection{}, ErrNotFound
	}
	if err != nil {
		return dbgen.GithubConnection{}, fmt.Errorf("get GitHub connection: %w", err)
	}
	c := row.GithubConnection
	if c.OwnerID == ownerID || c.OwnerID == who.UserID {
		return c, nil
	}
	ok, err := s.canManage(ctx, who, c.OwnerID, row.OwnerKind)
	if err != nil {
		return c, err
	}
	if !ok {
		return c, ErrNotFound
	}
	return c, nil
}

// safeError is the message of err that can be stored and shown: API and
// git errors carry no secret (tokens travel only in the environment).
func safeError(err error) string {
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return msg
}
