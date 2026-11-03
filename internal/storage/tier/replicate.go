package tier

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// maxErrorLen bounds the error text stored in storage_objects.last_error.
const maxErrorLen = 500

// copyVerified copies one object from src to dst, checking size and sha256
// while streaming: a mismatch aborts the write on dst, so dst never holds a
// corrupt copy. A dst that already has an object of the right size is
// re-hashed and kept when it matches.
func copyVerified(ctx context.Context, src, dst *Target, key string, size int64, sum []byte) error {
	if info, err := dst.Store.Stat(ctx, key); err == nil && info.Size == size {
		if err := hashObject(ctx, dst, key, size, sum); err == nil {
			return nil
		}
	}
	rc, _, err := src.Store.Get(ctx, key, nil)
	if err != nil {
		return fmt.Errorf("read %s from %s: %w", key, src.Name, err)
	}
	defer rc.Close()
	vr := newVerifyReader(rc, size, sum)
	if _, err := dst.Store.Put(ctx, key, vr, size, blob.PutOpts{SHA256: hex.EncodeToString(sum)}); err != nil {
		return fmt.Errorf("write %s to %s: %w", key, dst.Name, err)
	}
	if !vr.Verified() {
		_ = dst.Store.Delete(context.WithoutCancel(ctx), key)
		return fmt.Errorf("%w: %s was not copied in full to %s", ErrCorrupt, key, dst.Name)
	}
	return nil
}

// hashObject downloads the object and checks its size and sha256.
func hashObject(ctx context.Context, tgt *Target, key string, size int64, sum []byte) error {
	rc, _, err := tgt.Store.Get(ctx, key, nil)
	if err != nil {
		return fmt.Errorf("read %s from %s: %w", key, tgt.Name, err)
	}
	defer rc.Close()
	if _, err := io.Copy(io.Discard, newVerifyReader(rc, size, sum)); err != nil {
		return fmt.Errorf("verify %s on %s: %w", key, tgt.Name, err)
	}
	return nil
}

func (t *Tier) loadRow(ctx context.Context, ref ObjectRef) (dbgen.StorageObject, *Class, error) {
	row, err := t.q.GetStorageObject(ctx, dbgen.GetStorageObjectParams{Target: ref.Target, Key: ref.Key})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return row, nil, fmt.Errorf("%s/%s: %w", ref.Target, ref.Key, blob.ErrNotFound)
		}
		return row, nil, fmt.Errorf("load storage object %s: %w", ref.Key, err)
	}
	c, ok := t.classes[row.Policy]
	if !ok {
		return row, nil, fmt.Errorf("%w %q (object %s)", ErrUnknownPolicy, row.Policy, row.Key)
	}
	return row, c, nil
}

func (t *Tier) recordError(ctx context.Context, row dbgen.StorageObject, err error) {
	msg := err.Error()
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen]
	}
	if rerr := t.q.SetStorageObjectError(context.WithoutCancel(ctx), dbgen.SetStorageObjectErrorParams{
		Target: row.Target, Key: row.Key, LastError: pgtype.Text{String: msg, Valid: true},
	}); rerr != nil {
		t.log.ErrorContext(ctx, "record storage error", "key", row.Key, "error", rerr)
	}
}

// Replicate copies one object from its primary to every replica of its
// policy and marks it replicated. A deleted object is not an error.
func (t *Tier) Replicate(ctx context.Context, ref ObjectRef) error {
	row, c, err := t.loadRow(ctx, ref)
	if errors.Is(err, blob.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	src := t.targets[row.Target]
	if src == nil {
		return fmt.Errorf("replicate %s: unknown target %q", row.Key, row.Target)
	}
	var errs []error
	for _, rep := range c.replicas {
		if rep.Name == src.Name {
			continue
		}
		if err := copyVerified(ctx, src, rep, row.Key, row.Size, row.Sha256); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.recordError(ctx, row, err)
		return fmt.Errorf("replicate %s: %w", row.Key, err)
	}
	if err := t.q.MarkStorageObjectReplicated(ctx, dbgen.MarkStorageObjectReplicatedParams{
		Target: row.Target, Key: row.Key, Sha256: row.Sha256,
	}); err != nil {
		return fmt.Errorf("mark %s replicated: %w", row.Key, err)
	}
	return nil
}

// PendingReplicas lists objects created before the cutoff that still miss a
// replica, for the sweep that re-enqueues lost replication jobs.
func (t *Tier) PendingReplicas(ctx context.Context, createdBefore pgtype.Timestamptz, limit int32) ([]ObjectRef, error) {
	rows, err := t.q.ListStorageObjectsPendingReplicas(ctx, dbgen.ListStorageObjectsPendingReplicasParams{
		CreatedBefore: createdBefore, MaxRows: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list pending replicas: %w", err)
	}
	out := make([]ObjectRef, len(rows))
	for i, r := range rows {
		out[i] = ObjectRef{Target: r.Target, Key: r.Key}
	}
	return out, nil
}

// VerifyReport summarises a verification run.
type VerifyReport struct {
	Checked  int
	OK       int
	Repaired int // primary restored from a replica
	Failed   int // primary unreadable or corrupt and no good replica
	Replicas int // replica copies found bad and queued for re-replication
}

// VerifySample re-hashes up to limit objects not verified since the cutoff,
// on the primary and, for replicated objects, on every replica. A corrupt
// primary is repaired from a good replica; a corrupt replica is queued for
// replication again.
func (t *Tier) VerifySample(ctx context.Context, verifiedBefore pgtype.Timestamptz, limit int32) (VerifyReport, error) {
	var rep VerifyReport
	rows, err := t.q.SampleStorageObjectsForVerify(ctx, dbgen.SampleStorageObjectsForVerifyParams{
		VerifiedBefore: verifiedBefore, MaxRows: limit,
	})
	if err != nil {
		return rep, fmt.Errorf("sample storage objects: %w", err)
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		rep.Checked++
		c, ok := t.classes[row.Policy]
		primary := t.targets[row.Target]
		if !ok || primary == nil {
			rep.Failed++
			t.recordError(ctx, row, fmt.Errorf("no policy or target for %s/%s", row.Policy, row.Target))
			continue
		}

		var goodReplica *Target
		badReplica := false
		if row.ReplicasOk {
			for _, r := range c.replicas {
				if err := hashObject(ctx, r, row.Key, row.Size, row.Sha256); err != nil {
					t.log.ErrorContext(ctx, "replica verification failed", "target", r.Name, "key", row.Key, "error", err)
					badReplica = true
				} else if goodReplica == nil {
					goodReplica = r
				}
			}
		}

		perr := hashObject(ctx, primary, row.Key, row.Size, row.Sha256)
		if perr != nil {
			t.log.ErrorContext(ctx, "primary verification failed", "target", primary.Name, "key", row.Key, "error", perr)
			if c.cache != nil {
				_ = c.cache.drop(ctx, row.Key)
			}
			if goodReplica != nil {
				if err := copyVerified(ctx, goodReplica, primary, row.Key, row.Size, row.Sha256); err == nil {
					t.log.WarnContext(ctx, "primary repaired from replica", "target", primary.Name, "from", goodReplica.Name, "key", row.Key)
					rep.Repaired++
					perr = nil
				} else {
					perr = errors.Join(perr, err)
				}
			}
		}
		if perr != nil {
			rep.Failed++
			t.recordError(ctx, row, perr)
			continue
		}

		if err := t.q.MarkStorageObjectVerified(ctx, dbgen.MarkStorageObjectVerifiedParams{
			Target: row.Target, Key: row.Key, Sha256: row.Sha256,
		}); err != nil {
			return rep, fmt.Errorf("mark %s verified: %w", row.Key, err)
		}
		if badReplica {
			rep.Replicas++
			if err := t.q.SetStorageObjectReplicasPending(ctx, dbgen.SetStorageObjectReplicasPendingParams{
				Target: row.Target, Key: row.Key, LastError: pgtype.Text{String: "replica failed verification", Valid: true},
			}); err != nil {
				return rep, fmt.Errorf("mark %s for re-replication: %w", row.Key, err)
			}
			if t.onStored != nil {
				if err := t.onStored(ctx, ObjectRef{Target: row.Target, Key: row.Key}); err != nil {
					t.log.WarnContext(ctx, "enqueue replication", "key", row.Key, "error", err)
				}
			}
		}
		if !badReplica {
			rep.OK++
		}
	}
	return rep, nil
}
