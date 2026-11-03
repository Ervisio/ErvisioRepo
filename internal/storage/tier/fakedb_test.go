package tier

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// fakeDB implements Queries in memory with the semantics of storage.sql.
type fakeDB struct {
	mu        sync.Mutex
	rows      map[[2]string]dbgen.StorageObject
	now       func() time.Time
	upsertErr error
}

func newFakeDB(now func() time.Time) *fakeDB {
	return &fakeDB{rows: map[[2]string]dbgen.StorageObject{}, now: now}
}

func (f *fakeDB) ts() pgtype.Timestamptz { return pgtype.Timestamptz{Time: f.now(), Valid: true} }

func (f *fakeDB) GetStorageObject(_ context.Context, a dbgen.GetStorageObjectParams) (dbgen.StorageObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[[2]string{a.Target, a.Key}]
	if !ok {
		return r, pgx.ErrNoRows
	}
	return r, nil
}

func (f *fakeDB) UpsertStorageObject(_ context.Context, a dbgen.UpsertStorageObjectParams) (dbgen.StorageObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return dbgen.StorageObject{}, f.upsertErr
	}
	k := [2]string{a.Target, a.Key}
	r, ok := f.rows[k]
	repl := a.ReplicasOk
	if ok && bytes.Equal(r.Sha256, a.Sha256) && r.Size == a.Size {
		repl = r.ReplicasOk || a.ReplicasOk
	}
	if !ok {
		r.CreatedAt = f.ts()
	}
	r.Target, r.Key, r.Policy, r.Size, r.Sha256, r.ReplicasOk = a.Target, a.Key, a.Policy, a.Size, a.Sha256, repl
	r.VerifiedAt, r.LastError, r.UpdatedAt = f.ts(), pgtype.Text{}, f.ts()
	f.rows[k] = r
	return r, nil
}

func (f *fakeDB) DeleteStorageObject(_ context.Context, a dbgen.DeleteStorageObjectParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, [2]string{a.Target, a.Key})
	return nil
}

func (f *fakeDB) update(target, key string, sha []byte, fn func(*dbgen.StorageObject)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := [2]string{target, key}
	r, ok := f.rows[k]
	if !ok || (sha != nil && !bytes.Equal(r.Sha256, sha)) {
		return
	}
	fn(&r)
	r.UpdatedAt = f.ts()
	f.rows[k] = r
}

func (f *fakeDB) MarkStorageObjectReplicated(_ context.Context, a dbgen.MarkStorageObjectReplicatedParams) error {
	f.update(a.Target, a.Key, a.Sha256, func(r *dbgen.StorageObject) { r.ReplicasOk, r.LastError = true, pgtype.Text{} })
	return nil
}

func (f *fakeDB) MarkStorageObjectVerified(_ context.Context, a dbgen.MarkStorageObjectVerifiedParams) error {
	f.update(a.Target, a.Key, a.Sha256, func(r *dbgen.StorageObject) { r.VerifiedAt, r.LastError = f.ts(), pgtype.Text{} })
	return nil
}

func (f *fakeDB) SetStorageObjectError(_ context.Context, a dbgen.SetStorageObjectErrorParams) error {
	f.update(a.Target, a.Key, nil, func(r *dbgen.StorageObject) { r.LastError = a.LastError })
	return nil
}

func (f *fakeDB) SetStorageObjectReplicasPending(_ context.Context, a dbgen.SetStorageObjectReplicasPendingParams) error {
	f.update(a.Target, a.Key, nil, func(r *dbgen.StorageObject) { r.ReplicasOk, r.LastError = false, a.LastError })
	return nil
}

func (f *fakeDB) sorted() []dbgen.StorageObject {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := slices.SortedFunc(maps.Keys(f.rows), func(a, b [2]string) int {
		if a[0] != b[0] {
			return compare(a[0], b[0])
		}
		return compare(a[1], b[1])
	})
	out := make([]dbgen.StorageObject, len(keys))
	for i, k := range keys {
		out[i] = f.rows[k]
	}
	return out
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (f *fakeDB) ListStorageObjectsPendingReplicas(_ context.Context, a dbgen.ListStorageObjectsPendingReplicasParams) ([]dbgen.StorageObject, error) {
	var out []dbgen.StorageObject
	for _, r := range f.sorted() {
		if !r.ReplicasOk && r.CreatedAt.Time.Before(a.CreatedBefore.Time) && len(out) < int(a.MaxRows) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeDB) SampleStorageObjectsForVerify(_ context.Context, a dbgen.SampleStorageObjectsForVerifyParams) ([]dbgen.StorageObject, error) {
	var out []dbgen.StorageObject
	for _, r := range f.sorted() {
		if !r.VerifiedAt.Valid || r.VerifiedAt.Time.Before(a.VerifiedBefore.Time) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		vi, vj := out[i].VerifiedAt, out[j].VerifiedAt
		if !vi.Valid || !vj.Valid {
			return !vi.Valid && vj.Valid
		}
		return vi.Time.Before(vj.Time)
	})
	if len(out) > int(a.MaxRows) {
		out = out[:a.MaxRows]
	}
	return out, nil
}

func (f *fakeDB) ListStorageObjectsByKeys(_ context.Context, a dbgen.ListStorageObjectsByKeysParams) ([]dbgen.StorageObject, error) {
	var out []dbgen.StorageObject
	for _, r := range f.sorted() {
		if r.Target == a.Target && slices.Contains(a.Keys, r.Key) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeDB) StorageObjectStats(context.Context) ([]dbgen.StorageObjectStatsRow, error) {
	by := map[string]*dbgen.StorageObjectStatsRow{}
	for _, r := range f.sorted() {
		s := by[r.Policy]
		if s == nil {
			s = &dbgen.StorageObjectStatsRow{Policy: r.Policy}
			by[r.Policy] = s
		}
		s.Objects++
		s.Bytes += r.Size
		if !r.ReplicasOk {
			s.PendingReplicas++
		}
		if r.LastError.Valid {
			s.Errors++
		}
	}
	var out []dbgen.StorageObjectStatsRow
	for _, p := range slices.Sorted(maps.Keys(by)) {
		out = append(out, *by[p])
	}
	return out, nil
}

func (f *fakeDB) row(target, key string) (dbgen.StorageObject, bool) {
	r, err := f.GetStorageObject(context.Background(), dbgen.GetStorageObjectParams{Target: target, Key: key})
	return r, !errors.Is(err, pgx.ErrNoRows)
}

func (f *fakeDB) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

var _ Queries = (*fakeDB)(nil)
