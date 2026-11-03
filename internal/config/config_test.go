package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const minimal = `
[server]
public_url = "https://git.example.com"
[database]
url = "postgres://u:p@localhost:5432/db?sslmode=disable"
`

const fullStorage = minimal + `
[[storage.target]]
name = "hot"
kind = "local"
path = "/var/lib/ervisio-repo/hot"
budget = "50GB"
high_watermark = 0.90
low_watermark  = 0.75

[[storage.target]]
name = "objstore"
kind = "s3"
endpoint = "https://fsn1.your-objectstorage.com"
bucket = "ervisio-repo"
region = "fsn1"
encrypt = true

[[storage.target]]
name = "box"
kind = "sftp"
host = "u1.your-storagebox.de"
port = 23
user = "u1-sub1"
key_file = "/etc/ervisio-repo/storagebox_ed25519"
known_hosts_file = "/etc/ervisio-repo/storagebox_known_hosts"
max_conns = 8
encrypt = true

[storage.policy.git]
primary  = "objstore"
replicas = ["box"]
cache    = "hot"
cache_ttl = "7d"
durability = "sync"

[storage.policy.lfs]
primary = "objstore"
replicas = ["box"]
cache = "hot"
cache_ttl = "7d"
cache_share = 0.25

[storage.policy.actions]
primary = "objstore"
cache_ttl = "1d"
retention = "90d"

[storage.policy.backup]
primary = "box"
replicas = ["objstore"]
`

func noEnv(string) (string, bool) { return "", false }

func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimal), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:3400" || cfg.Security.Registration != "invite" ||
		cfg.Database.MaxConns != 10 || !cfg.Actions.Enabled || cfg.Log.Format != "json" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestParseFullStorage(t *testing.T) {
	cfg, err := Parse([]byte(fullStorage), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	hot := cfg.Storage.Targets[0]
	if hot.Budget != 50*1e9 {
		t.Errorf("budget = %d", hot.Budget)
	}
	git := cfg.Storage.Policy["git"]
	if time.Duration(git.CacheTTL) != 7*24*time.Hour || git.Replicas[0] != "box" {
		t.Errorf("git policy = %+v", git)
	}
	if cfg.Storage.Policy["lfs"].Durability != "sync" {
		t.Error("durability default not applied")
	}
	if d := time.Duration(cfg.Storage.Policy["actions"].Retention); d != 90*24*time.Hour {
		t.Errorf("retention = %v", d)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want []string // every substring must appear in the error
	}{
		{"unknown keys", minimal + "[server.extra]\nfoo = 1\n[log]\nlvl = \"x\"\n",
			[]string{"server.extra.foo: unknown key", "log.lvl: unknown key"}},
		{"unknown target key", minimal + "[[storage.target]]\nname=\"a\"\nkind=\"local\"\npath=\"/a\"\nbogus=1\n",
			[]string{"storage.target.bogus: unknown key"}},
		{"missing required", "", []string{"server.public_url: is required", "database.url: is required"}},
		{"bad values", `
[server]
public_url = "ftp://x"
listen = "nope"
plugin_socket = "rel/sock"
[database]
url = "postgres://localhost/db"
max_conns = 0
[security]
registration = "maybe"
[actions]
default_actions_url = "gitlab"
[log]
level = "trace"
format = "xml"
`, []string{
			"server.public_url: must be an absolute http(s) URL",
			"server.listen: must be host:port",
			"server.plugin_socket: must be an absolute path",
			"database.max_conns: must be between 1 and 1000",
			"security.registration: must be one of open, invite, closed",
			"actions.default_actions_url",
			"log.level", "log.format",
		}},
		{"bad toml", "[server", []string{"parse toml"}},
		{"bad size", minimal + "[[storage.target]]\nname=\"a\"\nkind=\"local\"\npath=\"/a\"\nbudget=\"50XB\"\n",
			[]string{"invalid size"}},
		{"storage refs", minimal + `
[[storage.target]]
name = "hot"
kind = "local"
path = "relative"
low_watermark = 0.95
high_watermark = 0.9
[[storage.target]]
name = "hot"
kind = "s3"
[[storage.target]]
name = "Bad Name"
kind = "ftp"
[[storage.target]]
name = "box"
kind = "sftp"
port = 70000
[[storage.target]]
name = "dav"
kind = "webdav"
url = "nope"
[storage.policy.git]
replicas = ["ghost", "ghost"]
cache = "box"
cache_share = 2.0
durability = "eventually"
[storage.policy.lfs]
primary = "box"
replicas = ["box"]
cache = "missing"
[storage.policy.photos]
primary = "hot"
`, []string{
			"storage.target[0].path: must be an absolute path",
			"storage.target[0]: watermarks must satisfy",
			"storage.target[1].name: duplicate target name \"hot\"",
			"storage.target[1].endpoint: is required",
			"storage.target[1].bucket: is required",
			"storage.target[2].name: must match",
			"storage.target[2].kind: must be one of local, s3, sftp, webdav",
			"storage.target[3].host: is required",
			"storage.target[3].port: must be between 1 and 65535",
			"storage.target[3].key_file: is required",
			"storage.target[3].known_hosts_file: is required",
			"storage.target[4].url: must be an absolute http(s) URL",
			"storage.target[4].password_file: is required",
			"storage.policy.git.primary: is required",
			"storage.policy.git.replicas[0]: unknown target \"ghost\"",
			"storage.policy.git.replicas[1]: target \"ghost\" is already used",
			"storage.policy.git.cache: target \"box\" must be of kind local",
			"storage.policy.git.cache_share: must be between 0 and 1",
			"storage.policy.git.durability",
			"storage.policy.lfs.replicas[0]: target \"box\" is already used",
			"storage.policy.lfs.cache: unknown target \"missing\"",
			"storage.policy.photos: unknown policy type",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.toml), noEnv)
			if err == nil {
				t.Fatal("expected error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error does not contain %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestDatabaseURLErrorHidesPassword(t *testing.T) {
	_, err := Parse([]byte(strings.Replace(minimal, "postgres://u:p@localhost:5432/db?sslmode=disable",
		"postgres://u:s3cret@localhost:5432/db?sslmode=bogus", 1)), noEnv)
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("want an error without the password, got %v", err)
	}
}

func TestEnvOverrides(t *testing.T) {
	cfg, err := Parse([]byte(minimal), envMap(map[string]string{
		"EREPO_SERVER_PUBLIC_URL":      "http://localhost:3400",
		"EREPO_DATABASE_MAX_CONNS":     "25",
		"EREPO_ACTIONS_ENABLED":        "false",
		"EREPO_SECURITY_REGISTRATION":  "open",
		"EREPO_LOG_FORMAT":             "text",
		"EREPO_STORAGE_TARGET":         "ignored",
		"EREPO_DATABASE_URL_UNRELATED": "ignored",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.PublicURL != "http://localhost:3400" || cfg.Database.MaxConns != 25 ||
		cfg.Actions.Enabled || cfg.Security.Registration != "open" || cfg.Log.Format != "text" {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestEnvOverrideErrors(t *testing.T) {
	_, err := Parse([]byte(minimal), envMap(map[string]string{
		"EREPO_DATABASE_MAX_CONNS": "many",
		"EREPO_ACTIONS_ENABLED":    "perhaps",
	}))
	for _, w := range []string{"env EREPO_DATABASE_MAX_CONNS: invalid integer", "env EREPO_ACTIONS_ENABLED: invalid boolean"} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("want %q in %v", w, err)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EREPO_LOG_LEVEL", "debug")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("level = %q", cfg.Log.Level)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestDevConfigIsValid(t *testing.T) {
	if _, err := Load("../../deploy/dev/config.toml"); err != nil {
		t.Fatal(err)
	}
}

func TestHotPath(t *testing.T) {
	cfg, err := Parse([]byte(fullStorage), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.HotPath != "/var/lib/ervisio-repo/hot/.hot" {
		t.Fatalf("default hot path %q", cfg.Storage.HotPath)
	}
	cfg, err = Parse([]byte(fullStorage), envMap(map[string]string{"EREPO_STORAGE_HOT_PATH": "/srv/hot"}))
	if err != nil || cfg.Storage.HotPath != "/srv/hot" {
		t.Fatalf("override %v %v", cfg, err)
	}
	for _, bad := range []string{"relative", "/var/lib/ervisio-repo/hot", "/var/lib/ervisio-repo/hot/git"} {
		_, err := Parse([]byte(fullStorage), envMap(map[string]string{"EREPO_STORAGE_HOT_PATH": bad}))
		if err == nil || !strings.Contains(err.Error(), "storage.hot_path") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	// A git policy without any local target needs an explicit hot path.
	noLocal := minimal + "[[storage.target]]\nname=\"o\"\nkind=\"s3\"\nendpoint=\"https://s3.example.com\"\nbucket=\"b\"\n[storage.policy.git]\nprimary=\"o\"\n"
	if _, err := Parse([]byte(noLocal), noEnv); err == nil || !strings.Contains(err.Error(), "storage.hot_path") {
		t.Fatalf("missing hot path %v", err)
	}
	if _, err := Parse([]byte(noLocal+"[storage]\nhot_path=\"/srv/hot\"\n"), noEnv); err != nil {
		t.Fatal(err)
	}
}
