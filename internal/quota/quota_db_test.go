package quota

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
)

func TestReserveDB(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, config.Database{URL: dbtest.URL(t), MaxConns: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)
	ns := uuid.New()
	if err := q.InsertNamespace(ctx, dbgen.InsertNamespaceParams{ID: ns, Kind: "org", Name: "quota-" + ns.String()[:8]}); err != nil {
		t.Fatal(err)
	}
	s, err := Get(ctx, q, ns)
	if err != nil || s.MaxRepos != Unlimited || s.MaxStorage != Unlimited || s.Repos != 0 {
		t.Fatalf("defaults %+v %v", s, err)
	}
	if _, err := q.SetQuotaDefault(ctx, dbgen.SetQuotaDefaultParams{Kind: "org", MaxRepos: pgtype.Int4{Int32: 100, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetQuotaOverride(ctx, dbgen.SetQuotaOverrideParams{NamespaceID: ns,
		MaxRepos: pgtype.Int4{Int32: 3, Valid: true}, MaxStorageBytes: pgtype.Int8{Int64: 1000, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	// Concurrent reservations never go beyond the limit.
	var ok atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Reserve(ctx, q, ns, Delta{Repos: 1, Bytes: 10}); err == nil {
				ok.Add(1)
			} else if !IsExceeded(err) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 3 {
		t.Fatalf("%d reservations succeeded", ok.Load())
	}
	if err := Check(ctx, q, ns, Delta{Bytes: 971}); !IsExceeded(err) {
		t.Fatalf("storage check %v", err)
	}
	if err := Release(ctx, q, ns, Delta{Repos: 10, Bytes: 10}); err != nil {
		t.Fatal(err)
	}
	s, _ = Get(ctx, q, ns)
	if s.Repos != 0 || s.StorageBytes != 20 || s.MaxRepos != 3 {
		t.Fatalf("after release %+v", s)
	}
	// Adjust ignores the limits; Reserve then refuses growth.
	if err := Adjust(ctx, q, ns, Delta{Bytes: 5000}); err != nil {
		t.Fatal(err)
	}
	if s, _ = Get(ctx, q, ns); s.StorageBytes != 5020 {
		t.Fatalf("after adjust %+v", s)
	}
	if err := Reserve(ctx, q, ns, Delta{Bytes: 1}); !IsExceeded(err) {
		t.Fatalf("reserve over limit %v", err)
	}
	if err := Adjust(ctx, q, ns, Delta{Bytes: -10000}); err != nil {
		t.Fatal(err)
	}
	if s, _ = Get(ctx, q, ns); s.StorageBytes != 0 {
		t.Fatalf("adjust below zero %+v", s)
	}
	// Without an override the default applies.
	if _, err := q.DeleteQuotaOverride(ctx, ns); err != nil {
		t.Fatal(err)
	}
	if s, _ = Get(ctx, q, ns); s.MaxRepos != 100 || s.MaxStorage != Unlimited {
		t.Fatalf("default %+v", s)
	}
}
