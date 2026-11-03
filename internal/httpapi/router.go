// Package httpapi wires the HTTP surface of the server: operational
// endpoints, the REST API generated from api/openapi.yaml and the SPA.
package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/mfa"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/sso"
)

// RequestTimeout bounds every non-streaming request.
const RequestTimeout = 60 * time.Second

// Pinger checks database connectivity (satisfied by *pgxpool.Pool).
type Pinger interface {
	Ping(ctx context.Context) error
}

// JobsStatus reports whether the job queue is running (satisfied by *jobs.Runner).
type JobsStatus interface {
	Running() bool
}

// Meta is the static instance information returned by GET /api/v1/meta.
type Meta struct {
	Version      string
	Commit       string
	InstanceID   uuid.UUID
	PublicURL    string
	Registration string
}

// Deps are the collaborators of the router.
type Deps struct {
	Log      *slog.Logger
	DB       Pinger
	Jobs     JobsStatus
	Meta     Meta
	Web      fs.FS // built SPA, see package web
	HasIndex bool
	Registry *prometheus.Registry // nil creates one with Go and process collectors
	Storage  Storage              // nil makes the storage admin endpoints answer 503
	// Identity serves the account endpoints; nil makes them answer 503.
	Identity Identity
	// MFA runs the two-step login and /user/security; nil disables it.
	MFA *mfa.Service
	// Orgs serves /orgs; nil makes those endpoints answer 503.
	Orgs Orgs
	// Repos serves /repos, /user/repos and the owner listings; nil answers 503.
	Repos Repos
	// Audit reads the instance audit log for /admin/audit; nil answers 503.
	Audit audit.Lister
	// Credentials serves /user/tokens and /user/keys; nil answers 503.
	Credentials *credentials.Service
	// SSO runs the OIDC sign-in and linking; nil means no providers.
	SSO *sso.Manager
	// Auth loads sessions and enforces CSRF and authorization on /api/v1.
	// Nil uses cookies derived from Meta.PublicURL and no sessions, so
	// every caller is anonymous.
	Auth *auth.Middleware
}

// NewRouter builds the root handler.
func NewRouter(d Deps) http.Handler {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Registry == nil {
		d.Registry = prometheus.NewRegistry()
		d.Registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}
	metrics := newHTTPMetrics(d.Registry)
	if d.Auth == nil {
		d.Auth = &auth.Middleware{Cookies: auth.CookiesFor(d.Meta.PublicURL)}
	}
	if d.Auth.Problem == nil {
		d.Auth.Problem = writeProblem
	}
	if d.Auth.Log == nil {
		d.Auth.Log = d.Log
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, accessLog(d.Log), recoverer(d.Log), securityHeaders, metrics.middleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	r.With(loopbackOnly).Handle("/metrics", promhttp.HandlerFor(d.Registry, promhttp.HandlerOpts{}))

	// Streaming routes (SSE under /api/events, git transports) will be
	// mounted outside this group so that they are not cut by the timeout.
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(RequestTimeout))
		r.Get("/readyz", readyz(d))
		r.Route("/api", func(api chi.Router) {
			api.NotFound(problemHandler(http.StatusNotFound))
			api.MethodNotAllowed(problemHandler(http.StatusMethodNotAllowed))
			api.Route("/v1", func(v1 chi.Router) { mountV1(v1, d) })
		})
		mountOIDC(r, d)
		r.Handle("/*", spaHandler(d.Web, d.HasIndex))
	})
	return r
}

func readyz(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.DB == nil || d.Jobs == nil {
			writeProblem(w, http.StatusServiceUnavailable, "server is starting")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			d.Log.WarnContext(r.Context(), "readiness: database unreachable", "error", err)
			writeProblem(w, http.StatusServiceUnavailable, "database unreachable")
			return
		}
		if !d.Jobs.Running() {
			writeProblem(w, http.StatusServiceUnavailable, "job queue not running")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	}
}

// loopbackOnly admits only direct connections from the local host. A request
// relayed by a local reverse proxy (Caddy) also arrives from loopback, so any
// forwarding header means the real client is remote.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !directLoopback(r) {
			writeProblem(w, http.StatusForbidden, "metrics are only available from localhost")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// directLoopback reports whether r comes from the local host without a proxy.
func directLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	forwarded := r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" ||
		r.Header.Get("X-Real-Ip") != ""
	return err == nil && ip != nil && ip.IsLoopback() && !forwarded
}

// mountV1 mounts the REST API. Every request loads the session and passes
// the CSRF check; each operation then enforces its OpenAPI security
// requirement (public, signed-in user or instance admin). The admin prefix
// is also guarded as a whole, so that a misdeclared operation cannot leak.
func mountV1(r chi.Router, d Deps) {
	r.Use(d.Auth.LoadSession, d.Auth.CSRF, adminOnly(d.Auth), mfaCookie(d.Auth.Cookies))
	srv := &v1Server{meta: d.Meta, storage: d.Storage, identity: d.Identity, cookies: d.Auth.Cookies, mfa: d.MFA, orgs: d.Orgs, repos: d.Repos, audit: d.Audit,
		creds: d.Credentials, sso: d.SSO}
	strict := v1gen.NewStrictHandlerWithOptions(srv, nil, v1gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeProblem(w, http.StatusBadRequest, err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			d.Log.ErrorContext(r.Context(), "api handler failed",
				"request_id", middleware.GetReqID(r.Context()), "error", err)
			writeProblem(w, http.StatusInternalServerError, "")
		},
	})
	v1gen.HandlerWithOptions(strict, v1gen.ChiServerOptions{
		BaseRouter:  r,
		Middlewares: []v1gen.MiddlewareFunc{d.Auth.Authorize(operationScopes, operationTokenScopes)},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeProblem(w, http.StatusBadRequest, err.Error())
		},
	})
}

// operationScopes returns the scopes the generated router stored for the
// operation; ok is false for public operations (security: []).
func operationScopes(ctx context.Context) ([]string, bool) {
	s, ok := ctx.Value(v1gen.SessionScopes).([]string)
	return s, ok
}

// operationTokenScopes returns the scopes an access token needs for the
// operation; ok is false when the operation does not accept tokens.
func operationTokenScopes(ctx context.Context) ([]string, bool) {
	s, ok := ctx.Value(v1gen.TokenScopes).([]string)
	return s, ok
}

// adminOnly requires an instance admin on the whole admin prefix.
func adminOnly(m *auth.Middleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		admin := m.RequireAdmin(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, adminPrefix) {
				admin.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
