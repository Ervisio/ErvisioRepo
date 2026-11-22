package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ReachableObjectBytes counts the on-disk bytes of objects reachable from refs.
// It includes borrowed objects, but not unrelated objects in an alternate store.
func ReachableObjectBytes(ctx context.Context, path string) (int64, error) {
	out, err := Run(ctx, path, "rev-list", "--disk-usage", "--objects", "--all")
	if err != nil {
		return 0, fmt.Errorf("measure reachable Git objects: %w", err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse reachable Git object bytes: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid reachable Git object bytes: %d", n)
	}
	return n, nil
}
