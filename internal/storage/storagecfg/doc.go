// Package storagecfg lets the web admin configure external storage on top of
// config.toml. The whole overlay is one JSON document in the settings table:
// targets of kind s3 and sftp whose credentials are sealed with the master
// key, and per-policy overrides of the primary, replicas and cache.
//
// The overlay is merged over the file configuration at start, in every place
// that builds the tier (see Apply); changes made at runtime apply after a
// restart. Credentials are never returned by any function of this package.
package storagecfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// SettingKey is the row of the settings table holding the overlay.
const SettingKey = "storage.overlay"

// Source of a target or policy, as shown by the admin API.
const (
	SourceFile     = "file"
	SourceDatabase = "database"
)

// Kinds that can be created from the web admin.
const (
	KindS3   = "s3"
	KindSFTP = "sftp"
)

// Names of the sealed credential fields.
const (
	SecretAccessKey  = "accessKey"
	SecretSecretKey  = "secretKey"
	SecretPrivateKey = "privateKey"
	SecretPassphrase = "passphrase"
	SecretKnownHosts = "knownHosts"
	SecretPassword   = "password"
)

// Doc is the stored overlay.
type Doc struct {
	Version  int                       `json:"version"`
	Targets  []StoredTarget            `json:"targets,omitempty"`
	Policies map[string]PolicyOverride `json:"policies,omitempty"`
	// HotCache replaces storage.hot_cache of config.toml when set.
	HotCache *HotCacheOverride `json:"hotCache,omitempty"`
}

// HotCacheOverride is the instance-wide automatic cache setting.
type HotCacheOverride struct {
	Enabled bool `json:"enabled"`
	// TTLSeconds is the idle time after which data moves back to the remote
	// storage; 0 uses the per data type defaults.
	TTLSeconds int64 `json:"ttlSeconds"`
}

func (h HotCacheOverride) config() config.HotCache {
	return config.HotCache{Enabled: h.Enabled, TTL: config.Duration(time.Duration(h.TTLSeconds) * time.Second)}
}

// hotCache returns the hot cache setting of base with the overlay applied.
func (d Doc) hotCache(base config.HotCache) config.HotCache {
	if d.HotCache == nil {
		return base
	}
	return d.HotCache.config()
}

// StoredTarget is a target created in the web admin. Secrets maps a field name
// to its sealed value (base64).
type StoredTarget struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Encrypt bool   `json:"encrypt,omitempty"`

	Endpoint  string `json:"endpoint,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	PathStyle bool   `json:"pathStyle,omitempty"`
	Prefix    string `json:"prefix,omitempty"`

	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Root     string `json:"root,omitempty"`
	MaxConns int    `json:"maxConns,omitempty"`

	Secrets map[string]string `json:"secrets,omitempty"`
}

// PolicyOverride replaces the primary, replicas and cache of a policy of
// config.toml, and its cache TTL when CacheTTLSeconds is set (0: the hot
// cache default). The other policy settings stay as in the file.
type PolicyOverride struct {
	Primary         string   `json:"primary"`
	Replicas        []string `json:"replicas"`
	Cache           string   `json:"cache,omitempty"`
	CacheTTLSeconds *int64   `json:"cacheTtlSeconds,omitempty"`
}

func (d *Doc) target(name string) int {
	return slices.IndexFunc(d.Targets, func(t StoredTarget) bool { return t.Name == name })
}

// empty reports whether the document changes nothing.
func (d Doc) empty() bool { return len(d.Targets) == 0 && len(d.Policies) == 0 && d.HotCache == nil }

// sameAs compares two documents as stored.
func (d Doc) sameAs(o Doc) bool {
	d.Version, o.Version = 0, 0
	a, errA := json.Marshal(d)
	b, errB := json.Marshal(o)
	return errA == nil && errB == nil && string(a) == string(b)
}

// Getter reads the settings table and the object rows.
type Getter interface {
	GetSetting(ctx context.Context, key string) (dbgen.Setting, error)
	StorageObjectCountsByTarget(ctx context.Context) ([]dbgen.StorageObjectCountsByTargetRow, error)
}

// ErrCorrupt is returned by Load for a stored document that cannot be decoded.
var ErrCorrupt = errors.New("stored storage configuration cannot be decoded")

// Load reads the overlay; a missing row is an empty document.
func Load(ctx context.Context, g Getter) (Doc, error) {
	row, err := g.GetSetting(ctx, SettingKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Doc{Version: 1}, nil
	}
	if err != nil {
		return Doc{}, fmt.Errorf("load storage configuration: %w", err)
	}
	var d Doc
	if err := json.Unmarshal(row.Value, &d); err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if d.Version == 0 {
		d.Version = 1
	}
	return d, nil
}

// cloneStorage copies the targets and policies of s, so that merging never
// touches the file configuration.
func cloneStorage(s config.Storage) config.Storage {
	out := s
	out.Targets = slices.Clone(s.Targets)
	out.Policy = make(map[string]config.Policy, len(s.Policy))
	for k, p := range s.Policy {
		p.Replicas = slices.Clone(p.Replicas)
		out.Policy[k] = p
	}
	return out
}

// applyPolicies returns policies with the overrides of doc applied.
func applyPolicies(policies map[string]config.Policy, doc Doc) (map[string]config.Policy, error) {
	out := make(map[string]config.Policy, len(policies))
	for k, p := range policies {
		out[k] = p
	}
	for _, name := range slices.Sorted(maps.Keys(doc.Policies)) {
		p, ok := out[name]
		if !ok {
			return nil, fmt.Errorf("policy %q is not configured in the configuration file", name)
		}
		ov := doc.Policies[name]
		p.Primary, p.Replicas, p.Cache = ov.Primary, slices.Clone(ov.Replicas), ov.Cache
		if ov.CacheTTLSeconds != nil {
			p.CacheTTL = config.Duration(time.Duration(*ov.CacheTTLSeconds) * time.Second)
		}
		out[name] = p
	}
	return out, nil
}

// config turns a stored target into a driver configuration, opening its
// sealed credentials.
func (st StoredTarget) config(s *sealer) (config.Target, error) {
	t := config.Target{
		Name: st.Name, Kind: st.Kind, Encrypt: st.Encrypt,
		Endpoint: st.Endpoint, Bucket: st.Bucket, Region: st.Region, PathStyle: st.PathStyle, Prefix: st.Prefix,
		Host: st.Host, Port: st.Port, User: st.User, Root: st.Root, MaxConns: st.MaxConns,
		Guarded: true,
	}
	open := func(field string, dst *string) error {
		sealed, ok := st.Secrets[field]
		if !ok {
			return nil
		}
		v, err := s.open(sealed, st.Name, field)
		if err != nil {
			return fmt.Errorf("target %s: %w", st.Name, err)
		}
		*dst = v
		return nil
	}
	for _, f := range []struct {
		name string
		dst  *string
	}{
		{SecretAccessKey, &t.AccessKey}, {SecretSecretKey, &t.SecretKey},
		{SecretPrivateKey, &t.KeyPEM}, {SecretPassphrase, &t.KeyPassphrase}, {SecretKnownHosts, &t.KnownHostsData},
		{SecretPassword, &t.Password},
	} {
		if err := open(f.name, f.dst); err != nil {
			return config.Target{}, err
		}
	}
	return t, nil
}

// Merge returns base with the overlay applied and validates the result. The
// master key opens the sealed credentials; it may be empty only when the
// overlay holds none.
func Merge(base config.Storage, doc Doc, masterKey []byte) (config.Storage, error) {
	s, err := newSealer(masterKey)
	if err != nil {
		return config.Storage{}, err
	}
	out := cloneStorage(base)
	taken := make(map[string]bool, len(base.Targets))
	for _, t := range base.Targets {
		taken[t.Name] = true
	}
	for _, st := range doc.Targets {
		if taken[st.Name] {
			return config.Storage{}, fmt.Errorf("target %q is defined more than once or also in the configuration file", st.Name)
		}
		taken[st.Name] = true
		t, err := st.config(s)
		if err != nil {
			return config.Storage{}, err
		}
		out.Targets = append(out.Targets, t)
	}
	if out.Policy, err = applyPolicies(out.Policy, doc); err != nil {
		return config.Storage{}, err
	}
	out.HotCache = doc.hotCache(base.HotCache)
	if err := out.Validate(); err != nil {
		return config.Storage{}, fmt.Errorf("invalid storage configuration: %w", err)
	}
	return out, nil
}

// PrimaryChanges lists the policies whose stored override moves the primary
// away from the one of base.
func (d Doc) PrimaryChanges(base config.Storage) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(d.Policies)) {
		if p, ok := base.Policy[name]; ok && p.Primary != d.Policies[name].Primary {
			out = append(out, name)
		}
	}
	return out
}

// Result is what Apply returns.
type Result struct {
	// Storage is the configuration to build the tier with.
	Storage config.Storage
	// Doc is the overlay that is part of Storage; empty when the file
	// configuration is used as it is.
	Doc Doc
	// Stored is the overlay found in the database at start, applied or not.
	Stored Doc
	// Fallback is set when a stored overlay could not be applied and the
	// file configuration was used instead.
	Fallback bool
	// Cause says why, without credentials; empty unless Fallback.
	Cause string
}

// maxCause bounds the text kept about why an overlay was not applied.
const maxCause = 300

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// Apply merges the stored overlay over base. When it cannot be loaded,
// opened or validated: if no policy would have moved its primary, a warning is
// logged and base is used; otherwise (or when the document cannot be read at
// all, so that nothing is known about primaries) Apply fails, because starting
// with the wrong primary would make stored objects unfindable.
//
// The same rule covers an overlay that moves the primary of a policy which
// already has objects under another target (for example written by the
// running server between the save and the restart): those objects would be
// unfindable, so the file configuration is used if its primary matches them,
// and the start is refused otherwise.
func Apply(ctx context.Context, g Getter, masterKey []byte, base config.Storage, log *slog.Logger) (Result, error) {
	doc, err := Load(ctx, g)
	if err != nil {
		return Result{}, fmt.Errorf("storage overlay (settings key %s): %w; do not delete the stored value, it holds the primary of your policies (see the recovery section of docs/install.md)", SettingKey, err)
	}
	if doc.empty() {
		return Result{Storage: base, Stored: doc}, nil
	}
	eff, err := Merge(base, doc, masterKey)
	if err != nil {
		return fallback(base, doc, err, log)
	}
	counts, err := g.StorageObjectCountsByTarget(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("storage overlay: count objects: %w", err)
	}
	if stray := strayPolicies(counts, eff.Policy, base.Policy, false); len(stray) > 0 {
		cause := fmt.Errorf("policies %s hold objects under a target that the overlay no longer makes their primary", strings.Join(stray, ", "))
		// The file configuration drops every override, so it is safe only if
		// it finds the objects of all policies, not just the stray ones.
		bad := strayPolicies(counts, base.Policy, base.Policy, true)
		if len(bad) > 0 {
			return Result{}, fmt.Errorf("storage overlay cannot be applied and config.toml does not match the stored objects of %s: %w", strings.Join(bad, ", "), cause)
		}
		msg := shorten(cause.Error(), maxCause)
		log.Warn("storage overlay from the web admin not applied, using config.toml only", "error", msg)
		return Result{Storage: base, Stored: doc, Fallback: true, Cause: msg}, nil
	}
	return Result{Storage: eff, Doc: doc, Stored: doc}, nil
}

// strayPolicies lists the policies of eff that have rows under a target other
// than their primary. Unless all is set, only policies whose primary differs
// from the one of file are looked at (the overlay moved them).
func strayPolicies(counts []dbgen.StorageObjectCountsByTargetRow, eff, file map[string]config.Policy, all bool) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(eff)) {
		if !all && eff[name].Primary == file[name].Primary {
			continue
		}
		if slices.ContainsFunc(counts, func(c dbgen.StorageObjectCountsByTargetRow) bool {
			return c.Policy == name && c.Target != eff[name].Primary && c.Objects > 0
		}) {
			out = append(out, name)
		}
	}
	return out
}

// Fallback is the handling of an overlay that was applied but whose stores
// could not be built; see Apply.
func Fallback(r Result, base config.Storage, cause error, log *slog.Logger) (Result, error) {
	if r.Doc.empty() {
		return r, cause
	}
	return fallback(base, r.Doc, cause, log)
}

// fallback uses base instead of the overlay in doc, unless the overlay moves
// a primary.
func fallback(base config.Storage, doc Doc, cause error, log *slog.Logger) (Result, error) {
	if moved := doc.PrimaryChanges(base); len(moved) > 0 {
		return Result{}, fmt.Errorf("storage overlay changes the primary of %s and cannot be applied: %w", strings.Join(moved, ", "), cause)
	}
	msg := shorten(cause.Error(), maxCause)
	log.Warn("storage overlay from the web admin not applied, using config.toml only", "error", msg)
	return Result{Storage: base, Stored: doc, Fallback: true, Cause: msg}, nil
}

// ClearBlockers lists why clearing the overlay would lose objects: policies
// with objects whose overlay primary differs from the one of the file.
func ClearBlockers(doc Doc, file config.Storage, counts []dbgen.StorageObjectCountsByTargetRow) []string {
	var out []string
	for _, name := range doc.PrimaryChanges(file) {
		n := int64(0)
		for _, c := range counts {
			if c.Policy == name {
				n += c.Objects
			}
		}
		if n > 0 {
			out = append(out, fmt.Sprintf("policy %s holds %d objects under primary %q; clearing would make %q the primary", name, n, doc.Policies[name].Primary, file.Policy[name].Primary))
		}
	}
	return out
}
