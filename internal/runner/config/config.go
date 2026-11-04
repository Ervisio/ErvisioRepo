// Package config loads the ervisio-runner configuration from a TOML file.
package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DefaultPath is where the packaged runner looks for its configuration.
const DefaultPath = "/etc/ervisio-runner/config.toml"

// DefaultStateFile holds the runner credential written by "register".
const DefaultStateFile = "/var/lib/ervisio-runner/.runner"

// Config is the full runner configuration.
type Config struct {
	Server    Server    `toml:"server"`
	Runner    Runner    `toml:"runner"`
	Log       Log       `toml:"log"`
	Container Container `toml:"container"`
	Host      Host      `toml:"host"`

	// LabelsSet is true when the file sets runner.labels; otherwise the
	// daemon declares the labels saved by "register".
	LabelsSet bool `toml:"-"`
}

// Server describes how the runner reaches ErvisioRepo.
type Server struct {
	// URL is the public URL of the server; empty means the URL stored by
	// "register" in the state file.
	URL string `toml:"url"`
	// InsecureTLS skips TLS certificate verification (self-signed test
	// instances only).
	InsecureTLS bool `toml:"insecure_tls"`
}

// Runner holds the scheduling and reporting knobs.
type Runner struct {
	StateFile string `toml:"state_file"`
	// Capacity is the number of jobs run concurrently.
	Capacity int `toml:"capacity"`
	// Labels map runs-on names to an execution backend:
	// "name=docker://image" or "name=host".
	Labels  []string          `toml:"labels"`
	Env     map[string]string `toml:"env"`
	EnvFile string            `toml:"env_file"`

	FetchTimeout      Duration `toml:"fetch_timeout"`
	FetchInterval     Duration `toml:"fetch_interval"`
	FetchIntervalMax  Duration `toml:"fetch_interval_max"`
	HeartbeatInterval Duration `toml:"heartbeat_interval"`
	ShutdownTimeout   Duration `toml:"shutdown_timeout"`
	// JobTimeout caps a job when the task carries no timeout.
	JobTimeout Duration `toml:"job_timeout"`
	// CacheDir keeps the clones of remote actions between jobs.
	CacheDir string `toml:"cache_dir"`

	LogReportInterval   Duration `toml:"log_report_interval"`
	LogReportMaxLatency Duration `toml:"log_report_max_latency"`
	LogReportBatchSize  int      `toml:"log_report_batch_size"`
	StateReportInterval Duration `toml:"state_report_interval"`
	ReportCloseTimeout  Duration `toml:"report_close_timeout"`
}

// Log configures the daemon log on stderr.
type Log struct {
	Level  string `toml:"level"`  // debug | info | warn | error
	Format string `toml:"format"` // json | text
}

// Container configures the docker backend.
type Container struct {
	// DockerHost overrides DOCKER_HOST, e.g. "unix:///var/run/docker.sock".
	DockerHost string `toml:"docker_host"`
	// Network is the docker network of job containers; empty creates a
	// dedicated network per job.
	Network    string `toml:"network"`
	Privileged bool   `toml:"privileged"`
	// CPUs, Memory and PidsLimit are the cgroup limits of the job
	// container ("" or 0 = unlimited).
	CPUs            string `toml:"cpus"`
	Memory          string `toml:"memory"`
	PidsLimit       int64  `toml:"pids_limit"`
	NoNewPrivileges bool   `toml:"no_new_privileges"`
	// ValidVolumes are the volumes and bind mounts jobs may mount
	// (glob patterns).
	ValidVolumes  []string `toml:"valid_volumes"`
	WorkdirParent string   `toml:"workdir_parent"`
	ForcePull     bool     `toml:"force_pull"`
	ForceRebuild  bool     `toml:"force_rebuild"`
}

// Host configures the host (self-hosted) backend.
type Host struct {
	// Enabled allows "name=host" labels: jobs then run directly on this
	// machine without isolation.
	Enabled       bool   `toml:"enabled"`
	WorkdirParent string `toml:"workdir_parent"`
}

// Default returns the configuration used for missing keys.
func Default() Config {
	return Config{
		Runner: Runner{
			StateFile: DefaultStateFile,
			Capacity:  1,
			Labels: []string{
				"ubuntu-latest=docker://ghcr.io/catthehacker/ubuntu:act-24.04",
				"ubuntu-24.04=docker://ghcr.io/catthehacker/ubuntu:act-24.04",
				"ubuntu-22.04=docker://ghcr.io/catthehacker/ubuntu:act-22.04",
			},
			FetchTimeout:        Duration(40 * time.Second),
			FetchInterval:       Duration(2 * time.Second),
			FetchIntervalMax:    Duration(30 * time.Second),
			HeartbeatInterval:   Duration(30 * time.Second),
			ShutdownTimeout:     Duration(10 * time.Minute),
			JobTimeout:          Duration(6 * time.Hour),
			CacheDir:            "/var/lib/ervisio-runner/cache",
			LogReportInterval:   Duration(5 * time.Second),
			LogReportMaxLatency: Duration(2 * time.Second),
			LogReportBatchSize:  100,
			StateReportInterval: Duration(5 * time.Second),
			ReportCloseTimeout:  Duration(10 * time.Second),
		},
		Log: Log{Level: "info", Format: "json"},
		Container: Container{
			CPUs:            "2",
			Memory:          "4g",
			PidsLimit:       4096,
			NoNewPrivileges: true,
			WorkdirParent:   "workspace",
		},
		Host: Host{WorkdirParent: "/var/lib/ervisio-runner/host"},
	}
}

// Load reads and validates the file at path. A missing file at the
// default path yields the defaults, so "daemon" works right after
// "register".
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == DefaultPath {
			data = nil
		} else {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes TOML strictly (unknown keys are errors), reads env_file
// and validates. All problems found are reported together.
func Parse(data []byte) (*Config, error) {
	cfg := Default()
	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&cfg)
	if err != nil {
		return nil, fmt.Errorf("parse toml: %w", err)
	}
	cfg.LabelsSet = md.IsDefined("runner", "labels")
	var errs []error
	for _, key := range md.Undecoded() {
		errs = append(errs, fmt.Errorf("%s: unknown key", key.String()))
	}
	if cfg.Runner.EnvFile != "" {
		env, err := readEnvFile(cfg.Runner.EnvFile)
		if err != nil {
			errs = append(errs, fmt.Errorf("runner.env_file: %w", err))
		}
		if cfg.Runner.Env == nil {
			cfg.Runner.Env = map[string]string{}
		}
		for k, v := range env {
			if _, ok := cfg.Runner.Env[k]; !ok {
				cfg.Runner.Env[k] = v
			}
		}
	}
	errs = append(errs, cfg.validate()...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &cfg, nil
}

var (
	cpusRe   = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	memoryRe = regexp.MustCompile(`^[0-9]+[bkmgBKMG]?$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// networkRe accepts docker network names; "host" is refused below.
	networkRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

func (c *Config) validate() []error {
	var errs []error
	add := func(path, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
	}
	if c.Server.URL != "" {
		if err := CheckURL(c.Server.URL); err != nil {
			add("server.url", "%v", err)
		}
	}
	if c.Runner.StateFile == "" {
		add("runner.state_file", "must not be empty")
	}
	if c.Runner.Capacity < 1 || c.Runner.Capacity > 64 {
		add("runner.capacity", "must be between 1 and 64")
	}
	if len(c.Runner.Labels) == 0 {
		add("runner.labels", "at least one label is required")
	}
	if _, err := c.CheckLabels(c.Runner.Labels); err != nil {
		add("runner.labels", "%v", err)
	}
	for k := range c.Runner.Env {
		if !envKeyRe.MatchString(k) {
			add("runner.env", "invalid variable name %q", k)
		}
	}
	for path, d := range map[string]Duration{
		"runner.fetch_timeout":          c.Runner.FetchTimeout,
		"runner.fetch_interval":         c.Runner.FetchInterval,
		"runner.fetch_interval_max":     c.Runner.FetchIntervalMax,
		"runner.heartbeat_interval":     c.Runner.HeartbeatInterval,
		"runner.shutdown_timeout":       c.Runner.ShutdownTimeout,
		"runner.job_timeout":            c.Runner.JobTimeout,
		"runner.log_report_interval":    c.Runner.LogReportInterval,
		"runner.log_report_max_latency": c.Runner.LogReportMaxLatency,
		"runner.state_report_interval":  c.Runner.StateReportInterval,
		"runner.report_close_timeout":   c.Runner.ReportCloseTimeout,
	} {
		if d <= 0 {
			add(path, "must be a positive duration")
		}
	}
	if c.Runner.FetchIntervalMax < c.Runner.FetchInterval {
		add("runner.fetch_interval_max", "must not be lower than fetch_interval")
	}
	if c.Runner.LogReportBatchSize < 1 {
		add("runner.log_report_batch_size", "must be at least 1")
	}
	if c.Runner.CacheDir == "" {
		add("runner.cache_dir", "must not be empty")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		add("log.level", "must be one of debug, info, warn, error")
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		add("log.format", "must be json or text")
	}
	if c.Container.CPUs != "" && !cpusRe.MatchString(c.Container.CPUs) {
		add("container.cpus", "must be a decimal number such as \"2\" or \"1.5\"")
	}
	if c.Container.Memory != "" && !memoryRe.MatchString(c.Container.Memory) {
		add("container.memory", "must be a docker size such as \"4g\" or \"512m\"")
	}
	if c.Container.PidsLimit < 0 {
		add("container.pids_limit", "must be 0 (unlimited) or positive")
	}
	if n := c.Container.Network; n != "" {
		if !networkRe.MatchString(n) {
			add("container.network", "invalid network name %q", n)
		} else if n == "host" {
			add("container.network", "the host network is not allowed")
		}
	}
	if c.Container.Privileged {
		add("container.privileged", "privileged job containers are not supported")
	}
	wp := strings.Trim(c.Container.WorkdirParent, "/")
	if wp == "" || strings.Contains(wp, "..") {
		add("container.workdir_parent", "must be a non-empty path without \"..\"")
	}
	if c.Host.Enabled && c.Host.WorkdirParent == "" {
		add("host.workdir_parent", "must not be empty when the host backend is enabled")
	}
	return errs
}

// CheckLabels parses list and refuses host labels unless the host
// backend is enabled.
func (c *Config) CheckLabels(list []string) (Labels, error) {
	ls, err := ParseLabels(list)
	if err != nil {
		return nil, err
	}
	if !c.Host.Enabled {
		for _, l := range ls {
			if l.Scheme == SchemeHost {
				return nil, fmt.Errorf("label %q uses the host backend, set host.enabled = true to allow it", l.Name)
			}
		}
	}
	return ls, nil
}

// CheckURL validates a server URL.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("must be an absolute http(s) URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("must not contain a query or a fragment")
	}
	return nil
}

// DockerOptions renders the cgroup limits and security options as the
// "docker run" flags act merges into the job container.
func (c Container) DockerOptions() string {
	var parts []string
	if c.CPUs != "" && c.CPUs != "0" {
		parts = append(parts, "--cpus="+c.CPUs)
	}
	if c.Memory != "" && c.Memory != "0" {
		parts = append(parts, "--memory="+c.Memory)
	}
	if c.PidsLimit > 0 {
		parts = append(parts, "--pids-limit="+strconv.FormatInt(c.PidsLimit, 10))
	}
	if c.NoNewPrivileges {
		parts = append(parts, "--security-opt=no-new-privileges")
	}
	return strings.Join(parts, " ")
}

// readEnvFile reads KEY=VALUE lines; blank lines and "#" comments are
// skipped, an optional "export " prefix and surrounding quotes are removed.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !envKeyRe.MatchString(k) {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		env[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return env, nil
}

// Duration is a time.Duration written as "30s", "10m" or "6h".
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", b, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }
