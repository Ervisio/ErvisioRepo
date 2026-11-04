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

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/mfa"
	"github.com/Ervisio/ErvisioRepo/internal/buildinfo"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
	"github.com/Ervisio/ErvisioRepo/internal/notify"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
	"github.com/Ervisio/ErvisioRepo/internal/pluginapi"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/releases"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/sso"
	"github.com/Ervisio/ErvisioRepo/internal/statuses"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
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
	bus := events.NewBus(pool, log.With("component", "events"))
	jobOpts = append(jobOpts, bus.JobsOption(), issues.JobsOption(dbgen.New(pool), storage, log))
	hooks, notifier, err := setupNotifications(cfg, pool, sender, log)
	if err != nil {
		return err
	}
	jobOpts = append(jobOpts, hooks.JobsOption(), notifier.JobsOption())
	journal, err := setupGitJournal(ctx, cfg, pool, gitTier, bus, log)
	if err != nil {
		return err
	}
	defer journal.Close()
	if journal != nil && journal.Jobs != nil {
		jobOpts = append(jobOpts, journal.Jobs)
	}
	if journal != nil {
		jobOpts = append(jobOpts, journal.GitHub.JobsOption())
	}
	actionsSvc, err := setupActions(cfg, pool, storage, log)
	if err != nil {
		return err
	}
	if actionsSvc != nil {
		jobOpts = append(jobOpts, actionsSvc.JobsOption())
	}
	actionsResults, resultsJobs, err := setupActionsResults(cfg, pool, storage, actionsSvc, log)
	if err != nil {
		return err
	}
	jobOpts = append(jobOpts, resultsJobs...)
	runner, err = jobs.New(pool, log, jobOpts...)
	if err != nil {
		return err
	}
	bus.SetClient(runner.Client())
	hooks.SetEnqueuer(runner)
	notifier.SetEnqueuer(runner)
	if journal != nil && journal.Journal != nil {
		journal.Journal.SetEnqueuer(runner)
	}
	if journal != nil {
		journal.GitHub.SetEnqueuer(runner)
		journal.GitHub.SetHooks(journal.Hooks)
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
		if git, err = setupGitTransports(cfg, pool, gitTier, repoSvc, journal.TransportHooks(gitTier), ids, creds, proxies, lfsSrv,
			actionsJobTokens(actionsSvc), log); err != nil {
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
			Sessions: ids.Sessions, Tokens: &auth.Tokens{Store: dbgen.New(pool), Jobs: actionsJobTokens(actionsSvc)},
			Cookies: auth.CookiesFor(cfg.Server.PublicURL), TrustedProxies: proxies, Log: log,
		},
	}
	if repoSvc != nil {
		deps.Repos = repoSvc
		var rj releases.Journal
		if journal != nil && journal.Journal != nil {
			rj = journal.Journal
		}
		relSvc := releases.New(releases.Options{Pool: pool, Repos: repoSvc, Git: gitTier, Journal: rj, Storage: storage, Bus: bus, Log: log})
		deps.Releases = relSvc
		deps.Git = git.HTTP
		deps.Tiering = tiering.Admin
		deps.Hydration = tiering.Hydrator
		browse, err := setupBrowse(cfg, pool, gitTier, journal, log)
		if err != nil {
			return err
		}
		defer browse.Close()
		deps.Browse = browse
		issueSvc := issues.New(issues.Options{Pool: pool, Repos: repoSvc, Browse: browse, Storage: storage, Bus: bus,
			MaxAttachmentSize: issues.DefaultMaxAttachmentSize, Log: log.With("component", "issues")})
		bus.Register("issues.autoclose", issueSvc.PushHandler(), events.KindPush)
		deps.Issues = issueSvc

		// Pull requests, reviews and forks.
		pullOpts := pulls.Options{Pool: pool, Repos: repoSvc, Browse: browse, Tier: gitTier, Issues: issueSvc, Bus: bus,
			Log: log.With("component", "pulls")}
		if journal != nil {
			pullOpts.Hooks, pullOpts.Protection = journal.Hooks, journal.Protection
		}
		pullSvc := pulls.New(pullOpts)
		// Deleting a repository closes the pull requests whose head lived
		// there and drops the pool of an empty fork network.
		repoSvc.OnDelete(pullSvc)
		bus.Register("pulls.sync", pullSvc.PushHandler(), events.KindPush)
		// Commit statuses and checks: the required status checks of branch
		// protection, and a new status or check re-evaluates auto-merge.
		statusSvc := statuses.New(statuses.Options{Pool: pool, Repos: repoSvc, Browse: browse, Bus: bus,
			Log: log.With("component", "statuses")})
		bus.Register("pulls.automerge", pullSvc.AutoMergeHandler(), events.KindStatus, events.KindCheckRun,
			events.KindCheckSuite, events.KindPullReview)
		if journal != nil {
			journal.Protection.SetSources(pullSvc, statusSvc)
		}
		deps.Pulls = pullSvc
		deps.Statuses = statusSvc
		if actionsSvc != nil {
			wireActions(ctx, actionsSvc, actions.Deps{Repos: repoSvc, Browse: browse, Statuses: statusSvc,
				Payloads: hooks, Jobs: runner, Bus: bus}, bus)
			deps.ActionsRunner = actionsSvc.RunnerHandler()
			deps.Actions = actionsSvc
			deps.ActionsResults = actionsResults
		}

		hooks.SetRepos(repoSvc)
		notifier.SetRepos(repoSvc)
		bus.Register("webhooks.deliver", hooks.EventHandler(), webhooks.EventKinds...)
		bus.Register("notify", notifier.EventHandler(), notify.EventKinds...)
		deps.Webhooks, deps.Notify = hooks, notifier
		if journal != nil {
			journal.Protection.SetRepos(repoSvc)
			deps.Protection = journal.Protection
			journal.GitHub.SetRepos(repoSvc)
			bus.Register("ghsync.push", journal.GitHub.EventHandler(), events.KindPush)
			// GitHub import of the non-git data (labels, milestones, issues,
			// pull requests, releases).
			journal.GitHub.SetImporters(append(issueSvc.GitHubImporters(),
				pullSvc.GitHubImporter(issueSvc.ImportGitHubComments), relSvc.GitHubImporter())...)
			deps.GitHub = journal.GitHub
		}
	}
	handler := httpapi.NewRouter(deps)

	if cfg.Server.PluginSocket != "" {
		popt := pluginapi.Options{Config: cfg, Version: info.Version, Commit: info.Commit,
			Storage: storage, Runners: dbgen.New(pool), Logger: log}
		if cfg.File != "" {
			popt.File = pluginapi.FileConfig(cfg.File)
		}
		plugin, err := pluginapi.New(popt)
		if err != nil {
			return err
		}
		go func() {
			if err := plugin.ListenAndServe(ctx, cfg.Server.PluginSocket); err != nil {
				log.Error("plugin API", "error", err)
			}
		}()
	}

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
