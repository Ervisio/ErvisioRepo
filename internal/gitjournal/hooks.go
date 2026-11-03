package gitjournal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
)

// pushState is the githooks.Session state of one push.
type pushState struct {
	pre      *pending
	marked   bool // the dirty marker was set by this session
	flushed  bool // the journal holds the committed refs
	needSync bool // a sync job must replicate the push
}

func state(s *githooks.Session) *pushState {
	if st, ok := s.State.(*pushState); ok {
		return st
	}
	st := &pushState{}
	s.State = st
	return st
}

var _ githooks.Handler = (*Journal)(nil)

// PreReceive checks the pushed objects (file size limit, push size limit,
// quota) and, with sync durability, uploads them as the next segment
// before git updates any ref.
func (j *Journal) PreReceive(ctx context.Context, s *githooks.Session, updates []githooks.RefUpdate, q githooks.Quarantine, out io.Writer) error {
	st := state(s)
	env := q.Env()
	var newTips []string
	for _, u := range updates {
		if !githooks.ZeroOID(u.New) {
			newTips = append(newTips, u.New)
		}
	}
	row, err := j.q.GetRepoJournal(ctx, s.RepoID)
	if err != nil {
		return fmt.Errorf("load repository: %w", err)
	}
	// Mark before anything reaches the hot copy: eviction must not drop a
	// repository whose refs are ahead of the journal.
	if err := j.markDirty(ctx, s.RepoID); err != nil {
		return err
	}
	st.marked = true

	cur, err := j.readRefs(ctx, s.RepoPath)
	if err != nil {
		return err
	}
	curTips := tips(cur.Refs)
	if len(newTips) > 0 {
		large, err := scanBlobs(ctx, s.RepoPath, env, newTips, curTips, j.o.MaxFileSize, maxLargeList)
		if err != nil {
			return err
		}
		if len(large) > 0 {
			return largeReject(large, j.o.MaxFileSize)
		}
	}

	checkSize := func(n int64) error {
		if j.o.MaxPushSize > 0 && n > j.o.MaxPushSize {
			return githooks.Rejectf("push of %s exceeds the limit of %s; split it into smaller pushes or move large files to Git LFS",
				human(n), human(j.o.MaxPushSize))
		}
		if n <= 0 {
			return nil
		}
		err := quota.Check(ctx, j.q, row.OwnerID, quota.Delta{Bytes: n})
		var ex *quota.ExceededError
		if errors.As(err, &ex) {
			return githooks.Rejectf("storage quota exceeded: %s of %s used", human(ex.Used), human(ex.Limit))
		}
		if err != nil {
			return fmt.Errorf("check quota: %w", err)
		}
		return nil
	}

	if j.o.Durability == DurabilityAsync {
		n, err := quarantineSize(ctx, s.RepoPath, env)
		if err != nil {
			return err
		}
		return checkSize(n)
	}

	h, err := j.loadHead(ctx, s.RepoID)
	if err != nil {
		return err
	}
	exclude, err := existing(ctx, s.RepoPath, nil, h.tips())
	if err != nil {
		return err
	}
	include := slices.Concat(curTips, newTips)
	_, _ = fmt.Fprintln(out, "Storing objects in the journal...")
	seg, err := j.uploadSegment(ctx, s.RepoID, s.RepoPath, env, include, exclude, h.m.RefsSeq+1, checkSize)
	if err != nil {
		return err
	}
	st.pre = &pending{baseSeq: h.m.RefsSeq, seg: seg}
	return nil
}

// Committed records the new refs in the journal: with sync durability the
// push is answered only after the manifest commit, with async a job does it.
func (j *Journal) Committed(ctx context.Context, s *githooks.Session, _ []githooks.RefUpdate, out io.Writer) error {
	st := state(s)
	if !st.marked {
		if err := j.markDirty(ctx, s.RepoID); err != nil {
			return err
		}
		st.marked = true
	}
	if j.o.Durability == DurabilityAsync {
		st.needSync = true
		return nil
	}
	if err := j.flush(ctx, s.RepoID, s.RepoPath, st.pre); err != nil {
		// The refs are already updated: the push cannot be undone, the
		// replication is retried by a job.
		j.log.ErrorContext(ctx, "git journal flush failed", "repo", s.RepoID, "error", err)
		_, _ = fmt.Fprintln(out, "warning: the push was accepted but not yet replicated; it will be retried")
		st.needSync = true
		return nil
	}
	st.flushed = true
	st.needSync = false
	return nil
}

// Finish schedules the replication of a session that changed the
// repository without a successful flush.
func (j *Journal) Finish(s *githooks.Session) {
	st, ok := s.State.(*pushState)
	if !ok {
		return
	}
	if st.needSync || (st.marked && !st.flushed) {
		j.enqueue(context.Background(), SyncArgs{RepoID: s.RepoID})
	}
}

func largeReject(large []largeBlob, limit int64) error {
	var b strings.Builder
	fmt.Fprintf(&b, "files larger than %s must be stored with Git LFS:\n", human(limit))
	for _, l := range large {
		name := l.Path
		if name == "" {
			name = l.OID
		}
		fmt.Fprintf(&b, "  %s (%s)\n", name, human(l.Size))
	}
	b.WriteString("Track them with \"git lfs track <pattern>\", or rewrite the history with \"git lfs migrate import --include=<pattern>\", then push again.")
	return &githooks.Reject{Msg: b.String()}
}

// human formats a byte count.
func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
