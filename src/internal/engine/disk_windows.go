//go:build windows

package engine

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// freeSpace returns the available bytes for the filesystem containing path.
func freeSpace(path string) (uint64, error) {
	var free, total, totalFree int64
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("UTF16PtrFromString %s: %w", path, err)
	}
	r, _, err := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceExW %s: %v", path, err)
	}
	return uint64(free), nil
}

// diskKey returns a stable identifier for the filesystem containing path.
func diskKey(path string) (string, error) {
	return filepath.VolumeName(path), nil
}
