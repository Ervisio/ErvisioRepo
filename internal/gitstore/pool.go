package gitstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"
)

// PoolPath is where the object pool of a fork network lives:
// <hot_path>/pools/<aa>/<network>.git. A pool is a local, append-only bare
// repository the forks of the network borrow objects from through
// objects/info/alternates; it has no row in repos and no remote journal
// (every repository's journal stays self-contained), so it can always be
// rebuilt. Only internal/git may touch it, and only under LockPool.
func (t *Tier) PoolPath(networkID uuid.UUID) string {
	return filepath.Join(t.o.HotPath, "pools", hex.EncodeToString(networkID[15:]), networkID.String()+".git")
}

// LockPool takes the exclusive lease of the object pool of a fork network.
// The network IDs are UUIDs of their own, so the lock never collides with
// a repository lock. The directory may not exist yet.
func (t *Tier) LockPool(ctx context.Context, networkID uuid.UUID) (*Lease, error) {
	l, err := t.lock(ctx, networkID, Exclusive)
	if err != nil {
		return nil, err
	}
	l.Path = t.PoolPath(networkID)
	return l, nil
}

// TrashPool moves the pool of a network that has no members left to the
// trash. The caller holds LockPool and has checked that no repository
// borrows from it any more. A missing directory is not an error.
func (t *Tier) TrashPool(ctx context.Context, networkID uuid.UUID) error {
	src := t.PoolPath(networkID)
	if err := os.MkdirAll(t.TrashDir(), 0o750); err != nil {
		return fmt.Errorf("trash pool %s: %w", networkID, err)
	}
	dst := filepath.Join(t.TrashDir(), "pool-"+networkID.String()+".git."+strconv.FormatInt(t.o.Now().UnixNano(), 10))
	if err := os.Rename(src, dst); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("trash pool %s: %w", networkID, err)
	}
	t.o.Log.InfoContext(ctx, "fork network pool moved to trash", "network", networkID, "path", dst)
	return nil
}
