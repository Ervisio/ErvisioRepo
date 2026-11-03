package protection

import (
	"context"
	"io"
	"log/slog"

	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
)

// Hooks is a githooks.Handler that enforces the rules and then hands the
// session to Next (the journal), which may be nil.
type Hooks struct {
	Engine *Engine
	Next   githooks.Handler
	Log    *slog.Logger
}

var (
	_ githooks.Handler  = (*Hooks)(nil)
	_ githooks.Preparer = (*Hooks)(nil)
)

func (h *Hooks) logger() *slog.Logger {
	if h.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return h.Log
}

// PreReceive checks a push (SSH or HTTP) whose objects are in quarantine.
func (h *Hooks) PreReceive(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, q githooks.Quarantine, out io.Writer) error {
	if err := h.check(ctx, s, updates, q.Env()); err != nil {
		return err
	}
	if h.Next != nil {
		return h.Next.PreReceive(ctx, s, updates, q, out)
	}
	return nil
}

// Prepared checks the ref updates of git update-ref (web edits, merges),
// which run no pre-receive hook.
func (h *Hooks) Prepared(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, out io.Writer) error {
	var checked []githooks.RefUpdate
	for _, u := range updates {
		if refKind(u.Ref) == KindOther {
			continue // HEAD, pseudo-refs, refs/pull/...
		}
		if githooks.ZeroOID(u.Old) {
			// update-ref without an expected old value reports zero:
			// read the current value to tell a creation from an update.
			cur, err := git.ResolveRef(ctx, s.RepoPath, nil, u.Ref)
			if err != nil {
				h.logger().ErrorContext(ctx, "protection: resolve ref", "repo", s.RepoID, "ref", u.Ref, "error", err)
				return githooks.Rejectf("could not check the branch protection rules")
			}
			if cur != "" {
				u.Old = cur
			}
		}
		checked = append(checked, u)
	}
	if err := h.check(ctx, s, checked, nil); err != nil {
		return err
	}
	if p, ok := h.Next.(githooks.Preparer); ok {
		return p.Prepared(ctx, s, updates, out)
	}
	return nil
}

// Committed hands the updates to Next.
func (h *Hooks) Committed(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, out io.Writer) error {
	if h.Next != nil {
		return h.Next.Committed(ctx, s, updates, out)
	}
	return nil
}

// Finish hands the end of the session to Next.
func (h *Hooks) Finish(s *githooks.Session) {
	if h.Next != nil {
		h.Next.Finish(s)
	}
}

func (h *Hooks) check(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, env []string) error {
	if h.Engine == nil || len(updates) == 0 {
		return nil
	}
	in := Input{RepoID: s.RepoID, RepoPath: s.RepoPath, Env: env, PusherID: s.PusherID, Protocol: s.Protocol}
	for _, u := range updates {
		in.Updates = append(in.Updates, Update{Ref: u.Ref, Old: u.Old, New: u.New})
	}
	res, err := h.Engine.CheckUpdates(ctx, in)
	if err != nil {
		// Fail closed: a rule that cannot be checked refuses the update.
		h.logger().ErrorContext(ctx, "protection: check ref updates", "repo", s.RepoID, "error", err)
		return githooks.Rejectf("could not check the branch protection rules, try again later")
	}
	if msg := Explain(res); msg != "" {
		h.logger().InfoContext(ctx, "protection: ref update refused", "repo", s.RepoID, "user", s.PusherID, "protocol", s.Protocol)
		return githooks.Rejectf("%s", msg)
	}
	return nil
}
