package runner

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

var vmStatPageSize = regexp.MustCompile(`page size of (\d+) bytes`)

// platform reads load and memory through sysctl and vm_stat. The CPU busy
// share needs host_processor_info (cgo) and is left unset.
func (c *metricsCollector) platform(ctx context.Context, _ *cpuState, m *runnerv1.RunnerMetrics) {
	if out, err := sys.Run(ctx, "sysctl", "-n", "vm.loadavg"); err == nil {
		// "{ 1.23 1.45 1.67 }" (a comma under some locales)
		if f := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}")); len(f) > 0 {
			if v, err := strconv.ParseFloat(strings.Replace(f[0], ",", ".", 1), 64); err == nil {
				m.Load1 = &v
			}
		}
	}
	if total := memoryBytes(); total > 0 {
		m.MemoryTotalBytes = &total
		if used, ok := vmStatUsed(ctx); ok {
			used = min(used, total)
			m.MemoryUsedBytes = &used
		}
	}
	c.disk(m)
	c.gpus(ctx, m)
}

// vmStatUsed is active + wired + compressed memory, as Activity Monitor
// roughly counts it.
func vmStatUsed(ctx context.Context) (int64, bool) {
	out, err := sys.Run(ctx, "vm_stat")
	if err != nil {
		return 0, false
	}
	text := string(out)
	sm := vmStatPageSize.FindStringSubmatch(text)
	if sm == nil {
		return 0, false
	}
	page, err := strconv.ParseInt(sm[1], 10, 64)
	if err != nil {
		return 0, false
	}
	var pages int64
	found := 0
	for _, line := range strings.Split(text, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Pages active", "Pages wired down", "Pages occupied by compressor":
			n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
			if err != nil {
				return 0, false
			}
			pages += n
			found++
		}
	}
	return pages * page, found == 3
}

// cpuState is empty: the CPU busy share is not measured here.
type cpuState struct{}
