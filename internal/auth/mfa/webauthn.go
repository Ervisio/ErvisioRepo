package mfa

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// MaxCredentialJSON bounds the browser response to a WebAuthn ceremony.
const MaxCredentialJSON = 64 << 10

// handleSize is the length of the WebAuthn user handle (random, never the
// account id, so it reveals nothing).
const handleSize = 32

type webAuthn struct{ *webauthn.WebAuthn }

// newWebAuthn derives the relying party from the public URL: its host is
// the RP id and its origin the only allowed origin.
func newWebAuthn(publicURL string) (*webAuthn, error) {
	u, err := url.Parse(publicURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("public url %q gives no relying party", publicURL)
	}
	w, err := webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Ervisio Repo",
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn: %w", err)
	}
	return &webAuthn{w}, nil
}

// waUser adapts an account to webauthn.User.
type waUser struct {
	id      uuid.UUID
	handle  []byte
	name    string
	display string
	creds   []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return u.handle }
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.display }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// loadUser builds the WebAuthn view of an account; create makes its handle
// when missing (only registration needs to).
func (s *Service) loadUser(ctx context.Context, userID uuid.UUID, create bool) (*waUser, error) {
	u, err := s.ids.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	handle, err := s.q.GetWebAuthnHandle(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) && create {
		h := make([]byte, handleSize)
		_, _ = rand.Read(h)
		if err := s.q.InsertWebAuthnHandle(ctx, dbgen.InsertWebAuthnHandleParams{UserID: userID, Handle: h}); err != nil {
			return nil, fmt.Errorf("insert webauthn handle: %w", err)
		}
		handle, err = s.q.GetWebAuthnHandle(ctx, userID)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get webauthn handle: %w", err)
	}
	rows, err := s.q.ListWebAuthnCredentials(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	wu := &waUser{id: userID, handle: handle, name: u.Username, display: u.DisplayName}
	if wu.display == "" {
		wu.display = u.Username
	}
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(r.Data, &c); err != nil {
			return nil, fmt.Errorf("decode credential: %w", err)
		}
		wu.creds = append(wu.creds, c)
	}
	return wu, nil
}

// BeginPasskeyRegistration starts adding a passkey (a discoverable
// credential with user verification, usable for passwordless sign-in and
// as a second factor). It returns the ceremony token and the options for
// navigator.credentials.create.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, p auth.Principal, r Reauth, c identity.Client) (string, json.RawMessage, error) {
	if s.wa == nil {
		return "", nil, ErrWebAuthnOff
	}
	if err := s.reauth(ctx, p, r, c); err != nil {
		return "", nil, err
	}
	u, err := s.loadUser(ctx, p.UserID, true)
	if err != nil {
		return "", nil, err
	}
	excl := make([]protocol.CredentialDescriptor, 0, len(u.creds))
	for i := range u.creds {
		excl = append(excl, u.creds[i].Descriptor())
	}
	creation, session, err := s.wa.BeginRegistration(u,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithExclusions(excl))
	if err != nil {
		return "", nil, fmt.Errorf("begin registration: %w", err)
	}
	return s.ceremony(ctx, kindRegister, uuid.NullUUID{UUID: p.UserID, Valid: true}, creation, session, c)
}

// FinishPasskeyRegistration verifies the authenticator response and stores
// the passkey under name.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, p auth.Principal, token, name string, credential []byte, c identity.Client) (Passkey, error) {
	if s.wa == nil {
		return Passkey{}, ErrWebAuthnOff
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return Passkey{}, ErrInvalidName
	}
	ch, session, err := s.takeCeremony(ctx, token, kindRegister)
	if err != nil {
		return Passkey{}, err
	}
	if ch.UserID.UUID != p.UserID {
		return Passkey{}, ErrChallenge
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credential)
	if err != nil {
		return Passkey{}, ErrInvalidPasskey
	}
	u, err := s.loadUser(ctx, p.UserID, false)
	if err != nil {
		return Passkey{}, err
	}
	cred, err := s.wa.CreateCredential(u, session, parsed)
	if err != nil {
		s.log.InfoContext(ctx, "passkey registration rejected", "user", p.UserID, "error", err)
		return Passkey{}, ErrInvalidPasskey
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return Passkey{}, fmt.Errorf("encode credential: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Passkey{}, fmt.Errorf("credential id: %w", err)
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.InsertWebAuthnCredential(ctx, dbgen.InsertWebAuthnCredentialParams{
			ID: id, UserID: p.UserID, CredentialID: cred.ID, Name: name, Data: data,
		}); err != nil {
			return fmt.Errorf("insert credential: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionPasskeyAdded, TargetType: "user",
			TargetID: p.UserID.String(), IP: c.IP, Data: map[string]any{"name": name}})
	})
	if err != nil {
		if isUniqueViolation(err) {
			return Passkey{}, ErrInvalidPasskey
		}
		return Passkey{}, err
	}
	return Passkey{ID: id, Name: name, CreatedAt: s.now()}, nil
}

// DeletePasskey removes a passkey of the account.
func (s *Service) DeletePasskey(ctx context.Context, p auth.Principal, id uuid.UUID, r Reauth, c identity.Client) error {
	if err := s.reauth(ctx, p, r, c); err != nil {
		return err
	}
	f, err := s.factors(ctx, p.UserID)
	if err != nil {
		return err
	}
	last := !f.totp && f.passkeys <= 1
	if last {
		if err := s.allowLastFactorRemoval(ctx, p); err != nil {
			return err
		}
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteWebAuthnCredential(ctx, dbgen.DeleteWebAuthnCredentialParams{ID: id, UserID: p.UserID})
		if err != nil {
			return fmt.Errorf("delete credential: %w", err)
		}
		if n != 1 {
			return ErrNotFound
		}
		if last {
			if err := q.DeleteRecoveryCodes(ctx, p.UserID); err != nil {
				return fmt.Errorf("delete recovery codes: %w", err)
			}
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionPasskeyRemoved, TargetType: "user",
			TargetID: p.UserID.String(), IP: c.IP})
	})
}

// BeginWebAuthnLogin returns the assertion options of a pending login whose
// account has security keys.
func (s *Service) BeginWebAuthnLogin(ctx context.Context, token string) (json.RawMessage, error) {
	if s.wa == nil {
		return nil, ErrWebAuthnOff
	}
	ch, err := s.secondFactor(ctx, token)
	if err != nil {
		return nil, err
	}
	u, err := s.loadUser(ctx, ch.UserID.UUID, false)
	if err != nil {
		return nil, err
	}
	if len(u.creds) == 0 || len(u.handle) == 0 {
		return nil, ErrNotEnabled
	}
	assertion, session, err := s.wa.BeginLogin(u, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		return nil, fmt.Errorf("begin login: %w", err)
	}
	raw, err := json.Marshal(session)
	if err != nil {
		return nil, fmt.Errorf("encode session: %w", err)
	}
	if _, err := s.q.SetMFAChallengeWebAuthn(ctx, dbgen.SetMFAChallengeWebAuthnParams{ID: ch.ID, Webauthn: raw}); err != nil {
		return nil, fmt.Errorf("store session: %w", err)
	}
	return json.Marshal(assertion)
}

// FinishWebAuthnLogin completes a pending login with a security key.
func (s *Service) FinishWebAuthnLogin(ctx context.Context, token string, credential []byte, oldToken string, c identity.Client) (identity.LoginResult, error) {
	if s.wa == nil {
		return identity.LoginResult{}, ErrWebAuthnOff
	}
	ch, err := s.secondFactor(ctx, token)
	if err != nil {
		return identity.LoginResult{}, err
	}
	if len(ch.Webauthn) == 0 {
		return identity.LoginResult{}, ErrChallenge
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(ch.Webauthn, &session); err != nil {
		return identity.LoginResult{}, fmt.Errorf("decode session: %w", err)
	}
	// The ceremony is single use: a failed or replayed assertion needs new
	// options.
	if _, err := s.q.SetMFAChallengeWebAuthn(ctx, dbgen.SetMFAChallengeWebAuthnParams{ID: ch.ID}); err != nil {
		return identity.LoginResult{}, fmt.Errorf("clear session: %w", err)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credential)
	if err != nil {
		return identity.LoginResult{}, s.fail(ctx, ch, MethodWebAuthn, c)
	}
	u, err := s.loadUser(ctx, ch.UserID.UUID, false)
	if err != nil {
		return identity.LoginResult{}, err
	}
	cred, err := s.wa.ValidateLogin(u, session, parsed)
	if err != nil || cred.Authenticator.CloneWarning {
		return identity.LoginResult{}, s.fail(ctx, ch, MethodWebAuthn, c)
	}
	if err := s.touchCredential(ctx, u.id, cred); err != nil {
		return identity.LoginResult{}, err
	}
	return s.finish(ctx, ch, oldToken, c, MethodWebAuthn)
}

// BeginPasskeyLogin starts a passwordless sign-in with a discoverable
// credential. It returns the ceremony token and the options for
// navigator.credentials.get.
func (s *Service) BeginPasskeyLogin(ctx context.Context, c identity.Client) (string, json.RawMessage, error) {
	if s.wa == nil {
		return "", nil, ErrWebAuthnOff
	}
	if ok, wait := s.limiter.AllowAction(auth.IPKey("passkey:" + c.IP)); !ok {
		return "", nil, &identity.RateLimitError{RetryAfter: wait}
	}
	assertion, session, err := s.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return "", nil, fmt.Errorf("begin passkey login: %w", err)
	}
	return s.ceremony(ctx, kindPasskey, uuid.NullUUID{}, assertion, session, c)
}

// FinishPasskeyLogin verifies a passkey assertion and starts the session.
// A passkey with user verification is two factors in one, so no further
// step follows.
func (s *Service) FinishPasskeyLogin(ctx context.Context, token string, credential []byte, oldToken string, c identity.Client) (identity.LoginResult, error) {
	if s.wa == nil {
		return identity.LoginResult{}, ErrWebAuthnOff
	}
	ipKey := auth.IPKey(c.IP)
	if wait := s.limiter.LoginWait(ipKey); wait > 0 {
		return identity.LoginResult{}, &identity.RateLimitError{RetryAfter: wait}
	}
	_, session, err := s.takeCeremony(ctx, token, kindPasskey)
	if err != nil {
		return identity.LoginResult{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credential)
	if err != nil {
		s.limiter.Fail(ipKey)
		return identity.LoginResult{}, ErrInvalidPasskey
	}
	var found *waUser
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		id, err := s.q.GetUserByWebAuthnHandle(ctx, userHandle)
		if err != nil {
			return nil, fmt.Errorf("unknown passkey: %w", err)
		}
		u, err := s.loadUser(ctx, id, false)
		if err != nil {
			return nil, err
		}
		found = u
		return u, nil
	}
	_, cred, err := s.wa.ValidatePasskeyLogin(handler, session, parsed)
	if err != nil || found == nil || cred.Authenticator.CloneWarning {
		s.limiter.Fail(ipKey)
		if found != nil {
			s.record(ctx, audit.Event{Actor: found.id, Action: ActionFailed, TargetType: "user",
				TargetID: found.id.String(), IP: c.IP, Data: map[string]any{"method": "passkey"}})
		}
		return identity.LoginResult{}, ErrInvalidPasskey
	}
	if err := s.touchCredential(ctx, found.id, cred); err != nil {
		return identity.LoginResult{}, err
	}
	return s.ids.StartSession(ctx, found.id, oldToken, c, "passkey")
}

// ceremony stores a WebAuthn ceremony (the session data) as a challenge
// and returns its token with the browser options.
func (s *Service) ceremony(ctx context.Context, kind string, userID uuid.NullUUID, options any, session *webauthn.SessionData, c identity.Client) (string, json.RawMessage, error) {
	raw, err := json.Marshal(session)
	if err != nil {
		return "", nil, fmt.Errorf("encode session: %w", err)
	}
	opts, err := json.Marshal(options)
	if err != nil {
		return "", nil, fmt.Errorf("encode options: %w", err)
	}
	token, _, err := s.newChallenge(ctx, kind, userID, false, raw, c)
	if err != nil {
		return "", nil, err
	}
	return token, opts, nil
}

// takeCeremony consumes a ceremony challenge: each one is single use.
func (s *Service) takeCeremony(ctx context.Context, token, kind string) (dbgen.GetMFAChallengeRow, webauthn.SessionData, error) {
	var session webauthn.SessionData
	ch, err := s.challenge(ctx, token, kind)
	if err != nil {
		return ch, session, err
	}
	n, err := s.q.ConsumeMFAChallenge(ctx, ch.ID)
	if err != nil {
		return ch, session, fmt.Errorf("consume challenge: %w", err)
	}
	if n != 1 {
		return ch, session, ErrChallenge
	}
	if err := json.Unmarshal(ch.Webauthn, &session); err != nil {
		return ch, session, fmt.Errorf("decode session: %w", err)
	}
	return ch, session, nil
}

// touchCredential stores the new signature counter and the last use.
func (s *Service) touchCredential(ctx context.Context, userID uuid.UUID, cred *webauthn.Credential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("encode credential: %w", err)
	}
	if err := s.q.UpdateWebAuthnCredentialUse(ctx, dbgen.UpdateWebAuthnCredentialUseParams{
		UserID: userID, CredentialID: cred.ID, Data: data,
	}); err != nil {
		return fmt.Errorf("update credential: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	type pgErr interface{ SQLState() string }
	var e pgErr
	return errors.As(err, &e) && e.SQLState() == "23505"
}
