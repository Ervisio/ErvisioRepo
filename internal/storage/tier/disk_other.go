//go:build !unix

package tier

import "errors"

func diskUsage(string) (DiskUsage, error) {
	return DiskUsage{}, errors.New("disk usage is not supported on this platform")
}
