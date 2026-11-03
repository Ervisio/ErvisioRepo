package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
)

type fakeSettings struct {
	got dbgen.UpsertSettingParams
	err error
}

func (f *fakeSettings) UpsertSetting(_ context.Context, arg dbgen.UpsertSettingParams) error {
	f.got = arg
	return f.err
}

func TestHeartbeatWorker(t *testing.T) {
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	fs := &fakeSettings{}
	w := &HeartbeatWorker{Settings: fs, Now: func() time.Time { return at }}
	if err := w.Work(context.Background(), &river.Job[HeartbeatArgs]{}); err != nil {
		t.Fatal(err)
	}
	var stored time.Time
	if err := json.Unmarshal(fs.got.Value, &stored); err != nil || !stored.Equal(at) || fs.got.Key != SettingHeartbeatAt {
		t.Fatalf("stored %q=%s (%v)", fs.got.Key, fs.got.Value, err)
	}

	fs.err = errors.New("boom")
	if err := w.Work(context.Background(), &river.Job[HeartbeatArgs]{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunnerHeartbeat(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, config.Database{URL: dbtest.URL(t), MaxConns: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.DiscardHandler)
	if err := db.Migrate(ctx, pool, log); err != nil {
		t.Fatal(err)
	}

	r, err := New(pool, log)
	if err != nil {
		t.Fatal(err)
	}
	events, cancel := r.Client().Subscribe(river.EventKindJobCompleted)
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.Running() {
		t.Fatal("Running() = false after Start")
	}

	select {
	case <-events:
	case <-time.After(30 * time.Second):
		t.Fatal("heartbeat job did not run on start")
	}
	if _, err := dbgen.New(pool).GetSetting(ctx, SettingHeartbeatAt); err != nil {
		t.Fatalf("heartbeat not stored: %v", err)
	}

	stopCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	if err := r.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if r.Running() {
		t.Fatal("Running() = true after Stop")
	}
}
