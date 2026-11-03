package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/buildinfo"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
	"github.com/Ervisio/ErvisioRepo/web"
)

const shutdownTimeout = 30 * time.Second

// serve runs the server until ctx is cancelled. When ln is nil it listens on
// cfg.Server.Listen.
func serve(ctx context.Context, cfg *config.Config, log *slog.Logger, ln net.Listener) error {
	info := buildinfo.Get()
	log.Info("starting ervisio-repo", "version", info.Version, "commit", info.Commit)

	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, log); err != nil {
		return err
	}
	instanceID, err := db.EnsureInstanceID(ctx, dbgen.New(pool))
	if err != nil {
		return err
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// The tier enqueues replication on the runner, which runs the tier's
	// jobs: the runner is assigned right after the tier is built.
	var runner *jobs.Runner
	storage, stores, err := openStorage(ctx, cfg, dbgen.New(pool), registry, log,
		func(ctx context.Context, ref tier.ObjectRef) error { return runner.EnqueueReplication(ctx, ref) })
	if err != nil {
		return err
	}
	defer tier.CloseStores(stores)

	mailPassword, err := readSecretFile(cfg.Mail.PasswordFile)
	if err != nil {
		return fmt.Errorf("mail password: %w", err)
	}
	sender, err := mail.New(cfg.Mail, mailPassword, log)
	if err != nil {
		return err
	}
	mailer := identity.MailerFor(cfg, dbgen.New(pool), sender, log)

	runner, err = jobs.New(pool, log, jobs.WithStorage(storage, log), identity.JobsOption(mailer))
	if err != nil {
		return err
	}
	ids, err := identity.Setup(cfg, pool, runner, log, nil)
	if err != nil {
		return err
	}
	proxies, err := auth.ParseTrustedProxies(cfg.Server.TrustedProxies)
	if err != nil {
		return err
	}
	if n, err := ids.Service.CountUsers(ctx); err == nil && n == 0 {
		log.Warn("no accounts yet: the first user to register becomes the instance admin")
	}
	if err := runner.Start(ctx); err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := runner.Stop(stopCtx); err != nil {
			log.Error("stop job queue", "error", err)
		}
	}()

	if !web.HasIndex() {
		log.Warn("web UI not embedded; run `make web` before building the server")
	}
	handler := httpapi.NewRouter(httpapi.Deps{
		Log:  log,
		DB:   pool,
		Jobs: runner,
		Meta: httpapi.Meta{
			Version:      info.Version,
			Commit:       info.Commit,
			InstanceID:   instanceID,
			PublicURL:    cfg.Server.PublicURL,
			Registration: cfg.Security.Registration,
		},
		Web:      web.FS(),
		HasIndex: web.HasIndex(),
		Registry: registry,
		Storage:  storage,
		Identity: ids.Service,
		Auth: &auth.Middleware{
			Sessions: ids.Sessions, Cookies: auth.CookiesFor(cfg.Server.PublicURL),
			TrustedProxies: proxies, Log: log,
		},
	})

	if ln == nil {
		ln, err = net.Listen("tcp", cfg.Server.Listen)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
	}
	// No WriteTimeout: streaming responses (SSE, git) are long-lived; normal
	// requests are bounded by the router's timeout middleware.
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("http server listening", "addr", ln.Addr().String(), "public_url", cfg.Server.PublicURL)

	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}

// readSecretFile returns the trimmed content of an operator-configured
// secret file, or "" when path is empty.
func readSecretFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // operator-configured secret file, not request data
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimRight(string(raw), "\r\n"), nil
}
