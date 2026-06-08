//go:build !windows

package transfer

import "syscall"

func availableSpace(filePath string) (uint64, error) {
	var statistics syscall.Statfs_t
	if err := syscall.Statfs(filePath, &statistics); err != nil {
		return 0, err
	}
	return uint64(statistics.Bavail) * uint64(statistics.Bsize), nil
}
