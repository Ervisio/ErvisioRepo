package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// OAuth device authorization grant (RFC 8628), shaped like GitHub's so that
// gh-style clients work unchanged: POST /login/device/code starts a request,
// the user approves it at /login/device, and the device polls
// POST /login/oauth/access_token until it receives an access token.

// Device flow parameters, as on GitHub.
const (
	DeviceCodeTTL      = 15 * time.Minute
	DevicePollInterval = 5 * time.Second
	// deviceSlowDownStep is added to the interval at every slow_down.
	deviceSlowDownStep = 5 * time.Second
	// maxDeviceName bounds the device name a client may send.
	maxDeviceName = 40
)

// Audit actions of the device flow.
const (
	ActionDeviceApproved = "oauth.device_approved"
	ActionDeviceDenied   = "oauth.device_denied"
)

// Device code statuses (oauth_device_codes.status).
const (
	deviceStatusPending  = "pending"
	deviceStatusApproved = "approved"
	deviceStatusDenied   = "denied"
	deviceStatusConsumed = "consumed"
)

// The built-in first-party clients are system rows of oauth_apps with the device flow enabled.

// EgClientID is the client_id of eg, the first-party command line client.
const EgClientID = "Ov23liErvisioEgCli01"

// GitHubCLIClientID is the client_id the gh CLI sends; accepting it lets
// `gh auth login --hostname <this server>` use the device flow too.
const GitHubCLIClientID = "178c6fc778ccc68e1d6a"

const ErvisioCodeClientID = "ErvisioCodeRemote"

// deviceApp returns the app clientID when it may use the device flow:
// a built-in client or a registered app with the device flow enabled.
func (s *Service) deviceApp(ctx context.Context, q *dbgen.Queries, clientID string) (OAuthApp, error) {
	app, err := lookupApp(ctx, q, clientID)
	if errors.Is(err, ErrNotFound) || (err == nil && !app.DeviceFlow) {
		return OAuthApp{}, ErrIncorrectClient
	}
	return app, err
}

// DeviceError is an OAuth error code of the token endpoint.
type DeviceError struct {
	Code        string
	Description string
	// Interval is the new polling interval (slow_down), in seconds.
	Interval int
}

func (e *DeviceError) Error() string { return e.Code + ": " + e.Description }

func deviceErr(code, desc string) *DeviceError { return &DeviceError{Code: code, Description: desc} }

// Errors of the device flow endpoints, with GitHub's codes.
var (
	ErrIncorrectClient     = deviceErr("incorrect_client_credentials", "The client_id passed is incorrect.")
	ErrIncorrectDeviceCode = deviceErr("incorrect_device_code", "The device_code provided is not valid.")
	ErrUnsupportedGrant    = deviceErr("unsupported_grant_type", "The grant type is not supported.")
	ErrAuthorizationPend   = deviceErr("authorization_pending", "The authorization request is still pending.")
	ErrExpiredDeviceCode   = deviceErr("expired_token", "The device_code has expired.")
	ErrDeviceAccessDenied  = deviceErr("access_denied", "The authorization request was denied.")
	ErrDeviceRateLimited   = deviceErr("slow_down", "Too many device codes requested; retry later.")
	ErrErvisioCodeScope    = deviceErr("invalid_scope", "ErvisioCodeRemote requires exactly ervisiocode:device.")
)

// ErrDeviceLimited means too many wrong user codes; Wait says how long to
// back off.
type ErrDeviceLimited struct{ Wait time.Duration }

func (e *ErrDeviceLimited) Error() string { return "too many attempts, retry later" }

// DeviceStart is the answer to a device code request.
type DeviceStart struct {
	DeviceCode string
	UserCode   string // XXXX-XXXX
	ExpiresIn  int    // seconds
	Interval   int    // seconds
}

// StartDevice starts a device authorization with an optional friendly label.
func (s *Service) StartDevice(ctx context.Context, clientID, scope, deviceName, ip string) (DeviceStart, error) {
	if _, err := s.deviceApp(ctx, s.q, clientID); err != nil {
		return DeviceStart{}, err
	}
	if ok, _ := s.device.AllowAction(auth.IPKey(ip)); !ok {
		return DeviceStart{}, ErrDeviceRateLimited
	}
	scopes, err := requestedDeviceScopes(clientID, scope)
	if err != nil {
		return DeviceStart{}, err
	}
	id, err := newID()
	if err != nil {
		return DeviceStart{}, err
	}
	var raw [20]byte
	_, _ = rand.Read(raw[:])
	deviceCode := hex.EncodeToString(raw[:])
	expires := s.now().Add(DeviceCodeTTL)
	for range 5 {
		userCode := NewUserCode()
		err = s.q.InsertDeviceCode(ctx, dbgen.InsertDeviceCodeParams{ID: id, DeviceCodeHash: auth.HashToken(deviceCode),
			UserCode: userCode, ClientID: clientID, Scopes: scopes, DeviceName: cleanDeviceName(deviceName),
			RequestIp: ip, IntervalSeconds: int32(DevicePollInterval / time.Second),
			ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
		if isUniqueViolation(err) {
			continue // user code collision: draw another
		}
		if err != nil {
			return DeviceStart{}, fmt.Errorf("insert device code: %w", err)
		}
		return DeviceStart{DeviceCode: deviceCode, UserCode: FormatUserCode(userCode),
			ExpiresIn: int(DeviceCodeTTL / time.Second), Interval: int(DevicePollInterval / time.Second)}, nil
	}
	return DeviceStart{}, fmt.Errorf("insert device code: %w", err)
}

func requestedDeviceScopes(clientID, scope string) ([]string, error) {
	scope = strings.TrimSpace(scope)
	switch {
	case clientID == ErvisioCodeClientID:
		if scope != auth.ScopeErvisioCodeDevice {
			return nil, ErrErvisioCodeScope
		}
		return []string{auth.ScopeErvisioCodeDevice}, nil
	case scope == "":
		return auth.DefaultGitHubScopes, nil
	default:
		return auth.ParseGitHubScopes(scope), nil
	}
}

func deviceScopes(clientID string, requested []string) []string {
	if clientID == ErvisioCodeClientID {
		return []string{auth.ScopeErvisioCodeDevice}
	}
	return auth.ScopesFromGitHub(requested)
}

// userCodeAlphabet has no vowels (no words) and no look-alike letters.
const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

// NewUserCode draws an eight-letter user code (see FormatUserCode).
func NewUserCode() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	out := make([]byte, len(b))
	for i, v := range b {
		// 256 % 20 = 16: a negligible bias for a short-lived code.
		out[i] = userCodeAlphabet[int(v)%len(userCodeAlphabet)]
	}
	return string(out)
}

// NormalizeUserCode upper-cases a user code and drops dashes and spaces;
// it returns "" when the result is not a well-formed code.
func NormalizeUserCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r == '-' || r == ' ' {
			continue
		}
		if !strings.ContainsRune(userCodeAlphabet, r) {
			return ""
		}
		b.WriteRune(r)
	}
	if b.Len() != 8 {
		return ""
	}
	return b.String()
}

// FormatUserCode renders a normalized user code as XXXX-XXXX.
func FormatUserCode(code string) string {
	if len(code) != 8 {
		return code
	}
	return code[:4] + "-" + code[4:]
}

func cleanDeviceName(name string) string {
	name = strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if !utf8.ValidString(name) {
		return ""
	}
	if r := []rune(name); len(r) > maxDeviceName {
		name = string(r[:maxDeviceName])
	}
	return name
}

// DeviceToken is the access token handed to an approved device.
type DeviceToken struct {
	AccessToken string
	// Scope is the granted OAuth scope string.
	Scope string
}

// PollDevice answers a poll of the token endpoint. Errors are *DeviceError
// values for the OAuth error codes, others are internal failures.
func (s *Service) PollDevice(ctx context.Context, clientID, deviceCode, ip string) (DeviceToken, error) {
	if _, err := s.deviceApp(ctx, s.q, clientID); err != nil {
		return DeviceToken{}, err
	}
	if deviceCode == "" {
		return DeviceToken{}, ErrIncorrectDeviceCode
	}
	var out DeviceToken
	var result error
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		row, err := q.GetDeviceCodeByHashForUpdate(ctx, auth.HashToken(deviceCode))
		if errors.Is(err, pgx.ErrNoRows) {
			result = ErrIncorrectDeviceCode
			return nil
		}
		if err != nil {
			return fmt.Errorf("get device code: %w", err)
		}
		if row.ClientID != clientID {
			result = ErrIncorrectClient
			return nil
		}
		now := s.now()
		if !row.ExpiresAt.Time.After(now) || row.Status == deviceStatusConsumed {
			result = ErrExpiredDeviceCode
			return nil
		}
		interval := time.Duration(row.IntervalSeconds) * time.Second
		if row.LastPollAt.Valid && now.Sub(row.LastPollAt.Time) < interval {
			interval += deviceSlowDownStep
			secs := int(interval / time.Second)
			result = &DeviceError{Code: "slow_down", Interval: secs,
				Description: "Too many requests have been made in the same timeframe."}
			if err := q.SetDeviceCodePoll(ctx, dbgen.SetDeviceCodePollParams{ID: row.ID,
				PolledAt: pgtype.Timestamptz{Time: now, Valid: true}, IntervalSeconds: int32(secs)}); err != nil { //nolint:gosec // bounded by the TTL
				return fmt.Errorf("record poll: %w", err)
			}
			return nil
		}
		if err := q.SetDeviceCodePoll(ctx, dbgen.SetDeviceCodePollParams{ID: row.ID,
			PolledAt: pgtype.Timestamptz{Time: now, Valid: true}, IntervalSeconds: row.IntervalSeconds}); err != nil {
			return fmt.Errorf("record poll: %w", err)
		}
		switch row.Status {
		case deviceStatusPending:
			result = ErrAuthorizationPend
			return nil
		case deviceStatusDenied:
			result = ErrDeviceAccessDenied
			return nil
		}
		if !row.UserID.Valid {
			return fmt.Errorf("approved device code %s without a user", row.ID)
		}
		tok, err := s.issueDeviceToken(ctx, q, row, ip)
		if err != nil {
			return err
		}
		out = tok
		return nil
	})
	if err != nil {
		return DeviceToken{}, err
	}
	if result != nil {
		return DeviceToken{}, result
	}
	return out, nil
}

// issueDeviceToken creates the access token of an approved device code and
// marks the code consumed, in the caller's transaction.
func (s *Service) issueDeviceToken(ctx context.Context, q *dbgen.Queries, row dbgen.OauthDeviceCode, ip string) (DeviceToken, error) {
	userID := row.UserID.UUID
	if err := lockUser(ctx, q, userID); err != nil {
		return DeviceToken{}, err
	}
	client, err := s.deviceApp(ctx, q, row.ClientID)
	if err != nil {
		return DeviceToken{}, err
	}
	isErvisioCode := client.System() && row.ClientID == ErvisioCodeClientID
	if isErvisioCode && !slices.Equal(row.Scopes, []string{auth.ScopeErvisioCodeDevice}) {
		return DeviceToken{}, ErrErvisioCodeScope
	}
	if !client.System() {
		// A registered app: the token belongs to the user's grant and is
		// listed among the authorized applications.
		grantID, err := upsertGrant(ctx, q, client, userID, row.Scopes, ip)
		if err != nil {
			return DeviceToken{}, err
		}
		secret, err := s.issueGrantToken(ctx, q, client, userID, grantID, row.Scopes, ip, "device_code")
		if err != nil {
			return DeviceToken{}, err
		}
		if err := q.ConsumeDeviceCode(ctx, dbgen.ConsumeDeviceCodeParams{ID: row.ID}); err != nil {
			return DeviceToken{}, fmt.Errorf("consume device code: %w", err)
		}
		return DeviceToken{AccessToken: secret, Scope: strings.Join(auth.GitHubScopes(auth.ScopesFromGitHub(row.Scopes)), ",")}, nil
	}
	base := s.deviceTokenName(client, row.DeviceName)
	name := base
	for i := 2; ; i++ {
		taken, err := q.TokenNameTaken(ctx, dbgen.TokenNameTakenParams{UserID: userID, Name: name})
		if err != nil {
			return DeviceToken{}, fmt.Errorf("check token name: %w", err)
		}
		if !taken {
			break
		}
		if i > MaxTokens+1 {
			return DeviceToken{}, ErrTooMany
		}
		name = base + " #" + strconv.Itoa(i)
	}
	id, err := newID()
	if err != nil {
		return DeviceToken{}, err
	}
	ttl := s.maxTTL
	if ttl <= 0 {
		ttl = 90 * 24 * time.Hour
	}
	now := s.now()
	scopes := deviceScopes(row.ClientID, row.Scopes)
	secret, hash := auth.NewAccessToken()
	t := CreatedToken{Token: Token{ID: id, Name: name, Hint: auth.TokenHint(secret), Scopes: scopes,
		ExpiresAt: now.Add(ttl).UTC(), CreatedAt: now}, Secret: secret}
	if err := insertToken(ctx, q, userID, t, hash, ip, isErvisioCode, map[string]any{"client_id": row.ClientID, "grant": "device_code"}); err != nil {
		return DeviceToken{}, err
	}
	if err := q.ConsumeDeviceCode(ctx, dbgen.ConsumeDeviceCodeParams{ID: row.ID, TokenID: uuid.NullUUID{UUID: id, Valid: true}}); err != nil {
		return DeviceToken{}, fmt.Errorf("consume device code: %w", err)
	}
	s.log.InfoContext(ctx, "device flow token issued", "user_id", userID, "client_id", row.ClientID, "token_id", id)
	if isErvisioCode {
		return DeviceToken{AccessToken: secret, Scope: auth.ScopeErvisioCodeDevice}, nil
	}
	return DeviceToken{AccessToken: secret, Scope: strings.Join(auth.GitHubScopes(scopes), ",")}, nil
}

// deviceTokenName is "<client> on <device or server host> (device)".
func (s *Service) deviceTokenName(c OAuthApp, device string) string {
	if c.ClientID == ErvisioCodeClientID {
		if device == "" {
			return "ErvisioCode device"
		}
		return "ErvisioCode on " + device
	}
	short := c.short
	if short == "" {
		short = c.Name
	}
	host := device
	if host == "" {
		host = s.host
	}
	if host == "" {
		return short + " (device)"
	}
	return short + " on " + host + " (device)"
}

// DeviceRequest is a pending device authorization shown to the user.
type DeviceRequest struct {
	UserCode        string // XXXX-XXXX
	ClientID        string
	AppName         string
	RequestedScopes []string // OAuth scopes
	Scopes          []string // token scopes
	DeviceName      string
	RequestIP       string
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

func deviceKeys(userID uuid.UUID, ip string) []string {
	return []string{auth.AccountKey("device:" + userID.String()), auth.IPKey("device:" + ip)}
}

// checkDeviceLimit refuses callers that entered too many wrong codes.
func (s *Service) checkDeviceLimit(userID uuid.UUID, ip string) error {
	if wait := s.device.LoginWait(deviceKeys(userID, ip)...); wait > 0 {
		return &ErrDeviceLimited{Wait: wait}
	}
	return nil
}

func (s *Service) deviceRequest(ctx context.Context, row dbgen.OauthDeviceCode) (DeviceRequest, error) {
	c, err := lookupApp(ctx, s.q, row.ClientID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return DeviceRequest{}, err
	}
	return DeviceRequest{UserCode: FormatUserCode(row.UserCode), ClientID: row.ClientID, AppName: c.Name,
		RequestedScopes: row.Scopes, Scopes: deviceScopes(row.ClientID, row.Scopes), DeviceName: row.DeviceName,
		RequestIP: row.RequestIp, CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time}, nil
}

// LookupDevice returns the pending request with userCode. Unknown codes
// count as failures for the brute-force limiter of userID and ip.
func (s *Service) LookupDevice(ctx context.Context, userID uuid.UUID, userCode, ip string) (DeviceRequest, error) {
	if err := s.checkDeviceLimit(userID, ip); err != nil {
		return DeviceRequest{}, err
	}
	code := NormalizeUserCode(userCode)
	if code == "" {
		s.device.Fail(deviceKeys(userID, ip)...)
		return DeviceRequest{}, ErrNotFound
	}
	row, err := s.q.GetPendingDeviceCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		s.device.Fail(deviceKeys(userID, ip)...)
		return DeviceRequest{}, ErrNotFound
	}
	if err != nil {
		return DeviceRequest{}, fmt.Errorf("get device code: %w", err)
	}
	return s.deviceRequest(ctx, row)
}

// DecideDevice approves or denies the pending request with userCode on
// behalf of userID.
func (s *Service) DecideDevice(ctx context.Context, userID uuid.UUID, userCode string, approve bool, ip string) error {
	if err := s.checkDeviceLimit(userID, ip); err != nil {
		return err
	}
	code := NormalizeUserCode(userCode)
	if code == "" {
		s.device.Fail(deviceKeys(userID, ip)...)
		return ErrNotFound
	}
	status, action := deviceStatusDenied, ActionDeviceDenied
	if approve {
		status, action = deviceStatusApproved, ActionDeviceApproved
	}
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		row, err := q.GetPendingDeviceCodeForUpdate(ctx, code)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get device code: %w", err)
		}
		if err := q.DecideDeviceCode(ctx, dbgen.DecideDeviceCodeParams{ID: row.ID, Status: status,
			UserID: uuid.NullUUID{UUID: userID, Valid: true}}); err != nil {
			return fmt.Errorf("decide device code: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: action, TargetType: "oauth_device",
			TargetID: row.ID.String(), IP: ip, Data: map[string]any{"client_id": row.ClientID, "scopes": row.Scopes}})
	})
	if errors.Is(err, ErrNotFound) {
		s.device.Fail(deviceKeys(userID, ip)...)
	}
	return err
}
