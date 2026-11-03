package ghsync

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DefaultBaseURL is the web URL of github.com.
const DefaultBaseURL = "https://github.com"

const (
	apiVersion   = "2022-11-28"
	maxAPIBody   = 4 << 20
	apiTimeout   = 30 * time.Second
	jwtLifetime  = 9 * time.Minute
	tokenRefresh = 5 * time.Minute
)

// APIURL is the REST endpoint of a GitHub web URL: api.github.com for
// github.com, <base>/api/v3 for GitHub Enterprise Server.
func APIURL(base string) string {
	if base == DefaultBaseURL {
		return "https://api.github.com"
	}
	return strings.TrimRight(base, "/") + "/api/v3"
}

// cloneURL is the HTTPS git URL of owner/name. The credential never goes
// into the URL: it is sent as an HTTP header through the environment.
func cloneURL(base, owner, name string) string {
	return strings.TrimRight(base, "/") + "/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + ".git"
}

// checkBaseURL validates the web URL of a GitHub server: https, a host
// and nothing else.
func checkBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", invalidf("baseUrl must be an https URL without a path, like %s", DefaultBaseURL)
	}
	return "https://" + strings.ToLower(u.Host), nil
}

// APIError is a non-2xx answer of the GitHub API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("GitHub API: HTTP %d", e.Status)
	}
	return fmt.Sprintf("GitHub API: HTTP %d: %s", e.Status, e.Message)
}

// API is an authenticated client of the GitHub REST API of one connection.
// Importers (see Importer) use it to read the non-git data of a
// repository.
type API struct {
	base  string
	token string
	http  *http.Client
}

// Do sends a request to path (relative to the API root, for example
// "/repos/o/n/issues?state=all") with body encoded as JSON when not nil,
// and decodes a JSON answer into out when not nil.
func (a *API) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "ErvisioRepo")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBody))
	if err != nil {
		return fmt.Errorf("github response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		msg := m.Message
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("github response: %w", err)
		}
	}
	return nil
}

// notFound reports whether err is a 404 of the API.
func notFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// credential is an opened connection.
type credential struct {
	id             uuid.UUID
	version        time.Time // updated_at: a change invalidates the cached token
	kind           string
	baseURL        string
	appID          int64
	installationID int64
	secret         []byte // PEM private key (app) or token (token)
}

// parsePrivateKey reads the RSA private key of a GitHub App (PKCS#1 as
// GitHub issues it, or PKCS#8).
func parsePrivateKey(p []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(p)
	if block == nil {
		return nil, invalidf("privateKey is not a PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, invalidf("privateKey is not an RSA private key")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, invalidf("privateKey is not an RSA private key")
	}
	return rk, nil
}

// appJWT is the RS256 JSON Web Token a GitHub App authenticates with.
func appJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		// Backdated against clock drift, as GitHub recommends.
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": strconv.FormatInt(appID, 10),
	})
	if err != nil {
		return "", fmt.Errorf("jwt claims: %w", err)
	}
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

type cachedToken struct {
	token   string
	expires time.Time
	version time.Time
}

// tokens caches installation tokens until shortly before they expire.
type tokens struct {
	mu   sync.Mutex
	byID map[uuid.UUID]cachedToken
	http *http.Client
}

func newTokens() *tokens {
	return &tokens{byID: map[uuid.UUID]cachedToken{}, http: &http.Client{Timeout: apiTimeout}}
}

func (t *tokens) forget(id uuid.UUID) {
	t.mu.Lock()
	delete(t.byID, id)
	t.mu.Unlock()
}

// token returns a token for git and the API: the fine-grained token
// itself, or an installation token of the App.
func (t *tokens) token(ctx context.Context, c credential) (string, error) {
	if c.kind == kindToken {
		return string(c.secret), nil
	}
	now := time.Now()
	t.mu.Lock()
	ct, ok := t.byID[c.id]
	t.mu.Unlock()
	if ok && ct.version.Equal(c.version) && now.Add(tokenRefresh).Before(ct.expires) {
		return ct.token, nil
	}
	key, err := parsePrivateKey(c.secret)
	if err != nil {
		return "", err
	}
	jwt, err := appJWT(c.appID, key, now)
	if err != nil {
		return "", err
	}
	app := &API{base: APIURL(c.baseURL), token: jwt, http: t.http}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := "/app/installations/" + strconv.FormatInt(c.installationID, 10) + "/access_tokens"
	if err := app.Do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return "", fmt.Errorf("installation token: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("installation token: empty answer")
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = now.Add(time.Hour)
	}
	t.mu.Lock()
	t.byID[c.id] = cachedToken{token: out.Token, expires: out.ExpiresAt, version: c.version}
	t.mu.Unlock()
	return out.Token, nil
}

// api returns an API client authenticated as the connection.
func (t *tokens) api(ctx context.Context, c credential) (*API, error) {
	tok, err := t.token(ctx, c)
	if err != nil {
		return nil, err
	}
	return &API{base: APIURL(c.baseURL), token: tok, http: t.http}, nil
}

// gitAuthEnv returns the environment that makes git send the token as an
// HTTP Basic credential (x-access-token works for App installation tokens
// and personal tokens alike). It is never put in argv or in a URL.
func gitAuthEnv(token string) []string {
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + basic,
		"GIT_CONFIG_KEY_1=credential.helper",
		"GIT_CONFIG_VALUE_1=",
		"GIT_ALLOW_PROTOCOL=https",
	}
}

// remoteRepo is the part of a GitHub repository the sync needs.
type remoteRepo struct {
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
}

func getRepo(ctx context.Context, a *API, owner, name string) (remoteRepo, error) {
	var r remoteRepo
	err := a.Do(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil, &r)
	return r, err
}

// createRepo creates owner/name as a private, empty repository: under the
// authenticated user when owner is that user (tokens), in the organization
// otherwise.
func createRepo(ctx context.Context, a *API, kind, owner, name string) error {
	body := map[string]any{"name": name, "private": true, "auto_init": false,
		"description": "Backup from ErvisioRepo"}
	if kind == kindToken {
		var me struct {
			Login string `json:"login"`
		}
		if err := a.Do(ctx, http.MethodGet, "/user", nil, &me); err == nil && strings.EqualFold(me.Login, owner) {
			if err := a.Do(ctx, http.MethodPost, "/user/repos", body, nil); err != nil {
				return fmt.Errorf("create GitHub repository: %w", err)
			}
			return nil
		}
	}
	if err := a.Do(ctx, http.MethodPost, "/orgs/"+url.PathEscape(owner)+"/repos", body, nil); err != nil {
		return fmt.Errorf("create GitHub repository: %w", err)
	}
	return nil
}

// check verifies that the credentials work.
func check(ctx context.Context, a *API, kind string) error {
	if kind == kindApp {
		return a.Do(ctx, http.MethodGet, "/installation/repositories?per_page=1", nil, nil)
	}
	return a.Do(ctx, http.MethodGet, "/user", nil, nil)
}
