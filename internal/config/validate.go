package config

import (
	"fmt"
	"maps"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// PolicyTypes are the data types a storage policy can be declared for.
var PolicyTypes = []string{"git", "lfs", "registry", "actions", "backup"}

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

	v.oneOf("actions.default_actions_url", c.Actions.DefaultActionsURL, "github", "mirror-only")
	v.oneOf("log.level", c.Log.Level, "debug", "info", "warn", "error")
	v.oneOf("log.format", c.Log.Format, "json", "text")

	v.storage(&c.Storage)
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
