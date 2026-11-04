package config

import (
	"fmt"
	"maps"
	"net"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// PolicyTypes are the data types a storage policy can be declared for.
var PolicyTypes = []string{"git", "lfs", "releases", "registry", "actions", "backup"}

var targetName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type validator struct{ errs []error }

func (v *validator) add(path, format string, args ...any) {
	v.errs = append(v.errs, &FieldError{Path: path, Msg: fmt.Sprintf(format, args...)})
}

func (v *validator) oneOf(path, value string, allowed ...string) {
	if !slices.Contains(allowed, value) {
		v.add(path, "must be one of %s, got %q", strings.Join(allowed, ", "), value)
	}
}

func (v *validator) httpURL(path, value string) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		v.add(path, "must be an absolute http(s) URL")
	}
}

func (v *validator) hostPort(path, value string) {
	if _, port, err := net.SplitHostPort(value); err != nil || port == "" {
		v.add(path, "must be host:port, got %q", value)
	}
}

func (v *validator) required(path, value string) bool {
	if strings.TrimSpace(value) == "" {
		v.add(path, "is required")
		return false
	}
	return true
}

func (v *validator) absPath(path, value string) {
	if v.required(path, value) && !filepath.IsAbs(value) {
		v.add(path, "must be an absolute path")
	}
}

func (c *Config) validate() []error {
	v := &validator{}

	if v.required("server.public_url", c.Server.PublicURL) {
		v.httpURL("server.public_url", c.Server.PublicURL)
	}
	v.hostPort("server.listen", c.Server.Listen)
	v.hostPort("server.ssh_listen", c.Server.SSHListen)
	if c.Server.PluginSocket != "" && !filepath.IsAbs(c.Server.PluginSocket) {
		v.add("server.plugin_socket", "must be an absolute path")
	}

	if v.required("database.url", c.Database.URL) {
		if _, err := pgconn.ParseConfig(c.Database.URL); err != nil {
			// The pgconn error may echo the URL, which can contain a password.
			v.add("database.url", "is not a valid PostgreSQL connection string")
		}
	}
	if c.Database.MaxConns < 1 || c.Database.MaxConns > 1000 {
		v.add("database.max_conns", "must be between 1 and 1000, got %d", c.Database.MaxConns)
	}

	v.required("security.master_key_file", c.Security.MasterKeyFile)
	v.oneOf("security.registration", c.Security.Registration,
		RegistrationOpen, RegistrationInvite, RegistrationClosed)
	v.oneOf("security.user_directory", c.Security.UserDirectory, UserDirectoryShared, UserDirectoryInstance)

	v.oneOf("actions.default_actions_url", c.Actions.DefaultActionsURL, "github", "mirror-only")
	if c.Actions.Enabled {
		v.actions(&c.Actions)
	}
	v.oneOf("log.level", c.Log.Level, "debug", "info", "warn", "error")
	v.oneOf("log.format", c.Log.Format, "json", "text")

	for i, cidr := range c.Server.TrustedProxies {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			v.add(fmt.Sprintf("server.trusted_proxies[%d]", i), "must be a CIDR such as 127.0.0.1/32, got %q", cidr)
		}
	}

	v.storage(&c.Storage)
	v.auth(&c.Auth)
	v.mail(&c.Mail)
	v.git(&c.Git)
	v.ssh(&c.SSH)
	v.webhooks(&c.Webhooks)
	return v.errs
}

func (v *validator) storage(s *Storage) {
	kinds := map[string]string{} // target name -> kind
	for i, t := range s.Targets {
		p := fmt.Sprintf("storage.target[%d]", i)
		switch {
		case !targetName.MatchString(t.Name):
			v.add(p+".name", "must match %s, got %q", targetName, t.Name)
		case kinds[t.Name] != "":
			v.add(p+".name", "duplicate target name %q", t.Name)
		default:
			kinds[t.Name] = t.Kind
		}
		v.target(p, &t)
	}

	for _, typ := range slices.Sorted(maps.Keys(s.Policy)) {
		pol := s.Policy[typ]
		p := "storage.policy." + typ
		if !slices.Contains(PolicyTypes, typ) {
			v.add(p, "unknown policy type, expected one of %s", strings.Join(PolicyTypes, ", "))
			continue
		}
		if v.required(p+".primary", pol.Primary) {
			v.targetRef(p+".primary", pol.Primary, kinds)
		}
		seen := map[string]bool{pol.Primary: true}
		for j, r := range pol.Replicas {
			rp := fmt.Sprintf("%s.replicas[%d]", p, j)
			if seen[r] {
				v.add(rp, "target %q is already used by this policy", r)
				continue
			}
			seen[r] = true
			v.targetRef(rp, r, kinds)
		}
		if pol.Cache != "" && v.targetRef(p+".cache", pol.Cache, kinds) && kinds[pol.Cache] != "local" {
			v.add(p+".cache", "target %q must be of kind local", pol.Cache)
		}
		if pol.CacheShare < 0 || pol.CacheShare > 1 {
			v.add(p+".cache_share", "must be between 0 and 1, got %g", pol.CacheShare)
		}
		v.oneOf(p+".durability", pol.Durability, "sync", "async")
	}
	v.hotPath(s)
}

// hotPath checks storage.hot_path. The hot repositories must live outside
// every blob namespace of a local target, or the cache loader would treat
// them as unregistered objects and delete them.
func (v *validator) hotPath(s *Storage) {
	if s.HotPath == "" {
		if _, ok := s.Policy["git"]; ok {
			v.add("storage.hot_path", "is required when the git policy has no local cache or primary")
		}
		return
	}
	if !filepath.IsAbs(s.HotPath) {
		v.add("storage.hot_path", "must be an absolute path")
		return
	}
	hot := filepath.Clean(s.HotPath)
	for _, t := range s.Targets {
		if t.Kind != "local" || !filepath.IsAbs(t.Path) {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(t.Path), hot)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue // outside the target
		}
		if rel == "." || !strings.HasPrefix(rel, ".") {
			v.add("storage.hot_path", "must not be inside the blob namespace of target %q (use a directory starting with '.' or a separate path)", t.Name)
		}
	}
}

func (v *validator) targetRef(path, name string, kinds map[string]string) bool {
	if _, ok := kinds[name]; !ok {
		v.add(path, "unknown target %q", name)
		return false
	}
	return true
}

func (v *validator) target(p string, t *Target) {
	switch t.Kind {
	case "local":
		v.absPath(p+".path", t.Path)
		if !(0 < t.LowWatermark && t.LowWatermark < t.HighWatermark && t.HighWatermark <= 1) {
			v.add(p, "watermarks must satisfy 0 < low_watermark < high_watermark <= 1, got low=%g high=%g",
				t.LowWatermark, t.HighWatermark)
		}
	case "s3":
		if v.required(p+".endpoint", t.Endpoint) {
			v.httpURL(p+".endpoint", t.Endpoint)
		}
		v.required(p+".bucket", t.Bucket)
		if t.Prefix != "" && !strings.HasSuffix(t.Prefix, "/") {
			v.add(p+".prefix", "must end with '/', got %q", t.Prefix)
		}
		if t.CredentialsFile != "" && !filepath.IsAbs(t.CredentialsFile) {
			v.add(p+".credentials_file", "must be an absolute path")
		}
	case "sftp":
		v.required(p+".host", t.Host)
		v.required(p+".user", t.User)
		v.absPath(p+".key_file", t.KeyFile)
		v.absPath(p+".known_hosts_file", t.KnownHostsFile)
		if t.Port < 1 || t.Port > 65535 {
			v.add(p+".port", "must be between 1 and 65535, got %d", t.Port)
		}
		if t.MaxConns < 1 {
			v.add(p+".max_conns", "must be at least 1, got %d", t.MaxConns)
		}
	case "webdav":
		if v.required(p+".url", t.URL) {
			v.httpURL(p+".url", t.URL)
		}
		v.required(p+".user", t.User)
		v.absPath(p+".password_file", t.PasswordFile)
	default:
		v.add(p+".kind", "must be one of local, s3, sftp, webdav, got %q", t.Kind)
	}
}

func (v *validator) intRange(path string, n, lo, hi int) {
	if n < lo || n > hi {
		v.add(path, "must be between %d and %d, got %d", lo, hi, n)
	}
}

func (v *validator) positive(path string, d Duration) {
	if d <= 0 {
		v.add(path, "must be a positive duration")
	}
}

func (v *validator) actions(a *Actions) {
	if a.MaxJobTimeout < Duration(time.Minute) || a.MaxJobTimeout > Duration(72*time.Hour) {
		v.add("actions.max_job_timeout", "must be between 1m and 72h")
	}
	if a.RunnerOfflineAfter < Duration(40*time.Second) || a.RunnerOfflineAfter > Duration(time.Hour) {
		v.add("actions.runner_offline_after", "must be between 40s and 1h")
	}
	if a.RegistrationTokenTTL < Duration(time.Minute) || a.RegistrationTokenTTL > Duration(30*24*time.Hour) {
		v.add("actions.registration_token_ttl", "must be between 1m and 720h")
	}
	v.intRange("actions.max_jobs_per_run", a.MaxJobsPerRun, 1, 10000)
	if a.CacheMaxRepoSize < 1<<20 {
		v.add("actions.cache_max_repo_size", "must be at least 1MiB")
	}
	if a.CacheTTL < Duration(time.Hour) {
		v.add("actions.cache_ttl", "must be at least 1h")
	}
	if a.ArtifactRetention < Duration(24*time.Hour) || a.ArtifactRetention > Duration(400*24*time.Hour) {
		v.add("actions.artifact_retention", "must be between 1d and 400d")
	}
}

func (v *validator) auth(a *Auth) {
	if a.Argon2Memory < 8<<10 || a.Argon2Memory > 4<<30 {
		v.add("auth.argon2_memory", "must be between 8KiB and 4GiB, got %s", a.Argon2Memory)
	}
	v.intRange("auth.argon2_iterations", a.Argon2Iterations, 1, 100)
	v.intRange("auth.argon2_parallelism", a.Argon2Parallelism, 1, 255)
	v.intRange("auth.argon2_max_concurrent", a.Argon2MaxConcurrent, 1, 1024)
	v.intRange("auth.password_min_length", a.PasswordMinLength, 8, 128)
	v.positive("auth.session_idle_timeout", a.SessionIdleTimeout)
	v.positive("auth.session_max_age", a.SessionMaxAge)
	if a.SessionIdleTimeout > a.SessionMaxAge {
		v.add("auth.session_idle_timeout", "must not exceed auth.session_max_age")
	}
	v.intRange("auth.login_free_failures_account", a.LoginFreeFailuresAccount, 1, 1000)
	v.intRange("auth.login_free_failures_ip", a.LoginFreeFailuresIP, 1, 100000)
	v.positive("auth.login_backoff_base", a.LoginBackoffBase)
	if a.LoginBackoffMax < a.LoginBackoffBase {
		v.add("auth.login_backoff_max", "must be at least auth.login_backoff_base")
	}
	v.intRange("auth.actions_per_ip_per_hour", a.ActionsPerIPPerHour, 1, 1000000)
	v.positive("auth.verify_token_ttl", a.VerifyTokenTTL)
	v.positive("auth.reset_token_ttl", a.ResetTokenTTL)
	v.positive("auth.invite_ttl", a.InviteTTL)
	v.oneOf("auth.require_2fa", a.Require2FA, Require2FAOff, Require2FAAdmins, Require2FAAll)
	if a.TokenMaxTTL <= 0 {
		v.add("auth.token_max_ttl", "must be positive (personal access tokens always expire)")
	}
	v.oidc(a.OIDC)
}

func (v *validator) mail(m *Mail) {
	v.oneOf("mail.tls", m.TLS, "starttls", "tls", "none")
	v.positive("mail.timeout", m.Timeout)
	if !m.Enabled {
		return
	}
	v.required("mail.host", m.Host)
	v.intRange("mail.port", m.Port, 1, 65535)
	if v.required("mail.from", m.From) {
		if _, err := mailAddress(m.From); err != nil {
			v.add("mail.from", "must be an email address such as \"Ervisio Repo <git@example.com>\"")
		}
	}
	if m.PasswordFile != "" && !filepath.IsAbs(m.PasswordFile) {
		v.add("mail.password_file", "must be an absolute path")
	}
	if m.PasswordFile != "" && m.Username == "" {
		v.add("mail.username", "is required when mail.password_file is set")
	}
}

func (v *validator) webhooks(w *Webhooks) {
	v.positive("webhooks.timeout", w.Timeout)
	if w.Timeout > Duration(5*time.Minute) {
		v.add("webhooks.timeout", "must not exceed 5m")
	}
	v.intRange("webhooks.max_attempts", w.MaxAttempts, 1, 25)
	for i, cidr := range w.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			v.add(fmt.Sprintf("webhooks.allowed_cidrs[%d]", i), "must be a CIDR such as 10.0.0.0/8, got %q", cidr)
		}
	}
}

func mailAddress(s string) (*mail.Address, error) { return mail.ParseAddress(s) }
