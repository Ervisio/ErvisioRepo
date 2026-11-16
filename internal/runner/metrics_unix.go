//go:build linux || darwin

package runner

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// gpuTimeout bounds one nvidia-smi call.
const gpuTimeout = 3 * time.Second

// disk reports the filesystem holding the runner data directory.
func (c *metricsCollector) disk(m *runnerv1.RunnerMetrics) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(c.dir, &st); err != nil {
		return
	}
	bsize := uint64(st.Bsize) //nolint:gosec,unconvert // block size is positive; its type differs per OS
	total, used := st.Blocks*bsize, (st.Blocks-st.Bfree)*bsize
	m.DiskTotalBytes, m.DiskUsedBytes = i64(total), i64(used)
}

func i64(v uint64) *int64 {
	n := int64(min(v, 1<<62)) //nolint:gosec // bounded above
	return &n
}

// gpus lists the NVIDIA GPUs through nvidia-smi, when installed.
func (c *metricsCollector) gpus(ctx context.Context, m *runnerv1.RunnerMetrics) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return
	}
	var out bytes.Buffer
	err := sys.Exec(ctx, sys.Cmd{Argv: []string{"nvidia-smi",
		"--query-gpu=name,utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits"},
		Stdout: &out, Timeout: gpuTimeout})
	if err != nil {
		if !c.gpuFailed {
			c.gpuFailed = true
			c.logger.Debug("nvidia-smi failed, GPU metrics skipped", "err", err)
		}
		return
	}
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Split(line, ",")
		if len(f) != 4 {
			continue
		}
		g := &runnerv1.RunnerGPU{Name: strings.TrimSpace(f[0])}
		if v, err := strconv.ParseFloat(strings.TrimSpace(f[1]), 64); err == nil {
			g.UtilizationPercent = &v
		}
		const mib = 1 << 20
		if v, err := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64); err == nil {
			v *= mib
			g.MemoryUsedBytes = &v
		}
		if v, err := strconv.ParseInt(strings.TrimSpace(f[3]), 10, 64); err == nil {
			v *= mib
			g.MemoryTotalBytes = &v
		}
		m.Gpus = append(m.Gpus, g)
	}
}
