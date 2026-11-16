package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// maxRunnerGPUs bounds the GPUs kept from one report.
const maxRunnerGPUs = 32

// RunnerMetrics is the latest resource usage a runner reported. Nil
// fields were not measured. It is stored as JSON in actions_runners.
type RunnerMetrics struct {
	ReportedAt       time.Time   `json:"-"`
	Hostname         string      `json:"hostname,omitempty"`
	OS               string      `json:"os,omitempty"`
	Arch             string      `json:"arch,omitempty"`
	CPUCount         int         `json:"cpu_count,omitempty"`
	CPUPercent       *float64    `json:"cpu_percent,omitempty"`
	Load1            *float64    `json:"load1,omitempty"`
	MemoryUsedBytes  *int64      `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes *int64      `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes    *int64      `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes   *int64      `json:"disk_total_bytes,omitempty"`
	GPUs             []RunnerGPU `json:"gpus,omitempty"`
}

// RunnerGPU is one GPU of a runner machine.
type RunnerGPU struct {
	Name               string   `json:"name"`
	UtilizationPercent *float64 `json:"utilization_percent,omitempty"`
	MemoryUsedBytes    *int64   `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes   *int64   `json:"memory_total_bytes,omitempty"`
}

// RunnerJob is a job running on a runner. Visible is false when the
// caller cannot read its repository: only ID and StartedAt are set then.
type RunnerJob struct {
	ID           uuid.UUID
	StartedAt    *time.Time
	Visible      bool
	Name         string
	RunID        uuid.UUID
	RunNumber    int64
	WorkflowName string
	Owner        string
	Repo         string
}

// clipText bounds a runner-reported string.
func clipText(s string, n int) string {
	if !utf8.ValidString(s) {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// percent keeps a finite value within 0-100.
func percent(v float64, ok bool) *float64 {
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	v = math.Round(min(max(v, 0), 100)*10) / 10
	return &v
}

func bytesVal(v int64, ok bool) *int64 {
	if !ok || v < 0 {
		return nil
	}
	return &v
}

// metricsFromProto sanitises a runner report; nil when there is none.
func metricsFromProto(m *runnerv1.RunnerMetrics) *RunnerMetrics {
	if m == nil {
		return nil
	}
	out := &RunnerMetrics{
		Hostname: clipText(m.GetHostname(), 255), OS: clipText(m.GetOs(), 32), Arch: clipText(m.GetArch(), 32),
		CPUCount:         int(min(max(m.GetCpuCount(), 0), 1<<16)),
		CPUPercent:       percent(m.GetCpuPercent(), m.CpuPercent != nil),
		MemoryUsedBytes:  bytesVal(m.GetMemoryUsedBytes(), m.MemoryUsedBytes != nil),
		MemoryTotalBytes: bytesVal(m.GetMemoryTotalBytes(), m.MemoryTotalBytes != nil),
		DiskUsedBytes:    bytesVal(m.GetDiskUsedBytes(), m.DiskUsedBytes != nil),
		DiskTotalBytes:   bytesVal(m.GetDiskTotalBytes(), m.DiskTotalBytes != nil),
	}
	if l := m.GetLoad1(); m.Load1 != nil && l >= 0 && !math.IsNaN(l) && !math.IsInf(l, 0) {
		l = math.Round(l*100) / 100
		out.Load1 = &l
	}
	for i, g := range m.GetGpus() {
		if i == maxRunnerGPUs {
			break
		}
		out.GPUs = append(out.GPUs, RunnerGPU{Name: clipText(g.GetName(), 255),
			UtilizationPercent: percent(g.GetUtilizationPercent(), g.UtilizationPercent != nil),
			MemoryUsedBytes:    bytesVal(g.GetMemoryUsedBytes(), g.MemoryUsedBytes != nil),
			MemoryTotalBytes:   bytesVal(g.GetMemoryTotalBytes(), g.MemoryTotalBytes != nil)})
	}
	return out
}

// saveMetrics stores the latest report of a runner. Failures are logged
// only: metrics never fail a runner call.
func (r *RunnerService) saveMetrics(ctx context.Context, id uuid.UUID, m *runnerv1.RunnerMetrics) {
	mt := metricsFromProto(m)
	if mt == nil {
		return
	}
	data, err := json.Marshal(mt)
	if err != nil {
		r.s.log.Warn("runner metrics", "runner_id", id, "err", err)
		return
	}
	if err := r.s.q.SetActionsRunnerMetrics(ctx, dbgen.SetActionsRunnerMetricsParams{Metrics: data, ID: id}); err != nil {
		r.s.log.Warn("runner metrics", "runner_id", id, "err", err)
	}
}

// runnerMetrics decodes the stored report of a runner.
func runnerMetrics(rn dbgen.ActionsRunner) *RunnerMetrics {
	if len(rn.Metrics) == 0 || !rn.MetricsAt.Valid {
		return nil
	}
	var m RunnerMetrics
	if err := json.Unmarshal(rn.Metrics, &m); err != nil {
		return nil
	}
	m.ReportedAt = rn.MetricsAt.Time
	return &m
}

// runnersFrom builds the runner views with their running jobs; repository
// details of a job are shown only when who can read the repository.
func (s *Service) runnersFrom(ctx context.Context, who auth.Principal, rows []dbgen.ActionsRunner) ([]Runner, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, rn := range rows {
		ids[i] = rn.ID
	}
	jobs, err := s.q.ListRunningActionsJobsOfRunners(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("runner jobs: %w", err)
	}
	byRunner := map[uuid.UUID][]RunnerJob{}
	visible := map[uuid.UUID]*[2]string{} // repo id → owner, name (nil: hidden)
	d, _ := s.d()
	for _, j := range jobs {
		rj := RunnerJob{ID: j.ID}
		if j.StartedAt.Valid {
			t := j.StartedAt.Time
			rj.StartedAt = &t
		}
		repo, seen := visible[j.RepoID]
		if !seen {
			if d != nil {
				if r, err := d.Repos.GetByID(ctx, who, j.RepoID); err == nil {
					repo = &[2]string{r.Owner, r.Name}
				}
			}
			visible[j.RepoID] = repo
		}
		if repo != nil {
			rj.Visible, rj.Name, rj.RunID, rj.RunNumber, rj.WorkflowName = true, j.Name, j.RunID, j.RunNumber, j.WorkflowName
			rj.Owner, rj.Repo = repo[0], repo[1]
		}
		byRunner[j.RunnerID.UUID] = append(byRunner[j.RunnerID.UUID], rj)
	}
	out := make([]Runner, 0, len(rows))
	for _, rn := range rows {
		r := s.runnerFrom(ctx, rn, len(byRunner[rn.ID]))
		r.Jobs = byRunner[rn.ID]
		out = append(out, r)
	}
	return out, nil
}
