package runner

import (
	"encoding/binary"
	"syscall"
)

// memoryBytes is the physical memory (sysctl hw.memsize).
func memoryBytes() int64 {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}
	// Sysctl drops one trailing NUL byte; pad the little-endian value.
	b := make([]byte, 8)
	copy(b, s)
	return int64(binary.LittleEndian.Uint64(b)) //nolint:gosec // physical memory fits
}
