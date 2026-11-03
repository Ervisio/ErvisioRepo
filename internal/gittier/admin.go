package gittier

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// Audit actions of the tiering administration.
const (
	ActionRepoPinned   = "repo.pinned"
	ActionRepoUnpinned = "repo.unpinned"
	ActionRepoEvicted  = "repo.evicted"
)

// RepoState is the tiering state of a repository.
type RepoState struct {
	ID          uuid.UUID
	State       string
	Pinned      bool
	SizeBytes   int64
	ManifestSeq int64
}

// Admin serves the instance-admin tiering operations (pin, unpin, evict).
type Admin struct {
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	evictor *Evictor
}

// NewAdmin returns an Admin evicting through ev.
func NewAdmin(pool *pgxpool.Pool, ev *Evictor) *Admin {
	return &Admin{pool: pool, q: dbgen.New(pool), evictor: ev}
}

// Resolve returns the ID of owner/name, or gitstore.ErrNotFound.
func (a *Admin) Resolve(ctx context.Context, owner, name string) (uuid.UUID, error) {
	r, err := a.q.GetRepoByPath(ctx, dbgen.GetRepoByPathParams{Owner: owner, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%s/%s: %w", owner, name, gitstore.ErrNotFound)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("load repository %s/%s: %w", owner, name, err)
	}
	return r.Repo.ID, nil
}

// State returns the tiering state of id.
func (a *Admin) State(ctx context.Context, id uuid.UUID) (RepoState, error) {
	r, err := a.q.GetRepoStorage(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RepoState{}, fmt.Errorf("%s: %w", id, gitstore.ErrNotFound)
	}
	if err != nil {
		return RepoState{}, fmt.Errorf("load repository state %s: %w", id, err)
	}
	return RepoState{ID: r.ID, State: r.StorageState, Pinned: r.Pinned, SizeBytes: r.SizeBytes, ManifestSeq: r.ManifestSeq}, nil
}

// SetPinned pins (never evicted automatically) or unpins id on behalf of
// actor, recording it in the audit log.
func (a *Admin) SetPinned(ctx context.Context, actor uuid.UUID, ip string, id uuid.UUID, pinned bool) (RepoState, error) {
	action := ActionRepoUnpinned
	if pinned {
		action = ActionRepoPinned
	}
	err := pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		q := a.q.WithTx(tx)
		if err := q.SetRepoPinned(ctx, dbgen.SetRepoPinnedParams{ID: id, Pinned: pinned}); err != nil {
			return fmt.Errorf("pin %s: %w", id, err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: actor, Action: action, TargetType: "repo", TargetID: id.String(), IP: ip})
	})
	if err != nil {
		return RepoState{}, err
	}
	return a.State(ctx, id)
}

// Evict forces the eviction of id, pinned or not; the safety checks still
// apply and fail with ErrNotEvictable.
func (a *Admin) Evict(ctx context.Context, actor uuid.UUID, ip string, id uuid.UUID) (RepoState, error) {
	if a.evictor == nil {
		return RepoState{}, fmt.Errorf("%w: eviction is not configured", ErrNotEvictable)
	}
	if err := a.evictor.Evict(ctx, id, true); err != nil {
		return RepoState{}, err
	}
	if err := audit.Record(ctx, a.q, audit.Event{Actor: actor, Action: ActionRepoEvicted, TargetType: "repo", TargetID: id.String(), IP: ip}); err != nil {
		return RepoState{}, err
	}
	return a.State(ctx, id)
}
