package diskspace

import (
	"fmt"
	"syscall"

	"github.com/golang/glog"
)

// GetDiskTotalSpaceMB returns the total disk space in MB for the given path.
func GetDiskTotalSpaceMB(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("failed to get filesystem stats for %s: %w", path, err)
	}

	// Calculate total space in MB
	blockSize := int64(stat.Bsize)
	totalBytes := int64(stat.Blocks) * blockSize
	totalMB := totalBytes / (1024 * 1024)

	glog.V(2).Infof("Total space for %s: %d MB", path, totalMB)
	return totalMB, nil
}

// GetDiskFreeSpaceMB returns the available disk space in MB for the given path.
func GetDiskFreeSpaceMB(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("failed to get filesystem stats for %s: %w", path, err)
	}

	// Calculate available space in MB (using Bavail which is available to non-root users)
	blockSize := int64(stat.Bsize)
	availBytes := int64(stat.Bavail) * blockSize
	availMB := availBytes / (1024 * 1024)

	glog.V(2).Infof("Available space for %s: %d MB", path, availMB)
	return availMB, nil
}
