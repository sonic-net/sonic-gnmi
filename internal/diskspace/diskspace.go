// Package diskspace provides filesystem disk space monitoring utilities.
// This package is vanilla Go compatible and does not require CGO or SONiC dependencies.
package diskspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Info represents disk space information for a filesystem path.
type Info struct {
	Path        string `json:"path"`
	TotalMB     uint64 `json:"total-mb"`
	AvailableMB uint64 `json:"available-mb"`
}

// Monitor provides disk space monitoring functionality.
type Monitor struct{}

// New creates a new disk space monitor.
func New() *Monitor {
	return &Monitor{}
}

// Get retrieves disk space information for the given filesystem path.
func (m *Monitor) Get(path string) (*Info, error) {
	if path == "" {
		return nil, fmt.Errorf("filesystem path cannot be empty")
	}

	// Clean the path
	cleanPath := filepath.Clean(path)

	// Handle container environment: translate host paths to container mount point
	// Only apply translation if /mnt/host exists (running in container)
	if !strings.HasPrefix(cleanPath, "/mnt/host") {
		if _, err := os.Stat("/mnt/host"); err == nil {
			cleanPath = "/mnt/host" + cleanPath
		}
	}

	// Get filesystem statistics using syscall.Statfs
	var stat syscall.Statfs_t
	err := syscall.Statfs(cleanPath, &stat)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem stats for %s: %v", cleanPath, err)
	}

	// Calculate sizes in megabytes
	blockSize := uint64(stat.Bsize)
	totalBlocks := uint64(stat.Blocks)
	availableBlocks := uint64(stat.Bavail) // Available to non-root users

	totalBytes := totalBlocks * blockSize
	availableBytes := availableBlocks * blockSize

	totalMB := totalBytes / (1024 * 1024)
	availableMB := availableBytes / (1024 * 1024)

	return &Info{
		Path:        path, // Return original path, not translated path
		TotalMB:     totalMB,
		AvailableMB: availableMB,
	}, nil
}

// IsValidPath checks if a filesystem path is valid and accessible.
func (m *Monitor) IsValidPath(path string) bool {
	if path == "" {
		return false
	}

	cleanPath := filepath.Clean(path)

	// Handle container environment: translate host paths to container mount point
	// Only apply translation if /mnt/host exists (running in container)
	if !strings.HasPrefix(cleanPath, "/mnt/host") {
		if _, err := os.Stat("/mnt/host"); err == nil {
			cleanPath = "/mnt/host" + cleanPath
		}
	}

	// Try to get filesystem stats - if it works, path is valid
	var stat syscall.Statfs_t
	err := syscall.Statfs(cleanPath, &stat)
	return err == nil
}

// GetMultiple retrieves disk space for multiple filesystem paths.
func (m *Monitor) GetMultiple(paths []string) ([]*Info, error) {
	var results []*Info
	var errors []string

	for _, path := range paths {
		info, err := m.Get(path)
		if err != nil {
			errors = append(errors, fmt.Sprintf("path %s: %v", path, err))
			continue
		}
		results = append(results, info)
	}

	if len(errors) > 0 {
		return results, fmt.Errorf("errors occurred: %s", strings.Join(errors, "; "))
	}

	return results, nil
}

// FormatInfo formats disk space information as a human-readable string.
func FormatInfo(info *Info) string {
	if info == nil {
		return "No disk space information available"
	}

	usedMB := info.TotalMB - info.AvailableMB
	usagePercent := float64(usedMB) / float64(info.TotalMB) * 100

	return fmt.Sprintf("Path: %s, Total: %d MB, Available: %d MB, Used: %d MB (%.1f%%)",
		info.Path, info.TotalMB, info.AvailableMB, usedMB, usagePercent)
}
