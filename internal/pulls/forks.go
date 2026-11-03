package pulls

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// ForkInput creates a fork.
type ForkInput struct {
	Owner string // user or organization; empty means the caller
	Name  string // empty means the parent's name
}

// CreateFork copies a repository the caller can read into a new
// repository with the same branches and tags, linked to its parent and to
// the object pool of their fork network. The fork counts as one repository
// of its owner and, as for a push, its content is charged to the owner's
// storage: a fork that would exceed the storage quota is refused. When the
// copy fails the half-created fork is removed.
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
	abandon := func(cause error) (repos.Repo, error) {
		if aerr := s.o.Repos.Abandon(context.WithoutCancel(ctx), fork.ID); aerr != nil {
			s.log.ErrorContext(ctx, "removing a half-created fork failed", "fork", fork.ID, "err", aerr)
		}
		return repos.Repo{}, cause
	}
	// Create reserved the repository; the bytes are charged by the journal
	// as the copy lands, so the check here is the admission control.
	if err := quota.Check(ctx, s.q, fork.OwnerID, quota.Delta{Bytes: parent.SizeBytes}); err != nil {
		return abandon(err)
	}
	if err := s.q.InsertRepoFork(ctx, dbgen.InsertRepoForkParams{RepoID: fork.ID, ParentID: nullID(parent.ID)}); err != nil {
		return abandon(fmt.Errorf("record fork: %w", err))
	}
	// Only public content goes to the shared pool: a pooled object can be
	// read by SHA through any linked fork, so the objects of a private or
	// internal parent stay in a full copy.
	network := uuid.Nil
	if parent.Visibility == authz.Public {
		if network, err = s.joinNetwork(ctx, chain, fork.ID); err != nil {
			s.log.WarnContext(ctx, "fork network unavailable, copying in full", "parent", parent.ID, "fork", fork.ID, "err", err)
			network = uuid.Nil
		}
	}
	if err := s.copyRefs(ctx, who, parent, fork, network); err != nil {
		s.log.WarnContext(ctx, "fork content copy failed", "parent", parent.ID, "fork", fork.ID, "err", err)
		return abandon(err)
	}
	return fork, nil
}

// copyRefs fetches the branches and tags of parent into fork. With a
// network it first fills the pool and links the fork to it, so the fetch
// finds the objects already present; a pool failure falls back to a full
// copy.
func (s *Service) copyRefs(ctx context.Context, who auth.Principal, parent, fork repos.Repo, network uuid.UUID) error {
	src, err := s.acquire(ctx, parent.ID, gitstore.Read)
	if err != nil {
		return err
	}
	defer src.Release()
	pooled := false
	if network != uuid.Nil {
		if err := s.fillPool(ctx, network, parent.ID, src.Path); err != nil {
			s.log.WarnContext(ctx, "fork network pool not filled, copying in full", "network", network, "err", err)
		} else {
			pooled = true
		}
	}
	dst, err := s.acquire(ctx, fork.ID, gitstore.Write)
	if err != nil {
		return err
	}
	defer dst.Release()
	if pooled {
		if err := git.SetAlternates(dst.Path, s.o.Tier.PoolPath(network)); err != nil {
			s.log.WarnContext(ctx, "fork not linked to the network pool", "fork", fork.ID, "err", err)
			pooled = false
		}
	}
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
	if pooled {
		if err := s.q.SetForkNetworkMemberPooled(ctx, dbgen.SetForkNetworkMemberPooledParams{RepoID: fork.ID, Pooled: true}); err != nil {
			s.log.WarnContext(ctx, "record pooled fork", "fork", fork.ID, "err", err)
		}
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
