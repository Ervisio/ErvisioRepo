package actions

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/buildinfo"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// Runner self-update: an admin asks a runner to update to the server
// version; the request travels in the Declare and Heartbeat answers until
// the runner declares that version (done) or reports a failure.

const (
	// BuiltinRunnerName is the instance runner shipped with the server's
	// compose file; it follows the server image and never self-updates.
	BuiltinRunnerName = "builtin"
	// runnerReleaseURL is where the release assets of a version live, the
	// same place install.sh and install.ps1 download from.
	runnerReleaseURL = "https://github.com/Ervisio/ErvisioRepo/releases/download/v"
	// maxUpdateError bounds the failure text a runner reports.
	maxUpdateError = 300
	// ActionRunnerUpdate is the audit action of an update request.
	ActionRunnerUpdate = "actions.runner_update"
)

// parseVersion reads "x.y.z" (optional "v" prefix, no pre-release).
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// olderVersion reports a < b; false when either is not a release version.
func olderVersion(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] < vb[i]
		}
	}
	return false
}

// serverVersion is the version runners update to ("" for a development
// build, which has no release assets).
func serverVersion() string {
	v, ok := parseVersion(buildinfo.Get().Version)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

func isBuiltinRunner(rn dbgen.ActionsRunner) bool {
	return rn.Scope == "instance" && rn.Name == BuiltinRunnerName
}

// canSelfUpdate: the runner said it can replace its binary, and it is not
// the built-in runner (updated with the server image) nor ephemeral.
func canSelfUpdate(rn dbgen.ActionsRunner) bool {
	return rn.SelfUpdate && !rn.Ephemeral && !isBuiltinRunner(rn)
}

// protoUpdate is the pending update request of a runner, or nil.
func protoUpdate(rn dbgen.ActionsRunner) *runnerv1.RunnerUpdate {
	if rn.UpdateVersion == "" {
		return nil
	}
	return &runnerv1.RunnerUpdate{Version: rn.UpdateVersion, DownloadUrl: runnerReleaseURL + rn.UpdateVersion}
}

// cleanUpdateError keeps a runner-reported failure short and printable.
func cleanUpdateError(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, strings.TrimSpace(s))
	if len(s) > maxUpdateError {
		s = s[:maxUpdateError]
	}
	return strings.ToValidUTF8(s, "")
}

// declaredUpdate clears a pending update once the runner declares the
// target version (or a newer one).
func (r *RunnerService) declaredUpdate(ctx context.Context, rn dbgen.ActionsRunner) dbgen.ActionsRunner {
	if rn.UpdateVersion == "" || olderVersion(rn.Version, rn.UpdateVersion) {
		return rn
	}
	if err := r.s.q.ClearActionsRunnerUpdate(ctx, dbgen.ClearActionsRunnerUpdateParams{ID: rn.ID, Version: rn.UpdateVersion}); err != nil {
		r.s.log.Warn("clear runner update", "runner_id", rn.ID, "err", err)
		return rn
	}
	r.s.log.Info("actions runner updated", "runner_id", rn.ID, "version", rn.Version)
	rn.UpdateVersion = ""
	rn.UpdateError = ""
	return rn
}

// failedUpdate records an update failure reported in a heartbeat.
func (r *RunnerService) failedUpdate(ctx context.Context, rn dbgen.ActionsRunner, msg *runnerv1.HeartbeatRequest) dbgen.ActionsRunner {
	text := cleanUpdateError(msg.GetUpdateError())
	if text == "" || rn.UpdateVersion == "" || msg.GetUpdateErrorVersion() != rn.UpdateVersion {
		return rn
	}
	if err := r.s.q.FailActionsRunnerUpdate(ctx, dbgen.FailActionsRunnerUpdateParams{ID: rn.ID, Version: rn.UpdateVersion, Error: text}); err != nil {
		r.s.log.Warn("runner update failure", "runner_id", rn.ID, "err", err)
		return rn
	}
	r.s.log.Warn("actions runner update failed", "runner_id", rn.ID, "version", rn.UpdateVersion, "error", text)
	rn.UpdateVersion = ""
	rn.UpdateError = text
	return rn
}

// updateFields fills the update state of a runner shown to users.
func updateFields(out *Runner, rn dbgen.ActionsRunner) {
	target := serverVersion()
	out.UpdateAvailable = target != "" && olderVersion(rn.Version, target)
	out.UpdatePending = rn.UpdateVersion != ""
	out.UpdateVersion = rn.UpdateVersion
	out.UpdateError = rn.UpdateError
	out.SelfUpdate = canSelfUpdate(rn)
}

// RequestRunnerUpdate asks a runner registered at sc to update itself to
// the server version.
func (s *Service) RequestRunnerUpdate(ctx context.Context, who auth.Principal, sc Scope, id uuid.UUID, ip string) (Runner, error) {
	rn, err := s.scopedRunner(ctx, sc, id)
	if err != nil {
		return Runner{}, err
	}
	target := serverVersion()
	switch {
	case target == "":
		return Runner{}, fmt.Errorf("%w: the server runs a development build, there is no release to update to", ErrConflict)
	case isBuiltinRunner(rn):
		return Runner{}, fmt.Errorf("%w: the built-in runner is updated with the server image", ErrConflict)
	case !canSelfUpdate(rn):
		return Runner{}, fmt.Errorf("%w: this runner cannot update itself; run the installer again with --upgrade", ErrConflict)
	case !olderVersion(rn.Version, target):
		return Runner{}, fmt.Errorf("%w: the runner already runs version %s", ErrConflict, rn.Version)
	}
	if rn, err = s.requestUpdate(ctx, who, sc, rn, target, ip); err != nil {
		return Runner{}, err
	}
	list, err := s.runnersFrom(ctx, who, []dbgen.ActionsRunner{rn})
	if err != nil {
		return Runner{}, err
	}
	return list[0], nil
}

// RequestRunnerUpdates asks every online, outdated runner of sc that can
// update itself to update; it returns how many were asked and the target.
func (s *Service) RequestRunnerUpdates(ctx context.Context, who auth.Principal, sc Scope, ip string) (int, string, error) {
	if err := runnerScopeOK(sc); err != nil {
		return 0, "", err
	}
	target := serverVersion()
	if target == "" {
		return 0, "", nil
	}
	owner, repo := sc.params()
	rows, err := s.q.ListActionsRunners(ctx, dbgen.ListActionsRunnersParams{Scope: sc.kind, OwnerID: owner, RepoID: repo})
	if err != nil {
		return 0, "", fmt.Errorf("list runners: %w", err)
	}
	n := 0
	for _, rn := range rows {
		online := rn.Online && rn.LastSeenAt.Valid && s.now().Sub(rn.LastSeenAt.Time) < s.offlineAfter()
		if !online || !canSelfUpdate(rn) || !olderVersion(rn.Version, target) || rn.UpdateVersion == target {
			continue
		}
		if _, err := s.requestUpdate(ctx, who, sc, rn, target, ip); err != nil {
			return n, target, err
		}
		n++
	}
	return n, target, nil
}

func (s *Service) requestUpdate(ctx context.Context, who auth.Principal, sc Scope, rn dbgen.ActionsRunner, target, ip string) (dbgen.ActionsRunner, error) {
	from := rn.Version
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		if rn, err = q.RequestActionsRunnerUpdate(ctx, dbgen.RequestActionsRunnerUpdateParams{Version: target, ID: rn.ID}); err != nil {
			return fmt.Errorf("request runner update: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: who.UserID, Action: ActionRunnerUpdate, TargetType: "actions_runner",
			TargetID: rn.ID.String(), IP: ip, OrgID: sc.auditOrg(),
			Data: map[string]any{"name": rn.Name, "scope": sc.label, "from": from, "to": target}})
	})
	if isNoRows(err) {
		return rn, ErrNotFound
	}
	if err != nil {
		return rn, err
	}
	s.log.Info("actions runner update requested", "runner_id", rn.ID, "from", from, "to", target, "by", who.Username)
	return rn, nil
}
