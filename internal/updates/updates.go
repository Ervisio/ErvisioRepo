// Package updates checks GitHub for new releases of the server and hands an
// update of a Docker installation to the updater container.
//
// The server never touches Docker itself: it has no access to the Docker
// socket. It writes the requested version (validated against the releases
// found on GitHub) to request.json in updates.dir, a folder shared with the
// optional "updater" compose service (deploy/docker/updater.sh). That
// container, the only one with the socket, runs deploy/docker/update.sh:
// backup, new compose files, ERVISIO_VERSION in .env, pull and up, rollback
// on failure. It reports through status.json, update.log and a heartbeat.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

const (
	// settingCheck holds the result of the last check (JSON), shared by
	// every server process; settingAuto the automatic update switch.
	settingCheck = "updates.check_result"
	settingAuto  = "updates.auto"
	// checkEvery is how often GitHub is asked.
	checkEvery = 6 * time.Hour
	// autoDelay lets the container image of a new release be built and
	// published before an automatic update pulls it.
	autoDelay = time.Hour
	// recentFailure: an automatic update does not retry a version that
	// failed within this time.
	recentFailure = 24 * time.Hour

	// Audit actions.
	ActionRequested = "admin.update_requested"
	ActionAuto      = "admin.update_auto_changed"
)

// Deployment kinds, from the ERVISIO_DEPLOY environment variable.
const (
	DeployDocker = "docker"
	DeployNative = "native"
)

// Errors of Apply and SetAuto.
var (
	// ErrUnavailable: the instance cannot be updated from the web (not
	// Docker, or the updater container is not running).
	ErrUnavailable = errors.New("updates: the updater is not available")
	// ErrConflict: an update is already running or requested, or the
	// version is not a newer release.
	ErrConflict = errors.New("updates: conflict")
)

// Options configure New.
type Options struct {
	Queries *dbgen.Queries
	// Current is the version of the running server.
	Current string
	// Check enables the GitHub check (updates.check).
	Check bool
	// Repository is the GitHub owner/name of the releases.
	Repository string
	// Dir is the folder shared with the updater (updates.dir).
	Dir string
	// Deploy is DeployDocker, DeployNative or empty when unknown.
	Deploy string
	// DeployDir is the host folder of compose.yaml (Docker), shown in the
	// manual update command.
	DeployDir string
	HTTP      *http.Client
	Log       *slog.Logger
	Now       func() time.Time
}

// Service checks for and requests updates.
type Service struct {
	o Options
}

// New builds the service.
func New(o Options) *Service {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.Log = o.Log.With("component", "updates")
	return &Service{o: o}
}

// Status is the update state shown in the admin.
type Status struct {
	Current string
	// Release: Current is a release version (not a development build).
	Release    bool
	Check      bool
	Repository string
	CheckedAt  *time.Time
	CheckError string
	// Latest is the newest release; nil before the first check.
	Latest *Release
	// Newer are the releases newer than Current, newest first.
	Newer []Release
	Auto  bool
	// Deploy is docker, native or empty; DeployDir the compose folder.
	Deploy    string
	DeployDir string
	Updater   UpdaterState
	// CanApply: an update can be started from the web now.
	CanApply bool
	// Command updates the instance from a terminal on the host.
	Command string
}

type checkResult struct {
	CheckedAt time.Time `json:"checkedAt"`
	Error     string    `json:"error,omitempty"`
	Releases  []Release `json:"releases"`
}

func (s *Service) loadCheck(ctx context.Context) (checkResult, error) {
	var r checkResult
	row, err := s.o.Queries.GetSetting(ctx, settingCheck)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, fmt.Errorf("load update check: %w", err)
	}
	if err := json.Unmarshal(row.Value, &r); err != nil {
		s.o.Log.WarnContext(ctx, "stored update check is unreadable, ignoring it", "error", err)
		return checkResult{}, nil
	}
	return r, nil
}

func (s *Service) auto(ctx context.Context) (bool, error) {
	row, err := s.o.Queries.GetSetting(ctx, settingAuto)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load automatic updates: %w", err)
	}
	var on bool
	_ = json.Unmarshal(row.Value, &on)
	return on, nil
}

// Status returns the current state, from the last stored check.
func (s *Service) Status(ctx context.Context) (Status, error) {
	r, err := s.loadCheck(ctx)
	if err != nil {
		return Status{}, err
	}
	auto, err := s.auto(ctx)
	if err != nil {
		return Status{}, err
	}
	cur := Normalize(s.o.Current)
	st := Status{Current: s.o.Current, Release: cur != "", Check: s.o.Check, Repository: s.o.Repository,
		CheckError: r.Error, Auto: auto, Deploy: s.o.Deploy, DeployDir: s.o.DeployDir, Updater: s.readUpdater()}
	if !r.CheckedAt.IsZero() {
		t := r.CheckedAt
		st.CheckedAt = &t
	}
	if len(r.Releases) > 0 {
		latest := r.Releases[0]
		st.Latest = &latest
	}
	for _, rel := range r.Releases {
		if cur != "" && Newer(cur, rel.Version) {
			st.Newer = append(st.Newer, rel)
		}
	}
	busy := st.Updater.State == StateRunning || st.Updater.Pending != ""
	st.CanApply = s.o.Deploy == DeployDocker && st.Updater.Available && !busy && len(st.Newer) > 0
	st.Command = s.command(st)
	return st, nil
}

// command is the terminal command that updates the instance by hand.
func (s *Service) command(st Status) string {
	raw := "https://raw.githubusercontent.com/" + s.o.Repository + "/main/deploy/"
	target := ""
	if len(st.Newer) > 0 {
		target = st.Newer[0].Version
	}
	switch s.o.Deploy {
	case DeployDocker:
		cmd := "curl -fsSL " + raw + "docker/update.sh | sh -s --"
		if s.o.DeployDir != "" {
			cmd += " --dir " + shellQuote(s.o.DeployDir)
		}
		if target != "" {
			cmd += " --version " + target
		}
		return cmd
	case DeployNative:
		cmd := "curl -fsSL " + raw + "install.sh | sudo sh -s --"
		if target != "" {
			cmd += " --version " + target
		}
		return cmd + " && sudo systemctl restart ervisio-repo"
	}
	return ""
}

// shellQuote quotes a path for a POSIX shell when it needs it.
func shellQuote(p string) string {
	if strings.IndexFunc(p, func(r rune) bool { return !shellSafe(r) }) >= 0 {
		return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	}
	return p
}

func shellSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-", r)
}

// Refresh asks GitHub for the releases now and stores the result (also a
// failure, so the admin sees it). It does nothing when the check is off.
func (s *Service) Refresh(ctx context.Context) error {
	if !s.o.Check {
		return nil
	}
	rels, err := s.fetchReleases(ctx)
	r := checkResult{CheckedAt: s.o.Now().UTC(), Releases: rels}
	if err != nil {
		prev, lerr := s.loadCheck(ctx)
		if lerr == nil {
			r.Releases = prev.Releases // keep what we knew
		}
		r.Error = err.Error()
		s.o.Log.WarnContext(ctx, "update check failed", "error", err)
	}
	b, merr := json.Marshal(r)
	if merr != nil {
		return fmt.Errorf("encode update check: %w", merr)
	}
	if uerr := s.o.Queries.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: settingCheck, Value: b}); uerr != nil {
		return fmt.Errorf("store update check: %w", uerr)
	}
	return nil
}

// SetAuto turns the automatic update on or off.
func (s *Service) SetAuto(ctx context.Context, actor uuid.UUID, ip string, on bool) error {
	b, _ := json.Marshal(on)
	if err := s.o.Queries.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: settingAuto, Value: b}); err != nil {
		return fmt.Errorf("store automatic updates: %w", err)
	}
	return audit.Record(ctx, s.o.Queries, audit.Event{Actor: actor, Action: ActionAuto, TargetType: "instance",
		TargetID: "updates", IP: ip, Data: map[string]any{"enabled": on}})
}

// Apply asks the updater to update to version, which must be a release
// newer than the running one found by the last check.
func (s *Service) Apply(ctx context.Context, actor uuid.UUID, login, ip, version string) error {
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if err := s.applicable(st, version); err != nil {
		return err
	}
	return s.request(ctx, st, actor, login, ip, Normalize(version))
}

func (s *Service) applicable(st Status, version string) error {
	v := Normalize(version)
	switch {
	case s.o.Deploy != DeployDocker:
		return fmt.Errorf("%w: only Docker installations can be updated from the web; use the command shown", ErrUnavailable)
	case !st.Updater.Available:
		return fmt.Errorf("%w: the updater container is not running (enable the \"updater\" compose profile)", ErrUnavailable)
	case st.Updater.State == StateRunning || st.Updater.Pending != "":
		return fmt.Errorf("%w: an update is already in progress", ErrConflict)
	case v == "":
		return fmt.Errorf("%w: %q is not a release version", ErrConflict, version)
	}
	for _, r := range st.Newer {
		if r.Version == v {
			return nil
		}
	}
	return fmt.Errorf("%w: %s is not a release newer than %s", ErrConflict, v, st.Current)
}

func (s *Service) request(ctx context.Context, st Status, actor uuid.UUID, login, ip, v string) error {
	if err := os.MkdirAll(s.o.Dir, 0o750); err != nil {
		return fmt.Errorf("%w: updater folder: %w", ErrUnavailable, err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("update id: %w", err)
	}
	if err := s.writeRequest(requestFile{ID: id.String(), Version: v, RequestedAt: s.o.Now().UTC().Format(time.RFC3339), By: login}); err != nil {
		return err
	}
	s.o.Log.InfoContext(ctx, "update requested", "from", st.Current, "to", v, "by", login)
	return audit.Record(ctx, s.o.Queries, audit.Event{Actor: actor, Action: ActionRequested, TargetType: "instance",
		TargetID: "updates", IP: ip, Data: map[string]any{"from": st.Current, "to": v, "automatic": actor == uuid.Nil}})
}

// tick is the periodic work: check GitHub when the last check is old, then
// start an automatic update when it is on and a release is old enough.
func (s *Service) tick(ctx context.Context) error {
	r, err := s.loadCheck(ctx)
	if err != nil {
		return err
	}
	if s.o.Check && s.o.Now().Sub(r.CheckedAt) >= checkEvery {
		if err := s.Refresh(ctx); err != nil {
			return err
		}
	}
	st, err := s.Status(ctx)
	if err != nil || !st.Auto || !st.CanApply {
		return err
	}
	target := st.Newer[0]
	if s.o.Now().Sub(target.PublishedAt) < autoDelay {
		return nil
	}
	u := st.Updater
	if u.State == StateFailed && u.Version == target.Version && u.FinishedAt != nil && s.o.Now().Sub(*u.FinishedAt) < recentFailure {
		return nil // do not loop on a broken release; the admin can still start it
	}
	return s.request(ctx, st, uuid.Nil, "automatic", "", target.Version)
}
