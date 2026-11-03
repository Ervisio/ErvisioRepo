package pulls

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// UpdateBranchInput brings the head branch of a pull request up to date
// with its base.
type UpdateBranchInput struct {
	Method          string // merge (default) or rebase
	ExpectedHeadSHA string // empty skips the check
}

// UpdateBranch merges the base branch into the head branch of pull request
// number of owner/name (or rebases the head onto it) as the caller, who
// must be allowed to push to the head repository. The head branch moves
// through a hook session, so its branch protection applies, and the push
// then synchronizes the pull request like any other. It returns the new
// head commit.
func (s *Service) UpdateBranch(ctx context.Context, who auth.Principal, owner, name string, number int64, in UpdateBranchInput) (string, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return "", err
	}
	method := in.Method
	switch method {
	case "":
		method = MethodMerge
	case MethodMerge, MethodRebase:
	default:
		return "", invalid("method must be merge or rebase")
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return "", err
	}
	switch {
	case p.MergedAt.Valid:
		return "", fmt.Errorf("%w: the pull request is already merged", ErrConflict)
	case i.State != issues.StateOpen:
		return "", fmt.Errorf("%w: the pull request is closed", ErrConflict)
	case !p.HeadRepoID.Valid:
		return "", fmt.Errorf("%w: the head repository was deleted", ErrConflict)
	}
	hr := r
	if p.HeadRepoID.UUID != r.ID {
		if hr, err = s.o.Repos.GetByID(ctx, who, p.HeadRepoID.UUID); err != nil {
			if errors.Is(err, repos.ErrNotFound) {
				return "", ErrForbidden
			}
			return "", err
		}
	}
	if !canWrite(hr) {
		return "", ErrForbidden
	}

	// Lock order base then head, as in synchronize.
	var bl *gitstore.Lease
	if hr.ID != r.ID {
		if bl, err = s.acquire(ctx, r.ID, gitstore.Read); err != nil {
			return "", err
		}
		defer bl.Release()
	}
	hl, err := s.acquire(ctx, hr.ID, gitstore.Write)
	if err != nil {
		return "", err
	}
	defer hl.Release()
	src := hl
	if bl != nil {
		src = bl
	}
	baseSHA, err := git.ResolveRef(ctx, src.Path, nil, "refs/heads/"+p.BaseRef)
	if err != nil {
		return "", fmt.Errorf("resolve base: %w", err)
	}
	if baseSHA == "" {
		return "", fmt.Errorf("%w: the base branch %s does not exist", ErrConflict, p.BaseRef)
	}
	headRef := "refs/heads/" + p.HeadRef
	headSHA, err := git.ResolveRef(ctx, hl.Path, nil, headRef)
	if err != nil {
		return "", fmt.Errorf("resolve head: %w", err)
	}
	if headSHA == "" {
		return "", fmt.Errorf("%w: the head branch %s does not exist", ErrConflict, p.HeadRef)
	}
	if in.ExpectedHeadSHA != "" && in.ExpectedHeadSHA != headSHA {
		return "", fmt.Errorf("%w: the head moved to %s", ErrConflict, headSHA)
	}
	if bl != nil {
		// Objects only: the head branch update below records them in the
		// journal of the head repository under the hook session.
		if err := git.FetchBranches(ctx, hl.Path, nil, bl.Path, "refs/heads/"+p.BaseRef); err != nil {
			return "", fmt.Errorf("fetch base: %w", err)
		}
	}
	mb, err := git.MergeBase(ctx, hl.Path, nil, baseSHA, headSHA)
	if err != nil {
		return "", fmt.Errorf("merge base: %w", err)
	}
	if mb == baseSHA {
		return "", fmt.Errorf("%w: the head branch is already up to date with %s", ErrConflict, p.BaseRef)
	}
	ident, err := s.o.Browse.Identity(ctx, who.UserID)
	if err != nil {
		return "", fmt.Errorf("committer identity: %w", err)
	}
	var commit string
	switch method {
	case MethodMerge:
		res, err := git.MergeTree(ctx, hl.Path, nil, headSHA, baseSHA, "")
		if err != nil {
			return "", fmt.Errorf("merge: %w", err)
		}
		if !res.Clean {
			return "", fmt.Errorf("%w: the branches conflict in %s", ErrConflict, strings.Join(res.Conflicts, ", "))
		}
		msg := "Merge branch '" + p.BaseRef + "' into " + p.HeadRef
		if commit, err = git.CommitTree(ctx, hl.Path, ident, res.Tree, []string{headSHA, baseSHA}, msg); err != nil {
			return "", fmt.Errorf("write merge commit: %w", err)
		}
	case MethodRebase:
		if commit, _, err = s.rebase(ctx, hl, ident, baseSHA, headSHA); err != nil {
			return "", err
		}
	}

	sess, env, done, err := s.session(ctx, hl, who.UserID, "web")
	if err != nil {
		return "", err
	}
	err = git.UpdateRefs(ctx, hl.Path, env, []git.RefChange{{Ref: headRef, New: commit, Old: headSHA}})
	rejected := ""
	if sess != nil {
		rejected = sess.Rejected()
	}
	done()
	if err != nil {
		if rejected != "" {
			return "", fmt.Errorf("%w: %s", ErrForbidden, rejected)
		}
		return "", fmt.Errorf("%w: the head branch moved, retry", ErrConflict)
	}
	s.log.InfoContext(ctx, "pull request branch updated", "repo", r.ID, "number", p.Number, "method", method, "commit", commit, "user", who.UserID)
	return commit, nil
}
