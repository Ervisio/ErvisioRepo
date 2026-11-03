// Package credentials manages the credentials an account uses outside the
// browser: personal access tokens, SSH keys and GPG keys (PLAN §8.1).
package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Audit actions of this package.
const (
	ActionTokenCreated = "token.created"
	ActionTokenRevoked = "token.revoked"
	ActionSSHKeyAdded  = "key.ssh_added"
	ActionSSHKeyRemove = "key.ssh_removed"
	ActionGPGKeyAdded  = "key.gpg_added"
	ActionGPGKeyRemove = "key.gpg_removed"
)

// Per-account limits.
const (
	MaxTokens   = 100
	MaxSSHKeys  = 100
	MaxGPGKeys  = 50
	MaxNameRune = 100
)

// Errors of the service; their messages are safe to show.
var (
	ErrNotFound     = errors.New("not found")
	ErrInvalidName  = errors.New("the name must be 1 to 100 characters")
	ErrNameTaken    = errors.New("a token with this name already exists")
	ErrInvalidScope = errors.New("unknown or missing token scope")
	ErrAdminScope   = errors.New("only instance admins can grant the admin scope")
	ErrExpiry       = errors.New("the expiry date must be in the future and within the allowed lifetime")
	ErrKeyTaken     = errors.New("this key is already in use")
	ErrTooMany      = errors.New("too many credentials of this kind")
)

// Service implements the credential operations.
type Service struct {
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	log    *slog.Logger
	maxTTL time.Duration
	now    func() time.Time
}

// Options configures a Service.
type Options struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
	// TokenMaxTTL bounds the lifetime of access tokens (auth.token_max_ttl).
	TokenMaxTTL time.Duration
	Now         func() time.Time
}

// New returns a Service.
func New(o Options) *Service {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: o.Pool, q: dbgen.New(o.Pool), log: log, maxTTL: o.TokenMaxTTL, now: now}
}

// MaxTokenTTL is the longest lifetime of a new token.
func (s *Service) MaxTokenTTL() time.Duration { return s.maxTTL }

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

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameRune || !utf8.ValidString(name) || strings.ContainsFunc(name, isControl) {
		return "", ErrInvalidName
	}
	return name, nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func ts(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("new id: %w", err)
	}
	return id, nil
}

// lockUser serializes credential changes of one account, so that the
// per-account limits hold under concurrent requests.
func lockUser(ctx context.Context, q *dbgen.Queries, userID uuid.UUID) error {
	if _, err := q.LockUserForUpdate(ctx, userID); err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	return nil
}

// Token is an access token as listed (never the secret).
type Token struct {
	ID         uuid.UUID
	Name       string
	Hint       string
	Scopes     []string
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

// NewTokenInput is a request for a token.
type NewTokenInput struct {
	Name      string
	Scopes    []string
	ExpiresAt time.Time
}

// CreatedToken is a new token with its secret, shown once.
type CreatedToken struct {
	Token
	Secret string
}

// CreateToken creates an access token for p. Only admins may grant the
// admin scope.
func (s *Service) CreateToken(ctx context.Context, p auth.Principal, in NewTokenInput, ip string) (CreatedToken, error) {
	name, err := checkName(in.Name)
	if err != nil {
		return CreatedToken{}, err
	}
	scopes := slices.Clone(in.Scopes)
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	if len(scopes) == 0 {
		return CreatedToken{}, ErrInvalidScope
	}
	for _, sc := range scopes {
		if !auth.ValidScope(sc) {
			return CreatedToken{}, ErrInvalidScope
		}
	}
	if slices.Contains(scopes, auth.ScopeAdmin) && !p.IsAdmin() {
		return CreatedToken{}, ErrAdminScope
	}
	now := s.now()
	if !in.ExpiresAt.After(now) || in.ExpiresAt.After(now.Add(s.maxTTL)) {
		return CreatedToken{}, ErrExpiry
	}
	id, err := newID()
	if err != nil {
		return CreatedToken{}, err
	}
	secret, hash := auth.NewAccessToken()
	out := CreatedToken{Token: Token{ID: id, Name: name, Hint: auth.TokenHint(secret), Scopes: scopes,
		ExpiresAt: in.ExpiresAt.UTC(), CreatedAt: now}, Secret: secret}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := lockUser(ctx, q, p.UserID); err != nil {
			return err
		}
		if n, err := q.CountUserTokens(ctx, p.UserID); err != nil {
			return fmt.Errorf("count tokens: %w", err)
		} else if n >= MaxTokens {
			return ErrTooMany
		}
		if err := q.InsertToken(ctx, dbgen.InsertTokenParams{ID: id, UserID: p.UserID, Name: name, TokenHash: hash,
			Hint: out.Hint, Scopes: scopes, ExpiresAt: pgtype.Timestamptz{Time: out.ExpiresAt, Valid: true}}); err != nil {
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return fmt.Errorf("insert token: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionTokenCreated, TargetType: "token",
			TargetID: id.String(), IP: ip, Data: map[string]any{"name": name, "scopes": scopes, "expires_at": out.ExpiresAt}})
	})
	if err != nil {
		return CreatedToken{}, err
	}
	return out, nil
}

// ListTokens returns the tokens of userID, newest first.
func (s *Service) ListTokens(ctx context.Context, userID uuid.UUID) ([]Token, error) {
	rows, err := s.q.ListUserTokens(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	out := make([]Token, 0, len(rows))
	for _, r := range rows {
		out = append(out, Token{ID: r.ID, Name: r.Name, Hint: r.Hint, Scopes: r.Scopes, ExpiresAt: r.ExpiresAt.Time,
			LastUsedAt: ts(r.LastUsedAt), CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

// RevokeToken deletes a token of userID.
func (s *Service) RevokeToken(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteUserToken(ctx, dbgen.DeleteUserTokenParams{UserID: userID, TokenID: id})
		if err != nil {
			return fmt.Errorf("delete token: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionTokenRevoked, TargetType: "token", TargetID: id.String(), IP: ip})
	})
}

// StoredSSHKey is an SSH key of an account.
type StoredSSHKey struct {
	ID          uuid.UUID
	Title       string
	Type        string
	Fingerprint string
	PublicKey   string
	Bits        int
	LastUsedAt  *time.Time
	CreatedAt   time.Time
}

// AddSSHKey parses and stores an SSH public key for userID. A key can
// belong to one account only. An empty title uses the key comment.
func (s *Service) AddSSHKey(ctx context.Context, userID uuid.UUID, title, key, ip string) (StoredSSHKey, error) {
	k, err := ParseSSHKey(key)
	if err != nil {
		return StoredSSHKey{}, err
	}
	if strings.TrimSpace(title) == "" {
		title = k.Comment
	}
	if strings.TrimSpace(title) == "" {
		title = k.Type
	}
	if title, err = checkName(title); err != nil {
		return StoredSSHKey{}, err
	}
	id, err := newID()
	if err != nil {
		return StoredSSHKey{}, err
	}
	out := StoredSSHKey{ID: id, Title: title, Type: k.Type, Fingerprint: k.Fingerprint, PublicKey: k.PublicKey, Bits: k.Bits, CreatedAt: s.now()}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := lockUser(ctx, q, userID); err != nil {
			return err
		}
		if n, err := q.CountUserSSHKeys(ctx, userID); err != nil {
			return fmt.Errorf("count ssh keys: %w", err)
		} else if n >= MaxSSHKeys {
			return ErrTooMany
		}
		if err := q.InsertSSHKey(ctx, dbgen.InsertSSHKeyParams{ID: id, UserID: userID, Title: title, KeyType: k.Type,
			Fingerprint: k.Fingerprint, PublicKey: k.PublicKey, Bits: int32(min(k.Bits, 1<<20))}); err != nil { //nolint:gosec // bounded
			if isUniqueViolation(err) {
				return ErrKeyTaken
			}
			return fmt.Errorf("insert ssh key: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionSSHKeyAdded, TargetType: "ssh_key", TargetID: id.String(),
			IP: ip, Data: map[string]any{"fingerprint": "SHA256:" + k.Fingerprint, "type": k.Type}})
	})
	if err != nil {
		return StoredSSHKey{}, err
	}
	return out, nil
}

// ListSSHKeys returns the SSH keys of userID.
func (s *Service) ListSSHKeys(ctx context.Context, userID uuid.UUID) ([]StoredSSHKey, error) {
	rows, err := s.q.ListUserSSHKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list ssh keys: %w", err)
	}
	out := make([]StoredSSHKey, 0, len(rows))
	for _, r := range rows {
		out = append(out, StoredSSHKey{ID: r.ID, Title: r.Title, Type: r.KeyType, Fingerprint: r.Fingerprint, PublicKey: r.PublicKey,
			Bits: int(r.Bits), LastUsedAt: ts(r.LastUsedAt), CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

// RemoveSSHKey deletes an SSH key of userID.
func (s *Service) RemoveSSHKey(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteUserSSHKey(ctx, dbgen.DeleteUserSSHKeyParams{UserID: userID, KeyID: id})
		if err != nil {
			return fmt.Errorf("delete ssh key: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionSSHKeyRemove, TargetType: "ssh_key", TargetID: id.String(), IP: ip})
	})
}

// StoredGPGKey is a GPG key of an account.
type StoredGPGKey struct {
	ID          uuid.UUID
	KeyID       string
	Fingerprint string
	SubkeyIDs   []string
	// Emails are the addresses of the key identities. Verified tells which
	// of them are verified addresses of the account: only those will mark
	// signed commits as verified (Phase 4).
	Emails    []string
	Verified  []string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// AddGPGKey parses and stores an armored GPG public key for userID.
func (s *Service) AddGPGKey(ctx context.Context, userID uuid.UUID, armored, ip string) (StoredGPGKey, error) {
	k, err := ParseGPGKey(armored)
	if err != nil {
		return StoredGPGKey{}, err
	}
	id, err := newID()
	if err != nil {
		return StoredGPGKey{}, err
	}
	out := StoredGPGKey{ID: id, KeyID: k.KeyID, Fingerprint: k.Fingerprint, SubkeyIDs: k.SubkeyIDs, Emails: k.Emails,
		ExpiresAt: k.ExpiresAt, CreatedAt: s.now()}
	var exp pgtype.Timestamptz
	if k.ExpiresAt != nil {
		exp = pgtype.Timestamptz{Time: *k.ExpiresAt, Valid: true}
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := lockUser(ctx, q, userID); err != nil {
			return err
		}
		if n, err := q.CountUserGPGKeys(ctx, userID); err != nil {
			return fmt.Errorf("count gpg keys: %w", err)
		} else if n >= MaxGPGKeys {
			return ErrTooMany
		}
		if err := q.InsertGPGKey(ctx, dbgen.InsertGPGKeyParams{ID: id, UserID: userID, KeyID: k.KeyID, Fingerprint: k.Fingerprint,
			SubkeyIds: k.SubkeyIDs, Emails: k.Emails, Armored: k.Armored, ExpiresAt: exp}); err != nil {
			if isUniqueViolation(err) {
				return ErrKeyTaken
			}
			return fmt.Errorf("insert gpg key: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionGPGKeyAdded, TargetType: "gpg_key", TargetID: id.String(),
			IP: ip, Data: map[string]any{"key_id": k.KeyID, "fingerprint": k.Fingerprint}})
	})
	if err != nil {
		return StoredGPGKey{}, err
	}
	if out.Verified, err = s.verifiedSubset(ctx, userID, k.Emails); err != nil {
		return StoredGPGKey{}, err
	}
	return out, nil
}

func (s *Service) verifiedSubset(ctx context.Context, userID uuid.UUID, emails []string) ([]string, error) {
	mine, err := s.q.ListUserVerifiedEmails(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list verified emails: %w", err)
	}
	out := []string{}
	for _, e := range emails {
		if slices.ContainsFunc(mine, func(m string) bool { return strings.EqualFold(m, e) }) {
			out = append(out, e)
		}
	}
	return out, nil
}

// ListGPGKeys returns the GPG keys of userID.
func (s *Service) ListGPGKeys(ctx context.Context, userID uuid.UUID) ([]StoredGPGKey, error) {
	rows, err := s.q.ListUserGPGKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list gpg keys: %w", err)
	}
	out := make([]StoredGPGKey, 0, len(rows))
	for _, r := range rows {
		v, err := s.verifiedSubset(ctx, userID, r.Emails)
		if err != nil {
			return nil, err
		}
		out = append(out, StoredGPGKey{ID: r.ID, KeyID: r.KeyID, Fingerprint: r.Fingerprint, SubkeyIDs: r.SubkeyIds, Emails: r.Emails,
			Verified: v, ExpiresAt: ts(r.ExpiresAt), CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

// RemoveGPGKey deletes a GPG key of userID.
func (s *Service) RemoveGPGKey(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteUserGPGKey(ctx, dbgen.DeleteUserGPGKeyParams{UserID: userID, KeyID: id})
		if err != nil {
			return fmt.Errorf("delete gpg key: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionGPGKeyRemove, TargetType: "gpg_key", TargetID: id.String(), IP: ip})
	})
}
