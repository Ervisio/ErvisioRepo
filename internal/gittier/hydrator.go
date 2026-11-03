package gittier

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// Options configure a Hydrator.
type Options struct {
	Pool *pgxpool.Pool
	// Hub receives the progress events; nil creates one (see Hydrator.Hub).
	Hub *Hub
	Log *slog.Logger
	// Paranoid runs git fsck on every hydrated repository before it goes
	// hot (storage.paranoid).
	Paranoid bool
	// Parallel bounds the packs downloaded and indexed at once (default 4).
	Parallel int
	// Timeout bounds one hydration (default 2 hours): it runs detached from
	// the callers, which only wait for it.
	Timeout time.Duration
}

// Hydrator implements gitstore.Hydrator: it rebuilds the bare repository of
// a cold repository from its remote pack journal. Concurrent calls for one
// repository share a single run (singleflight), also across processes
// thanks to the exclusive lease taken with gitstore.Tier.Lock.
type Hydrator struct {
	o    Options
	q    *dbgen.Queries
	hub  *Hub
	mu   sync.Mutex
	tier *gitstore.Tier
	runs map[uuid.UUID]*run
}

type run struct {
	done chan struct{}
	err  error
}

var _ gitstore.Hydrator = (*Hydrator)(nil)

// New returns a Hydrator. Bind it to its tier with SetTier before use: the
// tier takes the hydrator in its options, so it cannot exist first.
func New(o Options) *Hydrator {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Hub == nil {
		o.Hub = NewHub()
	}
	if o.Parallel <= 0 {
		o.Parallel = 4
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Hour
	}
	return &Hydrator{o: o, q: dbgen.New(o.Pool), hub: o.Hub, runs: map[uuid.UUID]*run{}}
}

// SetTier binds the hydrator to t.
func (h *Hydrator) SetTier(t *gitstore.Tier) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tier = t
}

// Hub returns the progress hub.
func (h *Hydrator) Hub() *Hub { return h.hub }

func (h *Hydrator) getTier() *gitstore.Tier {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tier
}

// errDegraded marks failures that make the repository degraded instead of
// cold: the remote journal is unreadable or inconsistent and retrying will
// not help.
type errDegraded struct{ err error }

func (e errDegraded) Error() string { return e.err.Error() }
func (e errDegraded) Unwrap() error { return e.err }

func degrade(format string, a ...any) error { return errDegraded{fmt.Errorf(format, a...)} }

// Hydrate makes repoID hot. It returns when the shared run ends or ctx is
// done (the run then continues for the other waiters). A ctx carrying an
// observer (WithObserver) receives the progress meanwhile.
func (h *Hydrator) Hydrate(ctx context.Context, repoID uuid.UUID) error {
	if obs := observerFrom(ctx); obs != nil {
		stop := h.forward(repoID, obs)
		defer stop()
	}
	r := h.start(repoID)
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return fmt.Errorf("hydrate %s: %w", repoID, ctx.Err())
	}
}

// Start begins hydrating repoID in the background, if it is not already.
func (h *Hydrator) Start(repoID uuid.UUID) { h.start(repoID) }

func (h *Hydrator) start(repoID uuid.UUID) *run {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.runs[repoID]; r != nil {
		return r
	}
	r := &run{done: make(chan struct{})}
	h.runs[repoID] = r
	go func() {
		r.err = h.run(repoID)
		h.mu.Lock()
		delete(h.runs, repoID)
		h.mu.Unlock()
		close(r.done)
	}()
	return r
}

// forward relays the hub events of repoID to obs until stop is called.
func (h *Hydrator) forward(repoID uuid.UUID, obs func(Event)) (stop func()) {
	ch, unsubscribe := h.hub.Subscribe(repoID)
	quit, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case e := <-ch:
				obs(e)
			case <-quit:
				select {
				case e := <-ch:
					obs(e)
				default:
				}
				return
			}
		}
	}()
	return func() {
		close(quit)
		<-finished
		unsubscribe()
	}
}

func (h *Hydrator) run(repoID uuid.UUID) error {
	t := h.getTier()
	if t == nil {
		return errors.New("gittier: hydrator not bound to a tier")
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.o.Timeout)
	defer cancel()
	log := h.o.Log.With("repo", repoID)

	l, err := t.Lock(ctx, repoID)
	if err != nil {
		return err
	}
	defer l.Release()
	row, err := h.q.GetRepoStorage(ctx, repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", repoID, gitstore.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("load repository state %s: %w", repoID, err)
	}
	present, err := dirExists(l.Path)
	if err != nil {
		return err
	}
	from := row.StorageState
	switch from {
	case gitstore.StateDegraded:
		return fmt.Errorf("%s: %w", repoID, gitstore.ErrDegraded)
	case gitstore.StateHot:
		if present {
			return nil
		}
		log.WarnContext(ctx, "hot repository missing on disk, hydrating it again")
	case gitstore.StateEvicting:
		if present {
			// An eviction died between the two state changes: the
			// directory is intact, keep it.
			return h.cas(ctx, repoID, from, gitstore.StateHot)
		}
	}
	if err := h.cas(ctx, repoID, from, gitstore.StateHydrating); err != nil {
		return err
	}
	h.publish(Event{Repo: repoID, Phase: PhaseStart})
	started := time.Now()
	err = h.hydrate(ctx, t, l, row)
	if err == nil {
		if err = h.cas(ctx, repoID, gitstore.StateHydrating, gitstore.StateHot); err == nil {
			log.InfoContext(ctx, "repository hydrated", "duration", time.Since(started).Round(time.Millisecond))
			h.publish(Event{Repo: repoID, Phase: PhaseDone, Percent: 100, State: gitstore.StateHot})
			return nil
		}
	}
	// Leave nothing half-built behind.
	if terr := t.Trash(context.WithoutCancel(ctx), repoID); terr != nil {
		log.ErrorContext(ctx, "trash failed hydration", "error", terr)
	}
	to := gitstore.StateCold
	if errors.As(err, new(errDegraded)) {
		to = gitstore.StateDegraded
	}
	log.ErrorContext(ctx, "hydration failed", "state", to, "error", err)
	if cerr := h.cas(context.WithoutCancel(ctx), repoID, gitstore.StateHydrating, to); cerr != nil {
		log.ErrorContext(ctx, "reset state after failed hydration", "error", cerr)
	}
	h.publish(Event{Repo: repoID, Phase: PhaseFailed, State: to, Error: "hydration failed"})
	if to == gitstore.StateDegraded {
		return fmt.Errorf("hydrate %s: %w: %w", repoID, gitstore.ErrDegraded, err)
	}
	return fmt.Errorf("hydrate %s: %w", repoID, err)
}

func (h *Hydrator) cas(ctx context.Context, id uuid.UUID, from, to string) error {
	return setState(ctx, h.q, id, from, to)
}

func (h *Hydrator) publish(e Event) { h.hub.Publish(e) }

func (h *Hydrator) hydrate(ctx context.Context, t *gitstore.Tier, l *gitstore.Lease, row dbgen.GetRepoStorageRow) error {
	id := row.ID
	store := t.Store()
	if store == nil {
		return fmt.Errorf("%s: no remote journal configured: %w", id, gitstore.ErrCold)
	}
	h.publish(Event{Repo: id, Phase: PhaseManifest})
	m, err := store.LoadManifest(ctx, id)
	switch {
	case errors.Is(err, gitstore.ErrInvalidManifest), errors.Is(err, gitstore.ErrNoManifest):
		return errDegraded{err}
	case err != nil:
		return err
	}
	if m.Seq() < row.ManifestSeq {
		return degrade("%s: remote journal at seq %d is behind the database (%d)", id, m.Seq(), row.ManifestSeq)
	}
	repo, err := h.q.GetRepo(ctx, id)
	if err != nil {
		return fmt.Errorf("load repository %s: %w", id, err)
	}
	if err := t.Trash(ctx, id); err != nil { // leftovers of an earlier attempt
		return err
	}
	if err := git.InitBare(ctx, l.Path, repo.Repo.DefaultBranch); err != nil {
		return fmt.Errorf("init %s: %w", id, err)
	}

	packs := m.Packs()
	if err := h.fetchPacks(ctx, store, l.Path, id, packs); err != nil {
		return err
	}

	h.publish(Event{Repo: id, Phase: PhaseRefs, Percent: 92})
	var snap *gitstore.RefsSnapshot
	if m.RefsSeq > 0 {
		if snap, err = store.LoadRefs(ctx, id, m.RefsSeq); err != nil {
			if errors.Is(err, gitstore.ErrInvalidManifest) {
				return errDegraded{err}
			}
			return err
		}
		if err := restoreRefs(ctx, l.Path, snap); err != nil {
			return err
		}
	}

	h.publish(Event{Repo: id, Phase: PhaseIndex, Percent: 95})
	if len(packs) > 0 {
		if _, err := git.Run(ctx, l.Path, "multi-pack-index", "write"); err != nil {
			return fmt.Errorf("multi-pack-index %s: %w", id, err)
		}
		if _, err := git.Run(ctx, l.Path, "commit-graph", "write", "--reachable", "--no-progress"); err != nil {
			return fmt.Errorf("commit-graph %s: %w", id, err)
		}
	}

	h.publish(Event{Repo: id, Phase: PhaseVerify, Percent: 98})
	if snap != nil {
		if err := verifyTips(ctx, l.Path, snap); err != nil {
			return errDegraded{fmt.Errorf("%s: %w", id, err)}
		}
	}
	if h.o.Paranoid && len(packs) > 0 {
		if _, err := git.Run(ctx, l.Path, "fsck", "--no-dangling", "--no-progress"); err != nil {
			return errDegraded{fmt.Errorf("fsck %s: %w", id, err)}
		}
	}
	return nil
}

// fetchPacks streams every pack into git index-pack, a few at a time.
func (h *Hydrator) fetchPacks(ctx context.Context, store *gitstore.Store, path string, id uuid.UUID, packs []gitstore.Segment) error {
	p := &progress{h: h, repo: id}
	for _, pk := range packs {
		p.total += pk.Size
	}
	p.report()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, h.o.Parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for _, pk := range packs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := indexPack(ctx, store, path, pk, p); err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
				cancel()
			}
		}()
	}
	wg.Wait()
	if first != nil {
		return fmt.Errorf("fetch packs %s: %w", id, first)
	}
	return ctx.Err()
}

func indexPack(ctx context.Context, store *gitstore.Store, path string, pk gitstore.Segment, p *progress) error {
	rc, _, err := store.OpenObject(ctx, pk.Pack, nil)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	src := &countingReader{r: rc, p: p}
	var out bytes.Buffer
	if err := git.Exec(ctx, git.Cmd{
		RepoPath: path, Args: []string{"index-pack", "--stdin"}, Stdin: src, Stdout: &out, Timeout: 2 * time.Hour,
	}); err != nil {
		return fmt.Errorf("index %s: %w", pk.Pack, err)
	}
	// index-pack may stop at the pack trailer: read to EOF so the storage
	// layer checks the sha256 of the whole object.
	if _, err := io.Copy(io.Discard, src); err != nil {
		return fmt.Errorf("read %s: %w", pk.Pack, err)
	}
	kind, sha, _ := strings.Cut(strings.TrimSpace(out.String()), "\t")
	if (kind != "pack" && kind != "keep") || sha != pk.SHA {
		return degrade("index %s: git reports pack %q, manifest says %s", pk.Pack, sha, pk.SHA)
	}
	return nil
}

// restoreRefs creates the refs of snap in one transaction and points HEAD.
func restoreRefs(ctx context.Context, path string, snap *gitstore.RefsSnapshot) error {
	var in bytes.Buffer
	for _, name := range slices.Sorted(maps.Keys(snap.Refs)) {
		fmt.Fprintf(&in, "create %s %s\n", name, snap.Refs[name])
	}
	if in.Len() > 0 {
		if err := git.Exec(ctx, git.Cmd{RepoPath: path, Args: []string{"update-ref", "--stdin"}, Stdin: &in}); err != nil {
			return errDegraded{fmt.Errorf("restore refs: %w", err)}
		}
	}
	if snap.Head != "" {
		if _, err := git.Run(ctx, path, "symbolic-ref", "HEAD", snap.Head); err != nil {
			return fmt.Errorf("restore HEAD: %w", err)
		}
	}
	return nil
}

// verifyTips checks every ref of snap points to an object present locally.
func verifyTips(ctx context.Context, path string, snap *gitstore.RefsSnapshot) error {
	if len(snap.Refs) == 0 {
		return nil
	}
	oids := slices.Sorted(maps.Values(snap.Refs))
	oids = slices.Compact(oids)
	var in, out bytes.Buffer
	for _, o := range oids {
		in.WriteString(o + "\n")
	}
	if err := git.Exec(ctx, git.Cmd{
		RepoPath: path, Args: []string{"cat-file", "--batch-check=%(objectname) %(objecttype)"}, Stdin: &in, Stdout: &out,
	}); err != nil {
		return fmt.Errorf("check tips: %w", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != len(oids) {
		return fmt.Errorf("check tips: %d answers for %d objects", len(lines), len(oids))
	}
	for _, line := range lines {
		if strings.HasSuffix(line, " missing") {
			return fmt.Errorf("ref tip %s missing from the journal packs", strings.TrimSuffix(line, " missing"))
		}
	}
	return nil
}

func dirExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", path, err)
}

// progress aggregates the bytes read by the parallel downloads.
type progress struct {
	h       *Hydrator
	repo    uuid.UUID
	mu      sync.Mutex
	done    int64
	total   int64
	percent int
}

func (p *progress) add(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += int64(n)
	if pc := p.pct(); pc != p.percent {
		p.percent = pc
		p.h.publish(Event{Repo: p.repo, Phase: PhasePacks, Done: p.done, Total: p.total, Percent: pc})
	}
}

func (p *progress) report() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.percent = p.pct()
	p.h.publish(Event{Repo: p.repo, Phase: PhasePacks, Done: p.done, Total: p.total, Percent: p.percent})
}

// pct maps the download to 0-90%; the remaining steps share the rest.
func (p *progress) pct() int {
	if p.total <= 0 {
		return 90
	}
	return int(min(p.done, p.total) * 90 / p.total)
}

type countingReader struct {
	r io.Reader
	p *progress
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	if n > 0 {
		c.p.add(n)
	}
	return n, err
}
