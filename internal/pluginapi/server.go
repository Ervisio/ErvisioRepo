package pluginapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Header carries the per-process secret.
const Header = "x-ervisio-plugin"

// StorageSource reports the storage picture; *tier.Tier satisfies it.
type StorageSource interface {
	Status(ctx context.Context) (tier.Status, error)
}

// RunnerSource lists runners; *dbgen.Queries satisfies it.
type RunnerSource interface {
	ListActionsRunners(ctx context.Context, arg dbgen.ListActionsRunnersParams) ([]dbgen.ActionsRunner, error)
}

// CountSource counts repositories, users and organizations; *dbgen.Queries
// satisfies it.
type CountSource interface {
	InstanceCounts(ctx context.Context) (dbgen.InstanceCountsRow, error)
}

// ErrNotFound is returned by a RunnerAdmin for an unknown runner.
var ErrNotFound = errors.New("not found")

// RunnerAdmin manages the instance runners, as the web admin does.
type RunnerAdmin interface {
	// CreateInstanceRegistrationToken issues an instance-scope registration token.
	CreateInstanceRegistrationToken(ctx context.Context) (token string, expiresAt time.Time, err error)
	// DeleteInstanceRunner removes an instance runner; ErrNotFound when it
	// does not exist or has another scope.
	DeleteInstanceRunner(ctx context.Context, id uuid.UUID) error
}

// Upstream says where a reverse proxy reaches the server. It mirrors the
// plugin's Upstream type.
type Upstream struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Container     string `json:"container"`
	ContainerPort int    `json:"containerPort"`
	Network       string `json:"network"`
}

// Options wires the server. Config and Version are required; the other
// sources are optional and their routes answer 501 when missing.
type Options struct {
	// Config is the configuration the process is running with.
	Config *config.Config
	// File is the TOML file the settings are written to.
	File ConfigFile
	// LookupEnv applies the same EREPO_* overrides as at start (default os.LookupEnv).
	LookupEnv func(string) (string, bool)
	Version   string
	Commit    string
	// StartedAt is when the process started (default: when New runs).
	StartedAt   time.Time
	Storage     StorageSource
	Runners     RunnerSource
	RunnerAdmin RunnerAdmin
	Counts      CountSource
	// Upstream overrides DefaultUpstream.
	Upstream *Upstream
	// Overrides are the instance settings stored by the web admin; the
	// settings view shows them applied and a save drops them.
	Overrides Overrides
	Logger    *slog.Logger
}

// Overrides is the part of instancesettings.Service the plugin uses.
type Overrides interface {
	Overlay(ctx context.Context, base *config.Config) (*config.Config, error)
	Delete(ctx context.Context, keys []string) error
}

// Server is the plugin API.
type Server struct {
	opt      Options
	secret   string
	upstream Upstream
	restart  atomic.Bool
	mu       sync.Mutex // serializes the read-modify-write of the config file
	log      *slog.Logger
}

// New creates the server and its secret.
func New(opt Options) (*Server, error) {
	if opt.Config == nil {
		return nil, errors.New("pluginapi: Config is required")
	}
	if opt.LookupEnv == nil {
		opt.LookupEnv = os.LookupEnv
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	if opt.StartedAt.IsZero() {
		opt.StartedAt = time.Now()
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate plugin secret: %w", err)
	}
	s := &Server{opt: opt, secret: hex.EncodeToString(b), log: opt.Logger}
	if opt.Upstream != nil {
		s.upstream = *opt.Upstream
	} else {
		s.upstream = DefaultUpstream(opt.Config, opt.LookupEnv)
	}
	return s, nil
}

// DefaultUpstream derives the upstream from the listen address. In Docker the
// published host address cannot be seen from the container: setup passes
// ERVISIO_UPSTREAM_HOST and ERVISIO_UPSTREAM_PORT (the host side of the
// published port) and ERVISIO_UPSTREAM_NETWORK / ERVISIO_UPSTREAM_CONTAINER.
func DefaultUpstream(cfg *config.Config, env func(string) (string, bool)) Upstream {
	host, port := "127.0.0.1", 3400
	if h, p, err := net.SplitHostPort(cfg.Server.Listen); err == nil {
		if h != "" && h != "0.0.0.0" && h != "::" {
			host = h
		}
		if n, err := net.LookupPort("tcp", p); err == nil {
			port = n
		}
	}
	u := Upstream{Host: host, Port: port, Container: "ervisiorepo-app", ContainerPort: port, Network: "ervisiorepo_edge"}
	get := func(k string) string { v, _ := env(k); return v }
	if v := get("ERVISIO_UPSTREAM_HOST"); v != "" {
		u.Host = v
	}
	if v := get("ERVISIO_UPSTREAM_PORT"); v != "" {
		if n, err := net.LookupPort("tcp", v); err == nil {
			u.Port = n
		}
	}
	if v := get("ERVISIO_UPSTREAM_CONTAINER"); v != "" {
		u.Container = v
	}
	if v := get("ERVISIO_UPSTREAM_NETWORK"); v != "" {
		u.Network = v
	}
	return u
}

// Handler serves /api/plugin/* and requires the secret header. Anything
// else, including a missing or wrong secret, is a 404.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeProblem(w, http.StatusNotFound, "")
			return
		}
		route, ok := strings.CutPrefix(r.URL.Path, "/api/plugin/")
		if !ok {
			writeProblem(w, http.StatusNotFound, "")
			return
		}
		switch route {
		case "status":
			s.only(w, r, http.MethodGet, s.status)
		case "settings":
			switch r.Method {
			case http.MethodGet:
				s.getSettings(w, r)
			case http.MethodPut:
				s.putSettings(w, r)
			default:
				methodNotAllowed(w, "GET, PUT")
			}
		case "storage":
			s.only(w, r, http.MethodGet, s.storage)
		case "runners":
			s.only(w, r, http.MethodGet, s.runners)
		case "runners/token":
			s.only(w, r, http.MethodPost, s.runnerToken)
		case "public-url":
			s.only(w, r, http.MethodPut, s.putPublicURL)
		case "config/check":
			s.only(w, r, http.MethodPost, s.configCheck)
		case "mail/test":
			s.only(w, r, http.MethodPost, s.mailTest)
		default:
			if id, ok := strings.CutPrefix(route, "runners/"); ok && id != "" && !strings.Contains(id, "/") {
				s.only(w, r, http.MethodDelete, func(w http.ResponseWriter, r *http.Request) { s.deleteRunner(w, r, id) })
				return
			}
			writeProblem(w, http.StatusNotFound, "")
		}
	})
}

func (s *Server) only(w http.ResponseWriter, r *http.Request, method string, h http.HandlerFunc) {
	if r.Method != method {
		methodNotAllowed(w, method)
		return
	}
	h(w, r)
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeProblem(w, http.StatusMethodNotAllowed, "")
}

func (s *Server) authorized(r *http.Request) bool {
	given := r.Header.Get(Header)
	return len(given) == len(s.secret) && subtle.ConstantTimeCompare([]byte(given), []byte(s.secret)) == 1
}

// SocketHandler is what the unix socket serves: only /api/plugin/*, with the
// secret added to every request (the socket's file permission is the access
// control, as in ErvisioDrive's socket proxy).
func (s *Server) SocketHandler() http.Handler {
	inner := s.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/plugin/") {
			writeProblem(w, http.StatusNotFound, "")
			return
		}
		r2 := r.Clone(r.Context())
		r2.Header.Set(Header, s.secret)
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		r2.Body = r.Body
		inner.ServeHTTP(w, r2)
	})
}

// ListenAndServe serves SocketHandler on the unix socket path until ctx is
// done. The socket is created with mode 0660; a stale socket file is removed.
// An empty path disables the API.
func (s *Server) ListenAndServe(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create socket folder: %w", err)
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("plugin socket path %s exists and is not a socket", path)
		}
		_ = os.Remove(path)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("listen on plugin socket: %w", err)
	}
	//nolint:gosec // group access to the socket is intended, like ErvisioDrive's (0660)
	if err := os.Chmod(path, 0o660); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chmod plugin socket: %w", err)
	}
	srv := &http.Server{Handler: s.SocketHandler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	s.log.Info("plugin API listening", "socket", path)
	err = srv.Serve(ln)
	<-done
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve plugin socket: %w", err)
	}
	return nil
}
