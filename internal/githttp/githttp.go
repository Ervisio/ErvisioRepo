// Package githttp serves the git Smart HTTP protocol (v0, v1 and v2) on
// /<owner>/<repo>.git (PLAN §5.1): info/refs, git-upload-pack and
// git-receive-pack. Callers authenticate with HTTP Basic and a personal
// access token as password (or a Bearer token), or with the session cookie;
// public repositories can be read anonymously. Account passwords are never
// accepted.
package githttp

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/gittransport"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Resolver finds a repository for a caller (satisfied by *repos.Service).
type Resolver interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
}

// Options configure the handler.
type Options struct {
	Transport *gittransport.Server
	Repos     Resolver
	// Tokens authenticates access tokens; nil refuses them.
	Tokens *auth.Tokens
	// Sessions authenticates the session cookie; nil ignores it.
	Sessions       *auth.Sessions
	Cookies        auth.Cookies
	TrustedProxies []netip.Prefix
	// Failures slows down invalid credentials per client IP; it is the
	// login limiter, so git and the sign-in form share the budget.
	Failures *auth.Limiter
	// Requests is the hourly request budget per client IP (anonymous),
	// per token or per account; nil disables it.
	Requests *auth.Limiter
	Log      *slog.Logger
}

// Handler serves the Smart HTTP endpoints.
type Handler struct{ o Options }

// New returns the handler.
func New(o Options) (*Handler, error) {
	if o.Transport == nil || o.Repos == nil {
		return nil, errors.New("githttp: transport and repos are required")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Handler{o: o}, nil
}

// Endpoints under the repository path.
const (
	pathInfoRefs = "/info/refs"
	realm        = `Basic realm="Ervisio Repo", charset="UTF-8"`
)

// request is a parsed Smart HTTP request.
type request struct {
	owner, name string
	suffix      string // "/info/refs", "/git-upload-pack", "/git-receive-pack"
	service     gittransport.Service
	advertise   bool
}

// parsePath splits /<owner>/<repo>[.git]/<endpoint>.
func parsePath(p string) (request, bool) {
	var req request
	for _, suffix := range []string{pathInfoRefs, "/" + string(gittransport.UploadPack), "/" + string(gittransport.ReceivePack)} {
		if rest, ok := strings.CutSuffix(p, suffix); ok {
			req.suffix = suffix
			p = rest
			break
		}
	}
	if req.suffix == "" {
		return req, false
	}
	owner, name, ok := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	if !ok {
		return req, false
	}
	name = strings.TrimSuffix(name, ".git")
	if identity.ValidateName(owner) != nil || git.ValidateRepoName(name) != nil {
		return req, false
	}
	req.owner, req.name = owner, name
	return req, true
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, ok := parsePath(r.URL.Path)
	if !ok {
		textError(w, http.StatusNotFound, "not found")
		return
	}
	switch {
	case req.suffix == pathInfoRefs && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		svc, ok := gittransport.ParseService(r.URL.Query().Get("service"))
		if !ok || svc == gittransport.UploadArchive {
			textError(w, http.StatusForbidden, "only the smart HTTP protocol is supported; upgrade git")
			return
		}
		req.service, req.advertise = svc, true
	case req.suffix != pathInfoRefs && r.Method == http.MethodPost:
		req.service, _ = gittransport.ParseService(strings.TrimPrefix(req.suffix, "/"))
		if r.Header.Get("Content-Type") != "application/x-"+string(req.service)+"-request" {
			textError(w, http.StatusUnsupportedMediaType, "unexpected content type")
			return
		}
	default:
		w.Header().Set("Allow", allowed(req.suffix))
		textError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.serve(w, r, req)
}

func allowed(suffix string) string {
	if suffix == pathInfoRefs {
		return "GET, HEAD"
	}
	return "POST"
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, req request) {
	ctx := r.Context()
	ip := auth.ClientIP(r, h.o.TrustedProxies)
	who, status, msg := h.authenticate(ctx, r, ip)
	if status != 0 {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", realm)
		}
		textError(w, status, msg)
		return
	}
	if ok, wait := h.allowRequest(who, ip); !ok {
		retryAfter(w, wait)
		textError(w, http.StatusTooManyRequests, "too many git requests; try again later")
		return
	}
	if who.Method == auth.MethodToken && !who.HasScope(req.service.Scope()) {
		textError(w, http.StatusForbidden, "the access token lacks the "+req.service.Scope()+" scope")
		return
	}

	repo, err := h.o.Repos.Get(ctx, who, req.owner, req.name)
	var moved *repos.MovedError
	switch {
	case errors.As(err, &moved):
		h.redirect(w, r, req, moved)
		return
	case errors.Is(err, repos.ErrNotFound):
		h.denied(w, who, http.StatusNotFound, "repository not found")
		return
	case err != nil:
		h.o.Log.ErrorContext(ctx, "git http: resolve repository", "error", err)
		textError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !authz.Allowed(repo.Permission, req.service.Action()) {
		h.denied(w, who, http.StatusForbidden, "you do not have permission to push to this repository")
		return
	}
	h.run(w, r, req, repo, who, ip)
}

// authenticate returns the caller, or the status and message of a refusal.
func (h *Handler) authenticate(ctx context.Context, r *http.Request, ip string) (auth.Principal, int, string) {
	token, hasAuth, ok := credentials(r)
	if !hasAuth {
		return h.sessionCaller(ctx, r), 0, ""
	}
	if h.o.Failures != nil {
		if wait := h.o.Failures.LoginWait(auth.IPKey(ip)); wait > 0 {
			return auth.Principal{}, http.StatusTooManyRequests, "too many failed authentications; retry in " + wait.Round(time.Second).String()
		}
	}
	if !ok || h.o.Tokens == nil {
		h.fail(ip)
		return auth.Principal{}, http.StatusUnauthorized, "use a personal access token as password"
	}
	p, err := h.o.Tokens.Lookup(ctx, token)
	switch {
	case errors.Is(err, auth.ErrNoToken):
		h.fail(ip)
		return auth.Principal{}, http.StatusUnauthorized, "invalid or expired access token"
	case err != nil:
		h.o.Log.ErrorContext(ctx, "git http: look up access token", "error", err)
		return auth.Principal{}, http.StatusServiceUnavailable, "authentication unavailable"
	}
	return p, 0, ""
}

func (h *Handler) fail(ip string) {
	if h.o.Failures != nil {
		h.o.Failures.Fail(auth.IPKey(ip))
	}
}

// credentials extracts an access token from the Authorization header. ok is
// false when credentials were sent but none looks like an access token.
func credentials(r *http.Request) (token string, hasAuth, ok bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false, false
	}
	if user, pass, isBasic := r.BasicAuth(); isBasic {
		switch {
		case auth.ValidAccessTokenFormat(pass):
			return pass, true, true
		case pass == "" && auth.ValidAccessTokenFormat(user):
			return user, true, true // https://<token>@host/...
		}
		return "", true, false
	}
	if scheme, cred, found := strings.Cut(h, " "); found && strings.EqualFold(scheme, "Bearer") {
		cred = strings.TrimSpace(cred)
		return cred, true, auth.ValidAccessTokenFormat(cred)
	}
	return "", true, false
}

// sessionCaller authenticates the session cookie of a same-site request;
// any failure leaves the caller anonymous.
func (h *Handler) sessionCaller(ctx context.Context, r *http.Request) auth.Principal {
	if h.o.Sessions == nil {
		return auth.Principal{}
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return auth.Principal{}
	}
	tok := h.o.Cookies.Session(r)
	if tok == "" {
		return auth.Principal{}
	}
	s, err := h.o.Sessions.Lookup(ctx, tok)
	if err != nil {
		if !errors.Is(err, auth.ErrNoSession) {
			h.o.Log.WarnContext(ctx, "git http: look up session", "error", err)
		}
		return auth.Principal{}
	}
	return s.Principal()
}

func (h *Handler) allowRequest(who auth.Principal, ip string) (bool, time.Duration) {
	if h.o.Requests == nil {
		return true, 0
	}
	key := "git:ip:" + ip
	switch who.Method {
	case auth.MethodToken:
		key = "git:token:" + who.TokenID.String()
	case auth.MethodSession:
		key = "git:user:" + who.UserID.String()
	}
	return h.o.Requests.AllowAction(key)
}

// denied hides private repositories from anonymous callers: they get 401
// so that git asks for credentials, signed-in callers the real status.
func (h *Handler) denied(w http.ResponseWriter, who auth.Principal, status int, msg string) {
	if who.UserID == uuid.Nil {
		w.Header().Set("WWW-Authenticate", realm)
		textError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	textError(w, status, msg)
}

// redirect sends the client to the new path of a renamed or transferred
// repository; git follows it on the initial info/refs request.
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, req request, m *repos.MovedError) {
	name := m.Name
	if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, req.suffix), ".git") {
		name += ".git"
	}
	u := url.URL{Path: "/" + m.Owner + "/" + name + req.suffix, RawQuery: r.URL.RawQuery}
	status := http.StatusMovedPermanently
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		status = http.StatusPermanentRedirect
	}
	http.Redirect(w, r, u.String(), status)
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request, req request, repo repos.Repo, who auth.Principal, ip string) {
	ctx := r.Context()
	start := time.Now()
	rc := http.NewResponseController(w)
	_ = rc.EnableFullDuplex() // git may answer while the request body is still arriving

	body, err := requestBody(r)
	if err != nil {
		textError(w, http.StatusBadRequest, "invalid request body encoding")
		return
	}
	if body != nil {
		defer func() { _ = body.Close() }()
	}
	treq := &gittransport.Request{
		Service: req.service, Repo: repo, Pusher: who, Transport: gittransport.TransportHTTP, RemoteIP: ip,
		Protocol: r.Header.Get("Git-Protocol"), StatelessRPC: true, AdvertiseRefs: req.advertise,
		Abort: func() {
			now := time.Now()
			_ = rc.SetReadDeadline(now)
			_ = rc.SetWriteDeadline(now)
		},
	}
	if body != nil {
		treq.Stdin = body
	}
	out := &gittransport.OnceWriter{W: flusher{w: w, rc: rc}, Before: func() {
		hdr := w.Header()
		hdr.Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
		hdr.Set("Pragma", "no-cache")
		hdr.Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
		if req.advertise {
			hdr.Set("Content-Type", "application/x-"+string(req.service)+"-advertisement")
		} else {
			hdr.Set("Content-Type", "application/x-"+string(req.service)+"-result")
		}
		w.WriteHeader(http.StatusOK)
	}}
	v2 := req.service == gittransport.UploadPack && gittransport.IsV2(gittransport.SanitizeProtocol(treq.Protocol))
	if req.advertise && !v2 {
		out.Prefix = serviceAnnouncement(req.service)
	}
	treq.Stdout = out

	lease, err := h.o.Transport.Acquire(ctx, treq)
	if err != nil {
		h.acquireFailed(w, r, repo, err)
		return
	}
	defer lease.Release()
	if r.Method == http.MethodHead {
		out.Before()
		return
	}
	code, err := h.o.Transport.Run(ctx, lease, treq)
	log := h.o.Log.With("service", string(req.service), "repo", repo.FullName(), "user", who.Username,
		"advertise", req.advertise, "duration", time.Since(start).Round(time.Millisecond))
	switch {
	case err == nil:
		log.InfoContext(ctx, "git http request", "code", code)
		if !out.Wrote() {
			out.Before() // git exited without output
		}
	case out.Wrote():
		// Too late for a status: the client sees a truncated stream.
		log.WarnContext(ctx, "git http request aborted", "error", err)
	case errors.Is(err, gittransport.ErrPushTooLarge):
		textError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, gittransport.ErrIdleTimeout), errors.Is(err, gittransport.ErrTooLong):
		textError(w, http.StatusRequestTimeout, err.Error())
	default:
		log.ErrorContext(ctx, "git http request failed", "error", err)
		textError(w, http.StatusInternalServerError, "internal error")
	}
}

func (h *Handler) acquireFailed(w http.ResponseWriter, r *http.Request, repo repos.Repo, err error) {
	switch {
	case errors.Is(err, gitstore.ErrBusy), errors.Is(err, gitstore.ErrCold):
		retryAfter(w, 30*time.Second)
		textError(w, http.StatusServiceUnavailable, "the repository is being moved between storage tiers; retry shortly")
	case errors.Is(err, gitstore.ErrDegraded), errors.Is(err, gitstore.ErrHotMissing):
		h.o.Log.ErrorContext(r.Context(), "git http: repository unavailable", "repo", repo.FullName(), "error", err)
		textError(w, http.StatusServiceUnavailable, "the repository is temporarily unavailable")
	case r.Context().Err() != nil:
		// The client went away while waiting for the lease.
	default:
		h.o.Log.ErrorContext(r.Context(), "git http: acquire repository", "repo", repo.FullName(), "error", err)
		textError(w, http.StatusInternalServerError, "internal error")
	}
}

// requestBody returns the request body, decompressed when the client sent
// it gzipped (git does for large negotiation requests).
func requestBody(r *http.Request) (io.ReadCloser, error) {
	if r.Method != http.MethodPost || r.Body == nil {
		return nil, nil
	}
	switch strings.ToLower(r.Header.Get("Content-Encoding")) {
	case "", "identity":
		return r.Body, nil
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip body: %w", err)
		}
		return zr, nil
	}
	return nil, errors.New("unsupported content encoding")
}

// serviceAnnouncement is the pkt-line "# service=..." block that precedes
// a v0/v1 smart advertisement.
func serviceAnnouncement(s gittransport.Service) []byte {
	line := "# service=" + string(s) + "\n"
	return []byte(fmt.Sprintf("%04x%s0000", len(line)+4, line))
}

// flusher flushes every write through the ResponseController, so that the
// stream goes out as git produces it.
type flusher struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (f flusher) Write(p []byte) (int, error) { return f.w.Write(p) }
func (f flusher) Flush() error                { return f.rc.Flush() }

func retryAfter(w http.ResponseWriter, d time.Duration) {
	secs := int(math.Ceil(d.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
}

// textError writes a plain-text error, which git prints to the user.
func textError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, msg+"\n") //nolint:gosec // text/plain with nosniff; msg is a server-chosen string
}
