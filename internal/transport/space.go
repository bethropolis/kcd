package transport

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// CheckFreeSpace rejects declared transfers that cannot fit on the target filesystem.
func CheckFreeSpace(dir string, size int64) error {
	if size < 0 {
		return nil
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(filepath.Clean(dir), &stat); err != nil {
		return fmt.Errorf("transport: inspect free space: %w", err)
	}
	if uint64(size) > stat.Bavail*uint64(stat.Bsize) {
		return fmt.Errorf("transport: insufficient free space for %d byte payload", size)
	}
	return nil
}
