//go:build !darwin

package runner

import (
	"fmt"
	"os"
	"strings"
)

// memoryBytes is the physical memory from /proc/meminfo (Linux), 0 when
// unknown.
func memoryBytes() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			var kb int64
			if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%d", &kb); err == nil {
				return kb * 1024
			}
		}
	}
	return 0
}
