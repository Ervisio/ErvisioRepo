// Package httpapi wires the HTTP surface of the server: operational
// endpoints, the REST API generated from api/openapi.yaml and the SPA.
package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
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
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		forwarded := r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" ||
			r.Header.Get("X-Real-Ip") != ""
		if err != nil || ip == nil || !ip.IsLoopback() || forwarded {
			writeProblem(w, http.StatusForbidden, "metrics are only available from localhost")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mountV1(r chi.Router, d Deps) {
	strict := v1gen.NewStrictHandlerWithOptions(&v1Server{meta: d.Meta}, nil, v1gen.StrictHTTPServerOptions{
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
		BaseRouter: r,
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeProblem(w, http.StatusBadRequest, err.Error())
		},
	})
}
