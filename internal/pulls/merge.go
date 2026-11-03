package pulls

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/protection"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// MergeInput merges a pull request.
type MergeInput struct {
	Method       string // merge (default), squash or rebase
	Title        string // subject of the merge or squash commit; empty = default
	Message      string // body of the merge or squash commit; empty = default
	SHA          string // expected head; empty skips the check
	DeleteBranch bool
}

// AutoMergeInput enables auto-merge.
type AutoMergeInput struct {
	Method       string
	Title        string
	Message      string
	DeleteBranch bool
}

func checkMethod(m string) (string, error) {
	switch m {
	case "":
		return MethodMerge, nil
	case MethodMerge, MethodSquash, MethodRebase:
		return m, nil
	}
	return "", invalid("method must be merge, squash or rebase")
}

func notMergeable(reasons ...string) error { return &NotMergeableError{Reasons: reasons} }

// Merge merges pull request number of owner/name and returns the new tip
// of the base branch.
func (s *Service) Merge(ctx context.Context, who auth.Principal, owner, name string, number int64, in MergeInput) (string, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return "", err
	}
	if !canMerge(r) {
		return "", ErrForbidden
	}
	return s.merge(ctx, who, r, number, in)
}

func (s *Service) merge(ctx context.Context, who auth.Principal, r repos.Repo, number int64, in MergeInput) (string, error) {
	method, err := checkMethod(in.Method)
	if err != nil {
		return "", err
	}
	if err := checkText("title", in.Title, MaxTitle); err != nil {
		return "", err
	}
	if err := checkText("message", in.Message, MaxBody); err != nil {
		return "", err
	}
	l, err := s.acquire(ctx, r.ID, gitstore.Write)
	if err != nil {
		return "", err
	}
	defer l.Release()
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return "", err
	}
	switch {
	case p.MergedAt.Valid:
		return "", notMergeable("the pull request is already merged")
	case i.State != issues.StateOpen:
		return "", notMergeable("the pull request is closed")
	case p.Draft:
		return "", notMergeable("the pull request is a draft")
	case !p.HeadRepoID.Valid:
		return "", notMergeable("the head repository was deleted")
	}
	if in.SHA != "" && in.SHA != p.HeadSha {
		return "", fmt.Errorf("%w: the head moved to %s", ErrConflict, p.HeadSha)
	}
	baseSHA, err := git.ResolveRef(ctx, l.Path, nil, "refs/heads/"+p.BaseRef)
	if err != nil {
		return "", fmt.Errorf("resolve base: %w", err)
	}
	if baseSHA == "" {
		return "", notMergeable(fmt.Sprintf("the base branch %s does not exist", p.BaseRef))
	}
	p.BaseSha = baseSHA
	if s.o.Protection != nil {
		st, err := s.o.Protection.PullRequestStatus(ctx, s.prInput(l, i, p))
		if err != nil {
			return "", fmt.Errorf("protection status: %w", err)
		}
		if !st.Mergeable() {
			reasons := make([]string, 0, len(st.Violations))
			for _, v := range st.Violations {
				reasons = append(reasons, v.Message)
			}
			return "", notMergeable(reasons...)
		}
	}

	merger, err := s.o.Browse.Identity(ctx, who.UserID)
	if err != nil {
		return "", fmt.Errorf("merger identity: %w", err)
	}
	headName := p.HeadRef
	if hr := s.newRepoNames(r).get(ctx, p.HeadRepoID); hr != "" {
		headOwner, _, _ := strings.Cut(hr, "/")
		headName = headOwner + "/" + p.HeadRef
	}
	var commit, message string
	switch method {
	case MethodMerge, MethodSquash:
		res, err := git.MergeTree(ctx, l.Path, nil, baseSHA, p.HeadSha, "")
		if err != nil {
			return "", fmt.Errorf("merge: %w", err)
		}
		if !res.Clean {
			return "", fmt.Errorf("%w: the branches conflict in %s", ErrConflict, strings.Join(res.Conflicts, ", "))
		}
		title, body := in.Title, in.Message
		env := merger
		parents := []string{baseSHA, p.HeadSha}
		if method == MethodMerge {
			if title == "" {
				title = "Merge pull request #" + strconv.FormatInt(p.Number, 10) + " from " + headName
			}
			if body == "" {
				body = i.Title
			}
		} else {
			if title == "" {
				title = i.Title + " (#" + strconv.FormatInt(p.Number, 10) + ")"
			}
			if body == "" {
				body = i.Body
			}
			parents = []string{baseSHA}
			if env, err = s.squashEnv(ctx, i, merger); err != nil {
				return "", err
			}
		}
		message = strings.TrimSpace(title)
		if b := strings.TrimSpace(body); b != "" {
			message += "\n\n" + b
		}
		commit, err = git.CommitTree(ctx, l.Path, env, res.Tree, parents, message)
		if err != nil {
			return "", fmt.Errorf("write merge commit: %w", err)
		}
	case MethodRebase:
		commit, message, err = s.rebase(ctx, l, merger, baseSHA, p.HeadSha)
		if err != nil {
			return "", err
		}
	}

	sess, env, done, err := s.session(ctx, l, who.UserID, protection.ProtocolMerge)
	if err != nil {
		return "", err
	}
	err = git.UpdateRefs(ctx, l.Path, env, []git.RefChange{{Ref: "refs/heads/" + p.BaseRef, New: commit, Old: baseSHA}})
	rejected := ""
	if sess != nil {
		rejected = sess.Rejected()
	}
	done()
	if err != nil {
		if rejected != "" {
			return "", notMergeable(rejected)
		}
		return "", fmt.Errorf("%w: the base branch moved, retry", ErrConflict)
	}

	err = s.tx(ctx, func(q *dbgen.Queries) error {
		np, err := q.SetPullMerged(ctx, dbgen.SetPullMergedParams{MergedBy: nullID(who.UserID), MergeCommitSha: commit,
			MergeMethod: method, IssueID: p.IssueID})
		if err != nil {
			return fmt.Errorf("record merge: %w", err)
		}
		row, err := q.SetIssueState(ctx, dbgen.SetIssueStateParams{ID: i.ID, State: issues.StateClosed,
			StateReason: issues.ReasonCompleted, ClosedAt: ts(s.o.Now()), ClosedBy: nullID(who.UserID)})
		if err != nil {
			return fmt.Errorf("close pull request: %w", err)
		}
		if err := s.timeline(ctx, q, row, who.UserID, "merged", map[string]any{"commit": commit, "method": method, "base": p.BaseRef}); err != nil {
			return err
		}
		extra := func(pl *events.PullPayload) { pl.MergeCommit, pl.MergeMethod = commit, method }
		if err := s.emitPull(ctx, q, "closed", who.UserID, row, np, extra); err != nil {
			return err
		}
		return s.emitPull(ctx, q, "merged", who.UserID, row, np, extra)
	})
	if err != nil {
		// The branch already moved: report the merge, log the bookkeeping.
		s.log.ErrorContext(ctx, "pull request merged but not recorded", "repo", r.ID, "number", p.Number, "commit", commit, "err", err)
		return commit, nil
	}
	l.Release()
	s.log.InfoContext(ctx, "pull request merged", "repo", r.ID, "number", p.Number, "method", method, "commit", commit, "user", who.UserID)

	if p.BaseRef == r.DefaultBranch && s.o.Issues != nil {
		text := i.Title + "\n" + i.Body + "\n" + message
		if _, err := s.o.Issues.CloseFromText(ctx, r.ID, who.UserID, text, issues.CloseSource{Pull: p.Number}); err != nil {
			s.log.WarnContext(ctx, "closing referenced issues failed", "repo", r.ID, "number", p.Number, "err", err)
		}
	}
	if in.DeleteBranch {
		s.deleteHead(ctx, who, r, p)
	}
	return commit, nil
}

// identityParts splits an Identity environment into its author and
// committer variables.
func identityParts(env []string) (author, committer []string) {
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "GIT_AUTHOR_"):
			author = append(author, kv)
		case strings.HasPrefix(kv, "GIT_COMMITTER_"):
			committer = append(committer, kv)
		}
	}
	return author, committer
}

// squashEnv authors a squash commit as the pull request author, committed
// by the merger.
func (s *Service) squashEnv(ctx context.Context, i dbgen.Issue, merger []string) ([]string, error) {
	mAuthor, mCommitter := identityParts(merger)
	if !i.AuthorID.Valid {
		return append(mAuthor, mCommitter...), nil
	}
	a, err := s.o.Browse.Identity(ctx, i.AuthorID.UUID)
	if err != nil {
		return append(mAuthor, mCommitter...), nil //nolint:nilerr // a deleted author falls back to the merger
	}
	author, _ := identityParts(a)
	return append(author, mCommitter...), nil
}

// rebase replays the commits of base..head onto base, keeping their
// authors and committing them as the merger. It returns the new tip and
// the concatenated messages.
func (s *Service) rebase(ctx context.Context, l *gitstore.Lease, merger []string, base, head string) (string, string, error) {
	ids, more, err := git.RevList(ctx, l.Path, nil, base, head, MaxRebaseCommits)
	if err != nil {
		return "", "", fmt.Errorf("list commits: %w", err)
	}
	if more {
		return "", "", notMergeable(fmt.Sprintf("a rebase merge replays at most %d commits", MaxRebaseCommits))
	}
	_, committer := identityParts(merger)
	cur := base
	var msgs strings.Builder
	for _, id := range ids {
		meta, err := git.ReadCommitMeta(ctx, l.Path, nil, id)
		if err != nil {
			return "", "", fmt.Errorf("read commit: %w", err)
		}
		if len(meta.Parents) != 1 {
			return "", "", notMergeable("a rebase merge cannot replay merge commits")
		}
		res, err := git.MergeTree(ctx, l.Path, nil, cur, id, meta.Parents[0])
		if err != nil {
			return "", "", fmt.Errorf("replay %s: %w", id, err)
		}
		if !res.Clean {
			return "", "", fmt.Errorf("%w: commit %s does not apply cleanly (%s)", ErrConflict, id, strings.Join(res.Conflicts, ", "))
		}
		env := append([]string{"GIT_AUTHOR_NAME=" + meta.AuthorName, "GIT_AUTHOR_EMAIL=" + meta.AuthorEmail,
			"GIT_AUTHOR_DATE=" + meta.AuthorDate}, committer...)
		next, err := git.CommitTree(ctx, l.Path, env, res.Tree, []string{cur}, meta.Message)
		if err != nil {
			return "", "", fmt.Errorf("replay %s: %w", id, err)
		}
		cur = next
		msgs.WriteString(meta.Message)
		msgs.WriteString("\n")
	}
	if cur == base {
		return "", "", notMergeable("nothing to merge")
	}
	return cur, msgs.String(), nil
}

// deleteHead deletes the head branch of a merged pull request when the
// merger may push to the head repository and no other pull request uses it.
func (s *Service) deleteHead(ctx context.Context, who auth.Principal, base repos.Repo, p dbgen.PullRequest) {
	if !p.HeadRepoID.Valid {
		return
	}
	hr, err := s.o.Repos.GetByID(ctx, who, p.HeadRepoID.UUID)
	if err != nil || !canWrite(hr) || p.HeadRef == hr.DefaultBranch {
		return
	}
	if others, err := s.q.OpenPullsByBase(ctx, dbgen.OpenPullsByBaseParams{RepoID: hr.ID, BaseRef: p.HeadRef}); err != nil || len(others) > 0 {
		return
	}
	if err := s.deleteBranch(ctx, who.UserID, hr.ID, p.HeadRef, p.HeadSha); err != nil {
		s.log.WarnContext(ctx, "deleting the head branch failed", "repo", base.ID, "number", p.Number, "err", err)
	}
}

func (s *Service) deleteBranch(ctx context.Context, user, repoID uuid.UUID, branch, expect string) error {
	l, err := s.acquire(ctx, repoID, gitstore.Write)
	if err != nil {
		return err
	}
	defer l.Release()
	sess, env, done, err := s.session(ctx, l, user, "web")
	if err != nil {
		return err
	}
	defer done()
	if err := git.UpdateRefs(ctx, l.Path, env, []git.RefChange{{Ref: "refs/heads/" + branch, Old: expect}}); err != nil {
		if sess != nil && sess.Rejected() != "" {
			return fmt.Errorf("delete branch: rejected: %s", sess.Rejected())
		}
		return fmt.Errorf("delete branch: %w", err)
	}
	return nil
}

// ---- auto-merge ----

// EnableAutoMerge merges the pull request as soon as its requirements are
// met, as the caller.
func (s *Service) EnableAutoMerge(ctx context.Context, who auth.Principal, owner, name string, number int64, in AutoMergeInput) (Pull, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Pull{}, err
	}
	if !canMerge(r) {
		return Pull{}, ErrForbidden
	}
	method, err := checkMethod(in.Method)
	if err != nil {
		return Pull{}, err
	}
	if err := checkText("title", in.Title, MaxTitle); err != nil {
		return Pull{}, err
	}
	if err := checkText("message", in.Message, MaxBody); err != nil {
		return Pull{}, err
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Pull{}, err
	}
	if p.MergedAt.Valid || i.State != issues.StateOpen {
		return Pull{}, fmt.Errorf("%w: the pull request is not open", ErrConflict)
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		np, err := q.SetPullAutoMerge(ctx, dbgen.SetPullAutoMergeParams{AutoMergeMethod: method, AutoMergeBy: nullID(who.UserID),
			AutoMergeTitle: in.Title, AutoMergeMessage: in.Message, AutoMergeDelete: in.DeleteBranch, IssueID: p.IssueID})
		if err != nil {
			return fmt.Errorf("enable auto-merge: %w", err)
		}
		p = np
		if err := s.timeline(ctx, q, i, who.UserID, "auto_merge_enabled", map[string]any{"method": method}); err != nil {
			return err
		}
		return s.emitPull(ctx, q, "auto_merge_enabled", who.UserID, i, np, nil)
	})
	if err != nil {
		return Pull{}, err
	}
	s.tryAutoMerge(ctx, r.ID, p.IssueID)
	i, p, err = s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Pull{}, err
	}
	return s.one(ctx, who.UserID, r, i, p)
}

// DisableAutoMerge cancels a pending auto-merge.
func (s *Service) DisableAutoMerge(ctx context.Context, who auth.Principal, owner, name string, number int64) (Pull, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Pull{}, err
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Pull{}, err
	}
	if !canMerge(r) && !isUser(p.AutoMergeBy, who.UserID) {
		return Pull{}, ErrForbidden
	}
	if p.AutoMergeMethod != "" {
		if err := s.clearAutoMerge(ctx, who.UserID, i, p); err != nil {
			return Pull{}, err
		}
	}
	i, p, err = s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Pull{}, err
	}
	return s.one(ctx, who.UserID, r, i, p)
}

func (s *Service) clearAutoMerge(ctx context.Context, actor uuid.UUID, i dbgen.Issue, p dbgen.PullRequest) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		np, err := q.SetPullAutoMerge(ctx, dbgen.SetPullAutoMergeParams{IssueID: p.IssueID})
		if err != nil {
			return fmt.Errorf("disable auto-merge: %w", err)
		}
		if err := s.timeline(ctx, q, i, actor, "auto_merge_disabled", nil); err != nil {
			return err
		}
		return s.emitPull(ctx, q, "auto_merge_disabled", actor, i, np, nil)
	})
}

// tryAutoMerge merges a pull request with auto-merge enabled when it can
// be merged; unmet requirements leave it waiting.
func (s *Service) tryAutoMerge(ctx context.Context, repoID, pullID uuid.UUID) {
	p, err := s.q.GetPullRequestByID(ctx, pullID)
	if err != nil || p.AutoMergeMethod == "" || p.MergedAt.Valid || p.Draft || p.MergeableState != MergeClean {
		return
	}
	who, ok, err := s.principalOf(ctx, p.AutoMergeBy.UUID)
	if err != nil {
		s.log.WarnContext(ctx, "auto-merge: load user", "repo", repoID, "number", p.Number, "err", err)
		return
	}
	var r repos.Repo
	if ok {
		r, err = s.o.Repos.GetByID(ctx, who, repoID)
		ok = err == nil && canMerge(r)
	}
	if !ok {
		if i, err := s.q.GetIssueByID(ctx, p.IssueID); err == nil {
			if err := s.clearAutoMerge(ctx, uuid.Nil, i, p); err != nil {
				s.log.WarnContext(ctx, "auto-merge: disable", "repo", repoID, "number", p.Number, "err", err)
			}
		}
		return
	}
	_, err = s.merge(ctx, who, r, p.Number, MergeInput{Method: p.AutoMergeMethod, Title: p.AutoMergeTitle,
		Message: p.AutoMergeMessage, SHA: p.HeadSha, DeleteBranch: p.AutoMergeDelete})
	switch {
	case err == nil:
	case errors.Is(err, ErrNotMergeable), errors.Is(err, ErrConflict):
		s.log.DebugContext(ctx, "auto-merge waiting", "repo", repoID, "number", p.Number, "reason", err)
	default:
		s.log.WarnContext(ctx, "auto-merge failed", "repo", repoID, "number", p.Number, "err", err)
	}
}
