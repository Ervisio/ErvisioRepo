//go:build unix

package tier

import (
	"fmt"
	"syscall"
)

func diskUsage(path string) (DiskUsage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskUsage{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	bs := int64(st.Bsize)          //nolint:gosec,unconvert // block size fits; its type differs per OS
	total := int64(st.Blocks) * bs //nolint:gosec // block counts fit in int64
	free := int64(st.Bfree) * bs   //nolint:gosec // block counts fit in int64
	return DiskUsage{Used: total - free, Total: total}, nil
}
