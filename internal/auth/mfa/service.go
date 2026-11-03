package mfa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// Limits of the two-step login.
const (
	ChallengeTTL = 5 * time.Minute
	MaxAttempts  = 5
)

// Second-factor methods offered to a pending login.
const (
	MethodTOTP     = "totp"
	MethodWebAuthn = "webauthn"
	MethodRecovery = "recovery"
)

// Challenge kinds (mfa_challenges.kind).
const (
	kindLogin    = "login"
	kindRegister = "webauthn_register"
	kindPasskey  = "passkey_login"
)

// Audit actions of the second factors.
const (
	ActionTOTPEnabled         = "auth.mfa_totp_enabled"
	ActionTOTPDisabled        = "auth.mfa_totp_disabled"
	ActionRecoveryRegenerated = "auth.mfa_recovery_regenerated"
	ActionRecoveryUsed        = "auth.mfa_recovery_used"
	ActionPasskeyAdded        = "auth.mfa_passkey_added"   //nolint:gosec // audit action name, not a credential
	ActionPasskeyRemoved      = "auth.mfa_passkey_removed" //nolint:gosec // audit action name, not a credential
	ActionFailed              = "auth.mfa_failed"
)

// Errors of the second-factor flows; their messages are safe to show.
var (
	ErrInvalidCode     = errors.New("invalid or already used code")
	ErrChallenge       = errors.New("the sign-in attempt is invalid or expired; sign in again")
	ErrTOTPEnabled     = errors.New("an authenticator app is already set up")
	ErrNoTOTPSetup     = errors.New("no authenticator app setup in progress")
	ErrNotEnabled      = errors.New("this factor is not set up")
	ErrFactorRequired  = errors.New("two-factor authentication is required on this account: the last factor cannot be removed")
	ErrNoFactor        = errors.New("set up a second factor first")
	ErrReauth          = errors.New("confirm with your password or a current code")
	ErrWebAuthnOff     = errors.New("security keys are not available on this instance")
	ErrInvalidPasskey  = errors.New("the security key response is invalid")
	ErrNotFound        = errors.New("not found")
	ErrInvalidName     = errors.New("the name must have 1 to 64 characters")
	errMalformedSecret = errors.New("mfa: malformed TOTP secret")
)

// Identity is the account service as used here (satisfied by
// *identity.Service).
type Identity interface {
	Authenticate(ctx context.Context, login, password string, c identity.Client) (auth.Principal, error)
	StartSession(ctx context.Context, userID uuid.UUID, oldToken string, c identity.Client, method string) (identity.LoginResult, error)
	VerifyPassword(ctx context.Context, userID uuid.UUID, password string) error
	User(ctx context.Context, id uuid.UUID) (identity.User, error)
}

// Service implements the second factors and the two-step login.
type Service struct {
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	ids     Identity
	limiter *auth.Limiter
	sealer  *Sealer
	wa      *webAuthn
	policy  string
	issuer  string
	log     *slog.Logger
	now     func() time.Time
}

// Options configures a Service.
type Options struct {
	Pool     *pgxpool.Pool
	Identity Identity
	Limiter  *auth.Limiter
	// MasterKey seals the TOTP secrets; nil stores them in clear.
	MasterKey []byte
	Config    *config.Config
	Log       *slog.Logger
	Now       func() time.Time // defaults to time.Now
}

// New builds the service. WebAuthn is disabled (with a warning) when the
// public URL gives no usable relying party.
func New(o Options) (*Service, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	sealer, err := NewSealer(o.MasterKey)
	if err != nil {
		return nil, err
	}
	if !sealer.Encrypted() {
		o.Log.Warn("no master key: TOTP secrets are stored unencrypted (set security.master_key_file)")
	}
	wa, err := newWebAuthn(o.Config.Server.PublicURL)
	if err != nil {
		o.Log.Warn("security keys and passkeys disabled", "error", err)
		wa = nil
	}
	return &Service{
		pool: o.Pool, q: dbgen.New(o.Pool), ids: o.Identity, limiter: o.Limiter, sealer: sealer, wa: wa,
		policy: o.Config.Auth.Require2FA, issuer: "Ervisio Repo", log: o.Log, now: o.Now,
	}, nil
}

// Pending is a login waiting for its second factor (the mfa_pending state):
// Token goes in the MFA cookie, never in a session cookie.
type Pending struct {
	Token     string
	ExpiresAt time.Time
	Methods   []string
	// Enroll means the account has no factor yet but policy requires one:
	// the login completes by setting up an authenticator app.
	Enroll bool
}

// Outcome is the result of the first step: a session or a pending login.
type Outcome struct {
	Session *identity.LoginResult
	Pending *Pending
}

// Status is the second-factor setup of an account.
type Status struct {
	TOTP           bool
	TOTPPending    bool
	Passkeys       []Passkey
	RecoveryUnused int
	RecoveryTotal  int
	Required       bool
	WebAuthn       bool
}

// Passkey is a registered WebAuthn credential.
type Passkey struct {
	ID         uuid.UUID
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Reauth confirms a sensitive change: the password, or a current TOTP or
// recovery code.
type Reauth struct {
	Password string
	Code     string
}

// Login checks the password, then either starts the session or returns a
// pending login when the account has (or must set up) a second factor.
func (s *Service) Login(ctx context.Context, login, password, oldToken string, c identity.Client) (Outcome, error) {
	p, err := s.ids.Authenticate(ctx, login, password, c)
	if err != nil {
		return Outcome{}, err
	}
	return s.Gate(ctx, p, oldToken, c, "password")
}

// Gate is the second step of every first-factor login (password today,
// OIDC later): it starts the session only when no second factor is due.
func (s *Service) Gate(ctx context.Context, p auth.Principal, oldToken string, c identity.Client, method string) (Outcome, error) {
	f, err := s.factors(ctx, p.UserID)
	if err != nil {
		return Outcome{}, err
	}
	var methods []string
	if f.totp {
		methods = append(methods, MethodTOTP)
	}
	if f.passkeys > 0 && s.wa != nil {
		methods = append(methods, MethodWebAuthn)
	}
	if f.recovery > 0 {
		methods = append(methods, MethodRecovery)
	}
	enroll := false
	if !f.any() {
		req, err := s.required(ctx, p.UserID, p.Role)
		if err != nil {
			return Outcome{}, err
		}
		if !req {
			res, err := s.ids.StartSession(ctx, p.UserID, oldToken, c, method)
			if err != nil {
				return Outcome{}, err
			}
			return Outcome{Session: &res}, nil
		}
		enroll, methods = true, []string{MethodTOTP}
	} else if len(methods) == 0 {
		// Only passkeys while WebAuthn is off: recovery codes still work.
		methods = []string{MethodRecovery}
	}
	token, exp, err := s.newChallenge(ctx, kindLogin, uuid.NullUUID{UUID: p.UserID, Valid: true}, enroll, nil, c)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Pending: &Pending{Token: token, ExpiresAt: exp, Methods: methods, Enroll: enroll}}, nil
}

// PendingInfo describes a pending login (for a page reload).
func (s *Service) PendingInfo(ctx context.Context, token string) (Pending, error) {
	ch, err := s.challenge(ctx, token, kindLogin)
	if err != nil {
		return Pending{}, err
	}
	if ch.Enroll {
		return Pending{ExpiresAt: ch.ExpiresAt.Time, Methods: []string{MethodTOTP}, Enroll: true}, nil
	}
	f, err := s.factors(ctx, ch.UserID.UUID)
	if err != nil {
		return Pending{}, err
	}
	var methods []string
	if f.totp {
		methods = append(methods, MethodTOTP)
	}
	if f.passkeys > 0 && s.wa != nil {
		methods = append(methods, MethodWebAuthn)
	}
	methods = append(methods, MethodRecovery)
	return Pending{ExpiresAt: ch.ExpiresAt.Time, Methods: methods}, nil
}

// VerifyTOTP completes a pending login with a TOTP code.
func (s *Service) VerifyTOTP(ctx context.Context, token, code, oldToken string, c identity.Client) (identity.LoginResult, error) {
	ch, err := s.secondFactor(ctx, token)
	if err != nil {
		return identity.LoginResult{}, err
	}
	ok, err := s.useTOTP(ctx, ch.UserID.UUID, code)
	if err != nil {
		return identity.LoginResult{}, err
	}
	if !ok {
		return identity.LoginResult{}, s.fail(ctx, ch, MethodTOTP, c)
	}
	return s.finish(ctx, ch, oldToken, c, MethodTOTP)
}

// VerifyRecovery completes a pending login with a recovery code, which is
// used up.
func (s *Service) VerifyRecovery(ctx context.Context, token, code, oldToken string, c identity.Client) (identity.LoginResult, error) {
	ch, err := s.secondFactor(ctx, token)
	if err != nil {
		return identity.LoginResult{}, err
	}
	ok, err := s.useRecovery(ctx, ch.UserID.UUID, code, c)
	if err != nil {
		return identity.LoginResult{}, err
	}
	if !ok {
		return identity.LoginResult{}, s.fail(ctx, ch, MethodRecovery, c)
	}
	return s.finish(ctx, ch, oldToken, c, MethodRecovery)
}

// secondFactor loads a pending login that expects a second factor and
// checks the account backoff.
func (s *Service) secondFactor(ctx context.Context, token string) (dbgen.GetMFAChallengeRow, error) {
	ch, err := s.challenge(ctx, token, kindLogin)
	if err != nil {
		return ch, err
	}
	if ch.Enroll {
		return ch, ErrChallenge
	}
	if wait := s.limiter.LoginWait(mfaKey(ch.UserID.UUID)); wait > 0 {
		return ch, &identity.RateLimitError{RetryAfter: wait}
	}
	return ch, nil
}

// BeginEnrollTOTP starts the authenticator app setup of a pending login
// whose account has no factor but must have one.
func (s *Service) BeginEnrollTOTP(ctx context.Context, token string) (secret, uri string, err error) {
	ch, err := s.challenge(ctx, token, kindLogin)
	if err != nil {
		return "", "", err
	}
	if !ch.Enroll {
		return "", "", ErrChallenge
	}
	return s.setupTOTP(ctx, ch.UserID.UUID)
}

// ConfirmEnrollTOTP confirms the setup with a first code, creates the
// recovery codes and completes the login.
func (s *Service) ConfirmEnrollTOTP(ctx context.Context, token, code, oldToken string, c identity.Client) (identity.LoginResult, []string, error) {
	ch, err := s.challenge(ctx, token, kindLogin)
	if err != nil {
		return identity.LoginResult{}, nil, err
	}
	if !ch.Enroll {
		return identity.LoginResult{}, nil, ErrChallenge
	}
	if wait := s.limiter.LoginWait(mfaKey(ch.UserID.UUID)); wait > 0 {
		return identity.LoginResult{}, nil, &identity.RateLimitError{RetryAfter: wait}
	}
	codes, err := s.confirmTOTP(ctx, ch.UserID.UUID, code, c)
	if errors.Is(err, ErrInvalidCode) {
		return identity.LoginResult{}, nil, s.fail(ctx, ch, MethodTOTP, c)
	}
	if err != nil {
		return identity.LoginResult{}, nil, err
	}
	res, err := s.finish(ctx, ch, oldToken, c, MethodTOTP)
	return res, codes, err
}

// finish consumes the pending login (once: a concurrent or later reuse
// fails) and starts the session.
func (s *Service) finish(ctx context.Context, ch dbgen.GetMFAChallengeRow, oldToken string, c identity.Client, method string) (identity.LoginResult, error) {
	n, err := s.q.ConsumeMFAChallenge(ctx, ch.ID)
	if err != nil {
		return identity.LoginResult{}, fmt.Errorf("consume challenge: %w", err)
	}
	if n != 1 {
		return identity.LoginResult{}, ErrChallenge
	}
	s.limiter.Reset(mfaKey(ch.UserID.UUID))
	return s.ids.StartSession(ctx, ch.UserID.UUID, oldToken, c, method)
}

// fail counts a wrong second factor: against the account backoff and the
// pending login, which is dropped after MaxAttempts.
func (s *Service) fail(ctx context.Context, ch dbgen.GetMFAChallengeRow, method string, c identity.Client) error {
	s.limiter.Fail(mfaKey(ch.UserID.UUID))
	n, err := s.q.FailMFAChallenge(ctx, ch.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("count attempt: %w", err)
	}
	if n >= MaxAttempts {
		if err := s.q.DeleteMFAChallenge(ctx, ch.ID); err != nil {
			return fmt.Errorf("drop challenge: %w", err)
		}
	}
	s.record(ctx, audit.Event{Actor: ch.UserID.UUID, Action: ActionFailed, TargetType: "user",
		TargetID: ch.UserID.UUID.String(), IP: c.IP, Data: map[string]any{"method": method}})
	return ErrInvalidCode
}

// Status returns the second-factor setup of an account.
func (s *Service) Status(ctx context.Context, p auth.Principal) (Status, error) {
	st := Status{WebAuthn: s.wa != nil}
	t, err := s.q.GetTOTP(ctx, p.UserID)
	switch {
	case err == nil:
		st.TOTP, st.TOTPPending = t.ConfirmedAt.Valid, !t.ConfirmedAt.Valid
	case !errors.Is(err, pgx.ErrNoRows):
		return st, fmt.Errorf("get totp: %w", err)
	}
	creds, err := s.q.ListWebAuthnCredentials(ctx, p.UserID)
	if err != nil {
		return st, fmt.Errorf("list credentials: %w", err)
	}
	st.Passkeys = make([]Passkey, 0, len(creds))
	for _, c := range creds {
		pk := Passkey{ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt.Time}
		if c.LastUsedAt.Valid {
			pk.LastUsedAt = &c.LastUsedAt.Time
		}
		st.Passkeys = append(st.Passkeys, pk)
	}
	rc, err := s.q.CountRecoveryCodes(ctx, p.UserID)
	if err != nil {
		return st, fmt.Errorf("count recovery codes: %w", err)
	}
	st.RecoveryUnused, st.RecoveryTotal = int(rc.Unused), int(rc.Total)
	if st.Required, err = s.required(ctx, p.UserID, p.Role); err != nil {
		return st, err
	}
	return st, nil
}

// SetupTOTP starts (or restarts) the authenticator app setup. Adding a
// factor needs reauthentication too: a stolen session must not be able to
// lock the owner out.
func (s *Service) SetupTOTP(ctx context.Context, p auth.Principal, r Reauth, c identity.Client) (secret, uri string, err error) {
	if err := s.reauth(ctx, p, r, c); err != nil {
		return "", "", err
	}
	return s.setupTOTP(ctx, p.UserID)
}

// ConfirmTOTP enables the authenticator app with a first code. It returns
// new recovery codes when the account had none.
func (s *Service) ConfirmTOTP(ctx context.Context, p auth.Principal, code string, c identity.Client) ([]string, error) {
	if wait := s.limiter.LoginWait(mfaKey(p.UserID)); wait > 0 {
		return nil, &identity.RateLimitError{RetryAfter: wait}
	}
	codes, err := s.confirmTOTP(ctx, p.UserID, code, c)
	if errors.Is(err, ErrInvalidCode) {
		s.limiter.Fail(mfaKey(p.UserID))
	}
	return codes, err
}

// DisableTOTP removes the authenticator app.
func (s *Service) DisableTOTP(ctx context.Context, p auth.Principal, r Reauth, c identity.Client) error {
	if err := s.reauth(ctx, p, r, c); err != nil {
		return err
	}
	f, err := s.factors(ctx, p.UserID)
	if err != nil {
		return err
	}
	if !f.totp {
		// A setup in progress can always be dropped.
		if _, err := s.q.DeleteTOTP(ctx, p.UserID); err != nil {
			return fmt.Errorf("delete totp: %w", err)
		}
		return nil
	}
	if f.passkeys == 0 {
		if err := s.allowLastFactorRemoval(ctx, p); err != nil {
			return err
		}
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		if _, err := q.DeleteTOTP(ctx, p.UserID); err != nil {
			return fmt.Errorf("delete totp: %w", err)
		}
		if f.passkeys == 0 {
			if err := q.DeleteRecoveryCodes(ctx, p.UserID); err != nil {
				return fmt.Errorf("delete recovery codes: %w", err)
			}
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionTOTPDisabled, TargetType: "user",
			TargetID: p.UserID.String(), IP: c.IP})
	})
}

// RegenerateRecoveryCodes replaces the recovery codes.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, p auth.Principal, r Reauth, c identity.Client) ([]string, error) {
	if err := s.reauth(ctx, p, r, c); err != nil {
		return nil, err
	}
	f, err := s.factors(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if !f.totp && f.passkeys == 0 {
		return nil, ErrNoFactor
	}
	var codes []string
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		if codes, err = replaceRecoveryCodes(ctx, q, p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionRecoveryRegenerated, TargetType: "user",
			TargetID: p.UserID.String(), IP: c.IP})
	})
	return codes, err
}

func (s *Service) setupTOTP(ctx context.Context, userID uuid.UUID) (string, string, error) {
	u, err := s.ids.User(ctx, userID)
	if err != nil {
		return "", "", err
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return "", "", err
	}
	sealed, err := s.sealer.Seal(secret, userID[:])
	if err != nil {
		return "", "", err
	}
	n, err := s.q.UpsertPendingTOTP(ctx, dbgen.UpsertPendingTOTPParams{UserID: userID, Secret: sealed})
	if err != nil {
		return "", "", fmt.Errorf("store totp: %w", err)
	}
	if n != 1 {
		return "", "", ErrTOTPEnabled
	}
	return EncodeSecret(secret), TOTPURI(s.issuer, u.Username, secret), nil
}

// confirmTOTP checks the first code of a setup and enables it; it returns
// new recovery codes when the account had none.
func (s *Service) confirmTOTP(ctx context.Context, userID uuid.UUID, code string, c identity.Client) ([]string, error) {
	t, err := s.q.GetTOTP(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoTOTPSetup
	}
	if err != nil {
		return nil, fmt.Errorf("get totp: %w", err)
	}
	if t.ConfirmedAt.Valid {
		return nil, ErrTOTPEnabled
	}
	secret, err := s.sealer.Open(t.Secret, userID[:])
	if err != nil {
		return nil, err
	}
	step, ok := MatchTOTP(secret, code, s.now(), t.LastStep)
	if !ok {
		return nil, ErrInvalidCode
	}
	var codes []string
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.ConfirmTOTP(ctx, dbgen.ConfirmTOTPParams{UserID: userID, Step: step})
		if err != nil {
			return fmt.Errorf("confirm totp: %w", err)
		}
		if n != 1 {
			return ErrInvalidCode
		}
		rc, err := q.CountRecoveryCodes(ctx, userID)
		if err != nil {
			return fmt.Errorf("count recovery codes: %w", err)
		}
		if rc.Unused == 0 {
			if codes, err = replaceRecoveryCodes(ctx, q, userID); err != nil {
				return err
			}
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionTOTPEnabled, TargetType: "user",
			TargetID: userID.String(), IP: c.IP})
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// useTOTP checks a code of the confirmed secret and accepts its time step
// only once (replay protection).
func (s *Service) useTOTP(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	t, err := s.q.GetTOTP(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get totp: %w", err)
	}
	if !t.ConfirmedAt.Valid {
		return false, nil
	}
	secret, err := s.sealer.Open(t.Secret, userID[:])
	if err != nil {
		return false, err
	}
	if len(secret) == 0 {
		return false, errMalformedSecret
	}
	step, ok := MatchTOTP(secret, code, s.now(), t.LastStep)
	if !ok {
		return false, nil
	}
	n, err := s.q.AdvanceTOTPStep(ctx, dbgen.AdvanceTOTPStepParams{UserID: userID, Step: step})
	if err != nil {
		return false, fmt.Errorf("advance totp: %w", err)
	}
	return n == 1, nil
}

// useRecovery uses up a recovery code; a used code matches no row.
func (s *Service) useRecovery(ctx context.Context, userID uuid.UUID, code string, c identity.Client) (bool, error) {
	canon := CanonicalRecoveryCode(code)
	if canon == "" {
		return false, nil
	}
	n, err := s.q.UseRecoveryCode(ctx, dbgen.UseRecoveryCodeParams{UserID: userID, CodeHash: HashRecoveryCode(canon)})
	if err != nil {
		return false, fmt.Errorf("use recovery code: %w", err)
	}
	if n != 1 {
		return false, nil
	}
	s.record(ctx, audit.Event{Actor: userID, Action: ActionRecoveryUsed, TargetType: "user", TargetID: userID.String(), IP: c.IP})
	return true, nil
}

func replaceRecoveryCodes(ctx context.Context, q *dbgen.Queries, userID uuid.UUID) ([]string, error) {
	codes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, fmt.Errorf("delete recovery codes: %w", err)
	}
	for _, code := range codes {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("recovery code id: %w", err)
		}
		if err := q.InsertRecoveryCode(ctx, dbgen.InsertRecoveryCodeParams{ID: id, UserID: userID,
			CodeHash: HashRecoveryCode(CanonicalRecoveryCode(code))}); err != nil {
			return nil, fmt.Errorf("insert recovery code: %w", err)
		}
	}
	return codes, nil
}

// reauth confirms a sensitive change with the password or a code. A code
// used here is used up like at login.
func (s *Service) reauth(ctx context.Context, p auth.Principal, r Reauth, c identity.Client) error {
	switch {
	case r.Password != "":
		err := s.ids.VerifyPassword(ctx, p.UserID, r.Password)
		if errors.Is(err, identity.ErrInvalidCredentials) {
			return ErrReauth
		}
		return err
	case r.Code != "":
		key := mfaKey(p.UserID)
		if wait := s.limiter.LoginWait(key); wait > 0 {
			return &identity.RateLimitError{RetryAfter: wait}
		}
		ok, err := s.useTOTP(ctx, p.UserID, r.Code)
		if err == nil && !ok {
			ok, err = s.useRecovery(ctx, p.UserID, r.Code, c)
		}
		if err != nil {
			return err
		}
		if !ok {
			s.limiter.Fail(key)
			return ErrReauth
		}
		s.limiter.Reset(key)
		return nil
	}
	return ErrReauth
}

// allowLastFactorRemoval refuses to remove the last factor of an account
// that must have one.
func (s *Service) allowLastFactorRemoval(ctx context.Context, p auth.Principal) error {
	req, err := s.required(ctx, p.UserID, p.Role)
	if err != nil {
		return err
	}
	if req {
		return ErrFactorRequired
	}
	return nil
}

// Required reports whether policy makes a second factor mandatory for the
// account: the instance setting or one of its organizations.
func (s *Service) Required(ctx context.Context, p auth.Principal) (bool, error) {
	return s.required(ctx, p.UserID, p.Role)
}

func (s *Service) required(ctx context.Context, userID uuid.UUID, role string) (bool, error) {
	switch s.policy {
	case config.Require2FAAll:
		return true, nil
	case config.Require2FAAdmins:
		if role == auth.RoleAdmin {
			return true, nil
		}
	}
	req, err := s.q.UserInRequire2FAOrg(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("org 2fa policy: %w", err)
	}
	return req, nil
}

type factorSet struct {
	totp     bool
	passkeys int64
	recovery int64
}

func (f factorSet) any() bool { return f.totp || f.passkeys > 0 }

func (s *Service) factors(ctx context.Context, userID uuid.UUID) (factorSet, error) {
	var f factorSet
	t, err := s.q.GetTOTP(ctx, userID)
	switch {
	case err == nil:
		f.totp = t.ConfirmedAt.Valid
	case !errors.Is(err, pgx.ErrNoRows):
		return f, fmt.Errorf("get totp: %w", err)
	}
	if f.passkeys, err = s.q.CountWebAuthnCredentials(ctx, userID); err != nil {
		return f, fmt.Errorf("count credentials: %w", err)
	}
	rc, err := s.q.CountRecoveryCodes(ctx, userID)
	if err != nil {
		return f, fmt.Errorf("count recovery codes: %w", err)
	}
	f.recovery = rc.Unused
	return f, nil
}

// newChallenge stores a challenge and returns its token. Expired ones are
// swept on the way.
func (s *Service) newChallenge(ctx context.Context, kind string, userID uuid.NullUUID, enroll bool, wa []byte, c identity.Client) (string, time.Time, error) {
	if _, err := s.q.DeleteExpiredMFAChallenges(ctx); err != nil {
		s.log.WarnContext(ctx, "sweep mfa challenges", "error", err)
	}
	token, hash := auth.NewToken()
	id, err := uuid.NewV7()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("challenge id: %w", err)
	}
	exp := time.Now().Add(ChallengeTTL)
	if err := s.q.InsertMFAChallenge(ctx, dbgen.InsertMFAChallengeParams{
		ID: id, TokenHash: hash, Kind: kind, UserID: userID, Enroll: enroll, Webauthn: wa,
		Ip: c.IP, UserAgent: truncate(c.UserAgent, 512), ExpiresAt: timestamptz(exp),
	}); err != nil {
		return "", time.Time{}, fmt.Errorf("insert challenge: %w", err)
	}
	return token, exp, nil
}

// challenge loads a live challenge of kind by its token.
func (s *Service) challenge(ctx context.Context, token, kind string) (dbgen.GetMFAChallengeRow, error) {
	if !auth.ValidTokenFormat(token) {
		return dbgen.GetMFAChallengeRow{}, ErrChallenge
	}
	ch, err := s.q.GetMFAChallenge(ctx, dbgen.GetMFAChallengeParams{TokenHash: auth.HashToken(token), Kind: kind})
	if errors.Is(err, pgx.ErrNoRows) {
		return ch, ErrChallenge
	}
	if err != nil {
		return ch, fmt.Errorf("get challenge: %w", err)
	}
	if ch.Attempts >= MaxAttempts {
		return ch, ErrChallenge
	}
	return ch, nil
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

func (s *Service) record(ctx context.Context, e audit.Event) {
	if err := audit.Record(context.WithoutCancel(ctx), s.q, e); err != nil {
		s.log.ErrorContext(ctx, "audit", "action", e.Action, "error", err)
	}
}

func mfaKey(id uuid.UUID) string { return auth.AccountKey("mfa:" + id.String()) }

func timestamptz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
