package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// Runner device registration, in the style of the OAuth device flow (RFC
// 8628): the installer calls RequestDevice and shows the user code, a
// signed-in user approves it at /runner/approve choosing the scope, and
// the installer's PollDevice receives a single-use registration token.
// The poll result is never a user token.

const (
	// RunnerDeviceTTL is how long a device code stays valid.
	RunnerDeviceTTL = 15 * time.Minute
	// RunnerApprovePath is the SPA approval page.
	RunnerApprovePath = "/runner/approve"
	// deviceInterval is the initial polling interval.
	deviceInterval = 5 * time.Second
	// deviceSlowDownStep is added to the interval at every slow_down.
	deviceSlowDownStep = 5 * time.Second
	// deviceTokenTTL is the life of the registration token of an
	// approved device (the installer registers right after its poll).
	deviceTokenTTL = 10 * time.Minute
	// deviceCodesPerHour bounds device codes per client IP.
	deviceCodesPerHour = 20
	// maxDeviceCapacity bounds the capacity requested or approved.
	maxDeviceCapacity = 64
)

// Device statuses (actions_runner_devices.status) and poll answers.
const (
	devicePending  = "pending"
	deviceApproved = "approved"
	deviceDenied   = "denied"
	deviceConsumed = "consumed"
	deviceSlowDown = "slow_down"
	deviceExpired  = "expired"
)

// Audit actions of the device registration.
const (
	ActionRunnerDeviceApproved = "actions.runner_device_approved"
	ActionRunnerDeviceDenied   = "actions.runner_device_denied"
)

// ErrDeviceLimited means too many wrong user codes; Wait says how long to
// back off.
type ErrDeviceLimited struct{ Wait time.Duration }

func (e *ErrDeviceLimited) Error() string { return "too many attempts, retry later" }

// requestIP is the client IP the router stored for a runner call, or the
// peer address.
func requestIP(ctx context.Context, peer string) string {
	if ip := auth.RequestInfoFrom(ctx).IP; ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(peer); err == nil {
		return host
	}
	return peer
}

// cleanText drops control characters and bounds s to n bytes.
func cleanText(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > n {
		s = strings.ToValidUTF8(s[:n], "")
	}
	return s
}

// labelNames validates label names (no backend) for a device request.
func labelNames(in []string) ([]string, error) {
	out := []string{}
	for _, l := range in {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" {
			continue
		}
		if len(l) > 255 || strings.ContainsAny(l, ":= ,\t") {
			return nil, invalid("invalid label name %.40q", l)
		}
		if !contains(out, l) {
			out = append(out, l)
		}
	}
	if len(out) > maxRunnerLabels {
		return nil, invalid("at most %d labels", maxRunnerLabels)
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// RequestDevice starts a device registration.
func (r *RunnerService) RequestDevice(ctx context.Context, req *connect.Request[runnerv1.RequestDeviceRequest]) (*connect.Response[runnerv1.RequestDeviceResponse], error) {
	s := r.s
	ip := requestIP(ctx, req.Peer().Addr)
	if ok, wait := s.device.AllowAction(auth.IPKey("runner-device:" + ip)); !ok {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			fmt.Errorf("too many runner registrations from this address, retry in %s", wait.Round(time.Minute)))
	}
	m := req.Msg
	labels, err := labelNames(m.GetLabels())
	if err != nil {
		return nil, badRequest("%v", err)
	}
	capacity := m.GetCapacity()
	if capacity < 0 || capacity > maxDeviceCapacity {
		return nil, badRequest("capacity must be between 1 and %d", maxDeviceCapacity)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, internalErr(s, "device id", err)
	}
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, internalErr(s, "device code", err)
	}
	deviceCode := "erdc_" + hex.EncodeToString(raw[:])
	expires := s.now().Add(RunnerDeviceTTL)
	params := dbgen.InsertActionsRunnerDeviceParams{ID: id, DeviceCodeHash: auth.HashToken(deviceCode),
		Hostname: cleanText(m.GetHostname(), 255), Os: cleanText(m.GetOs(), 32), Arch: cleanText(m.GetArch(), 32),
		Cpus: min(max(m.GetCpus(), 0), 1<<16), MemoryBytes: max(m.GetMemoryBytes(), 0), Docker: m.GetDocker(),
		Version: cleanText(m.GetVersion(), 255), Name: cleanText(m.GetName(), 255), Labels: labels,
		Capacity: capacity, RequestIp: ip, IntervalSeconds: int32(deviceInterval / time.Second), ExpiresAt: ts(expires)}
	for range 5 {
		params.UserCode = credentials.NewUserCode()
		err = s.q.InsertActionsRunnerDevice(ctx, params)
		if isUniqueViolation(err) {
			continue
		}
		break
	}
	if err != nil {
		return nil, internalErr(s, "insert runner device", err)
	}
	user := credentials.FormatUserCode(params.UserCode)
	uri := s.public + RunnerApprovePath
	s.log.Info("runner device registration requested", "device_id", id, "hostname", params.Hostname, "ip", ip)
	return connect.NewResponse(&runnerv1.RequestDeviceResponse{DeviceCode: deviceCode, UserCode: user,
		VerificationUri: uri, VerificationUriComplete: uri + "?code=" + user,
		ExpiresIn: int32(RunnerDeviceTTL / time.Second), Interval: params.IntervalSeconds}), nil
}

// PollDevice answers the state of a device registration.
func (r *RunnerService) PollDevice(ctx context.Context, req *connect.Request[runnerv1.PollDeviceRequest]) (*connect.Response[runnerv1.PollDeviceResponse], error) {
	s := r.s
	code := strings.TrimSpace(req.Msg.GetDeviceCode())
	if !validToken(code, "erdc_") {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown device code"))
	}
	var out *runnerv1.PollDeviceResponse
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		row, err := q.GetActionsRunnerDeviceByHashForUpdate(ctx, auth.HashToken(code))
		if isNoRows(err) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get runner device: %w", err)
		}
		now := s.now()
		if !row.ExpiresAt.Time.After(now) || row.Status == deviceConsumed {
			out = &runnerv1.PollDeviceResponse{Status: deviceExpired}
			return nil
		}
		interval := row.IntervalSeconds
		status := ""
		if row.LastPollAt.Valid && now.Sub(row.LastPollAt.Time) < time.Duration(interval)*time.Second {
			interval += int32(deviceSlowDownStep / time.Second)
			status = deviceSlowDown
		}
		if err := q.SetActionsRunnerDevicePoll(ctx, dbgen.SetActionsRunnerDevicePollParams{ID: row.ID,
			PolledAt: ts(now), IntervalSeconds: interval}); err != nil {
			return fmt.Errorf("record poll: %w", err)
		}
		if status == "" {
			status = row.Status
		}
		out = &runnerv1.PollDeviceResponse{Status: status, Interval: interval}
		if status != deviceApproved {
			return nil
		}
		tok, err := s.deviceToken(ctx, q, row)
		if err != nil {
			return err
		}
		if err := q.ConsumeActionsRunnerDevice(ctx, row.ID); err != nil {
			return fmt.Errorf("consume runner device: %w", err)
		}
		out.RegistrationToken, out.Name, out.Labels, out.Capacity = tok, row.Name, row.Labels, row.Capacity
		out.Scope = s.runnerScope(ctx, row.Scope.String, row.OwnerID, row.RepoID)
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown device code"))
	}
	if err != nil {
		return nil, internalErr(s, "poll runner device", err)
	}
	if out.GetStatus() == deviceApproved {
		s.log.Info("runner device registration token issued", "scope", out.GetScope(), "name", out.GetName())
	}
	return connect.NewResponse(out), nil
}

// deviceToken mints the single-use registration token of an approved
// device, in the caller's transaction.
func (s *Service) deviceToken(ctx context.Context, q *dbgen.Queries, row dbgen.ActionsRunnerDevice) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("token id: %w", err)
	}
	tok, hash := newSecretToken(RegistrationTokenPrefix)
	if _, err := q.InsertActionsRegistrationToken(ctx, dbgen.InsertActionsRegistrationTokenParams{ID: id,
		TokenHash: hash, Scope: row.Scope.String, OwnerID: row.OwnerID, RepoID: row.RepoID, CreatedBy: row.DecidedBy,
		ExpiresAt: ts(s.now().Add(deviceTokenTTL)), SingleUse: true}); err != nil {
		return "", fmt.Errorf("insert registration token: %w", err)
	}
	return tok, nil
}

// RunnerDevice is a pending device registration shown to the approver.
type RunnerDevice struct {
	UserCode    string
	Hostname    string
	OS          string
	Arch        string
	CPUs        int
	MemoryBytes int64
	Docker      bool
	Version     string
	Name        string
	Labels      []string
	Capacity    int
	RequestIP   string
	// CanInstance tells whether the caller may approve an instance runner.
	CanInstance bool
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

func deviceKeys(userID uuid.UUID, ip string) []string {
	return []string{auth.AccountKey("runner-device:" + userID.String()), auth.IPKey("runner-device-code:" + ip)}
}

// pendingDevice resolves a user code, counting wrong ones for the
// brute-force limiter.
func (s *Service) pendingDevice(ctx context.Context, q *dbgen.Queries, who auth.Principal, userCode, ip string, lock bool) (dbgen.ActionsRunnerDevice, error) {
	if who.UserID == uuid.Nil || who.Grant != nil {
		return dbgen.ActionsRunnerDevice{}, ErrForbidden
	}
	keys := deviceKeys(who.UserID, ip)
	if wait := s.device.LoginWait(keys...); wait > 0 {
		return dbgen.ActionsRunnerDevice{}, &ErrDeviceLimited{Wait: wait}
	}
	code := credentials.NormalizeUserCode(userCode)
	if code == "" {
		s.device.Fail(keys...)
		return dbgen.ActionsRunnerDevice{}, ErrNotFound
	}
	get := q.GetPendingActionsRunnerDevice
	if lock {
		get = q.GetPendingActionsRunnerDeviceForUpdate
	}
	row, err := get(ctx, code)
	if isNoRows(err) {
		s.device.Fail(keys...)
		return dbgen.ActionsRunnerDevice{}, ErrNotFound
	}
	if err != nil {
		return dbgen.ActionsRunnerDevice{}, fmt.Errorf("get runner device: %w", err)
	}
	return row, nil
}

// LookupRunnerDevice returns the pending registration with userCode.
func (s *Service) LookupRunnerDevice(ctx context.Context, who auth.Principal, userCode, ip string) (RunnerDevice, error) {
	row, err := s.pendingDevice(ctx, s.q, who, userCode, ip, false)
	if err != nil {
		return RunnerDevice{}, err
	}
	return RunnerDevice{UserCode: credentials.FormatUserCode(row.UserCode), Hostname: row.Hostname, OS: row.Os,
		Arch: row.Arch, CPUs: int(row.Cpus), MemoryBytes: row.MemoryBytes, Docker: row.Docker, Version: row.Version,
		Name: row.Name, Labels: row.Labels, Capacity: int(row.Capacity), RequestIP: row.RequestIp,
		CanInstance: who.IsAdmin() && who.Grant == nil, CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time}, nil
}

// RunnerDeviceDecision is the approver's answer.
type RunnerDeviceDecision struct {
	Approve bool
	// Scope is "instance", "org:<name>" or "repo:<owner>/<name>".
	Scope string
	// Name, Labels and Capacity override the requested ones when set.
	Name     string
	Labels   []string
	Capacity int
}

// ParseScope resolves a scope label and checks the caller may manage it.
func (s *Service) ParseScope(ctx context.Context, who auth.Principal, label string) (Scope, error) {
	label = strings.TrimSpace(label)
	switch {
	case label == "instance":
		return s.InstanceScope(who)
	case strings.HasPrefix(label, "org:"):
		name := strings.TrimSpace(strings.TrimPrefix(label, "org:"))
		if name == "" {
			return Scope{}, invalid("the organization name is missing")
		}
		return s.OwnerScope(ctx, who, name)
	case strings.HasPrefix(label, "repo:"):
		owner, name, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(label, "repo:")), "/")
		if !ok || owner == "" || name == "" {
			return Scope{}, invalid("the repository must be owner/name")
		}
		return s.RepoScope(ctx, who, owner, name, "")
	}
	return Scope{}, invalid("the scope must be instance, org:<name> or repo:<owner>/<name>")
}

// DecideRunnerDevice approves or denies the pending registration with
// userCode. Approval needs the right to manage runners at the scope.
func (s *Service) DecideRunnerDevice(ctx context.Context, who auth.Principal, userCode string, d RunnerDeviceDecision, ip string) error {
	if _, err := s.pendingDevice(ctx, s.q, who, userCode, ip, false); err != nil {
		return err
	}
	var sc Scope
	if d.Approve {
		var err error
		if sc, err = s.ParseScope(ctx, who, d.Scope); err != nil {
			return err
		}
		if err := runnerScopeOK(sc); err != nil {
			return err
		}
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		row, err := s.pendingDevice(ctx, q, who, userCode, ip, true)
		if err != nil {
			return err
		}
		p := dbgen.DecideActionsRunnerDeviceParams{ID: row.ID, Status: deviceDenied, Name: row.Name,
			Labels: row.Labels, Capacity: row.Capacity, DecidedBy: nullID(who.UserID)}
		action := ActionRunnerDeviceDenied
		if d.Approve {
			action = ActionRunnerDeviceApproved
			p.Status = deviceApproved
			if n := cleanText(d.Name, 255); n != "" {
				p.Name = n
			}
			if p.Name == "" {
				p.Name = row.Hostname
			}
			if p.Name == "" {
				return invalid("the runner name is required")
			}
			if d.Labels != nil {
				if p.Labels, err = labelNames(d.Labels); err != nil {
					return err
				}
			}
			if d.Capacity < 0 || d.Capacity > maxDeviceCapacity {
				return invalid("capacity must be between 1 and %d", maxDeviceCapacity)
			}
			if d.Capacity > 0 {
				p.Capacity = int32(d.Capacity) //nolint:gosec // bounded above
			}
			owner, repo := sc.params()
			p.Scope = pgtype.Text{String: sc.kind, Valid: true}
			p.OwnerID, p.RepoID = owner, repo
		}
		if err := q.DecideActionsRunnerDevice(ctx, p); err != nil {
			return fmt.Errorf("decide runner device: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: who.UserID, Action: action, TargetType: "actions_runner_device",
			TargetID: row.ID.String(), IP: ip, OrgID: sc.auditOrg(), Data: map[string]any{"hostname": row.Hostname,
				"name": p.Name, "scope": sc.label, "labels": p.Labels, "capacity": p.Capacity, "request_ip": row.RequestIp}})
	})
}
