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
	apiVersion = "2022-11-28"
	maxAPIBody = 32 << 20
	// Rate limits: how long a request may wait for a reset, and how many
	// times it is sent again.
	maxRateWait    = time.Hour
	maxRateRetries = 5
	apiTimeout     = 30 * time.Second
	jwtLifetime    = 9 * time.Minute
	tokenRefresh   = 5 * time.Minute
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
// repository. Requests that hit a rate limit wait for it to reset (at most
// maxRateWait) and are sent again.
type API struct {
	base  string
	token string
	// source, when set, returns the current token before every request
	// (App installation tokens expire after an hour; long imports outlive
	// them).
	source func(ctx context.Context) (string, error)
	http   *http.Client
}

// Do sends a request to path (relative to the API root, for example
// "/repos/o/n/issues?state=all") with body encoded as JSON when not nil,
// and decodes a JSON answer into out when not nil.
func (a *API) Do(ctx context.Context, method, path string, body, out any) error {
	_, err := a.do(ctx, method, path, body, out)
	return err
}

func (a *API) do(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		payload = b
	}
	resp, err := a.send(ctx, a.http, method, path, payload, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBody))
	if err != nil {
		return nil, fmt.Errorf("github response: %w", err)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("github response: %w", err)
		}
	}
	return resp.Header, nil
}

// send sends a request and returns the 2xx response, whose body the
// caller closes. A rate-limited request waits and is sent again; any other
// non-2xx answer is an *APIError.
func (a *API) send(ctx context.Context, c *http.Client, method, path string, payload []byte, accept string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
		if err != nil {
			return nil, fmt.Errorf("github request: %w", err)
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("X-GitHub-Api-Version", apiVersion)
		req.Header.Set("User-Agent", "ErvisioRepo")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		tok := a.token
		if a.source != nil {
			if tok, err = a.source(ctx); err != nil {
				return nil, fmt.Errorf("GitHub authentication: %w", err)
			}
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github request: %w", err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			return resp, nil
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		msg := m.Message
		if len(msg) > 300 {
			msg = msg[:300]
		}
		wait, limited := rateLimitWait(resp, msg)
		if !limited || attempt >= maxRateRetries {
			return nil, &APIError{Status: resp.StatusCode, Message: msg}
		}
		if wait > maxRateWait {
			return nil, &APIError{Status: resp.StatusCode, Message: "rate limit exceeded until " +
				time.Now().Add(wait).UTC().Format(time.RFC3339)}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("github request: %w", ctx.Err())
		case <-t.C:
		}
	}
}

// rateLimitWait reports whether resp is a rate limit refusal and how long
// to wait before sending the request again: Retry-After when given
// (secondary limits), otherwise until X-RateLimit-Reset when the primary
// limit is exhausted.
func rateLimitWait(resp *http.Response, msg string) (time.Duration, bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n)*time.Second + time.Second, true
		}
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return max(time.Until(time.Unix(n, 0))+time.Second, time.Second), true
		}
		return time.Minute, true
	}
	if resp.StatusCode == http.StatusTooManyRequests || strings.Contains(strings.ToLower(msg), "rate limit") {
		return time.Minute, true
	}
	return 0, false
}

// Each GETs path, a list endpoint, and every following page (Link
// rel="next"), and calls fn on each element in order. per_page=100 is
// added when path sets no page size.
func Each[T any](ctx context.Context, a *API, path string, fn func(T) error) error {
	next := path
	if !strings.Contains(path, "per_page=") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		next = path + sep + "per_page=100"
	}
	for next != "" {
		var page []T
		hdr, err := a.do(ctx, http.MethodGet, next, nil, &page)
		if err != nil {
			return err
		}
		for _, v := range page {
			if err := fn(v); err != nil {
				return err
			}
		}
		if next, err = a.nextPage(hdr.Get("Link")); err != nil {
			return err
		}
	}
	return nil
}

// nextPage returns the API path of the rel="next" link of a Link header,
// "" on the last page. A link outside the API root is refused: the token
// must not be sent anywhere else.
func (a *API) nextPage(link string) (string, error) {
	for part := range strings.SplitSeq(link, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
		if !ok || !strings.Contains(params, `rel="next"`) {
			continue
		}
		u := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(target), "<"), ">")
		path, ok := strings.CutPrefix(u, a.base)
		if !ok || !strings.HasPrefix(path, "/") {
			return "", errors.New("github pagination: link outside the API")
		}
		return path, nil
	}
	return "", nil
}

// Download streams the binary content at path (a release asset:
// "/repos/o/n/releases/assets/<id>") to fn. GitHub answers with a redirect
// to its storage; the Authorization header is not forwarded to another
// host. The transfer has no overall timeout besides ctx.
func (a *API) Download(ctx context.Context, path string, fn func(io.Reader) error) error {
	resp, err := a.send(ctx, downloadClient, http.MethodGet, path, nil, "application/octet-stream")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return fn(resp.Body)
}

// downloadClient has no overall timeout (assets can be large) but bounds
// the wait for the response headers.
var downloadClient = func() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = time.Minute
	return &http.Client{Transport: t}
}()

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
