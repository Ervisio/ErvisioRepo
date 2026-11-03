package db_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
)

func TestOpenInvalidURL(t *testing.T) {
	if _, err := db.Open(context.Background(), config.Database{URL: "postgres://%zz"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestMigrateAndInstanceID(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, config.Database{URL: dbtest.URL(t), MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.DiscardHandler)

	before, err := db.Status(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range before {
		if m.Applied {
			t.Fatalf("fresh database has applied migration %+v", m)
		}
	}

	// Migrating twice must be a no-op the second time.
	for range 2 {
		if err := db.Migrate(ctx, pool, log); err != nil {
			t.Fatal(err)
		}
	}
	after, err := db.Status(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	components := map[string]bool{}
	for _, m := range after {
		components[m.Component] = true
		if !m.Applied {
			t.Errorf("migration not applied: %+v", m)
		}
	}
	if !components["app"] || !components["river"] {
		t.Errorf("missing components in status: %v", components)
	}

	q := dbgen.New(pool)
	var (
		wg  sync.WaitGroup
		ids [4]uuid.UUID
	)
	for i := range ids {
		wg.Go(func() {
			id, err := db.EnsureInstanceID(ctx, q)
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("instance id not stable: %v", ids)
		}
	}
	if ids[0].Version() != 7 {
		t.Errorf("instance id %s is not a UUID v7", ids[0])
	}
}
