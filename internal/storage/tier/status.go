package tier

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Warning codes reported by Warnings. The UI translates them by code.
const (
	// WarnNotConfigured: no storage policy at all.
	WarnNotConfigured = "storage_not_configured"
	// WarnLocalOnly: every copy of every data type is on local disks, so a
	// disk failure loses data (PLAN §4.2). Shown permanently in the admin UI.
	WarnLocalOnly = "storage_local_only"
	// WarnPolicyLocal: one data type has no copy off the local disks.
	WarnPolicyLocal = "storage_policy_local"
)

// Warning is a permanent configuration warning.
type Warning struct {
	Code    string
	Policy  string // empty for global warnings
	Message string
}

// Warnings returns the configuration warnings of cfg, global first.
func Warnings(cfg config.Storage) []Warning {
	if len(cfg.Policy) == 0 {
		return []Warning{{Code: WarnNotConfigured,
			Message: "no storage policy is configured; data cannot be stored"}}
	}
	kinds := map[string]string{}
	for _, t := range cfg.Targets {
		kinds[t.Name] = t.Kind
	}
	var local []string
	for _, name := range slices.Sorted(maps.Keys(cfg.Policy)) {
		p := cfg.Policy[name]
		remote := kinds[p.Primary] != "local"
		for _, r := range p.Replicas {
			remote = remote || kinds[r] != "local"
		}
		if !remote {
			local = append(local, name)
		}
	}
	if len(local) == len(cfg.Policy) {
		return []Warning{{Code: WarnLocalOnly,
			Message: "all data is stored only on local disks: there is no protection against disk failure"}}
	}
	out := make([]Warning, 0, len(local))
	for _, name := range local {
		out = append(out, Warning{Code: WarnPolicyLocal, Policy: name,
			Message: fmt.Sprintf("%s data is stored only on local disks: there is no protection against disk failure", name)})
	}
	return out
}

// TargetStatus describes one target for the admin page.
type TargetStatus struct {
	Name      string
	Kind      string
	Encrypted bool
	Roles     []string // "primary:<policy>", "replica:<policy>", "cache:<policy>"
	Caps      blob.Caps
}

// PolicyStatus describes one data type for the admin page.
type PolicyStatus struct {
	Name            string
	Primary         string
	Replicas        []string
	Cache           string
	CacheTTL        time.Duration
	CacheShare      float64
	Durability      string
	Objects         int64
	Bytes           int64
	PendingReplicas int64
	Errors          int64
}

// Status is the full storage picture shown by the admin endpoint.
type Status struct {
	Targets  []TargetStatus
	Policies []PolicyStatus
	Caches   []CacheStats
	Warnings []Warning
}

// Status collects configuration, usage and object statistics.
func (t *Tier) Status(ctx context.Context) (Status, error) {
	st := Status{Warnings: Warnings(t.cfg)}
	roles := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(t.classes)) {
		c := t.classes[name]
		roles[c.primary.Name] = append(roles[c.primary.Name], "primary:"+name)
		for _, r := range c.replicas {
			roles[r.Name] = append(roles[r.Name], "replica:"+name)
		}
		if c.cache != nil {
			roles[c.cache.name] = append(roles[c.cache.name], "cache:"+name)
		}
	}
	for _, tgt := range t.Targets() {
		r := roles[tgt.Name]
		if r == nil {
			r = []string{}
		}
		st.Targets = append(st.Targets, TargetStatus{
			Name: tgt.Name, Kind: tgt.Kind, Encrypted: tgt.Encrypted, Roles: r, Caps: tgt.Store.Caps(),
		})
	}

	stats, err := t.q.StorageObjectStats(ctx)
	if err != nil {
		return st, fmt.Errorf("storage object stats: %w", err)
	}
	for _, name := range slices.Sorted(maps.Keys(t.classes)) {
		c := t.classes[name]
		ps := PolicyStatus{
			Name: name, Primary: c.primary.Name, Replicas: []string{},
			CacheTTL: time.Duration(c.policy.CacheTTL), CacheShare: c.policy.CacheShare, Durability: c.policy.Durability,
		}
		for _, r := range c.replicas {
			ps.Replicas = append(ps.Replicas, r.Name)
		}
		if c.cache != nil {
			ps.Cache = c.cache.name
		}
		for _, s := range stats {
			if s.Policy == name {
				ps.Objects, ps.Bytes, ps.PendingReplicas, ps.Errors = s.Objects, s.Bytes, s.PendingReplicas, s.Errors
			}
		}
		st.Policies = append(st.Policies, ps)
	}
	for _, c := range t.Caches() {
		st.Caches = append(st.Caches, c.Stats())
	}
	return st, nil
}
