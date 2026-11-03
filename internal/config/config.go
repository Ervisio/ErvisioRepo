// Package config loads the server configuration from a TOML file, applies
// EREPO_* environment overrides and validates the result.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config is the full server configuration.
type Config struct {
	Server   Server   `toml:"server"`
	Database Database `toml:"database"`
	Security Security `toml:"security"`
	Actions  Actions  `toml:"actions"`
	Log      Log      `toml:"log"`
	Storage  Storage  `toml:"storage"`
}

type Server struct {
	PublicURL    string `toml:"public_url"`
	Listen       string `toml:"listen"`
	SSHListen    string `toml:"ssh_listen"`
	PluginSocket string `toml:"plugin_socket"` // empty disables the plugin API
}

type Database struct {
	URL      string `toml:"url"`
	MaxConns int32  `toml:"max_conns"`
}

// Registration modes.
const (
	RegistrationOpen   = "open"
	RegistrationInvite = "invite"
	RegistrationClosed = "closed"
)

type Security struct {
	MasterKeyFile string `toml:"master_key_file"`
	Registration  string `toml:"registration"`
}

type Actions struct {
	Enabled           bool   `toml:"enabled"`
	DefaultActionsURL string `toml:"default_actions_url"` // github | mirror-only
}

type Log struct {
	Level  string `toml:"level"`  // debug | info | warn | error
	Format string `toml:"format"` // json | text
}

// Storage describes the physical targets and, for each data type, the policy
// that places it on them (PLAN §4.2).
type Storage struct {
	Targets []Target          `toml:"target"`
	Policy  map[string]Policy `toml:"policy"`
}

// Target is a physical storage. Which fields apply depends on Kind.
type Target struct {
	Name    string `toml:"name"`
	Kind    string `toml:"kind"` // local | s3 | sftp | webdav
	Encrypt bool   `toml:"encrypt"`

	// local
	Path          string  `toml:"path"`
	Budget        Size    `toml:"budget"`
	HighWatermark float64 `toml:"high_watermark"`
	LowWatermark  float64 `toml:"low_watermark"`

	// s3
	Endpoint        string `toml:"endpoint"`
	Bucket          string `toml:"bucket"`
	Region          string `toml:"region"`
	PathStyle       bool   `toml:"path_style"`
	CredentialsFile string `toml:"credentials_file"`
	// Prefix is an optional key prefix inside the bucket, ending with '/'.
	Prefix string `toml:"prefix"`

	// sftp and webdav
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	User     string `toml:"user"`
	KeyFile  string `toml:"key_file"`
	MaxConns int    `toml:"max_conns"`
	// KnownHostsFile verifies the SFTP server key (OpenSSH known_hosts format).
	KnownHostsFile string `toml:"known_hosts_file"`
	// Root is the remote SFTP directory holding the objects (default: login directory).
	Root         string `toml:"root"`
	URL          string `toml:"url"`
	PasswordFile string `toml:"password_file"`
}

// Policy says where one data type lives.
type Policy struct {
	Primary    string   `toml:"primary"`
	Replicas   []string `toml:"replicas"`
	Cache      string   `toml:"cache"`
	CacheTTL   Duration `toml:"cache_ttl"`
	CacheShare float64  `toml:"cache_share"`
	Durability string   `toml:"durability"` // sync | async
	Retention  Duration `toml:"retention"`
}

// Default returns the configuration used for every key the file leaves out.
func Default() Config {
	return Config{
		Server: Server{
			Listen:       "127.0.0.1:3400",
			SSHListen:    ":2222",
			PluginSocket: "/run/ervisio-repo/api.sock",
		},
		Database: Database{MaxConns: 10},
		Security: Security{
			MasterKeyFile: "/etc/ervisio-repo/master.key",
			Registration:  RegistrationInvite,
		},
		Actions: Actions{Enabled: true, DefaultActionsURL: "github"},
		Log:     Log{Level: "info", Format: "json"},
	}
}

// Load reads the file at path, applies environment overrides and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data, os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes TOML strictly (unknown keys are errors), applies overrides
// from lookupEnv and validates. All problems found are reported together.
func Parse(data []byte, lookupEnv func(string) (string, bool)) (*Config, error) {
	cfg := Default()
	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&cfg)
	if err != nil {
		return nil, fmt.Errorf("parse toml: %w", err)
	}
	var errs []error
	for _, key := range md.Undecoded() {
		errs = append(errs, &FieldError{Path: key.String(), Msg: "unknown key"})
	}
	if lookupEnv != nil {
		errs = append(errs, applyEnv(&cfg, lookupEnv)...)
	}
	applyStorageDefaults(&cfg.Storage)
	errs = append(errs, cfg.validate()...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &cfg, nil
}

// FieldError is a problem with a single configuration key.
type FieldError struct {
	Path string // e.g. "server.public_url" or "env EREPO_DATABASE_MAX_CONNS"
	Msg  string
}

func (e *FieldError) Error() string { return e.Path + ": " + e.Msg }

func applyStorageDefaults(s *Storage) {
	for i := range s.Targets {
		t := &s.Targets[i]
		switch t.Kind {
		case "local":
			if t.HighWatermark == 0 {
				t.HighWatermark = 0.90
			}
			if t.LowWatermark == 0 {
				t.LowWatermark = 0.75
			}
		case "sftp":
			if t.Port == 0 {
				t.Port = 22
			}
			if t.MaxConns == 0 {
				t.MaxConns = 4
			}
		}
	}
	for name, p := range s.Policy {
		if p.Durability == "" {
			p.Durability = "sync"
			s.Policy[name] = p
		}
	}
}
