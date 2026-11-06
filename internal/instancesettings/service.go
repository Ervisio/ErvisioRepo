package instancesettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// ActionUpdated is the audit action of a change from the web admin.
const ActionUpdated = "admin.settings_updated"

// Options configures a Service.
type Options struct {
	Pool *pgxpool.Pool
	// ReadFile returns config.toml; nil when the server runs without a file.
	ReadFile  func() ([]byte, error)
	LookupEnv func(string) (string, bool)
	Log       *slog.Logger
}

// Service stores the overrides of the web admin and computes the settings
// the next start will run with.
type Service struct {
	opt     Options
	q       *dbgen.Queries
	base    *config.Config // file + environment at start, before overrides
	running *config.Config // the configuration this process runs with
}

// New returns a Service; call Apply before serving requests.
func New(opt Options) *Service {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	return &Service{opt: opt, q: dbgen.New(opt.Pool)}
}

// Setting is one editable key as shown by the web admin.
type Setting struct {
	Key            string `json:"key"`
	Value          any    `json:"value"`     // what the next start runs with
	FileValue      any    `json:"fileValue"` // config.toml, environment or default
	Source         string `json:"source"`
	Locked         bool   `json:"locked"`
	RestartPending bool   `json:"restartPending"`
}

// Document is the full list of editable settings.
type Document struct {
	Settings        []Setting `json:"settings"`
	RestartRequired bool      `json:"restartRequired"`
}

// ValidationError lists the keys an update was rejected for.
type ValidationError struct{ Errors []FieldError }

func (e *ValidationError) Error() string { return "invalid settings" }

// ErrEmpty is returned by Update without values.
var ErrEmpty = errors.New("no setting to change")

// Apply overlays the stored overrides on base (config.toml plus the
// environment) and returns the configuration to run with. A bad override is
// logged and skipped; only a database error is returned, and then the caller
// should go on with base.
func (s *Service) Apply(ctx context.Context, base *config.Config) (*config.Config, error) {
	s.base, s.running = base, base
	cfg, err := s.Overlay(ctx, base)
	if err != nil {
		return base, err
	}
	s.running = cfg
	return cfg, nil
}

// Overlay applies the stored overrides on base, logging the skipped ones.
func (s *Service) Overlay(ctx context.Context, base *config.Config) (*config.Config, error) {
	ov, err := s.overrides(ctx)
	if err != nil {
		return base, err
	}
	cfg, skipped := Overlay(base, ov, s.opt.LookupEnv)
	for _, sk := range skipped {
		s.opt.Log.Warn("instance settings: override ignored", "key", sk.Key, "reason", sk.Reason)
	}
	return cfg, nil
}

// Delete removes the overrides of keys, e.g. after the plugin wrote them to
// config.toml.
func (s *Service) Delete(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if _, err := s.q.DeleteInstanceSettings(ctx, keys); err != nil {
		return fmt.Errorf("delete instance settings: %w", err)
	}
	return nil
}

func (s *Service) overrides(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.q.ListInstanceSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list instance settings: %w", err)
	}
	out := make(map[string]json.RawMessage, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

// fileBase parses config.toml again, so that edits made by the plugin show
// up; it falls back to the configuration read at start.
func (s *Service) fileBase() *config.Config {
	if s.opt.ReadFile == nil {
		return s.base
	}
	data, err := s.opt.ReadFile()
	if err != nil {
		return s.base
	}
	cfg, err := config.Parse(data, s.opt.LookupEnv)
	if err != nil {
		return s.base
	}
	return cfg
}

// Document returns every editable setting with its source and state.
func (s *Service) Document(ctx context.Context) (Document, error) {
	ov, err := s.overrides(ctx)
	if err != nil {
		return Document{}, err
	}
	return s.document(s.fileBase(), ov), nil
}

func (s *Service) document(base *config.Config, ov map[string]json.RawMessage) Document {
	next, skipped := Overlay(base, ov, s.opt.LookupEnv)
	applied := make(map[string]bool, len(ov))
	for k := range ov {
		applied[k] = true
	}
	for _, sk := range skipped {
		delete(applied, sk.Key)
	}
	def := config.Default()
	defVals, baseVals, nextVals, runVals := Values(&def), Values(base), Values(next), Values(s.running)
	doc := Document{Settings: make([]Setting, 0, len(config.Editable))}
	for _, k := range config.Editable {
		st := Setting{Key: k, Value: nextVals[k], FileValue: baseVals[k], Locked: Locked(base, k, s.opt.LookupEnv)}
		switch {
		case FromEnv(k, s.opt.LookupEnv):
			st.Source = SourceEnv
		case applied[k]:
			st.Source = SourceDB
		case !reflect.DeepEqual(baseVals[k], defVals[k]):
			st.Source = SourceFile
		default:
			st.Source = SourceDefault
		}
		st.RestartPending = !reflect.DeepEqual(nextVals[k], runVals[k])
		doc.RestartRequired = doc.RestartRequired || st.RestartPending
		doc.Settings = append(doc.Settings, st)
	}
	return doc
}

// Update stores the given values as overrides. A value equal to the one of
// config.toml removes the override. The resulting configuration must be
// valid as a whole; changes apply at the next start.
func (s *Service) Update(ctx context.Context, actor uuid.UUID, ip string, values map[string]json.RawMessage) (Document, error) {
	if len(values) == 0 {
		return Document{}, ErrEmpty
	}
	base := s.fileBase()
	ov, err := s.overrides(ctx)
	if err != nil {
		return Document{}, err
	}
	baseVals := Values(base)
	keys := slices.Sorted(maps.Keys(values))
	norm := make(map[string]any, len(keys))
	var bad []FieldError
	for _, k := range keys {
		v, err := Normalize(k, values[k])
		switch {
		case err != nil:
			bad = append(bad, FieldError{k, err.Error()})
		case Locked(base, k, s.opt.LookupEnv):
			bad = append(bad, FieldError{k, "locked by the configuration file or the environment"})
		default:
			norm[k] = v
		}
	}
	if len(bad) > 0 {
		return Document{}, &ValidationError{bad}
	}
	// The other stored overrides apply as at start; the new values must
	// leave a valid configuration.
	rest := maps.Clone(ov)
	for _, k := range keys {
		delete(rest, k)
	}
	cand, _ := Overlay(base, rest, s.opt.LookupEnv)
	for _, k := range keys {
		if err := set(cand, k, norm[k]); err != nil {
			bad = append(bad, FieldError{k, err.Error()})
		}
	}
	if len(bad) == 0 {
		if err := cand.Validate(); err != nil {
			bad = FieldErrors(err)
		}
	}
	if len(bad) > 0 {
		return Document{}, &ValidationError{bad}
	}

	var drop []string
	upsert := map[string][]byte{}
	for _, k := range keys {
		if reflect.DeepEqual(norm[k], baseVals[k]) {
			drop = append(drop, k)
			delete(ov, k)
			continue
		}
		raw, err := json.Marshal(norm[k])
		if err != nil {
			return Document{}, fmt.Errorf("encode %s: %w", k, err)
		}
		upsert[k] = raw
		ov[k] = raw
	}
	tx, err := s.opt.Pool.Begin(ctx)
	if err != nil {
		return Document{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := s.q.WithTx(tx)
	for _, k := range slices.Sorted(maps.Keys(upsert)) {
		err := q.UpsertInstanceSetting(ctx, dbgen.UpsertInstanceSettingParams{Key: k, Value: upsert[k],
			UpdatedBy: uuid.NullUUID{UUID: actor, Valid: actor != uuid.Nil}})
		if err != nil {
			return Document{}, fmt.Errorf("store instance setting: %w", err)
		}
	}
	if len(drop) > 0 {
		if _, err := q.DeleteInstanceSettings(ctx, drop); err != nil {
			return Document{}, fmt.Errorf("delete instance settings: %w", err)
		}
	}
	err = audit.Record(ctx, q, audit.Event{Actor: actor, Action: ActionUpdated, TargetType: "instance",
		TargetID: "settings", IP: ip, Data: map[string]any{"keys": keys}})
	if err != nil {
		return Document{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, fmt.Errorf("commit: %w", err)
	}
	s.opt.Log.Info("instance settings updated", "keys", keys)
	return s.document(base, ov), nil
}
