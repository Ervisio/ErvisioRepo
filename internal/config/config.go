// Package config loads the server configuration from a TOML file, applies
// EREPO_* environment overrides and validates the result.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

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
	Auth     Auth     `toml:"auth"`
	Mail     Mail     `toml:"mail"`
}

type Server struct {
	PublicURL    string `toml:"public_url"`
	Listen       string `toml:"listen"`
	SSHListen    string `toml:"ssh_listen"`
	PluginSocket string `toml:"plugin_socket"` // empty disables the plugin API
	// TrustedProxies are the CIDRs of reverse proxies (Caddy) whose
	// X-Forwarded-For header is believed when computing the client IP.
	TrustedProxies []string `toml:"trusted_proxies"`
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

// Values of auth.require_2fa.
const (
	Require2FAOff    = "off"
	Require2FAAdmins = "admins"
	Require2FAAll    = "all"
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

// Auth configures local authentication and sessions (PLAN §8.1).
type Auth struct {
	// Argon2id cost parameters for password hashes. Existing hashes made with
	// other parameters are upgraded at the next successful login.
	Argon2Memory      Size `toml:"argon2_memory"`
	Argon2Iterations  int  `toml:"argon2_iterations"`
	Argon2Parallelism int  `toml:"argon2_parallelism"`
	// Argon2MaxConcurrent bounds simultaneous hash computations (memory DoS).
	Argon2MaxConcurrent int `toml:"argon2_max_concurrent"`
	PasswordMinLength   int `toml:"password_min_length"`

	SessionIdleTimeout Duration `toml:"session_idle_timeout"`
	SessionMaxAge      Duration `toml:"session_max_age"`

	// RequireVerifiedEmail refuses login until the address is verified. It
	// only applies when [mail] is enabled.
	RequireVerifiedEmail bool `toml:"require_verified_email"`

	// Brute-force protection: after the free failures each further failure
	// doubles the wait, from LoginBackoffBase up to LoginBackoffMax.
	LoginFreeFailuresAccount int      `toml:"login_free_failures_account"`
	LoginFreeFailuresIP      int      `toml:"login_free_failures_ip"`
	LoginBackoffBase         Duration `toml:"login_backoff_base"`
	LoginBackoffMax          Duration `toml:"login_backoff_max"`
	// ActionsPerIPPerHour limits registrations and mail-sending requests.
	ActionsPerIPPerHour int `toml:"actions_per_ip_per_hour"`

	VerifyTokenTTL Duration `toml:"verify_token_ttl"`
	ResetTokenTTL  Duration `toml:"reset_token_ttl"`
	InviteTTL      Duration `toml:"invite_ttl"`
	// Require2FA makes a second factor mandatory: "off", "admins" or "all".
	// Organizations can also require it of their members.
	Require2FA string `toml:"require_2fa"`

	// TokenMaxTTL bounds the lifetime of personal access tokens. Every
	// token expires (PLAN §8.1), so it must be positive.
	TokenMaxTTL Duration `toml:"token_max_ttl"`
	// OIDC lists the external identity providers ([[auth.oidc]]).
	OIDC []OIDCProvider `toml:"oidc"`
}

// Mail configures outgoing SMTP mail (verification and password reset).
type Mail struct {
	Enabled bool   `toml:"enabled"`
	Host    string `toml:"host"`
	Port    int    `toml:"port"`
	// TLS is "starttls" (required), "tls" (implicit TLS) or "none" (only for
	// local relays such as Mailpit).
	TLS          string `toml:"tls"`
	Username     string `toml:"username"`
	PasswordFile string `toml:"password_file"`
	From         string `toml:"from"`
	// HeloName defaults to the host of server.public_url.
	HeloName string   `toml:"helo_name"`
	Timeout  Duration `toml:"timeout"`
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
		Auth: Auth{
			Argon2Memory:             64 << 20,
			Argon2Iterations:         3,
			Argon2Parallelism:        2,
			Argon2MaxConcurrent:      4,
			PasswordMinLength:        10,
			SessionIdleTimeout:       Duration(14 * 24 * time.Hour),
			SessionMaxAge:            Duration(30 * 24 * time.Hour),
			RequireVerifiedEmail:     true,
			LoginFreeFailuresAccount: 5,
			LoginFreeFailuresIP:      20,
			LoginBackoffBase:         Duration(time.Second),
			LoginBackoffMax:          Duration(15 * time.Minute),
			ActionsPerIPPerHour:      20,
			VerifyTokenTTL:           Duration(48 * time.Hour),
			ResetTokenTTL:            Duration(time.Hour),
			InviteTTL:                Duration(7 * 24 * time.Hour),
			Require2FA:               Require2FAOff,
			TokenMaxTTL:              Duration(366 * 24 * time.Hour),
		},
		Mail: Mail{Port: 587, TLS: "starttls", Timeout: Duration(30 * time.Second)},
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
	applyOIDCDefaults(cfg.Auth.OIDC)
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
