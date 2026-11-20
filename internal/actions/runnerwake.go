package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// On-demand runners: an instance runner whose machine is off while idle
// gets a start URL. When a queued job needs it and it is offline, the
// server POSTs to the URL (an AWS Lambda function URL that starts an EC2
// instance, a cloud provider API proxy, …) at most once per cooldown.

const (
	wakeDefaultCooldown = 180
	// wakeRetry is the pause after a failed call (network error, machine
	// still stopping, server error); wakeRefusedRetry after a refusal
	// (wrong token, blocked hours) that will not change within a minute.
	wakeRetry        = 60
	wakeRefusedRetry = 600
	// wakeHold keeps a claimed runner away from other server processes
	// while the call runs.
	wakeHold = 60
	// wakeSeenGrace: a runner silent for longer is treated as off, even
	// before the sweep marks it offline.
	wakeSeenGrace = 75 * time.Second
	// wakeBusyGrace: a job an online runner could take wakes the
	// on-demand runner only after waiting this long.
	wakeBusyGrace = 90 * time.Second
	wakeInterval  = 20 * time.Second
	wakeTimeout   = 20 * time.Second
)

// Audit actions of the on-demand start.
const (
	ActionRunnerWakeSet     = "actions.runner_wake_set"
	ActionRunnerWakeRemoved = "actions.runner_wake_removed"
	ActionRunnerWoken       = "actions.runner_woken"
)

var wakeHeaderRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

// RunnerWake is the on-demand start of a runner as shown to admins.
type RunnerWake struct {
	Enabled    bool
	URL        string
	Header     string
	HasToken   bool
	Cooldown   int
	LastWakeAt *time.Time
	LastStatus int
	LastError  string
}

// RunnerWakeSettings configures the on-demand start. A nil Token keeps
// the stored one; an empty one removes it.
type RunnerWakeSettings struct {
	Enabled  bool
	URL      string
	Header   string
	Token    *string
	Cooldown int
}

func wakeFields(out *Runner, rn dbgen.ActionsRunner) {
	if rn.WakeUrl == "" {
		return
	}
	w := &RunnerWake{Enabled: rn.WakeEnabled, URL: rn.WakeUrl, Header: rn.WakeHeader, HasToken: len(rn.WakeToken) > 0,
		Cooldown: int(rn.WakeCooldown), LastStatus: int(rn.WakeStatus), LastError: rn.WakeError}
	if rn.LastWakeAt.Valid {
		t := rn.LastWakeAt.Time
		w.LastWakeAt = &t
	}
	out.Wake = w
}

func wakeAAD(id uuid.UUID) []byte { return []byte("runner-wake:" + id.String()) }

func checkWakeURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return invalid("the start URL must be an https:// URL without credentials")
	}
	return nil
}

// SetRunnerWake makes an instance runner on-demand.
func (s *Service) SetRunnerWake(ctx context.Context, who auth.Principal, sc Scope, id uuid.UUID, in RunnerWakeSettings, ip string) (Runner, error) {
	if sc.kind != "instance" {
		return Runner{}, ErrForbidden
	}
	rn, err := s.scopedRunner(ctx, sc, id)
	if err != nil {
		return Runner{}, err
	}
	in.URL, in.Header = strings.TrimSpace(in.URL), strings.TrimSpace(in.Header)
	if err := checkWakeURL(in.URL); err != nil {
		return Runner{}, err
	}
	if in.Header != "" && !wakeHeaderRe.MatchString(in.Header) {
		return Runner{}, invalid("invalid header name %q", in.Header)
	}
	if in.Cooldown == 0 {
		in.Cooldown = wakeDefaultCooldown
	}
	if in.Cooldown < 30 || in.Cooldown > 3600 {
		return Runner{}, invalid("the cooldown must be between 30 and 3600 seconds")
	}
	token := rn.WakeToken
	if in.Token != nil {
		token = nil
		if v := strings.TrimSpace(*in.Token); v != "" {
			if !s.seal.ok() {
				return Runner{}, fmt.Errorf("%w: %w", ErrConflict, ErrNoMasterKey)
			}
			if token, err = s.seal.seal([]byte(v), wakeAAD(id)); err != nil {
				return Runner{}, fmt.Errorf("seal wake token: %w", err)
			}
		}
	}
	return s.saveWake(ctx, who, sc, id, dbgen.SetActionsRunnerWakeParams{Enabled: in.Enabled, Url: in.URL,
		Header: in.Header, Token: token, Cooldown: int32(in.Cooldown), ID: id}, ActionRunnerWakeSet, ip) //nolint:gosec // checked above
}

// DeleteRunnerWake removes the on-demand start of an instance runner.
func (s *Service) DeleteRunnerWake(ctx context.Context, who auth.Principal, sc Scope, id uuid.UUID, ip string) (Runner, error) {
	if sc.kind != "instance" {
		return Runner{}, ErrForbidden
	}
	if _, err := s.scopedRunner(ctx, sc, id); err != nil {
		return Runner{}, err
	}
	return s.saveWake(ctx, who, sc, id, dbgen.SetActionsRunnerWakeParams{Cooldown: wakeDefaultCooldown, ID: id},
		ActionRunnerWakeRemoved, ip)
}

func (s *Service) saveWake(ctx context.Context, who auth.Principal, sc Scope, id uuid.UUID, p dbgen.SetActionsRunnerWakeParams, action, ip string) (Runner, error) {
	var rn dbgen.ActionsRunner
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		if rn, err = q.SetActionsRunnerWake(ctx, p); err != nil {
			return fmt.Errorf("set runner wake: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: who.UserID, Action: action, TargetType: "actions_runner",
			TargetID: id.String(), IP: ip, Data: map[string]any{"name": rn.Name, "url": p.Url, "enabled": p.Enabled}})
	})
	if isNoRows(err) {
		return Runner{}, ErrNotFound
	}
	if err != nil {
		return Runner{}, err
	}
	s.log.Info("actions runner wake updated", "runner_id", id, "action", action, "enabled", p.Enabled, "by", who.Username)
	if p.Enabled {
		s.hub.fireTasks()
	}
	return s.oneRunner(ctx, who, rn)
}

// WakeRunner calls the start URL of an instance runner now.
func (s *Service) WakeRunner(ctx context.Context, who auth.Principal, sc Scope, id uuid.UUID, ip string) (Runner, error) {
	if sc.kind != "instance" {
		return Runner{}, ErrForbidden
	}
	if _, err := s.scopedRunner(ctx, sc, id); err != nil {
		return Runner{}, err
	}
	rn, err := s.q.HoldActionsRunnerWake(ctx, dbgen.HoldActionsRunnerWakeParams{Hold: wakeHold, ID: id})
	if isNoRows(err) {
		return Runner{}, fmt.Errorf("%w: the runner has no start URL", ErrConflict)
	}
	if err != nil {
		return Runner{}, fmt.Errorf("hold runner wake: %w", err)
	}
	rn = s.callWake(ctx, rn)
	if err := audit.Record(ctx, s.q, audit.Event{Actor: who.UserID, Action: ActionRunnerWoken, TargetType: "actions_runner",
		TargetID: id.String(), IP: ip, Data: map[string]any{"name": rn.Name, "status": rn.WakeStatus}}); err != nil {
		s.log.Warn("audit runner wake", "runner_id", id, "err", err)
	}
	return s.oneRunner(ctx, who, rn)
}

func (s *Service) oneRunner(ctx context.Context, who auth.Principal, rn dbgen.ActionsRunner) (Runner, error) {
	list, err := s.runnersFrom(ctx, who, []dbgen.ActionsRunner{rn})
	if err != nil {
		return Runner{}, err
	}
	return list[0], nil
}

// WakeLoop starts the on-demand runners queued jobs need, until ctx ends.
// It runs when jobs are queued and every wakeInterval.
func (s *Service) WakeLoop(ctx context.Context) {
	for ctx.Err() == nil {
		ch := s.hub.tasksChanged()
		s.wakeNeeded(ctx)
		t := time.NewTimer(wakeInterval)
		select {
		case <-ctx.Done():
		case <-ch:
			// Let a burst of queued jobs settle and online runners claim.
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		case <-t.C:
		}
		t.Stop()
	}
}

func (s *Service) wakeNeeded(ctx context.Context) {
	now := s.now()
	rows, err := s.q.ClaimActionsRunnerWakes(ctx, dbgen.ClaimActionsRunnerWakesParams{Hold: wakeHold,
		SeenBefore: ts(now.Add(-wakeSeenGrace)), BusyBefore: ts(now.Add(-wakeBusyGrace))})
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("claim runner wakes", "err", err)
		}
		return
	}
	for _, rn := range rows {
		s.callWake(ctx, rn)
	}
}

// callWake POSTs to the start URL of rn and records the outcome: after a
// 2xx the next call waits the cooldown, after a failure wakeRetry or
// wakeRefusedRetry.
func (s *Service) callWake(ctx context.Context, rn dbgen.ActionsRunner) dbgen.ActionsRunner {
	status, msg := s.postWake(ctx, rn)
	retry := int32(wakeRetry)
	if status >= 200 && status < 300 {
		retry = rn.WakeCooldown
		s.log.Info("actions runner start requested", "runner_id", rn.ID, "name", rn.Name, "status", status)
	} else {
		if status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusConflict &&
			status != http.StatusTooEarly && status != http.StatusTooManyRequests {
			retry = wakeRefusedRetry
		}
		s.log.Warn("actions runner start failed", "runner_id", rn.ID, "name", rn.Name, "status", status, "err", msg)
	}
	out, err := s.q.RecordActionsRunnerWake(context.WithoutCancel(ctx), dbgen.RecordActionsRunnerWakeParams{
		Status: int32(status), Error: msg, RetryAfter: retry, ID: rn.ID}) //nolint:gosec // HTTP status
	if err != nil {
		s.log.Warn("record runner wake", "runner_id", rn.ID, "err", err)
		return rn
	}
	return out
}

func (s *Service) postWake(ctx context.Context, rn dbgen.ActionsRunner) (int, string) {
	if err := checkWakeURL(rn.WakeUrl); err != nil {
		return 0, err.Error()
	}
	var token string
	if len(rn.WakeToken) > 0 {
		plain, err := s.seal.open(rn.WakeToken, wakeAAD(rn.ID))
		if err != nil {
			return 0, "cannot read the stored token: " + err.Error()
		}
		token = string(plain)
	}
	cctx, cancel := context.WithTimeout(ctx, wakeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, rn.WakeUrl, http.NoBody)
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("User-Agent", "ErvisioRepo/"+serverVersion())
	req.Header.Set("X-Ervisio-Runner", rn.Name)
	switch {
	case token == "":
	case rn.WakeHeader == "":
		req.Header.Set("Authorization", "Bearer "+token)
	default:
		req.Header.Set(rn.WakeHeader, token)
	}
	client := &http.Client{Timeout: wakeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		// The URL may carry a token in the query: keep only the cause.
		if ue := (*url.Error)(nil); errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, cleanWakeBody(body, token)
}

// cleanWakeBody keeps a short, printable answer for display, without the
// token should the endpoint echo it.
func cleanWakeBody(b []byte, token string) string {
	if !utf8.Valid(b) {
		return ""
	}
	s := strings.Join(strings.Fields(string(b)), " ")
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300])
	}
	return s
}
