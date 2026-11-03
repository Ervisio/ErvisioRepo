package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Errors returned by the account flows. Callers map them to HTTP statuses;
// their messages are safe to show.
var (
	ErrRegistrationClosed = errors.New("registration is closed on this instance")
	ErrInviteRequired     = errors.New("a valid invitation is required to register")
	ErrUsernameTaken      = errors.New("this name is already taken")
	ErrWeakPassword       = errors.New("password too short or too long")
	ErrInvalidCredentials = errors.New("invalid login or password")
	ErrEmailNotVerified   = errors.New("the email address is not verified yet; a new confirmation link was sent")
	ErrSuspended          = errors.New("this account is suspended")
	ErrInvalidToken       = errors.New("the link is invalid or expired")
	ErrNotFound           = errors.New("not found")
)

// RateLimitError means the caller must wait RetryAfter before trying again.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return "too many attempts, retry later" }

// Enqueuer inserts background jobs (jobs.Runner implements it).
type Enqueuer interface {
	Enqueue(ctx context.Context, args river.JobArgs) error
}

// Token purposes stored in one_time_tokens.
const (
	PurposeVerifyEmail   = "verify_email"
	PurposeResetPassword = "reset_password"
)

// Service implements the account flows.
type Service struct {
	pool     *pgxpool.Pool
	q        *dbgen.Queries
	hasher   *auth.Hasher
	sessions *auth.Sessions
	limiter  *auth.Limiter
	jobs     Enqueuer
	log      *slog.Logger
	cfg      config.Auth
	policy   string
	mailOn   bool
	now      func() time.Time
}

// Options configures a Service.
type Options struct {
	Pool     *pgxpool.Pool
	Hasher   *auth.Hasher
	Sessions *auth.Sessions
	Limiter  *auth.Limiter
	Jobs     Enqueuer // nil drops mail jobs (tests)
	Log      *slog.Logger
	Config   *config.Config
	Now      func() time.Time // defaults to time.Now
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
	return &Service{
		pool: o.Pool, q: dbgen.New(o.Pool), hasher: o.Hasher, sessions: o.Sessions, limiter: o.Limiter,
		jobs: o.Jobs, log: log, cfg: o.Config.Auth, policy: o.Config.Security.Registration,
		mailOn: o.Config.Mail.Enabled, now: now,
	}
}

// VerificationRequired reports whether new accounts must verify their email
// before signing in.
func (s *Service) VerificationRequired() bool { return s.mailOn && s.cfg.RequireVerifiedEmail }

// User is the public view of an account.
type User struct {
	ID            uuid.UUID
	Username      string
	DisplayName   string
	Role          string
	Locale        string
	Email         string
	EmailVerified bool
	CreatedAt     time.Time
}

// Client describes the caller of a flow, for rate limits and the audit log.
type Client struct {
	IP        string
	UserAgent string
}

// RegisterInput is a sign-up request.
type RegisterInput struct {
	Username    string
	Email       string
	Password    string
	InviteToken string
	Locale      string
}

// RegisterResult tells whether the address must be verified before login.
// It is the same whether or not the address already had an account.
type RegisterResult struct {
	VerificationRequired bool
}

func (s *Service) checkPassword(pw string) error {
	if utf8.RuneCountInString(pw) < s.cfg.PasswordMinLength || len(pw) > auth.MaxPasswordBytes {
		return ErrWeakPassword
	}
	return nil
}

func (s *Service) allowAction(c Client) error {
	if ok, wait := s.limiter.AllowAction("action:" + c.IP); !ok {
		return &RateLimitError{RetryAfter: wait}
	}
	return nil
}

func normLocale(l string) string {
	if l == "it" {
		return "it"
	}
	return "en"
}

// Register creates an account. The first account of the instance becomes
// its admin, whatever the registration policy. A taken email address is
// not revealed: nothing is created, the owner gets a notice by mail and the
// result is the same as a success.
func (s *Service) Register(ctx context.Context, in RegisterInput, c Client) (RegisterResult, error) {
	if err := ValidateName(in.Username); err != nil {
		return RegisterResult{}, err
	}
	addr, err := NormalizeEmail(in.Email)
	if err != nil {
		return RegisterResult{}, err
	}
	if err := s.checkPassword(in.Password); err != nil {
		return RegisterResult{}, err
	}
	if err := s.allowAction(c); err != nil {
		return RegisterResult{}, err
	}
	// Hash before any lookup so every outcome costs the same.
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return RegisterResult{}, err
	}

	var (
		first     bool
		duplicate bool
		emailID   uuid.UUID
	)
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.AcquireRegistrationLock(ctx); err != nil {
			return fmt.Errorf("registration lock: %w", err)
		}
		n, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		first = n == 0
		var inviteHash []byte
		if !first {
			switch s.policy {
			case config.RegistrationClosed:
				return ErrRegistrationClosed
			case config.RegistrationInvite:
				if !auth.ValidTokenFormat(in.InviteToken) {
					return ErrInviteRequired
				}
				inviteHash = auth.HashToken(in.InviteToken)
				inv, err := q.GetValidInvite(ctx, inviteHash)
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrInviteRequired
				}
				if err != nil {
					return fmt.Errorf("get invite: %w", err)
				}
				if inv.Email.Valid && !strings.EqualFold(inv.Email.String, addr) {
					return ErrInviteRequired
				}
			}
		}
		taken, err := q.NamespaceTaken(ctx, in.Username)
		if err != nil {
			return fmt.Errorf("namespace taken: %w", err)
		}
		if taken {
			return ErrUsernameTaken
		}
		if taken, err = q.EmailTaken(ctx, addr); err != nil {
			return fmt.Errorf("email taken: %w", err)
		} else if taken {
			duplicate = true
			return audit.Record(ctx, q, audit.Event{Action: audit.ActionRegisterDuplicate, IP: c.IP,
				Data: map[string]any{"username": in.Username}})
		}

		userID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("user id: %w", err)
		}
		if emailID, err = uuid.NewV7(); err != nil {
			return fmt.Errorf("email id: %w", err)
		}
		role := auth.RoleUser
		var verifiedAt pgtype.Timestamptz
		if first {
			// The bootstrap admin has no one to verify the address for.
			role, verifiedAt = auth.RoleAdmin, pgtype.Timestamptz{Time: s.now(), Valid: true}
		}
		if err := q.InsertNamespace(ctx, dbgen.InsertNamespaceParams{ID: userID, Kind: "user", Name: in.Username}); err != nil {
			if isUniqueViolation(err) {
				return ErrUsernameTaken
			}
			return fmt.Errorf("insert namespace: %w", err)
		}
		if err := q.InsertUser(ctx, dbgen.InsertUserParams{ID: userID, DisplayName: "", Role: role, Locale: normLocale(in.Locale)}); err != nil {
			return fmt.Errorf("insert user: %w", err)
		}
		if err := q.InsertEmail(ctx, dbgen.InsertEmailParams{ID: emailID, UserID: userID, Address: addr, IsPrimary: true, VerifiedAt: verifiedAt}); err != nil {
			return fmt.Errorf("insert email: %w", err)
		}
		if err := q.UpsertPassword(ctx, dbgen.UpsertPasswordParams{UserID: userID, Hash: hash}); err != nil {
			return fmt.Errorf("insert password: %w", err)
		}
		if inviteHash != nil {
			if _, err := q.ConsumeInvite(ctx, dbgen.ConsumeInviteParams{UsedBy: uuid.NullUUID{UUID: userID, Valid: true}, TokenHash: inviteHash}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrInviteRequired
				}
				return fmt.Errorf("consume invite: %w", err)
			}
		}
		if err := audit.Record(ctx, q, audit.Event{Actor: userID, Action: audit.ActionRegister, TargetType: "user",
			TargetID: userID.String(), IP: c.IP, Data: map[string]any{"username": in.Username, "policy": s.policy}}); err != nil {
			return err
		}
		if first {
			return audit.Record(ctx, q, audit.Event{Actor: userID, Action: audit.ActionFirstAdmin, TargetType: "user",
				TargetID: userID.String(), IP: c.IP})
		}
		return nil
	})
	if err != nil {
		return RegisterResult{}, err
	}
	switch {
	case duplicate:
		s.enqueue(ctx, RegistrationNoticeArgs{Address: addr})
	case !first:
		s.enqueue(ctx, VerifyEmailArgs{EmailID: emailID})
	}
	if first {
		s.log.InfoContext(ctx, "first user registered as instance admin", "username", in.Username)
		return RegisterResult{VerificationRequired: false}, nil
	}
	return RegisterResult{VerificationRequired: s.VerificationRequired()}, nil
}

// LoginResult is a successful login.
type LoginResult struct {
	User    User
	Session auth.NewSession
}

// Login checks a username or email address and its password, then starts a
// new session. oldToken, when set, is the session cookie the browser sent:
// that session is revoked (rotation at login). Unknown accounts cost the
// same as wrong passwords and give the same error. It does not ask for a
// second factor: the HTTP login goes through the mfa service instead.
func (s *Service) Login(ctx context.Context, login, password, oldToken string, c Client) (LoginResult, error) {
	p, err := s.Authenticate(ctx, login, password, c)
	if err != nil {
		return LoginResult{}, err
	}
	return s.StartSession(ctx, p.UserID, oldToken, c, "password")
}

// Authenticate checks a username or email address and its password (the
// first factor) without starting a session. Unknown accounts cost the same
// as wrong passwords and give the same error.
func (s *Service) Authenticate(ctx context.Context, login, password string, c Client) (auth.Principal, error) {
	login = strings.TrimSpace(login)
	if login == "" || len(login) > MaxEmailLength || password == "" || len(password) > auth.MaxPasswordBytes {
		return auth.Principal{}, ErrInvalidCredentials
	}
	ipKey, loginKey := auth.IPKey(c.IP), auth.AccountKey(strings.ToLower(login))
	if wait := s.limiter.LoginWait(ipKey, loginKey); wait > 0 {
		return auth.Principal{}, &RateLimitError{RetryAfter: wait}
	}

	acct, found, err := s.lookupLogin(ctx, login)
	if err != nil {
		return auth.Principal{}, err
	}
	keys := []string{ipKey, loginKey}
	if found {
		userKey := auth.AccountKey(acct.ID.String())
		keys = append(keys, userKey)
		if wait := s.limiter.LoginWait(userKey); wait > 0 {
			return auth.Principal{}, &RateLimitError{RetryAfter: wait}
		}
	}

	ok, rehash := false, false
	if found && acct.PasswordHash.Valid {
		ok, rehash, err = s.hasher.Verify(ctx, acct.PasswordHash.String, password)
		if err != nil && !errors.Is(err, auth.ErrMalformedHash) {
			return auth.Principal{}, err
		}
	} else {
		s.hasher.VerifyDummy(ctx, password)
	}
	if !ok {
		s.limiter.Fail(keys...)
		ev := audit.Event{Action: audit.ActionLoginFailed, IP: c.IP, TargetType: "user"}
		if found {
			ev.TargetID = acct.ID.String()
		}
		s.record(ctx, ev)
		return auth.Principal{}, ErrInvalidCredentials
	}

	if acct.SuspendedAt.Valid {
		return auth.Principal{}, ErrSuspended
	}
	if s.VerificationRequired() && !acct.EmailVerifiedAt.Valid {
		if acct.EmailID.Valid && s.allowAction(c) == nil {
			s.enqueue(ctx, VerifyEmailArgs{EmailID: acct.EmailID.UUID})
		}
		return auth.Principal{}, ErrEmailNotVerified
	}
	s.limiter.Reset(loginKey)
	s.limiter.Reset(auth.AccountKey(acct.ID.String()))

	if rehash {
		if h, err := s.hasher.Hash(ctx, password); err == nil {
			if err := s.q.UpsertPassword(ctx, dbgen.UpsertPasswordParams{UserID: acct.ID, Hash: h}); err != nil {
				s.log.WarnContext(ctx, "store rehashed password", "error", err)
			} else {
				s.record(ctx, audit.Event{Actor: acct.ID, Action: audit.ActionPasswordRehash, TargetType: "user", TargetID: acct.ID.String(), IP: c.IP})
			}
		}
	}
	return auth.Principal{UserID: acct.ID, Username: acct.Username, Role: acct.Role}, nil
}

// StartSession starts a session for an account whose factors were checked
// by the caller. It checks again that the account may sign in, revokes
// oldToken (the session cookie the browser sent, rotation at login) and
// records the login with method ("password", "totp", "passkey"...).
func (s *Service) StartSession(ctx context.Context, userID uuid.UUID, oldToken string, c Client, method string) (LoginResult, error) {
	row, err := s.q.GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("get user: %w", err)
	}
	if row.SuspendedAt.Valid {
		return LoginResult{}, ErrSuspended
	}
	if s.VerificationRequired() && !row.EmailVerifiedAt.Valid {
		return LoginResult{}, ErrEmailNotVerified
	}
	if oldToken != "" {
		if old, err := s.sessions.Lookup(ctx, oldToken); err == nil {
			if err := s.sessions.Revoke(ctx, old.ID); err != nil {
				return LoginResult{}, err
			}
		}
	}
	p := auth.Principal{UserID: row.ID, Username: row.Username, Role: row.Role, Method: auth.MethodSession}
	sess, err := s.sessions.Create(ctx, p, c.IP, c.UserAgent)
	if err != nil {
		return LoginResult{}, err
	}
	s.record(ctx, audit.Event{Actor: row.ID, Action: audit.ActionLogin, TargetType: "session", TargetID: sess.ID.String(), IP: c.IP,
		Data: map[string]any{"method": method}})
	u, err := s.User(ctx, row.ID)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: u, Session: sess}, nil
}

// VerifyPassword checks the password of a signed-in account (re-auth
// before a sensitive change). Failures count against the account.
func (s *Service) VerifyPassword(ctx context.Context, userID uuid.UUID, password string) error {
	key := auth.AccountKey(userID.String())
	if wait := s.limiter.LoginWait(key); wait > 0 {
		return &RateLimitError{RetryAfter: wait}
	}
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	acct, found, err := s.lookupLogin(ctx, u.Username)
	if err != nil {
		return err
	}
	ok := false
	if found && acct.ID == userID && acct.PasswordHash.Valid && password != "" && len(password) <= auth.MaxPasswordBytes {
		ok, _, err = s.hasher.Verify(ctx, acct.PasswordHash.String, password)
		if err != nil && !errors.Is(err, auth.ErrMalformedHash) {
			return err
		}
	} else {
		s.hasher.VerifyDummy(ctx, password)
	}
	if !ok {
		s.limiter.Fail(key)
		return ErrInvalidCredentials
	}
	return nil
}

type loginRow struct {
	ID              uuid.UUID
	Username        string
	Role            string
	SuspendedAt     pgtype.Timestamptz
	PasswordHash    pgtype.Text
	EmailID         uuid.NullUUID
	EmailVerifiedAt pgtype.Timestamptz
}

func (s *Service) lookupLogin(ctx context.Context, login string) (loginRow, bool, error) {
	var (
		r   loginRow
		err error
	)
	if strings.Contains(login, "@") {
		var row dbgen.GetLoginByEmailRow
		row, err = s.q.GetLoginByEmail(ctx, login)
		r = loginRow{row.ID, row.Username, row.Role, row.SuspendedAt, row.PasswordHash,
			uuid.NullUUID{UUID: row.EmailID, Valid: err == nil}, row.EmailVerifiedAt}
	} else {
		var row dbgen.GetLoginByUsernameRow
		row, err = s.q.GetLoginByUsername(ctx, login)
		r = loginRow{row.ID, row.Username, row.Role, row.SuspendedAt, row.PasswordHash, row.EmailID, row.EmailVerifiedAt}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return loginRow{}, false, nil
	}
	if err != nil {
		return loginRow{}, false, fmt.Errorf("lookup login: %w", err)
	}
	return r, true, nil
}

// Logout revokes the caller's session.
func (s *Service) Logout(ctx context.Context, p auth.Principal, c Client) error {
	if p.SessionID == uuid.Nil {
		return nil
	}
	if err := s.sessions.Revoke(ctx, p.SessionID); err != nil {
		return err
	}
	s.record(ctx, audit.Event{Actor: p.UserID, Action: audit.ActionLogout, TargetType: "session", TargetID: p.SessionID.String(), IP: c.IP})
	return nil
}

// User returns an account by id, or ErrNotFound.
func (s *Service) User(ctx context.Context, id uuid.UUID) (User, error) {
	row, err := s.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return User{ID: row.ID, Username: row.Username, DisplayName: row.DisplayName, Role: row.Role, Locale: row.Locale,
		Email: row.Email.String, EmailVerified: row.EmailVerifiedAt.Valid, CreatedAt: row.CreatedAt.Time}, nil
}

// CountUsers returns the number of accounts.
func (s *Service) CountUsers(ctx context.Context) (int64, error) {
	n, err := s.q.CountUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// VerifyEmail consumes a verification token and marks its address verified.
func (s *Service) VerifyEmail(ctx context.Context, token string, c Client) error {
	if !auth.ValidTokenFormat(token) {
		return ErrInvalidToken
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		t, err := s.consume(ctx, q, token, PurposeVerifyEmail)
		if err != nil {
			return err
		}
		if !t.EmailID.Valid {
			return ErrInvalidToken
		}
		if _, err := q.MarkEmailVerified(ctx, dbgen.MarkEmailVerifiedParams{ID: t.EmailID.UUID, UserID: t.UserID}); err != nil {
			return fmt.Errorf("mark email verified: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: t.UserID, Action: audit.ActionEmailVerified, TargetType: "email",
			TargetID: t.EmailID.UUID.String(), IP: c.IP})
	})
}

// ResendVerification sends a new verification link if address belongs to
// an unverified account. The answer never says whether it does.
func (s *Service) ResendVerification(ctx context.Context, address string, c Client) error {
	addr, err := NormalizeEmail(address)
	if err != nil {
		return err
	}
	if err := s.allowAction(c); err != nil {
		return err
	}
	s.enqueue(ctx, ResendVerificationArgs{Address: addr})
	return nil
}

// RequestPasswordReset sends a reset link if address belongs to an account.
// The answer never says whether it does; the lookup happens in the job.
func (s *Service) RequestPasswordReset(ctx context.Context, address string, c Client) error {
	addr, err := NormalizeEmail(address)
	if err != nil {
		return err
	}
	if err := s.allowAction(c); err != nil {
		return err
	}
	s.record(ctx, audit.Event{Action: audit.ActionPasswordResetRequest, IP: c.IP})
	s.enqueue(ctx, PasswordResetArgs{Address: addr})
	return nil
}

// ResetPassword consumes a reset token and sets a new password. It also
// verifies the address the link was sent to and signs the user out
// everywhere.
func (s *Service) ResetPassword(ctx context.Context, token, password string, c Client) error {
	if !auth.ValidTokenFormat(token) {
		return ErrInvalidToken
	}
	if err := s.checkPassword(password); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	var userID uuid.UUID
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		t, err := s.consume(ctx, q, token, PurposeResetPassword)
		if err != nil {
			return err
		}
		userID = t.UserID
		if err := q.UpsertPassword(ctx, dbgen.UpsertPasswordParams{UserID: t.UserID, Hash: hash}); err != nil {
			return fmt.Errorf("store password: %w", err)
		}
		if t.EmailID.Valid {
			if _, err := q.MarkEmailVerified(ctx, dbgen.MarkEmailVerifiedParams{ID: t.EmailID.UUID, UserID: t.UserID}); err != nil {
				return fmt.Errorf("mark email verified: %w", err)
			}
		}
		if err := q.DeleteOneTimeTokens(ctx, dbgen.DeleteOneTimeTokensParams{UserID: t.UserID, Purpose: PurposeResetPassword}); err != nil {
			return fmt.Errorf("delete reset tokens: %w", err)
		}
		if _, err := q.DeleteUserSessions(ctx, t.UserID); err != nil {
			return fmt.Errorf("revoke sessions: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: t.UserID, Action: audit.ActionPasswordReset, TargetType: "user",
			TargetID: t.UserID.String(), IP: c.IP})
	})
	if err != nil {
		return err
	}
	s.limiter.Reset(auth.AccountKey(userID.String()))
	return nil
}

// Invite is a new invitation. Token is shown once and never stored.
type Invite struct {
	ID        uuid.UUID
	Token     string
	Email     string
	ExpiresAt time.Time
}

// CreateInvite creates an invitation, optionally bound to one address.
// Authorization (instance admin) is the caller's job.
func (s *Service) CreateInvite(ctx context.Context, actor auth.Principal, email string, c Client) (Invite, error) {
	var addr pgtype.Text
	if strings.TrimSpace(email) != "" {
		a, err := NormalizeEmail(email)
		if err != nil {
			return Invite{}, err
		}
		addr = pgtype.Text{String: a, Valid: true}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Invite{}, fmt.Errorf("invite id: %w", err)
	}
	token, hash := auth.NewToken()
	expires := s.now().Add(time.Duration(s.cfg.InviteTTL))
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.InsertInvite(ctx, dbgen.InsertInviteParams{ID: id, TokenHash: hash, Email: addr,
			CreatedBy: uuid.NullUUID{UUID: actor.UserID, Valid: actor.UserID != uuid.Nil},
			ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}); err != nil {
			return fmt.Errorf("insert invite: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: actor.UserID, Action: audit.ActionInviteCreated, TargetType: "invite",
			TargetID: id.String(), IP: c.IP, Data: map[string]any{"bound_to_email": addr.Valid}})
	})
	if err != nil {
		return Invite{}, err
	}
	return Invite{ID: id, Token: token, Email: addr.String, ExpiresAt: expires}, nil
}

func (s *Service) consume(ctx context.Context, q *dbgen.Queries, token, purpose string) (dbgen.ConsumeOneTimeTokenRow, error) {
	t, err := q.ConsumeOneTimeToken(ctx, dbgen.ConsumeOneTimeTokenParams{TokenHash: auth.HashToken(token), Purpose: purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrInvalidToken
	}
	if err != nil {
		return t, fmt.Errorf("consume token: %w", err)
	}
	if !t.ExpiresAt.Time.After(s.now()) {
		return t, ErrInvalidToken
	}
	return t, nil
}

// tx runs fn in a transaction, committed when fn returns nil.
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

// enqueue inserts a mail job; failures are logged, never shown to the
// caller (the answer must not depend on them).
func (s *Service) enqueue(ctx context.Context, args river.JobArgs) {
	if !s.mailOn || s.jobs == nil {
		return
	}
	if err := s.jobs.Enqueue(context.WithoutCancel(ctx), args); err != nil {
		s.log.ErrorContext(ctx, "enqueue mail job", "kind", args.Kind(), "error", err)
	}
}

// record appends an audit event outside a transaction; failures are logged.
func (s *Service) record(ctx context.Context, e audit.Event) {
	if err := audit.Record(context.WithoutCancel(ctx), s.q, e); err != nil {
		s.log.ErrorContext(ctx, "audit", "action", e.Action, "error", err)
	}
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
