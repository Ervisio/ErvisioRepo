// Package authtest provides in-memory sessions and request helpers for
// tests of code behind the auth middleware.
package authtest

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Store is an in-memory auth.SessionStore. Users must be added with
// AddUser before sessions are created for them.
type Store struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]dbgen.InsertSessionParams
	seen     map[uuid.UUID]time.Time
	users    map[uuid.UUID]user
	Now      func() time.Time
}

type user struct {
	name, role string
	suspended  bool
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{sessions: map[uuid.UUID]dbgen.InsertSessionParams{}, seen: map[uuid.UUID]time.Time{}, users: map[uuid.UUID]user{}}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// AddUser registers a user known to the store.
func (s *Store) AddUser(id uuid.UUID, name, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[id] = user{name: name, role: role}
}

// Suspend marks a user suspended.
func (s *Store) Suspend(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[id]
	u.suspended = true
	s.users[id] = u
}

// Len is the number of stored sessions.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// InsertSession implements auth.SessionStore.
func (s *Store) InsertSession(_ context.Context, arg dbgen.InsertSessionParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[arg.ID] = arg
	s.seen[arg.ID] = s.now()
	return nil
}

// GetSessionByTokenHash implements auth.SessionStore.
func (s *Store) GetSessionByTokenHash(_ context.Context, hash []byte) (dbgen.GetSessionByTokenHashRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.sessions {
		if string(p.TokenHash) != string(hash) {
			continue
		}
		u, ok := s.users[p.UserID]
		if !ok {
			return dbgen.GetSessionByTokenHashRow{}, pgx.ErrNoRows
		}
		return dbgen.GetSessionByTokenHashRow{
			ID: id, UserID: p.UserID, CsrfToken: p.CsrfToken,
			LastSeenAt: pgtype.Timestamptz{Time: s.seen[id], Valid: true}, ExpiresAt: p.ExpiresAt,
			Username: u.name, Role: u.role, SuspendedAt: pgtype.Timestamptz{Time: s.now(), Valid: u.suspended},
		}, nil
	}
	return dbgen.GetSessionByTokenHashRow{}, pgx.ErrNoRows
}

// TouchSession implements auth.SessionStore.
func (s *Store) TouchSession(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; ok {
		s.seen[id] = s.now()
	}
	return nil
}

// DeleteSession implements auth.SessionStore.
func (s *Store) DeleteSession(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	delete(s.seen, id)
	return nil
}

// DeleteUserSessions implements auth.SessionStore.
func (s *Store) DeleteUserSessions(_ context.Context, userID uuid.UUID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id, p := range s.sessions {
		if p.UserID == userID {
			delete(s.sessions, id)
			delete(s.seen, id)
			n++
		}
	}
	return n, nil
}

// Env is a middleware backed by an in-memory store.
type Env struct {
	Store      *Store
	Sessions   *auth.Sessions
	Middleware *auth.Middleware
}

// New returns an Env whose cookies follow publicURL.
func New(publicURL string) *Env {
	st := NewStore()
	ss := &auth.Sessions{Store: st, Idle: time.Hour, MaxAge: 24 * time.Hour}
	return &Env{Store: st, Sessions: ss, Middleware: &auth.Middleware{Sessions: ss, Cookies: auth.CookiesFor(publicURL)}}
}

// Login creates a user with role and a session for it, and returns a
// request modifier that sends the session cookie, the CSRF cookie and the
// CSRF header.
func (e *Env) Login(t testing.TB, name, role string) (auth.Principal, func(*http.Request)) {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{UserID: id, Username: name, Role: role, Method: auth.MethodSession}
	e.Store.AddUser(id, name, role)
	s, err := e.Sessions.Create(context.Background(), p, "", "")
	if err != nil {
		t.Fatal(err)
	}
	p.SessionID = s.ID
	return p, e.WithSession(s.Token, s.CSRFToken)
}

// WithSession returns a request modifier that sends the given session
// cookie and CSRF token.
func (e *Env) WithSession(token, csrf string) func(*http.Request) {
	c := e.Middleware.Cookies
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: c.SessionName(), Value: token}) //nolint:gosec // request cookie: attributes do not apply
		WithCSRF(c, csrf)(r)
	}
}

// WithCSRF returns a request modifier that sends csrf as cookie and header.
func WithCSRF(c auth.Cookies, csrf string) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: c.CSRFName(), Value: csrf}) //nolint:gosec // request cookie: attributes do not apply
		r.Header.Set(auth.CSRFHeader, csrf)
	}
}

// AnonCSRF returns a request modifier for an anonymous client holding a
// valid CSRF cookie and header.
func AnonCSRF(c auth.Cookies) func(*http.Request) {
	t, _ := auth.NewToken()
	return WithCSRF(c, t)
}
