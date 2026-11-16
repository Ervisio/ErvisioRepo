package runner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// metricsTimeout bounds one collection (external commands included).
const metricsTimeout = 5 * time.Second

// metricsCollector measures the machine usage sent on Declare and
// Heartbeat. It is best effort: what cannot be measured stays unset and a
// failure never stops the call that carries the report.
type metricsCollector struct {
	dir    string // its filesystem is the reported disk
	logger *slog.Logger

	mu        sync.Mutex
	cpu       cpuState
	gpuFailed bool
}

func newMetricsCollector(stateFile string, logger *slog.Logger) *metricsCollector {
	dir := "."
	if stateFile != "" {
		dir = filepath.Dir(stateFile)
	}
	return &metricsCollector{dir: dir, logger: logger}
}

// collect returns the current usage. It never fails.
func (c *metricsCollector) collect(ctx context.Context) (m *runnerv1.RunnerMetrics) {
	host, _ := os.Hostname()
	m = &runnerv1.RunnerMetrics{Hostname: host, Os: runtime.GOOS, Arch: runtime.GOARCH,
		CpuCount: int32(runtime.NumCPU())} //nolint:gosec // CPU count fits
	defer func() {
		if r := recover(); r != nil {
			c.logger.Debug("runner metrics", "panic", r)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, metricsTimeout)
	defer cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.platform(ctx, &c.cpu, m)
	return m
}
