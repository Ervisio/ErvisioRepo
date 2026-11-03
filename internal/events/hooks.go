package events

import (
	"context"
	"io"
	"log/slog"

	"github.com/Ervisio/ErvisioRepo/internal/githooks"
)

// PushHooks is a githooks.Handler that publishes a KindPush event (action
// "pushed", PushPayload) once git has updated refs, then hands every call
// to Next. Pushes over the transports and ref updates made by the server
// (web edits, merges) are both published. A failed publish is logged and
// does not affect the push.
type PushHooks struct {
	Bus  *Bus
	Next githooks.Handler
	Log  *slog.Logger
}

var (
	_ githooks.Handler  = (*PushHooks)(nil)
	_ githooks.Preparer = (*PushHooks)(nil)
)

// PreReceive hands the push to Next.
func (h *PushHooks) PreReceive(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, q githooks.Quarantine, out io.Writer) error {
	if h.Next != nil {
		return h.Next.PreReceive(ctx, s, updates, q, out)
	}
	return nil
}

// Prepared hands the updates to Next when it is a githooks.Preparer.
func (h *PushHooks) Prepared(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, out io.Writer) error {
	if p, ok := h.Next.(githooks.Preparer); ok {
		return p.Prepared(ctx, s, updates, out)
	}
	return nil
}

// Committed hands the updates to Next, then publishes them.
func (h *PushHooks) Committed(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, out io.Writer) error {
	var err error
	if h.Next != nil {
		err = h.Next.Committed(ctx, s, updates, out)
	}
	if h.Bus != nil && len(updates) > 0 {
		p := PushPayload{Updates: make([]RefUpdate, 0, len(updates))}
		for _, u := range updates {
			p.Updates = append(p.Updates, RefUpdate{Ref: u.Ref, Old: u.Old, New: u.New})
		}
		ev := Event{Kind: KindPush, Action: "pushed", RepoID: s.RepoID, ActorID: s.PusherID}
		if perr := h.Bus.Publish(context.WithoutCancel(ctx), ev, p); perr != nil {
			log := h.Log
			if log == nil {
				log = h.Bus.log
			}
			log.WarnContext(ctx, "publish push event", "repo_id", s.RepoID, "error", perr)
		}
	}
	return err
}

// Finish hands the end of the session to Next.
func (h *PushHooks) Finish(s *githooks.Session) {
	if h.Next != nil {
		h.Next.Finish(s)
	}
}
