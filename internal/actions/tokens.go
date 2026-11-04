package actions

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
)

// Token prefixes. Job tokens use auth.JobTokenPrefix (erj_).
const (
	// RegistrationTokenPrefix starts runner registration tokens.
	RegistrationTokenPrefix = "errt_"
	// RunnerCredentialPrefix starts the credential a runner authenticates
	// with after registering.
	RunnerCredentialPrefix = "erun_"
)

// newSecretToken returns a random token with prefix and its stored hash.
func newSecretToken(prefix string) (string, []byte) {
	t, _ := auth.NewToken()
	tok := prefix + t
	return tok, auth.HashToken(tok)
}

// validToken reports whether s looks like a token from newSecretToken.
func validToken(s, prefix string) bool {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok || len(rest) != base64.RawURLEncoding.EncodedLen(auth.TokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(rest)
	return err == nil
}

// ErrInvalidRuntimeToken means a runtime token is malformed, forged,
// expired or not meant for Ervisio Actions.
var ErrInvalidRuntimeToken = errors.New("invalid runtime token")

const (
	runtimeIssuer    = "ervisio-actions"
	runtimeKeyInfo   = "ervisio-repo actions runtime token v1"
	runtimeScopeBase = "Actions.Results"
)

// RuntimeClaims are the claims of an ACTIONS_RUNTIME_TOKEN.
type RuntimeClaims struct {
	RepoID uuid.UUID
	RunID  uuid.UUID
	JobID  uuid.UUID
	// Scope is "Actions.Results:<runID>:<jobID>".
	Scope     string
	ExpiresAt time.Time
}

type jwtClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	ExpiresAt int64  `json:"exp"`
	Scope     string `json:"scp"`
	RepoID    string `json:"repo_id"`
	RunID     string `json:"run_id"`
	JobID     string `json:"job_id"`
}

// runtimeKey derives the HS256 key of runtime tokens from the master key.
// Without a master key the key is random: runtime tokens then stop
// working when the server restarts.
func runtimeKey(master []byte) ([]byte, error) {
	if len(master) == 0 {
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return nil, fmt.Errorf("runtime token key: %w", err)
		}
		return k, nil
	}
	k, err := hkdf.Key(sha256.New, master, nil, runtimeKeyInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("runtime token key: %w", err)
	}
	return k, nil
}

var jwtHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// signRuntimeToken issues the runtime token of a job.
func signRuntimeToken(key []byte, repoID, runID, jobID uuid.UUID, now time.Time, ttl time.Duration) (string, error) {
	c := jwtClaims{Issuer: runtimeIssuer, Subject: jobID.String(), IssuedAt: now.Unix(),
		NotBefore: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(ttl).Unix(),
		Scope:  runtimeScopeBase + ":" + runID.String() + ":" + jobID.String(),
		RepoID: repoID.String(), RunID: runID.String(), JobID: jobID.String()}
	body, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("runtime token: %w", err)
	}
	signing := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyRuntimeToken checks an HS256 runtime token issued with key and
// returns its claims. The artifact and cache services call it through
// Service.VerifyRuntimeToken.
func VerifyRuntimeToken(key []byte, token string, now time.Time) (RuntimeClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != jwtHeader {
		return RuntimeClaims{}, ErrInvalidRuntimeToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return RuntimeClaims{}, ErrInvalidRuntimeToken
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return RuntimeClaims{}, ErrInvalidRuntimeToken
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return RuntimeClaims{}, ErrInvalidRuntimeToken
	}
	var c jwtClaims
	if err := json.Unmarshal(body, &c); err != nil || c.Issuer != runtimeIssuer {
		return RuntimeClaims{}, ErrInvalidRuntimeToken
	}
	if now.Unix() >= c.ExpiresAt || now.Unix() < c.NotBefore {
		return RuntimeClaims{}, ErrInvalidRuntimeToken
	}
	out := RuntimeClaims{Scope: c.Scope, ExpiresAt: time.Unix(c.ExpiresAt, 0)}
	var e1, e2, e3 error
	out.RepoID, e1 = uuid.Parse(c.RepoID)
	out.RunID, e2 = uuid.Parse(c.RunID)
	out.JobID, e3 = uuid.Parse(c.JobID)
	if e1 != nil || e2 != nil || e3 != nil ||
		c.Scope != runtimeScopeBase+":"+out.RunID.String()+":"+out.JobID.String() {
		return RuntimeClaims{}, ErrInvalidRuntimeToken
	}
	return out, nil
}
