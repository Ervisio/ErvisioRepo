package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/mfa"
	"github.com/Ervisio/ErvisioRepo/internal/buildinfo"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
	"github.com/Ervisio/ErvisioRepo/internal/releases"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/sso"
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

	tiering, err := setupGitTiering(cfg, pool, storage, log)
	if err != nil {
		return err
	}
	var gitTier *gitstore.Tier
	if tiering != nil {
		gitTier = tiering.Tier
	}
	jobOpts := []jobs.Option{jobs.WithStorage(storage, log), identity.JobsOption(mailer),
		orgs.JobsOption(orgs.MailerFor(cfg, dbgen.New(pool), sender, log))}
	jobOpts = append(jobOpts, releases.JobsOption(dbgen.New(pool), storage, log))
	if gitTier != nil {
		jobOpts = append(jobOpts, repos.JobsOption(gitTier, log))
		jobOpts = append(jobOpts, tiering.jobOptions(log)...)
	}
	lfsSrv, err := setupLFS(cfg, pool, storage, gitTier, log)
	if err != nil {
		return err
	}
	if lfsSrv != nil {
		jobOpts = append(jobOpts, lfsSrv.JobsOption())
	}
	if opt, ok := backupJobOption(cfg, storage, log); ok {
		jobOpts = append(jobOpts, opt)
	}
	journal, err := setupGitJournal(ctx, cfg, pool, gitTier, log)
	if err != nil {
		return err
	}
	defer journal.Close()
	if journal != nil {
		jobOpts = append(jobOpts, journal.Jobs)
	}
	runner, err = jobs.New(pool, log, jobOpts...)
	if err != nil {
		return err
	}
	if journal != nil {
		journal.Journal.SetEnqueuer(runner)
	}
	ids, err := identity.Setup(cfg, pool, runner, log, nil)
	if err != nil {
		return err
	}
	mfaSvc, err := setupMFA(cfg, pool, ids, log)
	if err != nil {
		return err
	}
	ssoMgr, err := setupSSO(cfg, pool, ids, mfaSvc, log)
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

	creds := credentials.New(credentials.Options{Pool: pool, Log: log,
		TokenMaxTTL: time.Duration(cfg.Auth.TokenMaxTTL)})
	var git *gitTransports
	var repoSvc *repos.Service
	if gitTier != nil {
		repoSvc = repos.New(repos.Options{Pool: pool, Tier: gitTier, Jobs: runner, Log: log})
		if git, err = setupGitTransports(cfg, pool, gitTier, repoSvc, journal.TransportHooks(gitTier), ids, creds, proxies, lfsSrv, log); err != nil {
			return err
		}
	}

	if !web.HasIndex() {
		log.Warn("web UI not embedded; run `make web` before building the server")
	}
	deps := httpapi.Deps{
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
		Web:         web.FS(),
		HasIndex:    web.HasIndex(),
		Registry:    registry,
		Storage:     storage,
		Identity:    ids.Service,
		MFA:         mfaSvc,
		Orgs:        orgs.New(orgs.Options{Pool: pool, Jobs: runner, Log: log, Config: cfg}),
		Audit:       dbgen.New(pool),
		Credentials: creds,
		SSO:         ssoMgr,
		Auth: &auth.Middleware{
			Sessions: ids.Sessions, Tokens: &auth.Tokens{Store: dbgen.New(pool)},
			Cookies: auth.CookiesFor(cfg.Server.PublicURL), TrustedProxies: proxies, Log: log,
		},
	}
	if repoSvc != nil {
		deps.Repos = repoSvc
		var rj releases.Journal
		if journal != nil {
			rj = journal.Journal
		}
		deps.Releases = releases.New(releases.Options{Pool: pool, Repos: repoSvc, Git: gitTier, Journal: rj, Storage: storage, Log: log})
		deps.Git = git.HTTP
		deps.Tiering = tiering.Admin
		deps.Hydration = tiering.Hydrator
		browse, err := setupBrowse(cfg, pool, gitTier, journal, log)
		if err != nil {
			return err
		}
		defer browse.Close()
		deps.Browse = browse
	}
	handler := httpapi.NewRouter(deps)

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

	sshErrc := make(chan error, 1)
	if git != nil && git.SSH != nil {
		sshLn, err := net.Listen("tcp", cfg.Server.SSHListen)
		if err != nil {
			_ = srv.Close()
			return fmt.Errorf("ssh listen: %w", err)
		}
		go func() { sshErrc <- git.SSH.Serve(sshLn) }()
		log.Info("ssh server listening", "addr", sshLn.Addr().String())
	}

	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case err := <-sshErrc:
		_ = srv.Close()
		return fmt.Errorf("ssh server: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if git != nil && git.SSH != nil {
		if err := git.SSH.Shutdown(shutCtx); err != nil {
			log.Error("ssh shutdown", "error", err)
		}
	}
	if err := srv.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}

// setupSSO reads the client secrets of the [[auth.oidc]] providers and
// builds the OIDC manager; it returns nil when no provider is configured.
func setupSSO(cfg *config.Config, pool *pgxpool.Pool, ids *identity.Stack, mfaSvc *mfa.Service, log *slog.Logger) (*sso.Manager, error) {
	if len(cfg.Auth.OIDC) == 0 {
		return nil, nil
	}
	secrets := make(map[string]string, len(cfg.Auth.OIDC))
	for _, p := range cfg.Auth.OIDC {
		secret, err := readSecretFile(p.ClientSecretFile)
		if err != nil {
			return nil, fmt.Errorf("oidc provider %s client secret: %w", p.Name, err)
		}
		secrets[p.Name] = secret
	}
	var gate sso.GateFunc
	if mfaSvc != nil {
		gate = mfaSvc.Gate
	}
	return sso.New(sso.Options{Pool: pool, Identity: ids.Service, Providers: cfg.Auth.OIDC, Secrets: secrets,
		PublicURL: cfg.Server.PublicURL, Cookies: auth.CookiesFor(cfg.Server.PublicURL), Log: log,
		Limiter: ids.Limiter, Gate: gate}), nil
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

// setupBrowse builds the code browsing service. Web edits go through the
// hook server, so that the journal records them like pushes.
func setupBrowse(cfg *config.Config, pool *pgxpool.Pool, t *gitstore.Tier, j *gitJournal, log *slog.Logger) (*gitbrowse.Service, error) {
	var hooks *githooks.Server
	if j != nil {
		hooks = j.Hooks
	}
	host := "localhost"
	if u, err := url.Parse(cfg.Server.PublicURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	s, err := gitbrowse.New(gitbrowse.Options{Tier: t, Hooks: hooks, DB: pool, NoReplyDomain: "users.noreply." + host,
		Log: log.With("component", "git-browse")})
	if err != nil {
		return nil, fmt.Errorf("code browsing: %w", err)
	}
	return s, nil
}
