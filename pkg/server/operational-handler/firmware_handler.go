package operationalhandler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/internal/firmware"
)

// FirmwareHandler implements PathHandler for firmware file queries.
// It acts as a gNMI adapter for the internal firmware package.
type FirmwareHandler struct {
	monitor *firmware.Monitor
}

// FirmwareFileInfo represents the firmware file information returned by gNMI queries.
// This maintains backward compatibility with existing gNMI handlers.
type FirmwareFileInfo struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ModTime     string `json:"mod_time"`
	IsDirectory bool   `json:"is_directory"`
	Permissions string `json:"permissions"`
	Type        string `json:"type"`
	MD5Sum      string `json:"md5sum,omitempty"` // MD5 checksum for files (empty for directories)
}

// NewFirmwareHandler creates a new FirmwareHandler.
func NewFirmwareHandler() *FirmwareHandler {
	return &FirmwareHandler{
		monitor: firmware.New(),
	}
}

// SupportedPaths returns the list of paths this handler supports.
func (h *FirmwareHandler) SupportedPaths() []string {
	return []string{
		"firmware/files",
	}
}

// HandleGet processes a gNMI Get request for firmware file information.
func (h *FirmwareHandler) HandleGet(path *gnmipb.Path) ([]byte, error) {
	// Extract the firmware directory and field from the gNMI path
	firmwareDir, field, err := h.extractFirmwarePathInfo(path)
	if err != nil {
		return nil, fmt.Errorf("failed to extract firmware path info: %v", err)
	}

	// Use internal firmware library to get firmware files information
	var value interface{}

	switch field {
	case "count":
		count, err := h.monitor.GetFileCount(firmwareDir, "")
		if err != nil {
			return nil, fmt.Errorf("failed to get firmware file count for path %s: %v", firmwareDir, err)
		}
		value = count

	case "list":
		files, err := h.monitor.ListFiles(firmwareDir, "")
		if err != nil {
			return nil, fmt.Errorf("failed to list firmware files for path %s: %v", firmwareDir, err)
		}

		// Convert to gNMI-compatible format
		firmwareFiles := make([]FirmwareFileInfo, len(files))
		for i, file := range files {
			firmwareFiles[i] = FirmwareFileInfo{
				Name:        file.Name,
				Size:        file.Size,
				ModTime:     file.ModTime.Format("2006-01-02T15:04:05Z07:00"),
				IsDirectory: file.IsDirectory,
				Permissions: file.Permissions,
				Type:        file.Type,
				MD5Sum:      file.MD5Sum,
			}
		}

		value = map[string]interface{}{
			"directory":  firmwareDir,
			"file_count": len(firmwareFiles),
			"files":      firmwareFiles,
		}

	case "types":
		// Get files grouped by type
		files, err := h.monitor.ListFiles(firmwareDir, "")
		if err != nil {
			return nil, fmt.Errorf("failed to list firmware files for path %s: %v", firmwareDir, err)
		}

		// Group files by type
		typeGroups := make(map[string][]FirmwareFileInfo)
		for _, file := range files {
			if !file.IsDirectory {
				firmwareFile := FirmwareFileInfo{
					Name:        file.Name,
					Size:        file.Size,
					ModTime:     file.ModTime.Format("2006-01-02T15:04:05Z07:00"),
					IsDirectory: file.IsDirectory,
					Permissions: file.Permissions,
					Type:        file.Type,
					MD5Sum:      file.MD5Sum,
				}
				typeGroups[file.Type] = append(typeGroups[file.Type], firmwareFile)
			}
		}

		value = map[string]interface{}{
			"directory": firmwareDir,
			"types":     typeGroups,
		}

	default:
		// Check if it's a specific file request
		if field != "" {
			fileInfo, err := h.monitor.GetFileInfo(firmwareDir, field, "")
			if err != nil {
				// Check if it's actually an unsupported field (not a file not found error)
				if strings.Contains(err.Error(), "file not found") {
					// Try to list files to see if directory exists - if it does, then it's an unsupported field
					_, listErr := h.monitor.ListFiles(firmwareDir, "")
					if listErr == nil {
						return nil, fmt.Errorf("unsupported firmware field: %s", field)
					}
				}
				return nil, fmt.Errorf("failed to get firmware file info for %s in path %s: %v", field, firmwareDir, err)
			}

			firmwareFile := FirmwareFileInfo{
				Name:        fileInfo.Name,
				Size:        fileInfo.Size,
				ModTime:     fileInfo.ModTime.Format("2006-01-02T15:04:05Z07:00"),
				IsDirectory: fileInfo.IsDirectory,
				Permissions: fileInfo.Permissions,
				Type:        fileInfo.Type,
				MD5Sum:      fileInfo.MD5Sum,
			}

			value = map[string]interface{}{
				"directory": firmwareDir,
				"file":      firmwareFile,
			}
		} else {
			return nil, fmt.Errorf("unsupported firmware field: %s", field)
		}
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal firmware data: %v", err)
	}

	return jsonData, nil
}

// extractFirmwarePathInfo extracts the firmware directory and field from a gNMI path.
// Handles paths like /sonic/system/firmware[directory=/lib/firmware]/files[type=driver]/count
func (h *FirmwareHandler) extractFirmwarePathInfo(path *gnmipb.Path) (string, string, error) {
	if path == nil {
		return "", "", fmt.Errorf("path cannot be nil")
	}

	elems := path.GetElem()
	if len(elems) == 0 {
		return "", "", fmt.Errorf("path elements cannot be empty")
	}

	var firmwareDir string
	var field string = "list" // default to listing all files

	// Look for firmware element with directory key
	for i, elem := range elems {
		if elem.GetName() == "firmware" {
			keys := elem.GetKey()
			if dirValue, exists := keys["directory"]; exists {
				if dirValue == "" {
					return "", "", fmt.Errorf("firmware directory cannot be empty")
				}
				firmwareDir = dirValue
			} else {
				return "", "", fmt.Errorf("firmware directory not specified in path")
			}
		}

		// Check for files element and subsequent field
		if elem.GetName() == "files" {
			// Check if there's a next element specifying the field
			if i+1 < len(elems) {
				nextElem := elems[i+1]
				field = nextElem.GetName()
			}
			// Check for type filter in files element
			if keys := elem.GetKey(); len(keys) > 0 {
				if typeValue, exists := keys["type"]; exists {
					field = "type:" + typeValue
				}
			}
		}
	}

	if firmwareDir == "" {
		return "", "", fmt.Errorf("no firmware directory found in gNMI path")
	}

	return firmwareDir, field, nil
}

// GetFirmwareFilesByType retrieves firmware files filtered by type.
func (h *FirmwareHandler) GetFirmwareFilesByType(directory string, fileType string) ([]FirmwareFileInfo, error) {
	files, err := h.monitor.GetFilesByType(directory, "", fileType)
	if err != nil {
		return nil, err
	}

	firmwareFiles := make([]FirmwareFileInfo, len(files))
	for i, file := range files {
		firmwareFiles[i] = FirmwareFileInfo{
			Name:        file.Name,
			Size:        file.Size,
			ModTime:     file.ModTime.Format("2006-01-02T15:04:05Z07:00"),
			IsDirectory: file.IsDirectory,
			Permissions: file.Permissions,
			Type:        file.Type,
			MD5Sum:      file.MD5Sum,
		}
	}

	return firmwareFiles, nil
}

// IsValidDirectory checks if a firmware directory path is valid and accessible.
func (h *FirmwareHandler) IsValidDirectory(directory string) bool {
	_, err := h.monitor.GetFileCount(directory, "")
	return err == nil
}

// FormatFirmwareFileInfo formats firmware file information as a human-readable string.
func (h *FirmwareHandler) FormatFirmwareFileInfo(info *FirmwareFileInfo) string {
	if info == nil {
		return "No firmware file information available"
	}

	// Convert back to internal format for formatting
	modTime, _ := time.Parse("2006-01-02T15:04:05Z07:00", info.ModTime)
	internalInfo := &firmware.FileInfo{
		Name:        info.Name,
		Size:        info.Size,
		ModTime:     modTime,
		IsDirectory: info.IsDirectory,
		Permissions: info.Permissions,
		Type:        info.Type,
		MD5Sum:      info.MD5Sum,
	}

	return firmware.FormatFileInfo(internalInfo)
}
