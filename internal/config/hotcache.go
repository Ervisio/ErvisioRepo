package config

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// HotCache is the automatic local cache of the data kept on remote targets
// ([storage.hot_cache]). When enabled, every data type whose primary is
// remote gets the local "hot" target as cache with an idle TTL and a share of
// its budget, unless its policy sets them; git repositories leave the local
// disk after the same kind of inactivity.
type HotCache struct {
	Enabled bool `toml:"enabled"`
	// TTL moves data back to the remote storage after this long without
	// reads or writes. 0 uses the per-type defaults (DefaultCacheTTL).
	TTL Duration `toml:"ttl"`
	// MaxSize bounds the local disk used by the hot cache (hot repositories
	// and cached objects together). 0 keeps the budget of the hot target,
	// else DefaultHotCacheSize: the cache never grows to the whole disk.
	MaxSize Size `toml:"max_size"`
	// LargeRepo is the size above which a repository is hydrated only for
	// the time it is used and evicted right after (it never stays in the
	// cache). 0 uses DefaultLargeRepoSize. With the cache disabled every
	// repository with a remote journal is handled this way.
	LargeRepo Size `toml:"large_repo"`
}

// Defaults of the hot cache size limits.
const (
	DefaultHotCacheSize  = Size(20 << 30)
	DefaultLargeRepoSize = Size(2 << 30)
)

// LargeRepoSize returns LargeRepo or its default.
func (h HotCache) LargeRepoSize() Size {
	if h.LargeRepo > 0 {
		return h.LargeRepo
	}
	return DefaultLargeRepoSize
}

// CacheNone in a policy's cache disables the automatic cache of that data
// type.
const CacheNone = "none"

// MinHotCacheTTL is the shortest idle TTL accepted for the hot cache.
const MinHotCacheTTL = Duration(time.Hour)

const day = 24 * time.Hour

// DefaultCacheTTL is the idle time after which data leaves the local disk
// when neither the policy nor storage.hot_cache.ttl set one. Repositories
// stay longer because hydrating a cold one costs a full clone from the
// remote journal; release assets and LFS objects are cheap to fetch again;
// Actions data (artifacts, caches) is read right after the run or by the
// next one, so a few days are enough.
var DefaultCacheTTL = map[string]time.Duration{
	"git":      14 * day,
	"lfs":      7 * day,
	"releases": 7 * day,
	"registry": 7 * day,
	"actions":  3 * day,
}

// DefaultCacheShare is the share of the hot target budget reserved to the
// objects of a data type cached automatically. What the shares leave goes
// to the hot git repositories (at least 60%).
var DefaultCacheShare = map[string]float64{
	"lfs":      0.15,
	"registry": 0.10,
	"releases": 0.05,
	"actions":  0.05,
}

// maxAutoShares bounds the shares given automatically on one target.
const maxAutoShares = 0.40

// HotTarget returns the local target holding the hot repositories (the one
// whose directory contains hot_path), else the first local target; nil when
// there is no local target.
func (s *Storage) HotTarget() *Target {
	var first *Target
	for i := range s.Targets {
		t := &s.Targets[i]
		if t.Kind != "local" || t.Path == "" {
			continue
		}
		if first == nil {
			first = t
		}
		if s.HotPath != "" && within(t.Path, s.HotPath) {
			return t
		}
	}
	return first
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func (s *Storage) remote(name string) bool {
	t := s.target(name)
	return t != nil && t.Kind != "local"
}

// WithHotCache returns s with the automatic hot cache applied (see HotCache).
// The result is what the tier and the eviction job run with:
//   - cache "none" becomes no cache;
//   - with the hot cache enabled, a data type with a remote primary and no
//     cache (backup excluded) is cached on HotTarget, with CacheAuto set and
//     the default share when it has none;
//   - with the hot cache enabled, every cached data type and git with a
//     remote primary get storage.hot_cache.ttl, else DefaultCacheTTL, when
//     their policy sets no cache_ttl;
//   - with the hot cache disabled, data types with a remote primary are not
//     cached at all;
//   - the hot target gets storage.hot_cache.max_size as budget, or
//     DefaultHotCacheSize when neither that nor its own budget is set.
//
// s is not modified.
func (s Storage) WithHotCache() Storage {
	out := s
	out.Policy = make(map[string]Policy, len(s.Policy))
	maps.Copy(out.Policy, s.Policy)
	out.Targets = slices.Clone(s.Targets)
	hot := out.HotTarget()
	if hot != nil {
		switch {
		case s.HotCache.MaxSize > 0:
			hot.Budget = s.HotCache.MaxSize
		case hot.Budget == 0:
			hot.Budget = DefaultHotCacheSize
		}
	}
	shares := map[string]float64{} // auto shares already given, by target
	for _, name := range slices.Sorted(maps.Keys(out.Policy)) {
		p := out.Policy[name]
		p.Replicas = slices.Clone(p.Replicas)
		if p.Cache == CacheNone || (!s.HotCache.Enabled && name != "git" && s.remote(p.Primary)) {
			p.Cache = ""
		} else if s.HotCache.Enabled && p.Cache == "" && name != "git" && name != "backup" &&
			hot != nil && s.remote(p.Primary) {
			p.Cache, p.CacheAuto = hot.Name, true
		}
		if p.Cache != "" {
			shares[p.Cache] += p.CacheShare
		}
		out.Policy[name] = p
	}
	if !s.HotCache.Enabled {
		return out
	}
	for _, name := range slices.Sorted(maps.Keys(out.Policy)) {
		p := out.Policy[name]
		cached := p.Cache != "" || (name == "git" && s.remote(p.Primary))
		if cached && p.CacheTTL == 0 {
			p.CacheTTL = s.HotCache.TTL
			if p.CacheTTL == 0 {
				p.CacheTTL = Duration(DefaultCacheTTL[name])
			}
		}
		if share := DefaultCacheShare[name]; p.CacheAuto && p.CacheShare == 0 && share > 0 &&
			shares[p.Cache]+share <= maxAutoShares+1e-9 {
			p.CacheShare = share
			shares[p.Cache] += share
		}
		out.Policy[name] = p
	}
	return out
}

// CacheShares sums the cache_share of the policies cached on target.
func (s *Storage) CacheShares(target string) float64 {
	sum := 0.0
	for _, p := range s.Policy {
		if p.Cache == target {
			sum += p.CacheShare
		}
	}
	return sum
}
