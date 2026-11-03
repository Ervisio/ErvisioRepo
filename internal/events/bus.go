package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

// Tuning.
const (
	// DrainInterval is the period of the safety-net drain (events whose
	// Kick was lost, e.g. on a crash right after the commit).
	DrainInterval = time.Minute
	// DeliverMaxAttempts bounds the retries of one handler for one event.
	DeliverMaxAttempts = 10
	// Retention keeps dispatched outbox rows this long (for redeliveries
	// still in flight), then the drain prunes them.
	Retention  = 7 * 24 * time.Hour
	drainBatch = 100
	// drainRounds bounds one drain job; the next job continues.
	drainRounds = 50
)

// DrainArgs moves pending outbox events to delivery jobs.
type DrainArgs struct{}

// Kind implements river.JobArgs.
func (DrainArgs) Kind() string { return "events_drain" }

// DeliverArgs delivers one event to one handler.
type DeliverArgs struct {
	EventID uuid.UUID `json:"event_id"`
	Handler string    `json:"handler"`
}

// Kind implements river.JobArgs.
func (DeliverArgs) Kind() string { return "events_deliver" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (DeliverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: DeliverMaxAttempts}
}

type registration struct {
	h     Handler
	kinds map[string]bool // nil: every kind
}

// Bus dispatches outbox events to the registered handlers.
type Bus struct {
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	log    *slog.Logger
	mu     sync.RWMutex
	hs     map[string]registration
	client atomic.Pointer[river.Client[pgx.Tx]]
}

// NewBus builds a Bus on pool.
func NewBus(pool *pgxpool.Pool, log *slog.Logger) *Bus {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Bus{pool: pool, q: dbgen.New(pool), log: log, hs: map[string]registration{}}
}

// Register adds (or replaces) the handler called name for the given kinds
// (every kind when none is given). The name identifies the handler in the
// delivery jobs, so it must stay stable across releases. Events drained
// before the registration are not delivered to it.
func (b *Bus) Register(name string, h Handler, kinds ...string) {
	reg := registration{h: h}
	if len(kinds) > 0 {
		reg.kinds = make(map[string]bool, len(kinds))
		for _, k := range kinds {
			reg.kinds[k] = true
		}
	}
	b.mu.Lock()
	b.hs[name] = reg
	b.mu.Unlock()
}

func (b *Bus) handler(name string) (Handler, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, ok := b.hs[name]
	return r.h, ok
}

func (b *Bus) handlersFor(kind string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var names []string
	for n, r := range b.hs {
		if r.kinds == nil || r.kinds[kind] {
			names = append(names, n)
		}
	}
	return names
}

// JobsOption registers the drain and delivery workers and the periodic
// drain. Pass it to jobs.New, then call SetClient with the runner client.
func (b *Bus) JobsOption() jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &drainWorker{b: b})
		river.AddWorker(r.Workers, &deliverWorker{b: b})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(DrainInterval),
			func() (river.JobArgs, *river.InsertOpts) { return DrainArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true}))
	}
}

// SetClient sets the River client Kick enqueues with.
func (b *Bus) SetClient(c *river.Client[pgx.Tx]) { b.client.Store(c) }

// Kick asks for a prompt drain after a commit that emitted events. It is
// best effort: without a client, or on error, the periodic drain delivers
// the events within DrainInterval.
func (b *Bus) Kick(ctx context.Context) {
	if b == nil {
		return
	}
	c := b.client.Load()
	if c == nil {
		return
	}
	if _, err := c.Insert(context.WithoutCancel(ctx), DrainArgs{}, nil); err != nil {
		b.log.WarnContext(ctx, "enqueue event drain", "error", err)
	}
}

// Publish emits ev in a transaction of its own and kicks the drain. Use
// Emit instead when the event describes a change made in a transaction.
func (b *Bus) Publish(ctx context.Context, ev Event, payload any) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("publish event: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := Emit(ctx, b.q.WithTx(tx), ev, payload); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("publish event: commit: %w", err)
	}
	b.Kick(ctx)
	return nil
}

type drainWorker struct {
	river.WorkerDefaults[DrainArgs]
	b *Bus
}

func (w *drainWorker) Work(ctx context.Context, _ *river.Job[DrainArgs]) error {
	client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return fmt.Errorf("event drain: %w", err)
	}
	total := 0
	for range drainRounds {
		n, err := w.b.drainOnce(ctx, client)
		if err != nil {
			return err
		}
		total += n
		if n < drainBatch {
			break
		}
	}
	if total > 0 {
		w.b.log.DebugContext(ctx, "events dispatched", "count", total)
	}
	before := pgtype.Timestamptz{Time: time.Now().Add(-Retention), Valid: true}
	if n, err := w.b.q.PruneOutboxEvents(ctx, before); err != nil {
		w.b.log.WarnContext(ctx, "prune event outbox", "error", err)
	} else if n > 0 {
		w.b.log.InfoContext(ctx, "pruned event outbox", "count", n)
	}
	return nil
}

// drainOnce claims a batch of pending events and, in the same transaction,
// enqueues their delivery jobs and marks them dispatched.
func (b *Bus) drainOnce(ctx context.Context, client *river.Client[pgx.Tx]) (int, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("event drain: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := b.q.WithTx(tx)
	rows, err := q.ClaimOutboxEvents(ctx, drainBatch)
	if err != nil {
		return 0, fmt.Errorf("event drain: claim: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	var params []river.InsertManyParams
	for _, r := range rows {
		ids = append(ids, r.ID)
		for _, h := range b.handlersFor(r.Kind) {
			params = append(params, river.InsertManyParams{Args: DeliverArgs{EventID: r.ID, Handler: h}})
		}
	}
	if len(params) > 0 {
		if _, err := client.InsertManyTx(ctx, tx, params); err != nil {
			return 0, fmt.Errorf("event drain: enqueue deliveries: %w", err)
		}
	}
	if err := q.MarkOutboxDispatched(ctx, ids); err != nil {
		return 0, fmt.Errorf("event drain: mark dispatched: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("event drain: commit: %w", err)
	}
	return len(rows), nil
}

type deliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	b *Bus
}

func (w *deliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	h, ok := w.b.handler(job.Args.Handler)
	if !ok {
		w.b.log.WarnContext(ctx, "event handler not registered; dropping delivery",
			"handler", job.Args.Handler, "event", job.Args.EventID)
		return nil
	}
	row, err := w.b.q.GetOutboxEvent(ctx, job.Args.EventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // pruned
	}
	if err != nil {
		return fmt.Errorf("load event: %w", err)
	}
	ev := Event{ID: row.ID, Kind: row.Kind, Action: row.Action, RepoID: row.RepoID.UUID, ActorID: row.ActorID.UUID,
		SubjectID: row.SubjectID.UUID, Payload: row.Payload, CreatedAt: row.CreatedAt.Time}
	if err := h.Handle(ctx, ev); err != nil {
		if job.Attempt >= job.MaxAttempts {
			w.b.log.ErrorContext(ctx, "event handler gave up", "handler", job.Args.Handler,
				"event", ev.ID, "kind", ev.Kind, "error", err)
		}
		return fmt.Errorf("event handler %s: %w", job.Args.Handler, err)
	}
	return nil
}
