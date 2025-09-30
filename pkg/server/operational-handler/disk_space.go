package operationalhandler

import (
	"encoding/json"
	"fmt"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/internal/diskspace"
)

// DiskSpaceHandler implements PathHandler for filesystem disk space queries.
// It acts as a gNMI adapter for the internal diskspace package.
type DiskSpaceHandler struct {
	monitor *diskspace.Monitor
}

// DiskSpaceInfo represents the disk space information returned by gNMI queries.
// This maintains backward compatibility with existing gNMI clients.
type DiskSpaceInfo struct {
	Path        string `json:"path"`
	TotalMB     uint64 `json:"total-mb"`
	AvailableMB uint64 `json:"available-mb"`
}

// NewDiskSpaceHandler creates a new DiskSpaceHandler.
func NewDiskSpaceHandler() *DiskSpaceHandler {
	return &DiskSpaceHandler{
		monitor: diskspace.New(),
	}
}

// SupportedPaths returns the list of paths this handler supports.
func (h *DiskSpaceHandler) SupportedPaths() []string {
	return []string{
		"filesystem/disk-space",
	}
}

// HandleGet processes a gNMI Get request for disk space information.
func (h *DiskSpaceHandler) HandleGet(path *gnmipb.Path) ([]byte, error) {
	// Extract the filesystem path from the gNMI path
	filesystemPath, err := h.extractFilesystemPath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to extract filesystem path: %v", err)
	}

	// Get disk space information using the internal monitor
	info, err := h.monitor.Get(filesystemPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get disk space for path %s: %v", filesystemPath, err)
	}

	// Convert to gNMI-compatible format
	diskSpace := &DiskSpaceInfo{
		Path:        info.Path,
		TotalMB:     info.TotalMB,
		AvailableMB: info.AvailableMB,
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(diskSpace)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal disk space data: %v", err)
	}

	return jsonData, nil
}

// extractFilesystemPath extracts the filesystem path from a gNMI path.
// Handles paths like /sonic/system/filesystem[path=/]/disk-space
func (h *DiskSpaceHandler) extractFilesystemPath(path *gnmipb.Path) (string, error) {
	if path == nil {
		return "", fmt.Errorf("path cannot be nil")
	}

	elems := path.GetElem()
	if len(elems) == 0 {
		return "", fmt.Errorf("path elements cannot be empty")
	}

	// Look for filesystem element with path key
	for _, elem := range elems {
		if elem.GetName() == "filesystem" {
			keys := elem.GetKey()
			if pathValue, exists := keys["path"]; exists {
				if pathValue == "" {
					return "", fmt.Errorf("filesystem path cannot be empty")
				}
				return pathValue, nil
			}
		}
	}

	return "", fmt.Errorf("no filesystem path found in gNMI path")
}

// IsValidPath checks if a filesystem path is valid and accessible.
func (h *DiskSpaceHandler) IsValidPath(path string) bool {
	return h.monitor.IsValidPath(path)
}

// FormatDiskSpaceInfo formats disk space information as a human-readable string.
func (h *DiskSpaceHandler) FormatDiskSpaceInfo(info *DiskSpaceInfo) string {
	if info == nil {
		return "No disk space information available"
	}

	// Convert to internal format and use its formatter
	internalInfo := &diskspace.Info{
		Path:        info.Path,
		TotalMB:     info.TotalMB,
		AvailableMB: info.AvailableMB,
	}

	return diskspace.FormatInfo(internalInfo)
}

// GetDiskSpaceForMultiplePaths retrieves disk space for multiple filesystem paths.
func (h *DiskSpaceHandler) GetDiskSpaceForMultiplePaths(paths []string) ([]*DiskSpaceInfo, error) {
	internalResults, err := h.monitor.GetMultiple(paths)
	if err != nil {
		// Convert internal results to gNMI format for partial results
		var results []*DiskSpaceInfo
		for _, info := range internalResults {
			results = append(results, &DiskSpaceInfo{
				Path:        info.Path,
				TotalMB:     info.TotalMB,
				AvailableMB: info.AvailableMB,
			})
		}
		return results, err
	}

	// Convert all results to gNMI format
	var results []*DiskSpaceInfo
	for _, info := range internalResults {
		results = append(results, &DiskSpaceInfo{
			Path:        info.Path,
			TotalMB:     info.TotalMB,
			AvailableMB: info.AvailableMB,
		})
	}

	return results, nil
}
