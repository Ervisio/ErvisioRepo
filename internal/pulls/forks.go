package pulls

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// ForkInput creates a fork.
type ForkInput struct {
	Owner string // user or organization; empty means the caller
	Name  string // empty means the parent's name
}

// CreateFork copies a repository the caller can read into a new
// repository with the same branches and tags, linked to its parent.
func (s *Service) CreateFork(ctx context.Context, who auth.Principal, owner, name string, in ForkInput, c identity.Client) (repos.Repo, error) {
	if who.UserID == uuid.Nil {
		return repos.Repo{}, ErrForbidden
	}
	parent, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return repos.Repo{}, err
	}
	chain, err := s.forkChain(ctx, parent.ID)
	if err != nil {
		return repos.Repo{}, err
	}
	if len(chain) > maxForkDepth {
		return repos.Repo{}, invalid("the fork network is too deep")
	}
	if in.Name == "" {
		in.Name = parent.Name
	}
	fork, err := s.o.Repos.Create(ctx, who, repos.CreateInput{Owner: in.Owner, Name: in.Name, Description: parent.Description,
		Visibility: string(parent.Visibility), DefaultBranch: parent.DefaultBranch}, c)
	if err != nil {
		return repos.Repo{}, err
	}
	if err := s.q.InsertRepoFork(ctx, dbgen.InsertRepoForkParams{RepoID: fork.ID, ParentID: nullID(parent.ID)}); err != nil {
		return fork, fmt.Errorf("record fork: %w", err)
	}
	if err := s.copyRefs(ctx, who, parent, fork); err != nil {
		s.log.WarnContext(ctx, "fork content copy failed", "parent", parent.ID, "fork", fork.ID, "err", err)
		return fork, err
	}
	return fork, nil
}

// copyRefs fetches the branches and tags of parent into fork.
func (s *Service) copyRefs(ctx context.Context, who auth.Principal, parent, fork repos.Repo) error {
	src, err := s.acquire(ctx, parent.ID, gitstore.Read)
	if err != nil {
		return err
	}
	defer src.Release()
	dst, err := s.acquire(ctx, fork.ID, gitstore.Write)
	if err != nil {
		return err
	}
	defer dst.Release()
	sess, env, done, err := s.session(ctx, dst, who.UserID, "web")
	if err != nil {
		return err
	}
	defer done()
	err = git.FetchBranches(ctx, dst.Path, env, src.Path, "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
	if err != nil {
		if sess != nil && sess.Rejected() != "" {
			return fmt.Errorf("copy fork content: rejected: %s", sess.Rejected())
		}
		return fmt.Errorf("copy fork content: %w", err)
	}
	return nil
}

// ListForks returns the direct forks of a repository the caller can read.
func (s *Service) ListForks(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]repos.Repo, error) {
	parent, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	off, lim := pageArgs(offset, limit)
	ids, err := s.q.ListRepoForks(ctx, dbgen.ListRepoForksParams{ParentID: nullID(parent.ID), Off: off, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("list forks: %w", err)
	}
	out := make([]repos.Repo, 0, len(ids))
	for _, id := range ids {
		r, err := s.o.Repos.GetByID(ctx, who, id)
		if err != nil {
			continue // not readable by the caller, or gone
		}
		out = append(out, r)
	}
	return out, nil
}
