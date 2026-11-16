package runner

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// platform reads CPU, load and memory from /proc.
func (c *metricsCollector) platform(ctx context.Context, cpu *cpuState, m *runnerv1.RunnerMetrics) {
	if idle, total, ok := procStat(); ok {
		if p, ok := cpu.share(idle, total); ok {
			m.CpuPercent = &p
		}
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(data)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				m.Load1 = &v
			}
		}
	}
	if total, avail, ok := procMeminfo(); ok {
		used := max(total-avail, 0)
		m.MemoryTotalBytes, m.MemoryUsedBytes = &total, &used
	}
	c.disk(m)
	c.gpus(ctx, m)
}

// procStat returns the cumulative idle and total ticks of all CPUs.
func procStat() (idle, total uint64, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	// user nice system idle iowait irq softirq steal; guest time is
	// already counted in user.
	for i, s := range f[1:min(len(f), 9)] {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return idle, total, true
}

// procMeminfo returns MemTotal and MemAvailable in bytes.
func procMeminfo() (total, avail int64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	var haveTotal, haveAvail bool
	for _, line := range strings.Split(string(data), "\n") {
		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total, haveTotal = kb*1024, true
		case "MemAvailable":
			avail, haveAvail = kb*1024, true
		}
	}
	return total, avail, haveTotal && haveAvail
}

// cpuState keeps the previous CPU tick counters, for the busy share
// between reports.
type cpuState struct{ prevIdle, prevTotal uint64 }

// share turns cumulative idle/total ticks into the busy percent since
// the previous call; ok is false on the first call.
func (c *cpuState) share(idle, total uint64) (float64, bool) {
	prevIdle, prevTotal := c.prevIdle, c.prevTotal
	c.prevIdle, c.prevTotal = idle, total
	if prevTotal == 0 || total <= prevTotal || idle < prevIdle {
		return 0, false
	}
	dt, di := total-prevTotal, idle-prevIdle
	return 100 * float64(dt-min(di, dt)) / float64(dt), true
}
