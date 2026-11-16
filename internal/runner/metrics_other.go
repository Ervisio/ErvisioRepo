//go:build !linux && !darwin

package runner

import (
	"context"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// platform measures nothing beyond the common facts on other systems
// (Windows): the fields stay unset.
func (c *metricsCollector) platform(context.Context, *cpuState, *runnerv1.RunnerMetrics) {}

// cpuState is empty: the CPU busy share is not measured here.
type cpuState struct{}
