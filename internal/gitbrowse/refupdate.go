package gitbrowse

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// Errors of UpdateRef; each wraps ErrConflict or ErrNotFound.
var (
	ErrRefExists      = fmt.Errorf("%w: reference already exists", ErrConflict)
	ErrRefMissing     = fmt.Errorf("%w: reference does not exist", ErrNotFound)
	ErrObjectMissing  = fmt.Errorf("%w: object does not exist", ErrNotFound)
	ErrNotFastForward = fmt.Errorf("%w: update is not a fast forward", ErrConflict)
)

// Ref update operations.
const (
	RefCreate = "create"
	RefUpdate = "update"
	RefDelete = "delete"
)

// RefChange is a direct change of a branch or a tag (the git refs API).
type RefChange struct {
	Op  string // RefCreate, RefUpdate or RefDelete
	Ref string // full name: refs/heads/<branch> or refs/tags/<tag>
	// SHA is the full id of the new target (create, update): a commit, or
	// a commit or an annotated tag object for a tag.
	SHA string
	// Force allows an update that is not a fast-forward.
	Force   bool
	ActorID uuid.UUID
}

// RefChangeResult is the ref after the change.
type RefChangeResult struct {
	Ref string
	Old string // "" when created
	New string // "" when deleted
}

// UpdateRef creates, moves or deletes a branch or a tag. Like Edit it holds
// the Write lease and runs a compare-and-swap update-ref inside a githooks
// session, so branch and tag protection apply and the journal records the
// change like a push. No object is written, so the quota is not involved.
func (s *Service) UpdateRef(ctx context.Context, repo Repo, c RefChange) (RefChangeResult, error) {
	res := RefChangeResult{Ref: c.Ref}
	if err := checkRefChange(c, repo); err != nil {
		return res, err
	}
	l, err := s.o.Tier.Acquire(ctx, repo.ID, gitstore.Write)
	if err != nil {
		return res, fmt.Errorf("acquire repository: %w", err)
	}
	defer l.Release()
	rp := l.Path

	old, err := refValue(ctx, rp, c.Ref)
	if err != nil {
		return res, err
	}
	switch {
	case c.Op == RefCreate && old != "":
		return res, ErrRefExists
	case c.Op != RefCreate && old == "":
		return res, ErrRefMissing
	}
	res.Old = old
	change := git.RefChange{Ref: c.Ref, New: c.SHA, Old: old}
	if c.Op == RefCreate {
		change.Old = strings.Repeat("0", len(c.SHA))
	}
	if c.Op != RefDelete {
		if err := checkTarget(ctx, rp, c); err != nil {
			return res, err
		}
		if old == c.SHA {
			res.New = c.SHA
			return res, nil
		}
		if c.Op == RefUpdate && !c.Force {
			ok, err := git.IsAncestor(ctx, rp, nil, old, c.SHA)
			if err != nil {
				return res, fmt.Errorf("check fast-forward: %w", err)
			}
			if !ok {
				return res, ErrNotFastForward
			}
		}
	}

	var env []string
	var sess *githooks.Session
	if s.o.Hooks != nil {
		sess, err = s.o.Hooks.Begin(ctx, l, c.ActorID, "web")
		if err != nil {
			return res, fmt.Errorf("begin hook session: %w", err)
		}
		defer sess.Close()
		env = sess.Env()
	}
	if err := git.UpdateRefs(ctx, rp, env, []git.RefChange{change}); err != nil {
		if sess != nil && sess.Rejected() != "" {
			return res, fmt.Errorf("%w: %s", ErrRejected, sess.Rejected())
		}
		if errors.Is(err, git.ErrInvalid) {
			return res, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if exitCode(err) == 128 {
			return res, fmt.Errorf("%w: %s changed, reload and retry", ErrConflict, c.Ref)
		}
		return res, fmt.Errorf("update ref: %w", err)
	}
	res.New = c.SHA
	s.log.InfoContext(ctx, "ref updated", "repo", repo.ID, "user", c.ActorID, "op", c.Op, "ref", c.Ref,
		"old", old, "new", c.SHA)
	return res, nil
}

// checkRefChange validates the arguments of a ref change.
func checkRefChange(c RefChange, repo Repo) error {
	switch c.Op {
	case RefCreate, RefUpdate, RefDelete:
	default:
		return invalidf("unknown operation %q", c.Op)
	}
	if c.ActorID == uuid.Nil {
		return invalidf("an actor is required")
	}
	switch {
	case strings.HasPrefix(c.Ref, "refs/heads/"):
		if err := git.ValidateBranchName(strings.TrimPrefix(c.Ref, "refs/heads/")); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if c.Op == RefDelete && strings.TrimPrefix(c.Ref, "refs/heads/") == repo.DefaultBranch {
			return invalidf("the default branch cannot be deleted")
		}
	case strings.HasPrefix(c.Ref, "refs/tags/"):
		if err := git.ValidateTagName(strings.TrimPrefix(c.Ref, "refs/tags/")); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	default:
		return invalidf("only refs/heads/ and refs/tags/ references can be changed")
	}
	if c.Op == RefDelete {
		if c.SHA != "" {
			return invalidf("a deletion takes no sha")
		}
		return nil
	}
	if c.SHA == "" {
		return invalidf("sha is required")
	}
	if err := git.ValidateObjectID(c.SHA); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// refValue returns the object id ref points to (not peeled), or "" if it
// does not exist.
func refValue(ctx context.Context, rp, ref string) (string, error) {
	out, err := run(ctx, rp, editTimeout, "for-each-ref", "--format=%(refname)%00%(objectname)", ref)
	if err != nil {
		return "", fmt.Errorf("read ref: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name, oid, ok := strings.Cut(line, "\x00"); ok && name == ref {
			return oid, nil
		}
	}
	return "", nil
}

// checkTarget checks that the new target exists and has a type the ref
// may point to: a commit for a branch, a commit or a tag object for a tag.
func checkTarget(ctx context.Context, rp string, c RefChange) error {
	out, err := run(ctx, rp, editTimeout, "cat-file", "-t", c.SHA)
	if exitCode(err) == 128 || exitCode(err) == 1 {
		return ErrObjectMissing
	}
	if err != nil {
		return fmt.Errorf("read object: %w", err)
	}
	typ := strings.TrimSpace(string(out))
	if typ == "commit" || (typ == "tag" && strings.HasPrefix(c.Ref, "refs/tags/")) {
		return nil
	}
	return invalidf("%s is a %s, not a commit", c.SHA, typ)
}
