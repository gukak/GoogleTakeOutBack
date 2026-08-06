//go:build !windows

package engine

import (
	"fmt"
	"syscall"
)

// freeSpace returns the available bytes for the filesystem containing path.
func freeSpace(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// diskKey returns a stable identifier for the filesystem containing path.
func diskKey(path string) (string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "", fmt.Errorf("statfs %s: %w", path, err)
	}
	return fmt.Sprintf("%d-%d", st.Fsid.X__val[0], st.Fsid.X__val[1]), nil
}
