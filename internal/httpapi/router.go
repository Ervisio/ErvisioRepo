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
	"github.com/Ervisio/ErvisioRepo/internal/dashboard"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/ghapi"
	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/notify"
	"github.com/Ervisio/ErvisioRepo/internal/sso"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
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
	// DefaultLayout is the layout new users start with (ui.default_layout).
	DefaultLayout string
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
	// Tiering serves the admin pin/unpin/evict endpoints; nil answers 503.
	Tiering Tiering
	// Releases serves the release endpoints and asset transfers; nil answers 503.
	Releases Releases
	// Browse serves the code browsing endpoints, raw files and archives;
	// nil answers 503.
	Browse *gitbrowse.Service
	// Protection serves the branch protection and tag rule endpoints; nil
	// answers 503.
	Protection Protection
	// Issues serves the issue, label and milestone endpoints and issue
	// attachments; nil answers 503.
	Issues Issues
	// Hydration streams the hydration progress; nil answers 503.
	Hydration Hydration
	// Git serves the Smart HTTP transport (internal/githttp) on
	// /<owner>/<repo>.git; nil leaves those paths to the SPA.
	Git http.Handler
	// Auth loads sessions and enforces CSRF and authorization on /api/v1.
	// Nil uses cookies derived from Meta.PublicURL and no sessions, so
	// every caller is anonymous.
	Auth *auth.Middleware
	// GitHub serves the GitHub sync endpoints and webhook; nil answers 503.
	GitHub *ghsync.Service
	// Webhooks serves the outgoing webhook endpoints; nil answers 503.
	Webhooks *webhooks.Service
	// Notify serves the notification endpoints; nil answers 503.
	Notify *notify.Service
	// Dashboard serves the home feeds and the user preferences; nil answers 503.
	Dashboard *dashboard.Service
	// Settings serves /admin/settings; nil answers 503.
	Settings InstanceSettings

	// Pulls serves the pull request, review and fork endpoints; nil answers 503.
	Pulls Pulls
	// Statuses serves the commit status and checks endpoints; nil answers 503.
	Statuses Statuses
	// ActionsRunner serves the Ervisio Actions runner protocol under
	// /api/actions/runner (long polls, outside the request timeout); nil
	// leaves it unmounted.
	ActionsRunner http.Handler
	// ActionsResults serves the actions/cache and artifact services
	// (/twirp, /api/actions/results, /api/actions/cache; long uploads,
	// outside the request timeout); nil leaves it unmounted.
	ActionsResults http.Handler
	// Actions serves the workflow run, log, secret and runner endpoints;
	// nil answers 503.
	Actions Actions
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
	r.Use(middleware.RequestID, accessLog(d.Log), recoverer(d.Log), securityHeaders, metrics.middleware, jobAPIAlias)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	r.With(loopbackOnly).Handle("/metrics", promhttp.HandlerFor(d.Registry, promhttp.HandlerOpts{}))

	// Streaming routes (SSE under /api/events, git transports) will be
	// mounted outside this group so that they are not cut by the timeout.
	mountHydration(r, d)
	mountReleases(r, d)
	mountIssues(r, d)
	mountGitHubWebhook(r, d)
	mountBrowse(r, d)
	mountGit(r, d)
	mountGitHubAPI(r, d)
	mountActions(r, d)
	if d.ActionsRunner != nil {
		r.Handle("/api/actions/runner/*", d.ActionsRunner)
	}
	if d.ActionsResults != nil {
		for _, p := range []string{"/twirp/*", "/api/actions/results/*", "/api/actions/cache/*"} {
			r.Handle(p, d.ActionsResults)
		}
	}
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

// mountGitHubAPI mounts the GitHub REST compatible subset at /api/v3. It
// applies the request timeout itself, outside the asset transfers.
func mountGitHubAPI(r chi.Router, d Deps) {
	db, _ := d.DB.(dbgen.DBTX)
	g := ghapi.Deps{Log: d.Log, DB: db, PublicURL: d.Meta.PublicURL, Version: d.Meta.Version, Auth: d.Auth,
		Browse: d.Browse, Timeout: RequestTimeout}
	// Assigned one by one: a nil service must stay a nil interface.
	if d.Repos != nil {
		g.Repos = d.Repos
	}
	if d.Orgs != nil {
		g.Orgs = d.Orgs
	}
	if d.Identity != nil {
		g.Identity = d.Identity
	}
	if d.Issues != nil {
		g.Issues = d.Issues
	}
	if d.Pulls != nil {
		g.Pulls = d.Pulls
	}
	if d.Releases != nil {
		g.Releases = d.Releases
	}
	if d.Statuses != nil {
		g.Statuses = d.Statuses
	}
	ghapi.Mount(r, g)
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

// jobAPIAlias sends /api/v1 requests made with a job token to the
// GitHub-compatible API: the act runtime hardcodes GITHUB_API_URL to
// <server>/api/v1 (Gitea's API), while ErvisioRepo serves it at /api/v3.
func jobAPIAlias(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/"); ok && hasJobToken(r) {
			r.URL.Path = ghapi.Prefix + "/" + rest
			r.URL.RawPath = ""
		}
		next.ServeHTTP(w, r)
	})
}

// hasJobToken reports whether the request carries a job token, in the
// "Bearer" or "token" scheme the GitHub clients use.
func hasJobToken(r *http.Request) bool {
	scheme, cred, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") && !strings.EqualFold(scheme, "token") {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(cred), auth.JobTokenPrefix)
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
		creds: d.Credentials, sso: d.SSO, tiering: d.Tiering, releases: d.Releases, browse: d.Browse,
		protection: d.Protection, github: d.GitHub, issues: d.Issues}
	srv.webhooks, srv.notify = d.Webhooks, d.Notify
	srv.pulls = d.Pulls
	srv.statuses = d.Statuses
	srv.actions = d.Actions
	srv.dashboard = d.Dashboard
	srv.settings = d.Settings
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

// mountGit mounts the Smart HTTP endpoints outside the request timeout: a
// clone or push can stream for hours (bounded by [git] limits instead).
func mountGit(r chi.Router, d Deps) {
	if d.Git == nil {
		return
	}
	r.Get("/{owner}/{repo}/info/refs", d.Git.ServeHTTP)
	r.Head("/{owner}/{repo}/info/refs", d.Git.ServeHTTP)
	r.Post("/{owner}/{repo}/git-upload-pack", d.Git.ServeHTTP)
	r.Post("/{owner}/{repo}/git-receive-pack", d.Git.ServeHTTP)
	// Git LFS batch, basic transfer and locks API (PLAN §4.4).
	r.Get("/{owner}/{repo}/info/lfs/*", d.Git.ServeHTTP)
	r.Put("/{owner}/{repo}/info/lfs/*", d.Git.ServeHTTP)
	r.Post("/{owner}/{repo}/info/lfs/*", d.Git.ServeHTTP)
}
