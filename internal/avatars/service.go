// Package avatars stores the avatar images uploaded by users. Images are
// normalized to a 460x460 PNG and kept under a content-addressed key
// (avatars/<sha256>.png) on a storage target chosen by the admin, so their
// URLs never change and can be cached forever, by browsers or by a CDN.
//
// The settings (target and optional CDN base URL) live in the settings
// table and apply at once; changing the target does not move the images
// already stored, which Open still finds on any running target.
package avatars

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/storagecfg"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// SettingKey is the row of the settings table holding the Settings.
const SettingKey = "avatars.storage"

// Prefix is the key prefix of the images on the targets and the URL path
// they are served under.
const Prefix = "avatars/"

// Audit actions.
const (
	ActionSettingsSaved = "admin.avatar_storage_saved"
	actionUploaded      = "user.avatar_uploaded"
	actionRemoved       = "user.avatar_removed"
)

// ContentType of every stored image.
const ContentType = "image/png"

// Errors returned by the service.
var (
	ErrNotFound    = errors.New("avatar not found")
	ErrUnavailable = errors.New("no storage target is available for avatars")
	ErrTooLarge    = errors.New("avatar image is larger than 2 MiB")
)

// fileName matches the name of a stored image.
var fileName = regexp.MustCompile(`^[0-9a-f]{64}\.png$`)

// Settings say where avatars go.
type Settings struct {
	// Target is the name of a storage target; empty means the first local
	// target of the instance.
	Target string `json:"target"`
	// PublicURL is the base URL of a CDN serving the images; empty means
	// this server.
	PublicURL string `json:"publicUrl"`
}

// TargetInfo is a running target that can hold avatars.
type TargetInfo struct {
	Name, Kind string
	Encrypted  bool
}

// View is the settings with what the admin page needs to edit them.
type View struct {
	Settings
	// LocalTarget is the target used when Target is empty.
	LocalTarget string
	Targets     []TargetInfo
}

// Targets is the part of the storage tier the service uses.
type Targets interface {
	Targets() []*tier.Target
	Target(name string) (*tier.Target, bool)
}

// Options configure New.
type Options struct {
	Pool    *pgxpool.Pool
	Targets Targets
	// ServerURL is server.public_url: images served by this server get
	// absolute URLs on it (a relative path when empty).
	ServerURL string
	Log       *slog.Logger
}

// Service uploads, removes and serves avatars. It is safe for concurrent use.
type Service struct {
	opt Options
	q   *dbgen.Queries
}

// New returns a Service.
func New(opt Options) *Service {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	opt.ServerURL = strings.TrimRight(opt.ServerURL, "/")
	return &Service{opt: opt, q: dbgen.New(opt.Pool)}
}

func (s *Service) settings(ctx context.Context) (Settings, error) {
	row, err := s.q.GetSetting(ctx, SettingKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("load avatar settings: %w", err)
	}
	var st Settings
	if err := json.Unmarshal(row.Value, &st); err != nil {
		return Settings{}, fmt.Errorf("decode avatar settings: %w", err)
	}
	return st, nil
}

// localTarget is the first running target of kind local.
func (s *Service) localTarget() *tier.Target {
	for _, t := range s.opt.Targets.Targets() {
		if t != nil && t.Kind == "local" {
			return t
		}
	}
	return nil
}

func (s *Service) view(st Settings) View {
	v := View{Settings: st, Targets: []TargetInfo{}}
	if l := s.localTarget(); l != nil {
		v.LocalTarget = l.Name
	}
	for _, t := range s.opt.Targets.Targets() {
		if t != nil {
			v.Targets = append(v.Targets, TargetInfo{Name: t.Name, Kind: t.Kind, Encrypted: t.Encrypted})
		}
	}
	return v
}

// Config returns the settings.
func (s *Service) Config(ctx context.Context) (View, error) {
	st, err := s.settings(ctx)
	if err != nil {
		return View{}, err
	}
	return s.view(st), nil
}

// PutConfig validates and saves the settings.
func (s *Service) PutConfig(ctx context.Context, actor uuid.UUID, ip string, in Settings) (View, error) {
	in.Target = strings.TrimSpace(in.Target)
	in.PublicURL = strings.TrimRight(strings.TrimSpace(in.PublicURL), "/")
	var errs []storagecfg.FieldError
	if in.Target != "" {
		if _, ok := s.opt.Targets.Target(in.Target); !ok {
			errs = append(errs, storagecfg.FieldError{Path: "target", Msg: fmt.Sprintf("%q is not a running storage target (a target added in the web admin is available after a restart)", in.Target)})
		}
	} else if s.localTarget() == nil {
		errs = append(errs, storagecfg.FieldError{Path: "target", Msg: "the instance has no local target: choose one"})
	}
	if in.PublicURL != "" {
		u, err := url.Parse(in.PublicURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			errs = append(errs, storagecfg.FieldError{Path: "publicUrl", Msg: "must be an absolute http(s) URL without query or fragment"})
		}
	}
	if len(errs) > 0 {
		return View{}, &storagecfg.ValidationError{Detail: "invalid avatar storage settings", Errors: errs}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return View{}, fmt.Errorf("encode avatar settings: %w", err)
	}
	tx, err := s.opt.Pool.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := s.q.WithTx(tx)
	if err := q.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: SettingKey, Value: raw}); err != nil {
		return View{}, fmt.Errorf("store avatar settings: %w", err)
	}
	if err := audit.Record(ctx, q, audit.Event{Actor: actor, Action: ActionSettingsSaved, TargetType: "settings", TargetID: SettingKey,
		IP: ip, Data: map[string]any{"target": in.Target, "publicUrl": in.PublicURL}}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit: %w", err)
	}
	return s.view(in), nil
}

// store returns the target chosen by st.
func (s *Service) store(st Settings) (*tier.Target, error) {
	if st.Target == "" {
		if t := s.localTarget(); t != nil {
			return t, nil
		}
		return nil, ErrUnavailable
	}
	t, ok := s.opt.Targets.Target(st.Target)
	if !ok {
		return nil, fmt.Errorf("%w: target %q is not running", ErrUnavailable, st.Target)
	}
	return t, nil
}

// Upload normalizes the image read from r, stores it and makes it the avatar
// of the user. It returns the new avatar URL.
func (s *Service) Upload(ctx context.Context, userID uuid.UUID, ip string, r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return "", fmt.Errorf("read avatar: %w", err)
	}
	if len(data) > MaxUploadBytes {
		return "", ErrTooLarge
	}
	img, err := normalize(data)
	if err != nil {
		return "", err
	}
	st, err := s.settings(ctx)
	if err != nil {
		return "", err
	}
	tgt, err := s.store(st)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(img)
	name := hex.EncodeToString(sum[:]) + ".png"
	key := Prefix + name
	if _, err := tgt.Store.Stat(ctx, key); errors.Is(err, blob.ErrNotFound) {
		if _, err := tgt.Store.Put(ctx, key, bytes.NewReader(img), int64(len(img)),
			blob.PutOpts{SHA256: hex.EncodeToString(sum[:]), ContentType: ContentType}); err != nil {
			return "", fmt.Errorf("store avatar on %s: %w", tgt.Name, err)
		}
	} else if err != nil {
		return "", fmt.Errorf("stat avatar on %s: %w", tgt.Name, err)
	}
	base := st.PublicURL
	if base == "" {
		base = s.opt.ServerURL
	}
	u := base + "/" + key
	if err := s.setAvatar(ctx, userID, ip, u, actionUploaded); err != nil {
		return "", err
	}
	s.opt.Log.InfoContext(ctx, "avatar uploaded", "user", userID, "target", tgt.Name, "file", name)
	return u, nil
}

// Remove clears the avatar of the user. The stored image stays: its URL is
// immutable and may be cached or shared by another user.
func (s *Service) Remove(ctx context.Context, userID uuid.UUID, ip string) error {
	return s.setAvatar(ctx, userID, ip, "", actionRemoved)
}

func (s *Service) setAvatar(ctx context.Context, userID uuid.UUID, ip, avatarURL, action string) error {
	tx, err := s.opt.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := s.q.WithTx(tx)
	if err := q.SetUserAvatar(ctx, dbgen.SetUserAvatarParams{ID: userID, AvatarUrl: avatarURL}); err != nil {
		return fmt.Errorf("set avatar: %w", err)
	}
	if err := audit.Record(ctx, q, audit.Event{Actor: userID, Action: action, TargetType: "user", TargetID: userID.String(), IP: ip}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Open returns a stored image by file name (<sha256>.png). It looks on the
// configured target first, then on every other running target, so images
// stored before a change of target stay reachable.
func (s *Service) Open(ctx context.Context, file string) (io.ReadCloser, blob.Info, error) {
	if !fileName.MatchString(file) {
		return nil, blob.Info{}, ErrNotFound
	}
	key := Prefix + file
	var first *tier.Target
	if st, err := s.settings(ctx); err == nil {
		first, _ = s.store(st)
	}
	tried := map[string]bool{}
	for _, t := range append([]*tier.Target{first}, s.opt.Targets.Targets()...) {
		if t == nil || tried[t.Name] {
			continue
		}
		tried[t.Name] = true
		rc, info, err := t.Store.Get(ctx, key, nil)
		if err == nil {
			return rc, info, nil
		}
		if !errors.Is(err, blob.ErrNotFound) {
			s.opt.Log.WarnContext(ctx, "read avatar", "target", t.Name, "file", file, "error", err)
		}
	}
	return nil, blob.Info{}, ErrNotFound
}
