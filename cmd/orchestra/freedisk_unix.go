//go:build darwin || linux

package main

import "syscall"

// freeDisk is how many bytes of the file system that holds path an unprivileged user may still write.
func freeDisk(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:gosec,unconvert // Bsize is int64 on linux, positive
}
