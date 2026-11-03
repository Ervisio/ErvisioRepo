// Package githooks runs the server side of the git hooks of a push
// (PLAN §4.3). The server installs thin hook scripts that exec
// "ervisio-repo hook <name>"; that process forwards the hook to the server
// over a unix socket, authenticated by a per-push token carried in the git
// environment, and relays the answer to the client.
//
// Transports call Server.Env (or Server.Begin) for every git process that
// may update refs (receive-pack, server-side writes) and pass the returned
// variables to git; without them the pre-receive hook refuses the push.
package githooks

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// Environment variables carried from the server to the hook process.
const (
	EnvSocket = "EREPO_HOOK_SOCKET"
	EnvToken  = "EREPO_HOOK_TOKEN" //nolint:gosec // variable name, not a credential
)

// Hook names served by the socket.
const (
	HookPreReceive           = "pre-receive"
	HookReferenceTransaction = "reference-transaction"
)

// Reference transaction states (githooks(5)).
const (
	TxPrepared  = "prepared"
	TxCommitted = "committed"
	TxAborted   = "aborted"
)

// maxStdin bounds the ref update lines a hook forwards.
const maxStdin = 32 << 20

// ZeroOID is the object ID git uses for a missing ref side.
func ZeroOID(oid string) bool { return strings.Trim(oid, "0") == "" }

// RefUpdate is one line of the hook input: "<old> <new> <ref>".
type RefUpdate struct {
	Old string `json:"old"`
	New string `json:"new"`
	Ref string `json:"ref"`
}

// ParseUpdates parses the hook input.
func ParseUpdates(s string) ([]RefUpdate, error) {
	var out []RefUpdate
	for line := range strings.Lines(s) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("malformed hook input line %q", line)
		}
		for _, oid := range f[:2] {
			if err := git.ValidateObjectID(oid); err != nil {
				return nil, fmt.Errorf("hook input: %w", err)
			}
		}
		// reference-transaction also reports HEAD and pseudo-refs.
		if f[2] != "HEAD" {
			if err := git.ValidateRefName(f[2]); err != nil {
				return nil, fmt.Errorf("hook input: %w", err)
			}
		}
		out = append(out, RefUpdate{Old: f[0], New: f[1], Ref: f[2]})
	}
	return out, nil
}

// request is what the hook process sends, as one JSON document.
type request struct {
	Token      string   `json:"token"`
	Hook       string   `json:"hook"`
	Args       []string `json:"args,omitempty"`
	Stdin      string   `json:"stdin"`
	Quarantine string   `json:"quarantine,omitempty"` // GIT_QUARANTINE_PATH
}

// reply is one NDJSON line of the server answer: progress lines carry
// only Msg, the last one has Done set.
type reply struct {
	Msg  string `json:"m,omitempty"`
	Done bool   `json:"done,omitempty"`
	OK   bool   `json:"ok,omitempty"`
}

// Reject is a refusal shown to the pusher. Handlers return it (wrapped or
// not) to fail a hook with a message; any other error fails it with a
// generic message and is logged.
type Reject struct{ Msg string }

func (r *Reject) Error() string { return r.Msg }

// Rejectf builds a Reject.
func Rejectf(format string, a ...any) error { return &Reject{Msg: fmt.Sprintf(format, a...)} }

func rejectMessage(err error) (string, bool) {
	var r *Reject
	if errors.As(err, &r) {
		return r.Msg, true
	}
	return "", false
}
