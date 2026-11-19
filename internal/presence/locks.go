package presence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// CreateLock locks path on branch for the caller. When the path is already
// locked there (by anyone) it returns the existing lock and ErrLockExists.
func (s *Service) CreateLock(ctx context.Context, who auth.Principal, owner, name, path, branch string) (Lock, error) {
	a, err := s.lookup(ctx, who, owner, name)
	if err != nil {
		return Lock{}, err
	}
	if err := a.writer(); err != nil {
		return Lock{}, err
	}
	if err := checkPath(path); err != nil {
		return Lock{}, err
	}
	if err := git.ValidateBranchName(branch); err != nil || len(branch) > MaxBranch {
		return Lock{}, invalid("invalid branch name")
	}
	ref := headsPrefix + branch
	id, err := uuid.NewV7()
	if err != nil {
		return Lock{}, fmt.Errorf("lock id: %w", err)
	}
	var out Lock
	exists := false
	err = s.inTx(ctx, func(q *dbgen.Queries) error {
		_, err := q.CreateLFSLock(ctx, dbgen.CreateLFSLockParams{ID: id, RepoID: a.repo.ID, Path: path, Refname: ref, OwnerID: who.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			ex, err := q.GetPresenceLockByPath(ctx, dbgen.GetPresenceLockByPathParams{RepoID: a.repo.ID, Refname: ref, Path: path})
			if err != nil {
				return fmt.Errorf("get existing lock: %w", err)
			}
			out, exists = lockFrom(ex.ID, ex.Path, ex.Refname, ex.OwnerID, ex.CreatedAt, ex.Login, ex.DisplayName, ex.AvatarUrl), true
			return nil
		}
		if err != nil {
			return fmt.Errorf("create lock: %w", err)
		}
		row, err := q.GetPresenceLock(ctx, dbgen.GetPresenceLockParams{RepoID: a.repo.ID, ID: id})
		if err != nil {
			return fmt.Errorf("get lock: %w", err)
		}
		out = lockFrom(row.ID, row.Path, row.Refname, row.OwnerID, row.CreatedAt, row.Login, row.DisplayName, row.AvatarUrl)
		return notify(ctx, q, note{Repo: a.repo.ID, Kind: kindLock, ID: id})
	})
	if err != nil {
		return Lock{}, err
	}
	if exists {
		return out, ErrLockExists
	}
	return out, nil
}

// Unlock releases a lock: the owner's own, or another user's with force by
// a caller allowed to force unlocks.
func (s *Service) Unlock(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, force bool) (Lock, error) {
	a, err := s.lookup(ctx, who, owner, name)
	if err != nil {
		return Lock{}, err
	}
	if err := a.writer(); err != nil {
		return Lock{}, err
	}
	var out Lock
	err = s.inTx(ctx, func(q *dbgen.Queries) error {
		row, err := q.GetPresenceLock(ctx, dbgen.GetPresenceLockParams{RepoID: a.repo.ID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLockNotFound
		}
		if err != nil {
			return fmt.Errorf("get lock: %w", err)
		}
		out = lockFrom(row.ID, row.Path, row.Refname, row.OwnerID, row.CreatedAt, row.Login, row.DisplayName, row.AvatarUrl)
		if row.OwnerID != who.UserID && (!force || !a.force) {
			return fmt.Errorf("%w: the lock belongs to %s", ErrForbidden, row.Login)
		}
		n, err := q.DeleteLFSLock(ctx, dbgen.DeleteLFSLockParams{RepoID: a.repo.ID, ID: id})
		if err != nil {
			return fmt.Errorf("delete lock: %w", err)
		}
		if n == 0 {
			return ErrLockNotFound
		}
		return notify(ctx, q, note{Repo: a.repo.ID, Kind: kindUnlock, ID: id})
	})
	if err != nil {
		return out, err
	}
	if out.OwnerID != who.UserID {
		s.log.InfoContext(ctx, "presence: lock broken", "repo", a.repo.ID, "lock", id, "by", who.UserID, "owner", out.OwnerID)
	}
	return out, nil
}

// LockCreated tells the streams about a lock made through the Git LFS
// Locks API.
func (s *Service) LockCreated(ctx context.Context, repoID, lockID uuid.UUID) {
	if err := notify(ctx, s.q, note{Repo: repoID, Kind: kindLock, ID: lockID}); err != nil {
		s.log.WarnContext(ctx, "presence: notify lock", "repo", repoID, "error", err)
	}
}

// LockReleased tells the streams about a lock released through the Git
// LFS Locks API.
func (s *Service) LockReleased(ctx context.Context, repoID, lockID uuid.UUID) {
	if err := notify(ctx, s.q, note{Repo: repoID, Kind: kindUnlock, ID: lockID}); err != nil {
		s.log.WarnContext(ctx, "presence: notify unlock", "repo", repoID, "error", err)
	}
}

// releaseRef releases the locks of a deleted ref.
func (s *Service) releaseRef(ctx context.Context, repoID uuid.UUID, ref string) error {
	return s.inTx(ctx, func(q *dbgen.Queries) error {
		ids, err := q.DeleteRefLocks(ctx, dbgen.DeleteRefLocksParams{RepoID: repoID, Refname: ref})
		if err != nil {
			return fmt.Errorf("release locks: %w", err)
		}
		for _, id := range ids {
			if err := notify(ctx, q, note{Repo: repoID, Kind: kindUnlock, ID: id}); err != nil {
				return err
			}
		}
		return nil
	})
}
