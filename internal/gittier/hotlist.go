package gittier

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// HotRepo is a repository as listed by the hot cache view.
type HotRepo struct {
	ID         uuid.UUID
	Owner      string
	Name       string
	State      string
	Pinned     bool
	SizeBytes  int64
	LastAccess time.Time
	// EvictAt is when the idle TTL moves the repository to the remote
	// storage; zero when it never does (pinned, not hot, or no TTL).
	EvictAt time.Time
	// Ephemeral is set when the repository is not kept in the cache and
	// leaves the local disk right after use.
	Ephemeral bool
}

// HotSummary is the usage of the hot tier.
type HotSummary struct {
	Repos, Pinned, Bytes, TotalGitBytes int64
	// Budget is the space of the hot tier in bytes (0: no watermark), High
	// and Low its watermarks, TTL the idle TTL (0: none), and Remote whether
	// a remote journal exists (without one nothing is ever evicted).
	Budget    int64
	High, Low float64
	TTL       time.Duration
	Remote    bool
	// Primary and Replicas are the targets of the remote journal;
	// CacheEnabled and LargeRepo the hot cache setting (see EvictorOptions).
	Primary      string
	Replicas     []string
	CacheEnabled bool
	LargeRepo    int64
}

// HotFilter selects the listed repositories.
type HotFilter struct {
	// Query is a substring of owner/name; All also lists cold repositories.
	Query         string
	All           bool
	Offset, Limit int
}

// Settings returns the TTL, budget and watermarks of the eviction job.
func (e *Evictor) Settings() (ttl time.Duration, budget int64, high, low float64) {
	return e.o.TTL, e.o.Budget, e.o.High, e.o.Low
}

func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(strings.ToLower(strings.TrimSpace(s)))
}

func clamp32(n int) int32 {
	return int32(max(0, min(n, math.MaxInt32))) //nolint:gosec // bounded above
}

// HotRepos returns a page of the repositories on the hot tier (or of all
// repositories with f.All), most recently used first, with the total.
func (a *Admin) HotRepos(ctx context.Context, f HotFilter) ([]HotRepo, int64, error) {
	pattern := likePattern(f.Query)
	rows, err := a.q.ListHotCacheRepos(ctx, dbgen.ListHotCacheReposParams{
		AllStates: f.All, Pattern: pattern, Off: clamp32(f.Offset), Lim: clamp32(f.Limit),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list hot repositories: %w", err)
	}
	total, err := a.q.CountHotCacheRepos(ctx, dbgen.CountHotCacheReposParams{AllStates: f.All, Pattern: pattern})
	if err != nil {
		return nil, 0, fmt.Errorf("count hot repositories: %w", err)
	}
	ttl, remote := a.ttl()
	out := make([]HotRepo, 0, len(rows))
	for _, r := range rows {
		h := HotRepo{ID: r.ID, Owner: r.OwnerName, Name: r.Name, State: r.StorageState, Pinned: r.Pinned,
			SizeBytes: r.SizeBytes, LastAccess: r.LastAccessAt.Time}
		h.Ephemeral = remote && a.evictor.ephemeral(r.SizeBytes)
		switch {
		case !remote || r.Pinned || r.StorageState != "hot":
		case h.Ephemeral:
			h.EvictAt = h.LastAccess.Add(a.evictor.o.EphemeralGrace)
		case ttl > 0:
			h.EvictAt = h.LastAccess.Add(ttl)
		}
		out = append(out, h)
	}
	return out, total, nil
}

func (a *Admin) ttl() (time.Duration, bool) {
	if a.evictor == nil {
		return 0, false
	}
	ttl, _, _, _ := a.evictor.Settings()
	return ttl, a.evictor.o.Tier.Store() != nil
}

// HotSummary returns the usage of the hot tier.
func (a *Admin) HotSummary(ctx context.Context) (HotSummary, error) {
	st, err := a.q.HotRepoStats(ctx)
	if err != nil {
		return HotSummary{}, fmt.Errorf("hot repository stats: %w", err)
	}
	s := HotSummary{Repos: st.Repos, Pinned: st.Pinned, Bytes: st.Bytes, TotalGitBytes: st.TotalGitBytes}
	if a.evictor != nil {
		s.TTL, s.Budget, s.High, s.Low = a.evictor.Settings()
		s.Remote = a.evictor.o.Tier.Store() != nil
		s.Primary, s.Replicas = a.evictor.o.Primary, a.evictor.o.Replicas
		s.CacheEnabled, s.LargeRepo = !a.evictor.o.NoCache, a.evictor.o.LargeRepo
	}
	return s, nil
}
