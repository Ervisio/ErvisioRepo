// Package quota enforces the per-user and per-organization limits (PLAN
// §8.2): the number of repositories and the storage space. The limit of a
// namespace is its override, else the instance default for its kind; a
// missing limit means unlimited. Usage lives in usage_counters and is
// changed only through Reserve, which never lets it exceed the limits.
package quota

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Resource is a limited resource.
type Resource string

// Limited resources.
const (
	Repos   Resource = "repos"
	Storage Resource = "storage_bytes"
)

// Unlimited is the value of a limit that does not apply.
const Unlimited int64 = -1

// ErrNoNamespace is returned for a namespace that does not exist.
var ErrNoNamespace = errors.New("namespace not found")

// ExceededError reports that a change would go beyond a limit.
type ExceededError struct {
	Resource Resource
	Limit    int64
	Used     int64
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("quota exceeded: %s %d of %d used", e.Resource, e.Used, e.Limit)
}

// IsExceeded reports whether err is (or wraps) an ExceededError.
func IsExceeded(err error) bool {
	var e *ExceededError
	return errors.As(err, &e)
}

// Status is the limits and usage of a namespace.
type Status struct {
	Namespace    uuid.UUID
	Kind         string
	MaxRepos     int64 // Unlimited when no limit applies
	MaxStorage   int64 // bytes, Unlimited when no limit applies
	Repos        int64
	StorageBytes int64
}

// Delta is a change of usage; negative values release usage.
type Delta struct {
	Repos int32
	Bytes int64
}

// Queries is the subset of dbgen.Querier used by the package.
type Queries interface {
	GetEffectiveQuota(ctx context.Context, id uuid.UUID) (dbgen.GetEffectiveQuotaRow, error)
	EnsureUsageCounter(ctx context.Context, namespaceID uuid.UUID) error
	ReserveUsage(ctx context.Context, arg dbgen.ReserveUsageParams) (int64, error)
}

// Get returns the limits and usage of a namespace.
func Get(ctx context.Context, q Queries, ns uuid.UUID) (Status, error) {
	row, err := q.GetEffectiveQuota(ctx, ns)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, ErrNoNamespace
	}
	if err != nil {
		return Status{}, fmt.Errorf("get quota: %w", err)
	}
	return Status{
		Namespace: row.ID, Kind: row.Kind,
		MaxRepos: int64(row.MaxRepos), MaxStorage: row.MaxStorageBytes,
		Repos: int64(row.Repos), StorageBytes: row.StorageBytes,
	}, nil
}

// Check returns an ExceededError if applying d to s would exceed a limit.
// Releases never fail.
func (s Status) Check(d Delta) error {
	if d.Repos > 0 && s.MaxRepos != Unlimited && s.Repos+int64(d.Repos) > s.MaxRepos {
		return &ExceededError{Resource: Repos, Limit: s.MaxRepos, Used: s.Repos}
	}
	if d.Bytes > 0 && s.MaxStorage != Unlimited && s.StorageBytes+d.Bytes > s.MaxStorage {
		return &ExceededError{Resource: Storage, Limit: s.MaxStorage, Used: s.StorageBytes}
	}
	return nil
}

// Check reports whether d fits in the quota of ns, without changing usage.
// It is advisory (for early, friendly errors); Reserve is authoritative.
func Check(ctx context.Context, q Queries, ns uuid.UUID, d Delta) error {
	s, err := Get(ctx, q, ns)
	if err != nil {
		return err
	}
	return s.Check(d)
}

// Reserve applies d to the usage of ns atomically, failing with an
// ExceededError (and changing nothing) if a limit would be exceeded. Run it
// in the transaction that creates the resource, so that a rollback also
// releases the reservation.
func Reserve(ctx context.Context, q Queries, ns uuid.UUID, d Delta) error {
	if err := q.EnsureUsageCounter(ctx, ns); err != nil {
		return fmt.Errorf("ensure usage counter: %w", err)
	}
	n, err := q.ReserveUsage(ctx, dbgen.ReserveUsageParams{NamespaceID: ns, Repos: d.Repos, Bytes: d.Bytes})
	if err != nil {
		return fmt.Errorf("reserve usage: %w", err)
	}
	if n == 1 {
		return nil
	}
	s, err := Get(ctx, q, ns)
	if err != nil {
		return err
	}
	if err := s.Check(d); err != nil {
		return err
	}
	// The limits changed between the update and the read: report the
	// conservative outcome.
	return &ExceededError{Resource: Repos, Limit: s.MaxRepos, Used: s.Repos}
}

// Release gives back usage; it is Reserve with the delta negated and never
// fails on limits.
func Release(ctx context.Context, q Queries, ns uuid.UUID, d Delta) error {
	return Reserve(ctx, q, ns, Delta{Repos: -max(d.Repos, 0), Bytes: -max(d.Bytes, 0)})
}
