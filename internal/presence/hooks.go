package presence

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
)

// maxRefusalLines bounds the locked files listed when a push is refused.
const maxRefusalLines = 50

// Hooks is a githooks.Handler that refuses, on repositories with the
// feature on, branch updates that change files locked on that branch by
// another user, and releases the locks of deleted branches. Every call is
// then handed to Next, which may be nil.
type Hooks struct {
	Service *Service
	Next    githooks.Handler
}

var (
	_ githooks.Handler  = (*Hooks)(nil)
	_ githooks.Preparer = (*Hooks)(nil)
)

// PreReceive checks a push whose objects are in quarantine.
func (h *Hooks) PreReceive(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, q githooks.Quarantine, out io.Writer) error {
	if err := h.check(ctx, s, updates, q.Env(), false); err != nil {
		return err
	}
	if h.Next != nil {
		return h.Next.PreReceive(ctx, s, updates, q, out)
	}
	return nil
}

// Prepared checks the ref updates made by the server (web edits, merges).
func (h *Hooks) Prepared(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, out io.Writer) error {
	if err := h.check(ctx, s, updates, nil, true); err != nil {
		return err
	}
	if p, ok := h.Next.(githooks.Preparer); ok {
		return p.Prepared(ctx, s, updates, out)
	}
	return nil
}

// Committed releases the locks of the deleted branches, then hands the
// updates to Next.
func (h *Hooks) Committed(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, out io.Writer) error {
	if h.Service != nil {
		for _, u := range updates {
			if strings.HasPrefix(u.Ref, headsPrefix) && githooks.ZeroOID(u.New) {
				if err := h.Service.releaseRef(context.WithoutCancel(ctx), s.RepoID, u.Ref); err != nil {
					h.Service.log.WarnContext(ctx, "presence: release locks of a deleted branch", "repo", s.RepoID, "ref", u.Ref, "error", err)
				}
			}
		}
	}
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

func (h *Hooks) check(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, env []string, prepared bool) error {
	if h.Service == nil || s.PusherID == uuid.Nil {
		return nil // server-internal writes (imports, mirrors) are not checked
	}
	var heads []githooks.RefUpdate
	for _, u := range updates {
		if strings.HasPrefix(u.Ref, headsPrefix) && !githooks.ZeroOID(u.New) {
			heads = append(heads, u)
		}
	}
	if len(heads) == 0 {
		return nil
	}
	lines, err := h.Service.lockedChanges(ctx, s, heads, env, prepared)
	if err != nil {
		// Fail closed: locks that cannot be checked refuse the push.
		h.Service.log.ErrorContext(ctx, "presence: check file locks", "repo", s.RepoID, "error", err)
		return githooks.Rejectf("could not check the file locks, try again later")
	}
	if len(lines) == 0 {
		return nil
	}
	h.Service.log.InfoContext(ctx, "presence: push refused by file locks", "repo", s.RepoID, "user", s.PusherID, "files", len(lines))
	if len(lines) > maxRefusalLines {
		more := len(lines) - maxRefusalLines
		lines = append(lines[:maxRefusalLines], fmt.Sprintf("error: and %d more locked files", more))
	}
	return githooks.Rejectf("%s", strings.Join(lines, "\n"))
}

// lockedChanges returns one refusal line per changed file locked by another
// user on the updated branch. Branches without such locks cost no git call.
func (s *Service) lockedChanges(ctx context.Context, sess *githooks.Session, heads []githooks.RefUpdate, env []string, prepared bool) ([]string, error) {
	on, err := s.q.GetRepoPresenceEnabled(ctx, sess.RepoID)
	if err != nil {
		return nil, fmt.Errorf("get presence setting: %w", err)
	}
	if !on {
		return nil, nil
	}
	var lines []string
	for _, u := range heads {
		locks, err := s.q.ListForeignRefLocks(ctx, dbgen.ListForeignRefLocksParams{RepoID: sess.RepoID, Refname: u.Ref, UserID: sess.PusherID})
		if err != nil {
			return nil, fmt.Errorf("list locks of %s: %w", u.Ref, err)
		}
		if len(locks) == 0 {
			continue
		}
		old := u.Old
		if prepared && githooks.ZeroOID(old) {
			// update-ref without an expected old value reports zero.
			if old, err = git.ResolveRef(ctx, sess.RepoPath, nil, u.Ref); err != nil {
				return nil, fmt.Errorf("resolve %s: %w", u.Ref, err)
			}
		}
		var paths []string
		if old == "" || githooks.ZeroOID(old) {
			paths, err = git.IntroducedPaths(ctx, sess.RepoPath, env, u.New)
		} else {
			paths, err = git.ChangedPaths(ctx, sess.RepoPath, env, old, u.New, 0)
		}
		if err != nil {
			return nil, fmt.Errorf("changed paths of %s: %w", u.Ref, err)
		}
		changed := make(map[string]bool, len(paths))
		for _, p := range paths {
			changed[p] = true
		}
		branch := strings.TrimPrefix(u.Ref, headsPrefix)
		for _, l := range locks {
			if changed[l.Path] {
				lines = append(lines, fmt.Sprintf("error: %s is locked on %s by %s since %s",
					l.Path, branch, l.Login, l.CreatedAt.Time.UTC().Format(time.RFC3339)))
			}
		}
	}
	return lines, nil
}
