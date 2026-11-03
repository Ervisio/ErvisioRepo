package tier_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// TestIntegrationPostgres runs the tier on local targets against a real
// PostgreSQL with the migrations applied, checking the SQL semantics the
// unit tests fake.
func TestIntegrationPostgres(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, config.Database{URL: dbtest.URL(t), MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)

	cfg := config.Storage{
		Targets: []config.Target{
			{Name: "cache", Kind: "local", Path: t.TempDir(), Budget: 1 << 20},
			{Name: "main", Kind: "local", Path: t.TempDir()},
			{Name: "copy", Kind: "local", Path: t.TempDir()},
		},
		Policy: map[string]config.Policy{
			"lfs": {Primary: "main", Replicas: []string{"copy"}, Cache: "cache"},
		},
	}
	stores, err := tier.BuildStores(ctx, cfg, tier.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tier.CloseStores(stores) })
	var enqueued []tier.ObjectRef
	tr, err := tier.New(tier.Options{Config: cfg, Stores: stores, Queries: q,
		OnStored: func(_ context.Context, r tier.ObjectRef) error { enqueued = append(enqueued, r); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.LoadCaches(ctx); err != nil {
		t.Fatal(err)
	}
	lfs, _ := tr.Class("lfs")

	data := bytes.Repeat([]byte("ervisio"), 10_000)
	sum := sha256.Sum256(data)
	obj, err := lfs.Put(ctx, "sha256/aa/obj", bytes.NewReader(data), int64(len(data)), sum[:])
	if err != nil {
		t.Fatal(err)
	}
	if obj.ReplicasOK || len(enqueued) != 1 {
		t.Fatalf("obj = %+v, enqueued = %v", obj, enqueued)
	}

	// Interrupted upload: no row.
	bad := bytes.Clone(data)
	bad[0] ^= 1
	if _, err := lfs.Put(ctx, "sha256/aa/bad", bytes.NewReader(bad), int64(len(bad)), sum[:]); err == nil {
		t.Fatal("corrupt upload accepted")
	}
	if _, err := lfs.Stat(ctx, "sha256/aa/bad"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("stat corrupt: %v", err)
	}

	// Cache lost: fallback to primary.
	if err := stores["cache"].Delete(ctx, obj.Key); err != nil {
		t.Fatal(err)
	}
	rc, _, err := lfs.Get(ctx, "sha256/aa/obj", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("fallback read: %v", err)
	}
	if _, err := stores["cache"].Stat(ctx, obj.Key); err != nil {
		t.Fatalf("cache not refilled: %v", err)
	}

	future := pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	refs, err := tr.PendingReplicas(ctx, future, 10)
	if err != nil || len(refs) != 1 {
		t.Fatalf("pending = %v, %v", refs, err)
	}
	if err := tr.Replicate(ctx, refs[0]); err != nil {
		t.Fatal(err)
	}
	if refs, _ := tr.PendingReplicas(ctx, future, 10); len(refs) != 0 {
		t.Fatalf("still pending: %v", refs)
	}
	// Same content again keeps the replica state.
	if o, err := lfs.Put(ctx, "sha256/aa/obj", bytes.NewReader(data), int64(len(data)), nil); err != nil || !o.ReplicasOK {
		t.Fatalf("re-put = %+v, %v", o, err)
	}

	rep, err := tr.VerifySample(ctx, future, 10)
	if err != nil || rep.Checked != 1 || rep.OK != 1 {
		t.Fatalf("verify = %+v, %v", rep, err)
	}
	st, err := tr.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Policies) != 1 || st.Policies[0].Objects != 1 || st.Policies[0].Bytes != int64(len(data)) ||
		len(st.Warnings) != 1 || st.Warnings[0].Code != tier.WarnLocalOnly {
		t.Fatalf("status = %+v", st)
	}
	if err := lfs.Delete(ctx, "sha256/aa/obj"); err != nil {
		t.Fatal(err)
	}
	if _, err := lfs.Stat(ctx, "sha256/aa/obj"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("stat after delete: %v", err)
	}
}
