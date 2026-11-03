package config

import (
	"path/filepath"
	"time"
)

// Git configures the git transports (PLAN §5.1) shared by Smart HTTP and SSH.
type Git struct {
	// MaxPushSize bounds the data received by one push, pack included.
	MaxPushSize Size `toml:"max_push_size"`
	// IdleTimeout ends a transfer that moves no byte in either direction
	// for this long.
	IdleTimeout Duration `toml:"idle_timeout"`
	// MaxDuration bounds a whole transfer, however active.
	MaxDuration Duration `toml:"max_duration"`
	// RequestsPerHour limits git requests per client IP for anonymous
	// callers and per token or account for authenticated ones.
	RequestsPerHour int `toml:"requests_per_hour"`
}

// SSH configures the SSH transport. It listens on server.ssh_listen.
type SSH struct {
	Enabled bool `toml:"enabled"`
	// HostKeyDir holds the ed25519 and RSA host keys, generated at the first
	// start with mode 0600. They belong in the recovery kit.
	HostKeyDir string `toml:"host_key_dir"`
	// MaxConnections bounds concurrent SSH connections.
	MaxConnections int `toml:"max_connections"`
	// LoginGraceTime bounds the handshake and authentication.
	LoginGraceTime Duration `toml:"login_grace_time"`
}

func defaultGit() Git {
	return Git{
		MaxPushSize:     2 << 30,
		IdleTimeout:     Duration(10 * time.Minute),
		MaxDuration:     Duration(6 * time.Hour),
		RequestsPerHour: 5000,
	}
}

func defaultSSH() SSH {
	return SSH{
		Enabled:        true,
		HostKeyDir:     "/var/lib/ervisio-repo/ssh",
		MaxConnections: 256,
		LoginGraceTime: Duration(30 * time.Second),
	}
}

func (v *validator) git(g *Git) {
	if g.MaxPushSize < 1<<20 {
		v.add("git.max_push_size", "must be at least 1MiB, got %s", g.MaxPushSize)
	}
	v.positive("git.idle_timeout", g.IdleTimeout)
	v.positive("git.max_duration", g.MaxDuration)
	if g.IdleTimeout > g.MaxDuration {
		v.add("git.idle_timeout", "must not exceed git.max_duration")
	}
	v.intRange("git.requests_per_hour", g.RequestsPerHour, 1, 10_000_000)
}

func (v *validator) ssh(s *SSH) {
	if !s.Enabled {
		return
	}
	if v.required("ssh.host_key_dir", s.HostKeyDir) && !filepath.IsAbs(s.HostKeyDir) {
		v.add("ssh.host_key_dir", "must be an absolute path")
	}
	v.intRange("ssh.max_connections", s.MaxConnections, 1, 100_000)
	v.positive("ssh.login_grace_time", s.LoginGraceTime)
}
