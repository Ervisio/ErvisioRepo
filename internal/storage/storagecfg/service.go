package storagecfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Audit actions of the changes made from the web admin. The entries carry
// names, non-secret locations and the list of credential fields touched,
// never their values.
const (
	ActionTargetSaved   = "admin.storage_target_saved"
	ActionTargetDeleted = "admin.storage_target_deleted"
	ActionPolicySaved   = "admin.storage_policy_saved"
)

// testTimeout bounds one connection test of a draft.
const testTimeout = 60 * time.Second

// maxTestError bounds the error text echoed for a failed test step.
const maxTestError = 300

// ErrNotFound is returned for an unknown target or policy.
var ErrNotFound = errors.New("storage configuration: not found")

// Options configures a Service.
type Options struct {
	Pool *pgxpool.Pool
	// File is the storage section of config.toml, before the overlay.
	File config.Storage
	// Running is the storage configuration the tier was built with.
	Running config.Storage
	// RunningDoc is the overlay that is part of Running (empty when none).
	RunningDoc Doc
	// StoredAtStart is the overlay found in the database at start, applied or
	// not; a restart is required only when the stored one differs from it.
	StoredAtStart Doc
	// Fallback is set when the stored overlay was not applied at start, with
	// FallbackCause (free of credentials) saying why.
	Fallback      bool
	FallbackCause string
	// MasterKey seals the credentials; empty makes every write fail.
	MasterKey []byte
	Log       *slog.Logger
}

// Service reads and writes the overlay. It is safe for concurrent use: every
// change is a read-modify-write in one transaction under an advisory lock.
type Service struct {
	opt    Options
	q      *dbgen.Queries
	sealer *sealer
}

// NewService returns a Service.
func NewService(opt Options) (*Service, error) {
	s, err := newSealer(opt.MasterKey)
	if err != nil {
		return nil, err
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	return &Service{opt: opt, q: dbgen.New(opt.Pool), sealer: s}, nil
}

// TargetView is a target as shown by the admin API. It has no credentials,
// only whether each one is stored.
type TargetView struct {
	Name, Kind, Source string
	Encrypt            bool

	Path string // local targets

	Endpoint, Bucket, Region, Prefix string
	PathStyle                        bool

	Host, User, Root string
	Port, MaxConns   int

	HasAccessKey, HasSecretKey, HasPrivateKey, HasPassphrase, HasKnownHosts, HasPassword bool

	UsedBy              []string // primary:<policy>, replica:<policy>, cache:<policy>
	RestartPending      bool
	RemovedAfterRestart bool
	// Objects counts stored objects living on the target, as primary or as a
	// confirmed replica. Locked is set when there are any: the location of
	// the target can no longer change.
	Objects int64
	Locked  bool
}

// PolicyView is a policy as shown by the admin API.
type PolicyView struct {
	Name           string
	Source         string
	Primary        string
	RunningPrimary string
	Replicas       []string
	Cache          string
	// File* are the values of config.toml.
	FilePrimary  string
	FileReplicas []string
	FileCache    string
	Objects      int64
	// PrimaryLocked is set when the policy holds objects: its primary can
	// only be the running one.
	PrimaryLocked  bool
	RestartPending bool
}

// View is the configuration as the next start will run with it.
type View struct {
	Targets  []TargetView
	Policies []PolicyView
	// SecretsAvailable is false without a master key: credentials cannot be saved.
	SecretsAvailable bool
	RestartRequired  bool
	// Fallback and FallbackCause tell that the stored configuration was not
	// applied at start.
	Fallback      bool
	FallbackCause string
}

// Config returns the configuration with the stored overlay.
func (s *Service) Config(ctx context.Context) (View, error) {
	doc, err := Load(ctx, s.q)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, doc)
}

func (s *Service) view(ctx context.Context, doc Doc) (View, error) {
	next, err := applyPolicies(s.opt.File.Policy, doc)
	if err != nil {
		next = s.opt.File.Policy // an orphan override is reported by the next save
	}
	rows, err := s.q.StorageObjectCountsByTarget(ctx)
	if err != nil {
		return View{}, fmt.Errorf("storage object counts: %w", err)
	}
	perPolicy := map[string]int64{}
	for _, r := range rows {
		perPolicy[r.Policy] += r.Objects
	}
	changed := !doc.sameAs(s.opt.StoredAtStart)
	// After a fallback start nothing of the stored overlay is pending until
	// it changes: it is "not applied", which the response says separately.
	hidePending := s.opt.Fallback && !changed
	v := View{
		SecretsAvailable: s.sealer.ok(), RestartRequired: changed,
		Fallback: s.opt.Fallback, FallbackCause: s.opt.FallbackCause,
	}

	roles := rolesOf(next)
	for _, t := range s.opt.File.Targets {
		tv := fileTargetView(t, roles[t.Name])
		tv.Objects = objectsOn(rows, t.Name, next, s.opt.Running.Policy)
		v.Targets = append(v.Targets, tv)
	}
	running := map[string]StoredTarget{}
	for _, t := range s.opt.RunningDoc.Targets {
		running[t.Name] = t
	}
	for _, st := range doc.Targets {
		tv := storedTargetView(st, roles[st.Name])
		prev, ok := running[st.Name]
		tv.RestartPending = (!ok || !targetEqual(prev, st)) && !hidePending
		tv.Objects = objectsOn(rows, st.Name, next, s.opt.Running.Policy)
		tv.Locked = tv.Objects > 0
		v.Targets = append(v.Targets, tv)
	}
	runRoles := rolesOf(s.opt.Running.Policy)
	for _, rt := range s.opt.RunningDoc.Targets {
		if doc.target(rt.Name) >= 0 {
			continue
		}
		tv := storedTargetView(rt, runRoles[rt.Name])
		tv.RemovedAfterRestart = true
		tv.Objects = objectsOn(rows, rt.Name, next, s.opt.Running.Policy)
		tv.Locked = tv.Objects > 0
		v.Targets = append(v.Targets, tv)
	}

	for _, name := range slices.Sorted(maps.Keys(s.opt.File.Policy)) {
		file, cur, run := s.opt.File.Policy[name], next[name], s.opt.Running.Policy[name]
		_, overridden := doc.Policies[name]
		pv := PolicyView{
			Name: name, Source: SourceFile, Primary: cur.Primary, RunningPrimary: run.Primary,
			Replicas: nonNil(cur.Replicas), Cache: cur.Cache,
			FilePrimary: file.Primary, FileReplicas: nonNil(file.Replicas), FileCache: file.Cache,
			Objects: perPolicy[name], PrimaryLocked: perPolicy[name] > 0,
			RestartPending: !samePlacement(cur, run) && !hidePending,
		}
		if overridden {
			pv.Source = SourceDatabase
		}
		v.Policies = append(v.Policies, pv)
	}
	return v, nil
}

// rolesOf lists, per target, how the policies use it.
func rolesOf(policies map[string]config.Policy) map[string][]string {
	roles := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(policies)) {
		p := policies[name]
		roles[p.Primary] = append(roles[p.Primary], "primary:"+name)
		for _, r := range p.Replicas {
			roles[r] = append(roles[r], "replica:"+name)
		}
		if p.Cache != "" {
			roles[p.Cache] = append(roles[p.Cache], "cache:"+name)
		}
	}
	return roles
}

// objectsOn counts the stored objects that live on target name: rows whose
// primary it is, plus the fully replicated rows of every policy that uses it
// as a replica in any of the given placements (saved or running).
func objectsOn(rows []dbgen.StorageObjectCountsByTargetRow, name string, placements ...map[string]config.Policy) int64 {
	replicaOf := map[string]bool{}
	for _, ps := range placements {
		for pn, p := range ps {
			if slices.Contains(p.Replicas, name) {
				replicaOf[pn] = true
			}
		}
	}
	var n int64
	for _, r := range rows {
		if r.Target == name {
			n += r.Objects
		}
		if replicaOf[r.Policy] {
			n += r.Replicated
		}
	}
	return n
}

func targetEqual(a, b StoredTarget) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && string(x) == string(y)
}

func samePlacement(a, b config.Policy) bool {
	return a.Primary == b.Primary && a.Cache == b.Cache && slices.Equal(a.Replicas, b.Replicas)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

func fileTargetView(t config.Target, usedBy []string) TargetView {
	return TargetView{
		Name: t.Name, Kind: t.Kind, Source: SourceFile, Encrypt: t.Encrypt, Path: t.Path,
		Endpoint: t.Endpoint, Bucket: t.Bucket, Region: t.Region, Prefix: t.Prefix, PathStyle: t.PathStyle,
		Host: t.Host, User: t.User, Root: t.Root, Port: t.Port, MaxConns: t.MaxConns,
		HasAccessKey: t.CredentialsFile != "", HasSecretKey: t.CredentialsFile != "",
		HasPrivateKey: t.KeyFile != "", HasKnownHosts: t.KnownHostsFile != "", HasPassword: t.Kind == KindSFTP && t.PasswordFile != "",
		UsedBy: nonNil(usedBy),
	}
}

func storedTargetView(t StoredTarget, usedBy []string) TargetView {
	_, access := t.Secrets[SecretAccessKey]
	_, secret := t.Secrets[SecretSecretKey]
	_, key := t.Secrets[SecretPrivateKey]
	_, pass := t.Secrets[SecretPassphrase]
	_, hosts := t.Secrets[SecretKnownHosts]
	_, password := t.Secrets[SecretPassword]
	return TargetView{
		Name: t.Name, Kind: t.Kind, Source: SourceDatabase, Encrypt: t.Encrypt,
		Endpoint: t.Endpoint, Bucket: t.Bucket, Region: t.Region, Prefix: t.Prefix, PathStyle: t.PathStyle,
		Host: t.Host, User: t.User, Root: t.Root, Port: t.Port, MaxConns: t.MaxConns,
		HasAccessKey: access, HasSecretKey: secret, HasPrivateKey: key, HasPassphrase: pass, HasKnownHosts: hosts, HasPassword: password,
		UsedBy: nonNil(usedBy),
	}
}

// locationChanged reports whether in moves the target to another place or
// account: anything that changes where its objects resolve.
func locationChanged(t StoredTarget, in TargetInput) bool {
	return t.Endpoint != in.Endpoint || t.Region != in.Region || t.Bucket != in.Bucket || t.Prefix != in.Prefix ||
		t.PathStyle != in.PathStyle || t.Host != in.Host || t.Port != in.Port || t.User != in.User || t.Root != in.Root
}

// addressChanged reports whether in points at another server than t: stored
// credentials must not be reused against it.
func addressChanged(t StoredTarget, in TargetInput) bool {
	return t.Endpoint != in.Endpoint || t.Host != in.Host || t.Port != in.Port
}

// mutate runs one change of the stored document as a read-modify-write in a
// transaction. The advisory lock makes concurrent changes (also from another
// server instance) wait, and fn sees the document and counts as they are
// inside the transaction. The result is validated as the next start will do.
func (s *Service) mutate(ctx context.Context, fn func(q *dbgen.Queries, doc *Doc) (audit.Event, error)) (View, error) {
	tx, err := s.opt.Pool.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := s.q.WithTx(tx)
	if err := q.LockStorageOverlay(ctx); err != nil {
		return View{}, fmt.Errorf("lock storage configuration: %w", err)
	}
	doc, err := Load(ctx, q)
	if err != nil {
		return View{}, err
	}
	ev, err := fn(q, &doc)
	if err != nil {
		return View{}, err
	}
	if err := s.validate(doc); err != nil {
		return View{}, err
	}
	doc.Version = 1
	raw, err := json.Marshal(doc)
	if err != nil {
		return View{}, fmt.Errorf("encode storage configuration: %w", err)
	}
	if err := q.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: SettingKey, Value: raw}); err != nil {
		return View{}, fmt.Errorf("store storage configuration: %w", err)
	}
	if err := audit.Record(ctx, q, ev); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit: %w", err)
	}
	s.opt.Log.Info("storage configuration updated", "action", ev.Action, "target", ev.TargetID)
	return s.view(ctx, doc)
}

// PutTarget creates or updates a target of the overlay. An omitted credential
// keeps the stored one, except when the address changes.
func (s *Service) PutTarget(ctx context.Context, actor uuid.UUID, ip, name string, in TargetInput) (View, error) {
	if in.Name = strings.TrimSpace(in.Name); in.Name != name {
		return View{}, fieldErr("name", "must match the name in the URL")
	}
	if err := in.normalize(true); err != nil {
		return View{}, err
	}
	if fileHasTarget(s.opt.File, name) {
		return View{}, fieldErr("name", "is a target of config.toml: it is read-only here and its name cannot be reused")
	}
	if !s.sealer.ok() {
		return View{}, ErrNoMasterKey
	}
	return s.mutate(ctx, func(q *dbgen.Queries, doc *Doc) (audit.Event, error) {
		idx := doc.target(name)
		var prev *StoredTarget
		if idx >= 0 {
			prev = &doc.Targets[idx]
			var p problems
			if prev.Kind != in.Kind {
				p.add("kind", "cannot be changed after creation")
			}
			if prev.Encrypt != in.Encrypt {
				p.add("encrypt", "cannot be changed after creation")
			}
			if err := p.err(); err != nil {
				return audit.Event{}, err
			}
			if locationChanged(*prev, in) {
				if err := s.checkUnlocked(ctx, q, name, *doc, "its location cannot change"); err != nil {
					return audit.Event{}, err
				}
			}
		}
		st, changed, err := s.build(in, prev, prev != nil && addressChanged(*prev, in))
		if err != nil {
			return audit.Event{}, err
		}
		if idx >= 0 {
			doc.Targets[idx] = st
		} else {
			doc.Targets = append(doc.Targets, st)
		}
		data := map[string]any{
			"name": name, "kind": in.Kind, "encrypt": in.Encrypt, "created": prev == nil, "credentials": changed,
			"endpoint": in.Endpoint, "region": in.Region, "bucket": in.Bucket, "prefix": in.Prefix, "pathStyle": in.PathStyle,
			"host": in.Host, "port": in.Port, "user": in.User, "root": in.Root,
		}
		return audit.Event{Actor: actor, Action: ActionTargetSaved, TargetType: "storage_target", TargetID: name, IP: ip, Data: data}, nil
	})
}

// checkUnlocked refuses a change when the target holds stored objects, as
// primary or as a confirmed replica of a saved or running placement.
func (s *Service) checkUnlocked(ctx context.Context, q *dbgen.Queries, name string, doc Doc, what string) error {
	rows, err := q.StorageObjectCountsByTarget(ctx)
	if err != nil {
		return fmt.Errorf("storage object counts: %w", err)
	}
	saved, err := applyPolicies(s.opt.File.Policy, doc)
	if err != nil {
		saved = s.opt.File.Policy
	}
	if n := objectsOn(rows, name, saved, s.opt.Running.Policy); n > 0 {
		return &ValidationError{Detail: "the target holds stored objects",
			Errors: []FieldError{{Path: "location", Msg: fmt.Sprintf("%d stored objects live on this target, so %s: a name stays bound to its location (credentials can still be replaced)", n, what)}}}
	}
	return nil
}

// build turns the input into its stored form, sealing the credentials. The
// returned list names the credential fields that were set or replaced. When
// moved is set the address changed and no stored credential is reused.
func (s *Service) build(in TargetInput, prev *StoredTarget, moved bool) (StoredTarget, []string, error) {
	st := StoredTarget{
		Name: in.Name, Kind: in.Kind, Encrypt: in.Encrypt,
		Endpoint: in.Endpoint, Bucket: in.Bucket, Region: in.Region, PathStyle: in.PathStyle, Prefix: in.Prefix,
		Host: in.Host, Port: in.Port, User: in.User, Root: in.Root, MaxConns: in.MaxConns,
		Secrets: map[string]string{},
	}
	var p problems
	var changed []string
	plain := map[string]string{} // credentials in clear text, for the checks below
	// take seals the typed value; when it is empty the stored one is kept,
	// unless keep is false (a new private key does not inherit the old
	// passphrase) or the address changed.
	take := func(field, value string, required, keep bool) {
		switch {
		case value != "":
			sealed, err := s.sealer.seal(value, in.Name, field)
			if err != nil {
				p.add("secrets."+field, "cannot be sealed")
				return
			}
			st.Secrets[field], plain[field] = sealed, value
			changed = append(changed, field)
		case keep && !moved && prev != nil && prev.Secrets[field] != "":
			st.Secrets[field] = prev.Secrets[field]
			if v, err := s.sealer.open(prev.Secrets[field], in.Name, field); err == nil {
				plain[field] = v
			} else {
				p.add("secrets."+field, "the stored value cannot be decrypted with the current master key: enter it again")
			}
		case required && moved:
			p.add("secrets."+field, "must be entered again because the address changed: stored credentials are not sent to another server")
		case required:
			p.add("secrets."+field, "is required")
		}
	}
	switch in.Kind {
	case KindS3:
		take(SecretAccessKey, in.Secrets.AccessKey, true, true)
		take(SecretSecretKey, in.Secrets.SecretKey, true, true)
	case KindSFTP:
		// A private key, a password or both. Typing only one of them replaces
		// the other: that is how the form switches the sign-in method.
		keepKey, keepPassword := sftpKeep(in.Secrets)
		take(SecretPrivateKey, in.Secrets.PrivateKey, false, keepKey)
		take(SecretPassphrase, in.Secrets.Passphrase, false, keepKey && in.Secrets.PrivateKey == "")
		take(SecretPassword, in.Secrets.Password, false, keepPassword)
		take(SecretKnownHosts, in.Secrets.KnownHosts, true, true)
		if st.Secrets[SecretPrivateKey] == "" && st.Secrets[SecretPassword] == "" {
			p.add("secrets.privateKey", "%s", missingSFTPAuth(moved))
		}
		if len(p) == 0 {
			checkSFTPSecrets(plain[SecretPrivateKey], plain[SecretPassphrase], plain[SecretKnownHosts], &p)
		}
	}
	if err := p.err(); err != nil {
		return StoredTarget{}, nil, err
	}
	return st, changed, nil
}

// validate merges doc over the file configuration and runs the whole storage
// validation, exactly as the next start will do.
func (s *Service) validate(doc Doc) error {
	if _, err := Merge(s.opt.File, doc, s.opt.MasterKey); err != nil {
		return &ValidationError{Detail: "the resulting storage configuration is not valid", Errors: fieldErrors(err)}
	}
	return nil
}

func fileHasTarget(f config.Storage, name string) bool {
	return slices.ContainsFunc(f.Targets, func(t config.Target) bool { return t.Name == name })
}

var pathPrefixRe = regexp.MustCompile(`^storage\.(target\[\d+\]|policy\.[a-z]+)\.`)

// fieldErrors extracts the field problems of a configuration error.
func fieldErrors(err error) []FieldError {
	var out []FieldError
	var walk func(error)
	walk = func(e error) {
		if j, ok := e.(interface{ Unwrap() []error }); ok {
			for _, x := range j.Unwrap() {
				walk(x)
			}
			return
		}
		var fe *config.FieldError
		if errors.As(e, &fe) {
			out = append(out, FieldError{Path: pathPrefixRe.ReplaceAllString(fe.Path, ""), Msg: fe.Msg})
			return
		}
		if u := errors.Unwrap(e); u != nil {
			walk(u)
			return
		}
		out = append(out, FieldError{Msg: e.Error()})
	}
	walk(err)
	return out
}

// DeleteTarget removes a target of the overlay. It is refused while a policy
// uses the target or while stored objects live on it, so that a name stays
// bound to its location.
func (s *Service) DeleteTarget(ctx context.Context, actor uuid.UUID, ip, name string) (View, error) {
	if fileHasTarget(s.opt.File, name) {
		return View{}, fieldErr("name", "is a target of config.toml: it is read-only here")
	}
	return s.mutate(ctx, func(q *dbgen.Queries, doc *Doc) (audit.Event, error) {
		idx := doc.target(name)
		if idx < 0 {
			return audit.Event{}, ErrNotFound
		}
		policies, err := applyPolicies(s.opt.File.Policy, *doc)
		if err != nil {
			return audit.Event{}, err
		}
		var users []string
		for _, pn := range slices.Sorted(maps.Keys(policies)) {
			p := policies[pn]
			if p.Primary == name {
				users = append(users, "primary of "+pn)
			}
			if slices.Contains(p.Replicas, name) {
				users = append(users, "replica of "+pn)
			}
			if p.Cache == name {
				users = append(users, "cache of "+pn)
			}
		}
		if len(users) > 0 {
			return audit.Event{}, &ValidationError{Detail: "the target is still used by a storage policy",
				Errors: []FieldError{{Path: "name", Msg: "is used as " + strings.Join(users, ", ") + ": change those policies first"}}}
		}
		if err := s.checkUnlocked(ctx, q, name, *doc, "it cannot be deleted"); err != nil {
			return audit.Event{}, err
		}
		t := doc.Targets[idx]
		doc.Targets = slices.Delete(doc.Targets, idx, idx+1)
		return audit.Event{Actor: actor, Action: ActionTargetDeleted, TargetType: "storage_target", TargetID: name, IP: ip,
			Data: map[string]any{"name": name, "kind": t.Kind, "endpoint": t.Endpoint, "bucket": t.Bucket, "prefix": t.Prefix,
				"host": t.Host, "port": t.Port, "root": t.Root}}, nil
	})
}

// PutPolicy sets the primary, replicas and cache of a data type. The primary
// can differ from the running one only while the policy holds no objects;
// setting it back to the running one is always accepted.
func (s *Service) PutPolicy(ctx context.Context, actor uuid.UUID, ip, policy string, in PolicyInput) (View, error) {
	file, ok := s.opt.File.Policy[policy]
	if !ok {
		return View{}, ErrNotFound
	}
	ov := PolicyOverride{Primary: strings.TrimSpace(in.Primary), Cache: strings.TrimSpace(in.Cache), Replicas: []string{}}
	for _, r := range in.Replicas {
		if r = strings.TrimSpace(r); r != "" {
			ov.Replicas = append(ov.Replicas, r)
		}
	}
	return s.mutate(ctx, func(q *dbgen.Queries, doc *Doc) (audit.Event, error) {
		if running, ok := s.opt.Running.Policy[policy]; ok && running.Primary != ov.Primary {
			rows, err := q.StorageObjectCountsByTarget(ctx)
			if err != nil {
				return audit.Event{}, fmt.Errorf("storage object counts: %w", err)
			}
			var n int64
			for _, r := range rows {
				if r.Policy == policy {
					n += r.Objects
				}
			}
			if n > 0 {
				return audit.Event{}, &ValidationError{Detail: "the primary of a policy with stored objects cannot be changed",
					Errors: []FieldError{{Path: "primary", Msg: fmt.Sprintf("%s holds %d stored objects on %q and there is no data migration: the primary can only change while the policy is empty (set it back to %q)", policy, n, running.Primary, running.Primary)}}}
			}
		}
		if doc.Policies == nil {
			doc.Policies = map[string]PolicyOverride{}
		}
		if samePlacement(config.Policy{Primary: ov.Primary, Replicas: ov.Replicas, Cache: ov.Cache}, file) {
			delete(doc.Policies, policy) // back to the file values
		} else {
			doc.Policies[policy] = ov
		}
		if len(doc.Policies) == 0 {
			doc.Policies = nil
		}
		return audit.Event{Actor: actor, Action: ActionPolicySaved, TargetType: "storage_policy", TargetID: policy, IP: ip,
			Data: map[string]any{"policy": policy, "primary": ov.Primary, "replicas": ov.Replicas, "cache": ov.Cache}}, nil
	})
}

// Test runs the connection test on a draft that is not saved. A credential
// left empty falls back to the stored one of the target with the same name,
// unless the address differs from the stored target.
func (s *Service) Test(ctx context.Context, in TargetInput) (tier.CheckResult, error) {
	if err := in.normalize(false); err != nil {
		return tier.CheckResult{}, err
	}
	if in.Encrypt && len(s.opt.MasterKey) == 0 {
		return tier.CheckResult{}, ErrNoMasterKey
	}
	var prev *StoredTarget
	moved := false
	if in.Name != "" {
		doc, err := Load(ctx, s.q)
		if err != nil {
			return tier.CheckResult{}, err
		}
		if i := doc.target(in.Name); i >= 0 && doc.Targets[i].Kind == in.Kind {
			prev = &doc.Targets[i]
			if moved = addressChanged(*prev, in); moved {
				prev = nil
			}
		}
	}
	t, err := s.draftConfig(in, prev, moved)
	if err != nil {
		return tier.CheckResult{}, err
	}
	if t.Name == "" {
		t.Name = "draft"
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	store, err := tier.BuildStore(ctx, t, tier.BuildOptions{MasterKey: s.opt.MasterKey, Log: s.opt.Log})
	if err != nil {
		return tier.CheckResult{}, &ValidationError{Detail: "the target cannot be opened", Errors: []FieldError{{Msg: shorten(err.Error(), maxTestError)}}}
	}
	defer tier.CloseStores(map[string]blob.Store{t.Name: store})
	res := tier.CheckStore(ctx, store)
	res.Target = in.Name
	for i := range res.Steps {
		res.Steps[i].Error = shorten(res.Steps[i].Error, maxTestError)
	}
	return res, nil
}

// draftConfig builds the driver configuration of a draft in clear text.
func (s *Service) draftConfig(in TargetInput, prev *StoredTarget, moved bool) (config.Target, error) {
	var p problems
	pick := func(field, typed string, required bool) string {
		switch {
		case typed != "":
			return typed
		case prev != nil && prev.Secrets[field] != "":
			v, err := s.sealer.open(prev.Secrets[field], in.Name, field)
			if err != nil {
				p.add("secrets."+field, "the stored value cannot be used: enter it again")
			}
			return v
		case required && moved:
			p.add("secrets."+field, "must be entered again because the address changed: stored credentials are not sent to another server")
		case required:
			p.add("secrets."+field, "is required")
		}
		return ""
	}
	t := config.Target{
		Name: in.Name, Kind: in.Kind, Encrypt: in.Encrypt, Guarded: true,
		Endpoint: in.Endpoint, Bucket: in.Bucket, Region: in.Region, PathStyle: in.PathStyle, Prefix: in.Prefix,
		Host: in.Host, Port: in.Port, User: in.User, Root: in.Root, MaxConns: in.MaxConns,
	}
	switch in.Kind {
	case KindS3:
		t.AccessKey = pick(SecretAccessKey, in.Secrets.AccessKey, true)
		t.SecretKey = pick(SecretSecretKey, in.Secrets.SecretKey, true)
	case KindSFTP:
		keepKey, keepPassword := sftpKeep(in.Secrets)
		if keepKey {
			t.KeyPEM = pick(SecretPrivateKey, in.Secrets.PrivateKey, false)
			if in.Secrets.PrivateKey != "" {
				t.KeyPassphrase = in.Secrets.Passphrase // a new key does not inherit the old passphrase
			} else {
				t.KeyPassphrase = pick(SecretPassphrase, in.Secrets.Passphrase, false)
			}
		}
		if keepPassword {
			t.Password = pick(SecretPassword, in.Secrets.Password, false)
		}
		if t.KeyPEM == "" && t.Password == "" {
			p.add("secrets.privateKey", "%s", missingSFTPAuth(moved))
		}
		t.KnownHostsData = pick(SecretKnownHosts, in.Secrets.KnownHosts, true)
		if len(p) == 0 {
			checkSFTPSecrets(t.KeyPEM, t.KeyPassphrase, t.KnownHostsData, &p)
		}
	}
	return t, p.err()
}

// sftpKeep tells which stored SFTP credentials an input keeps when it leaves
// them blank: typing only a password drops the stored key (and passphrase),
// typing only a key drops the stored password.
func sftpKeep(s Secrets) (key, password bool) {
	typedKey, typedPassword := s.PrivateKey != "", s.Password != ""
	return !typedPassword || typedKey, !typedKey || typedPassword
}

func missingSFTPAuth(moved bool) string {
	if moved {
		return "must be entered again because the address changed: stored credentials are not sent to another server"
	}
	return "a private key or a password is required"
}

// ScanHostKeys reads the SSH host keys of an SFTP server for the form. The
// host gets the same checks as a target host.
func (s *Service) ScanHostKeys(ctx context.Context, host string, port int) ([]tier.HostKey, error) {
	var p problems
	host = strings.TrimSpace(host)
	if port == 0 {
		port = defaultPort
	}
	switch {
	case !validHost(host):
		p.add("host", "must be a host name or an IP address")
	case blockedHost(host):
		p.add("host", "is a link-local, metadata, unspecified or multicast address, which is not allowed")
	}
	if port < 1 || port > 65535 {
		p.add("port", "must be between 1 and 65535")
	}
	if err := p.err(); err != nil {
		return nil, err
	}
	keys, err := tier.ScanHostKeys(ctx, host, port)
	if err != nil {
		return nil, &ValidationError{Detail: "the server did not answer", Errors: []FieldError{{Path: "host", Msg: shorten(err.Error(), maxTestError)}}}
	}
	return keys, nil
}
