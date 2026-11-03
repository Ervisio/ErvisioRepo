package ghsync

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
)

// Hooks wraps the git hook handler chain: it refuses user writes to the
// branches and tags of a mirror, and schedules a debounced backup or sync
// run after a user push. Writes made by this package (Protocol) pass
// through and schedule nothing (loop prevention).
type Hooks struct {
	Service *Service
	Next    githooks.Handler
}

const mirrorRefusal = "this repository is a read-only mirror of a GitHub repository: push to GitHub instead"

func (h *Hooks) mode(ctx context.Context, s *githooks.Session) string {
	if h.Service == nil || s.Protocol == Protocol || s.Protocol == ImportProtocol {
		return ""
	}
	mode, err := h.Service.q.GetGitHubSyncModeByRepo(ctx, s.RepoID)
	if err != nil {
		return "" // no link (or a database error, reported by the next handlers)
	}
	return mode
}

// mirrorWrite reports whether updates touch the branches or tags of a
// mirror (internal refs such as merge request heads stay writable).
func mirrorWrite(updates []githooks.RefUpdate) bool {
	for _, u := range updates {
		if strings.HasPrefix(u.Ref, headsPrefix) || strings.HasPrefix(u.Ref, tagsPrefix) {
			return true
		}
	}
	return false
}

// PreReceive implements githooks.Handler.
func (h *Hooks) PreReceive(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, q githooks.Quarantine, out io.Writer) error {
	if mirrorWrite(updates) && h.mode(ctx, s) == ModeMirror {
		return &githooks.Reject{Msg: mirrorRefusal}
	}
	if h.Next == nil {
		return nil
	}
	return h.Next.PreReceive(ctx, s, updates, q, out)
}

// Prepared implements githooks.Preparer (web edits, merges).
func (h *Hooks) Prepared(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, out io.Writer) error {
	if mirrorWrite(updates) && h.mode(ctx, s) == ModeMirror {
		return &githooks.Reject{Msg: mirrorRefusal}
	}
	if p, ok := h.Next.(githooks.Preparer); ok {
		return p.Prepared(ctx, s, updates, out)
	}
	return nil
}

// Committed implements githooks.Handler. Backups after a push are
// scheduled by HandleEvent from the events.KindPush bus event.
func (h *Hooks) Committed(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, out io.Writer) error {
	if h.Next != nil {
		return h.Next.Committed(ctx, s, updates, out)
	}
	return nil
}

// Finish implements githooks.Handler.
func (h *Hooks) Finish(s *githooks.Session) {
	if h.Next != nil {
		h.Next.Finish(s)
	}
}

// HandleEvent schedules a debounced run of a backup or sync link after a
// push (events.KindPush). Writes of this package are skipped by the
// equal-SHA check of the run.
func (s *Service) HandleEvent(ctx context.Context, ev events.Event) error {
	if ev.Kind != events.KindPush || ev.RepoID == uuid.Nil {
		return nil
	}
	mode, err := s.q.GetGitHubSyncModeByRepo(ctx, ev.RepoID)
	if noRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get sync mode: %w", err)
	}
	if mode != ModeBackup && mode != ModeSync {
		return nil
	}
	if err := s.enqueue(ctx, RunArgs{RepoID: ev.RepoID, Debounced: true}); err != nil {
		return fmt.Errorf("enqueue github backup: %w", err)
	}
	return nil
}

// EventHandler is HandleEvent as an events.Handler, to register for
// events.KindPush.
func (s *Service) EventHandler() events.Handler { return events.HandlerFunc(s.HandleEvent) }
