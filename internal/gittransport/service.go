// Package gittransport runs the git server commands (upload-pack,
// receive-pack, upload-archive) for the Smart HTTP and SSH transports
// (PLAN §5.1). It takes the repository lease from the gitstore tier, builds
// the argv and environment of git, streams stdin and stdout without
// buffering and enforces the push size, idle and duration limits.
// Authentication and authorization are the transports' job.
package gittransport

import (
	"context"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Service is a git server command.
type Service string

// The services a client may ask for.
const (
	UploadPack    Service = "git-upload-pack"
	ReceivePack   Service = "git-receive-pack"
	UploadArchive Service = "git-upload-archive"
)

// ParseService returns the service named s ("git-upload-pack", ...).
func ParseService(s string) (Service, bool) {
	switch Service(s) {
	case UploadPack, ReceivePack, UploadArchive:
		return Service(s), true
	}
	return "", false
}

// Action is the repository permission the service needs.
func (s Service) Action() authz.Action {
	if s == ReceivePack {
		return authz.ActPush
	}
	return authz.ActRead
}

// Scope is the access token scope the service needs.
func (s Service) Scope() string {
	if s == ReceivePack {
		return auth.ScopeWriteRepo
	}
	return auth.ScopeReadRepo
}

// IsWrite reports whether the service updates the repository.
func (s Service) IsWrite() bool { return s == ReceivePack }

// subcommand is the git subcommand ("upload-pack").
func (s Service) subcommand() string { return strings.TrimPrefix(string(s), "git-") }

// Transports, as passed to Hooks.
const (
	TransportHTTP = "http"
	TransportSSH  = "ssh"
)

// Push describes a receive-pack to the hooks.
type Push struct {
	Repo      repos.Repo
	Pusher    auth.Principal
	Transport string // TransportHTTP or TransportSSH
	RemoteIP  string
	// Protocol is the sanitized GIT_PROTOCOL value, possibly empty.
	Protocol string
}

// Hooks prepares the environment of a receive-pack so that the server-side
// hooks (internal/githooks) can identify the push. done, when not nil, is
// called once git has exited, whatever the outcome.
type Hooks interface {
	HookEnv(ctx context.Context, p Push) (env []string, done func(), err error)
}

// leaseMode is the gitstore lease a request needs: advertisements and
// reads take a Read lease, a receive-pack the Write lease.
func leaseMode(s Service, advertise bool) gitstore.Mode {
	if s.IsWrite() && !advertise {
		return gitstore.Write
	}
	return gitstore.Read
}

// maxProtocolLen bounds GIT_PROTOCOL.
const maxProtocolLen = 256

// SanitizeProtocol returns v if it is a plausible GIT_PROTOCOL value
// ("version=2", "version=2:object-format=sha256"), "" otherwise. The value
// reaches the git environment, so it is restricted to a safe alphabet.
func SanitizeProtocol(v string) string {
	if v == "" || len(v) > maxProtocolLen {
		return ""
	}
	for i := range len(v) {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '=' || c == ':' || c == '.' || c == '_' || c == '-':
		default:
			return ""
		}
	}
	return v
}

// IsV2 reports whether a sanitized GIT_PROTOCOL asks for protocol v2.
func IsV2(protocol string) bool {
	for f := range strings.SplitSeq(protocol, ":") {
		if f == "version=2" {
			return true
		}
	}
	return false
}
