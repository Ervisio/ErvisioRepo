package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// ErrNoSession means the token does not name a live session.
var ErrNoSession = errors.New("no valid session")

// SessionStore is the subset of dbgen.Querier used by Sessions.
type SessionStore interface {
	InsertSession(ctx context.Context, arg dbgen.InsertSessionParams) error
	GetSessionByTokenHash(ctx context.Context, tokenHash []byte) (dbgen.GetSessionByTokenHashRow, error)
	TouchSession(ctx context.Context, id uuid.UUID) error
	DeleteSession(ctx context.Context, id uuid.UUID) error
	DeleteUserSessions(ctx context.Context, userID uuid.UUID) (int64, error)
}

// Session is a live browser session.
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Username  string
	Role      string
	CSRFToken string
	ExpiresAt time.Time
}

// Principal returns the caller authenticated by s.
func (s Session) Principal() Principal {
	return Principal{UserID: s.UserID, Username: s.Username, Role: s.Role, Method: MethodSession, SessionID: s.ID}
}

// NewSession is a session just created, with the secrets to hand to the
// browser. They are never stored in clear.
type NewSession struct {
	Session
	Token string
}

// Sessions manages database sessions with an idle and an absolute expiry.
type Sessions struct {
	Store  SessionStore
	Idle   time.Duration
	MaxAge time.Duration
	Now    func() time.Time // defaults to time.Now
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Create starts a session for a user. ip and userAgent are informational.
func (s *Sessions) Create(ctx context.Context, user Principal, ip, userAgent string) (NewSession, error) {
	token, hash := NewToken()
	csrf, _ := NewToken()
	id, err := uuid.NewV7()
	if err != nil {
		return NewSession{}, fmt.Errorf("session id: %w", err)
	}
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	expires := s.now().Add(s.MaxAge)
	if err := s.Store.InsertSession(ctx, dbgen.InsertSessionParams{
		ID: id, TokenHash: hash, UserID: user.UserID, CsrfToken: csrf, Ip: ip, UserAgent: userAgent,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return NewSession{}, fmt.Errorf("insert session: %w", err)
	}
	return NewSession{
		Session: Session{ID: id, UserID: user.UserID, Username: user.Username, Role: user.Role, CSRFToken: csrf, ExpiresAt: expires},
		Token:   token,
	}, nil
}

// Lookup returns the live session named by token. An expired session, or
// one of a suspended user, is deleted and reported as ErrNoSession.
func (s *Sessions) Lookup(ctx context.Context, token string) (Session, error) {
	if !ValidTokenFormat(token) {
		return Session{}, ErrNoSession
	}
	row, err := s.Store.GetSessionByTokenHash(ctx, HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	now := s.now()
	if !row.ExpiresAt.Time.After(now) || !row.LastSeenAt.Time.Add(s.Idle).After(now) || row.SuspendedAt.Valid {
		if err := s.Store.DeleteSession(ctx, row.ID); err != nil {
			return Session{}, fmt.Errorf("delete expired session: %w", err)
		}
		return Session{}, ErrNoSession
	}
	if err := s.Store.TouchSession(ctx, row.ID); err != nil {
		return Session{}, fmt.Errorf("touch session: %w", err)
	}
	return Session{ID: row.ID, UserID: row.UserID, Username: row.Username, Role: row.Role,
		CSRFToken: row.CsrfToken, ExpiresAt: row.ExpiresAt.Time}, nil
}

// Revoke deletes one session.
func (s *Sessions) Revoke(ctx context.Context, id uuid.UUID) error {
	if err := s.Store.DeleteSession(ctx, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// RevokeUser deletes every session of a user.
func (s *Sessions) RevokeUser(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.Store.DeleteUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}
