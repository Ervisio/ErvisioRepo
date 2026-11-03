package pulls

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/protection"
)

// testMergeIdentity signs the test merge commits of refs/pull/<n>/merge.
var testMergeIdentity = []string{
	"GIT_AUTHOR_NAME=Ervisio", "GIT_AUTHOR_EMAIL=noreply@ervisio.local",
	"GIT_COMMITTER_NAME=Ervisio", "GIT_COMMITTER_EMAIL=noreply@ervisio.local",
}

const zeroOID = "0000000000000000000000000000000000000000"

func pullRef(n int64, kind string) string {
	return "refs/pull/" + strconv.FormatInt(n, 10) + "/" + kind
}

func (s *Service) prInput(l *gitstore.Lease, i dbgen.Issue, p dbgen.PullRequest) protection.PRInput {
	return protection.PRInput{RepoID: p.RepoID, RepoPath: l.Path, Base: p.BaseRef, BaseSHA: p.BaseSha,
		HeadSHA: p.HeadSha, PullID: p.IssueID, AuthorID: i.AuthorID.UUID}
}

// refresh recomputes the test merge of p (whose head objects are in the
// base repository held by the Write lease l), points refs/pull/<n>/head
// and refs/pull/<n>/merge at the head and the merge, and records the
// result. actor is the user the ref updates are attributed to.
func (s *Service) refresh(ctx context.Context, l *gitstore.Lease, actor uuid.UUID, p dbgen.PullRequest) (dbgen.PullRequest, error) {
	baseSHA, err := git.ResolveRef(ctx, l.Path, nil, "refs/heads/"+p.BaseRef)
	if err != nil {
		return p, fmt.Errorf("resolve base: %w", err)
	}
	arg := dbgen.SetPullMergeabilityParams{IssueID: p.IssueID, HeadSha: p.HeadSha, BaseSha: baseSHA,
		MergeableState: MergeError, ConflictFiles: []string{}}
	if baseSHA == "" {
		arg.BaseSha = p.BaseSha
	}
	changes := []git.RefChange{{Ref: pullRef(p.Number, "head"), New: p.HeadSha}}
	if baseSHA != "" {
		mb, err := git.MergeBase(ctx, l.Path, nil, baseSHA, p.HeadSha)
		if err != nil {
			return p, fmt.Errorf("merge base: %w", err)
		}
		arg.MergeBaseSha = mb
		if mb != "" {
			res, err := git.MergeTree(ctx, l.Path, nil, baseSHA, p.HeadSha, "")
			if err != nil {
				return p, fmt.Errorf("test merge: %w", err)
			}
			if res.Clean {
				msg := "Merge " + p.HeadSha + " into " + baseSHA
				commit, err := git.CommitTree(ctx, l.Path, testMergeIdentity, res.Tree, []string{baseSHA, p.HeadSha}, msg)
				if err != nil {
					return p, fmt.Errorf("test merge commit: %w", err)
				}
				arg.MergeableState, arg.MergeSha = MergeClean, commit
				changes = append(changes, git.RefChange{Ref: pullRef(p.Number, "merge"), New: commit})
			} else {
				arg.MergeableState, arg.ConflictFiles = MergeConflict, res.Conflicts
			}
		}
	}
	if arg.MergeSha == "" {
		cur, err := git.ResolveRef(ctx, l.Path, nil, pullRef(p.Number, "merge"))
		if err != nil {
			return p, fmt.Errorf("resolve merge ref: %w", err)
		}
		if cur != "" {
			changes = append(changes, git.RefChange{Ref: pullRef(p.Number, "merge"), Old: cur})
		}
	}
	sess, env, done, err := s.session(ctx, l, actor, "web")
	if err != nil {
		return p, err
	}
	err = git.UpdateRefs(ctx, l.Path, env, changes)
	rejected := ""
	if sess != nil {
		rejected = sess.Rejected()
	}
	done()
	if err != nil {
		if rejected != "" {
			return p, fmt.Errorf("update pull refs: rejected: %s", rejected)
		}
		return p, fmt.Errorf("update pull refs: %w", err)
	}
	np, err := s.q.SetPullMergeability(ctx, arg)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil // the head moved meanwhile; the newer sync records its own result
	}
	if err != nil {
		return p, fmt.Errorf("record mergeability: %w", err)
	}
	if p.MergeBaseSha != "" && arg.MergeBaseSha != "" && p.MergeBaseSha != arg.MergeBaseSha {
		if err := s.reanchorLeft(ctx, l, np, p.MergeBaseSha, arg.MergeBaseSha); err != nil {
			s.log.WarnContext(ctx, "base side review comments re-anchoring failed", "repo", p.RepoID, "number", p.Number, "err", err)
		}
	}
	return np, nil
}

// PushHandler keeps the pull requests in step with the pushes: a push to a
// head branch updates the head (synchronize), re-anchors the review
// comments, recomputes the test merge and tries the auto-merge; a push to
// a base branch recomputes the test merge of the pull requests into it.
func (s *Service) PushHandler() events.Handler {
	return events.HandlerFunc(func(ctx context.Context, ev events.Event) error {
		var pl events.PushPayload
		if err := ev.Decode(&pl); err != nil {
			return err
		}
		var retry error
		for _, u := range pl.Updates {
			branch, ok := strings.CutPrefix(u.Ref, "refs/heads/")
			if !ok || u.New == "" || u.New == zeroOID {
				continue // refs/pull/*, tags, deletions
			}
			heads, err := s.q.OpenPullsByHead(ctx, dbgen.OpenPullsByHeadParams{HeadRepoID: nullID(ev.RepoID), HeadRef: branch})
			if err != nil {
				return fmt.Errorf("pulls by head: %w", err)
			}
			for _, p := range heads {
				if err := s.synchronize(ctx, ev.ActorID, p, ev.RepoID, branch); err != nil {
					s.log.WarnContext(ctx, "pull request synchronize failed", "repo", p.RepoID, "number", p.Number, "err", err)
					if errors.Is(err, ErrUnavailable) {
						retry = err
					}
				}
			}
			bases, err := s.q.OpenPullsByBase(ctx, dbgen.OpenPullsByBaseParams{RepoID: ev.RepoID, BaseRef: branch})
			if err != nil {
				return fmt.Errorf("pulls by base: %w", err)
			}
			for _, p := range bases {
				if err := s.recheck(ctx, ev.ActorID, p); err != nil {
					s.log.WarnContext(ctx, "pull request test merge failed", "repo", p.RepoID, "number", p.Number, "err", err)
					if errors.Is(err, ErrUnavailable) {
						retry = err
					}
				}
			}
		}
		return retry
	})
}

// recheck recomputes the test merge of p after its base moved.
func (s *Service) recheck(ctx context.Context, actor uuid.UUID, p dbgen.PullRequest) error {
	l, err := s.acquire(ctx, p.RepoID, gitstore.Write)
	if err != nil {
		return err
	}
	defer l.Release()
	cur, err := s.q.GetPullRequestByID(ctx, p.IssueID)
	if err != nil {
		return fmt.Errorf("get pull request: %w", err)
	}
	if _, err := s.refresh(ctx, l, actor, cur); err != nil {
		return err
	}
	l.Release()
	s.tryAutoMerge(ctx, cur.RepoID, cur.IssueID)
	return nil
}

// synchronize moves the head of p to the new tip of branch in headRepo.
func (s *Service) synchronize(ctx context.Context, actor uuid.UUID, p dbgen.PullRequest, headRepo uuid.UUID, branch string) error {
	l, err := s.acquire(ctx, p.RepoID, gitstore.Write)
	if err != nil {
		return err
	}
	defer l.Release()
	sha, err := s.headTip(ctx, l, p.RepoID, headRepo, branch)
	if err != nil {
		return err
	}
	if sha == "" {
		return nil
	}
	cur, err := s.q.GetPullRequestByID(ctx, p.IssueID)
	if err != nil {
		return fmt.Errorf("get pull request: %w", err)
	}
	before := cur.HeadSha
	if before != sha {
		err = s.tx(ctx, func(q *dbgen.Queries) error {
			np, err := q.SetPullHead(ctx, dbgen.SetPullHeadParams{HeadSha: sha, IssueID: cur.IssueID})
			if err != nil {
				return fmt.Errorf("set head: %w", err)
			}
			cur = np
			i, err := q.GetIssueByID(ctx, np.IssueID)
			if err != nil {
				return fmt.Errorf("get pull request issue: %w", err)
			}
			if err := s.timeline(ctx, q, i, actor, "head_updated", map[string]any{"before": before, "after": sha}); err != nil {
				return err
			}
			return s.emitPull(ctx, q, "synchronize", actor, i, np, func(pl *events.PullPayload) { pl.Before = before })
		})
		if err != nil {
			return err
		}
		if err := s.reanchor(ctx, l, cur, before, sha); err != nil {
			s.log.WarnContext(ctx, "review comments re-anchoring failed", "repo", cur.RepoID, "number", cur.Number, "err", err)
		}
	}
	if _, err := s.refresh(ctx, l, actor, cur); err != nil {
		return err
	}
	l.Release()
	s.tryAutoMerge(ctx, cur.RepoID, cur.IssueID)
	return nil
}

// AutoMergeHandler retries the auto-merge of the pull requests of a
// repository when a status or a review may have completed its
// requirements.
func (s *Service) AutoMergeHandler() events.Handler {
	return events.HandlerFunc(func(ctx context.Context, ev events.Event) error {
		if ev.RepoID == uuid.Nil {
			return nil
		}
		ps, err := s.q.AutoMergePulls(ctx, ev.RepoID)
		if err != nil {
			return fmt.Errorf("auto-merge pulls: %w", err)
		}
		for _, p := range ps {
			s.tryAutoMerge(ctx, p.RepoID, p.IssueID)
		}
		return nil
	})
}
