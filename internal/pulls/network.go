package pulls

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// ---- fork networks and the object pool ----
//
// The repositories of a fork family form a network with one local object
// pool (gitstore.Tier.PoolPath). When a fork is created the branches and
// tags of its parent are fetched into the pool (refs/net/<repo>/...) and
// the new fork borrows from it through objects/info/alternates, so the
// fork's own copy only holds what is pushed to it later.
//
// Safety rules:
//   - The pool is append-only: no gc, no prune, no repack. A pooled object
//     never disappears while the network exists.
//   - The remote journal of every repository stays self-contained (its
//     packs are written by pack-objects, which reads through alternates),
//     so a fork can be evicted and hydrated on its own; a hydrated fork is
//     a full copy without alternates.
//   - Compaction of a pooled fork repacks with -l (local objects only),
//     which is safe because of the first rule.
//   - The pool goes to the trash only when the network has no members.

// joinNetwork returns the network of parent (creating it, with the
// topmost ancestor in chain as root, when parent has none) and adds fork
// to it.
func (s *Service) joinNetwork(ctx context.Context, chain []uuid.UUID, fork uuid.UUID) (uuid.UUID, error) {
	var network uuid.UUID
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		for _, id := range chain {
			m, err := q.GetForkNetworkMember(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("get fork network: %w", err)
			}
			network = m.NetworkID
			break
		}
		if network == uuid.Nil {
			id, err := newID()
			if err != nil {
				return err
			}
			if err := q.InsertForkNetwork(ctx, dbgen.InsertForkNetworkParams{ID: id, RootRepoID: nullID(chain[len(chain)-1])}); err != nil {
				return fmt.Errorf("create fork network: %w", err)
			}
			network = id
		}
		// The parent (chain[0]) is a member too; existing full-copy forks
		// join without being linked to the pool.
		if err := q.AddForkNetworkMember(ctx, dbgen.AddForkNetworkMemberParams{RepoID: chain[0], NetworkID: network}); err != nil {
			return fmt.Errorf("add fork network member: %w", err)
		}
		if err := q.AddForkNetworkMember(ctx, dbgen.AddForkNetworkMemberParams{RepoID: fork, NetworkID: network}); err != nil {
			return fmt.Errorf("add fork network member: %w", err)
		}
		return nil
	})
	return network, err
}

// fillPool fetches the branches and tags of the repository at srcPath
// (held by the caller) into the pool of network.
func (s *Service) fillPool(ctx context.Context, network, src uuid.UUID, srcPath string) error {
	l, err := s.o.Tier.LockPool(ctx, network)
	if err != nil {
		return fmt.Errorf("lock pool: %w", err)
	}
	defer l.Release()
	if err := git.InitPool(ctx, l.Path); err != nil {
		return err
	}
	ns := "refs/net/" + src.String()
	if err := git.FetchBranches(ctx, l.Path, nil, srcPath, "+refs/heads/*:"+ns+"/heads/*", "+refs/tags/*:"+ns+"/tags/*"); err != nil {
		return fmt.Errorf("fill pool: %w", err)
	}
	return nil
}

// dropPool removes network and trashes its pool when no repository is a
// member any more.
func (s *Service) dropPool(ctx context.Context, network uuid.UUID) {
	if s.o.Tier == nil {
		return
	}
	l, err := s.o.Tier.LockPool(ctx, network)
	if err != nil {
		s.log.WarnContext(ctx, "lock fork network pool", "network", network, "err", err)
		return
	}
	defer l.Release()
	n, err := s.q.DeleteEmptyForkNetwork(ctx, network)
	if err != nil {
		s.log.WarnContext(ctx, "delete fork network", "network", network, "err", err)
		return
	}
	if n == 0 {
		return
	}
	if err := s.o.Tier.TrashPool(ctx, network); err != nil {
		s.log.WarnContext(ctx, "trash fork network pool", "network", network, "err", err)
	}
}

// ---- repository deletion ----

// RepoDeleting implements repos.DeleteHook. The open pull requests of
// other repositories whose head lives in the deleted repository are closed
// (their head can no longer move or be merged); after the commit their
// test merge refs are removed and an empty fork network drops its pool.
// The pull requests of the deleted repository itself go with its issues.
func (s *Service) RepoDeleting(ctx context.Context, q *dbgen.Queries, repoID uuid.UUID) (func(context.Context), error) {
	ps, err := q.PullsWithHeadRepoForDelete(ctx, nullID(repoID))
	if err != nil {
		return nil, fmt.Errorf("pulls with head repo: %w", err)
	}
	var closed []dbgen.PullRequest
	for _, p := range ps {
		row, err := q.GetIssueByID(ctx, p.IssueID)
		if err != nil {
			return nil, fmt.Errorf("get pull request issue: %w", err)
		}
		if err := s.timeline(ctx, q, row, uuid.Nil, "head_ref_deleted", map[string]any{"head_ref": p.HeadRef, "reason": "repository_deleted"}); err != nil {
			return nil, err
		}
		if err := s.setState(ctx, q, uuid.Nil, &row, p, StateClosed); err != nil {
			return nil, err
		}
		closed = append(closed, p)
	}
	network := uuid.Nil
	if m, err := q.GetForkNetworkMember(ctx, repoID); err == nil {
		network = m.NetworkID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get fork network: %w", err)
	}
	if len(closed) == 0 && network == uuid.Nil {
		return nil, nil
	}
	return func(ctx context.Context) {
		if s.o.Bus != nil && len(closed) > 0 {
			s.o.Bus.Kick(ctx)
		}
		for _, p := range closed {
			if err := s.dropMergeRef(ctx, p); err != nil {
				s.log.WarnContext(ctx, "removing the test merge ref failed", "repo", p.RepoID, "number", p.Number, "err", err)
			}
		}
		if network != uuid.Nil {
			s.dropPool(ctx, network)
		}
		if len(closed) > 0 {
			s.log.InfoContext(ctx, "pull requests closed with their head repository", "repo", repoID, "count", len(closed))
		}
	}, nil
}

// dropMergeRef deletes refs/pull/<n>/merge of a pull request that can no
// longer be merged; refs/pull/<n>/head stays so the commits remain
// readable.
func (s *Service) dropMergeRef(ctx context.Context, p dbgen.PullRequest) error {
	l, err := s.acquire(ctx, p.RepoID, gitstore.Write)
	if err != nil {
		return err
	}
	defer l.Release()
	ref := pullRef(p.Number, "merge")
	cur, err := git.ResolveRef(ctx, l.Path, nil, ref)
	if err != nil || cur == "" {
		return err
	}
	sess, env, done, err := s.session(ctx, l, uuid.Nil, "web")
	if err != nil {
		return err
	}
	defer done()
	if err := git.UpdateRefs(ctx, l.Path, env, []git.RefChange{{Ref: ref, Old: cur}}); err != nil {
		if sess != nil && sess.Rejected() != "" {
			return fmt.Errorf("delete merge ref: rejected: %s", sess.Rejected())
		}
		return fmt.Errorf("delete merge ref: %w", err)
	}
	return nil
}
