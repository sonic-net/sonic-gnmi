package gnmi

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
	"github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/internal/diskspace"
)

// handleFilesystemPath processes filesystem-related gNMI path requests.
// It supports disk space queries for any filesystem path.
func (s *Server) handleFilesystemPath(path *gnmi.Path) (*gnmi.Update, error) {
	// Extract the filesystem path from the gNMI path
	fsPath, err := extractFilesystemPath(path)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid filesystem path: %v", err)
	}

	// Check if this is a disk space request
	if isDiskSpacePath(path) {
		return s.handleDiskSpaceRequest(path, fsPath)
	}

	// For now, only disk space is supported
	return nil, status.Errorf(codes.NotFound, "unsupported filesystem metric: %s", pathToString(path))
}

// handleDiskSpaceRequest processes disk space queries for a specific filesystem path.
func (s *Server) handleDiskSpaceRequest(path *gnmi.Path, fsPath string) (*gnmi.Update, error) {
	// Determine which field is being requested
	field, err := getDiskSpaceField(path)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid disk space path: %v", err)
	}

	// Resolve the filesystem path with rootFS
	resolvedPath := s.resolveFilesystemPath(fsPath)

	glog.V(2).Infof("Getting disk space for filesystem path: %s (resolved: %s)", fsPath, resolvedPath)

	// Get disk space information
	info, err := diskspace.GetDiskSpace(resolvedPath)
	if err != nil {
		glog.Errorf("Failed to get disk space for %s: %v", resolvedPath, err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve disk space for path %s: %v", fsPath, err)
	}

	// Create the response value based on requested field
	value, err := s.createDiskSpaceValue(info, fsPath, field)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create response: %v", err)
	}

	// Marshal to JSON
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal response: %v", err)
	}

	return &gnmi.Update{
		Path: path,
		Val: &gnmi.TypedValue{
			Value: &gnmi.TypedValue_JsonVal{
				JsonVal: jsonBytes,
			},
		},
	}, nil
}

// resolveFilesystemPath resolves a filesystem path with the server's rootFS.
// This handles the difference between containerized and bare-metal deployments.
func (s *Server) resolveFilesystemPath(fsPath string) string {
	// If no rootFS is configured or it's root, use the path as-is
	if s.rootFS == "" || s.rootFS == "/" {
		return fsPath
	}

	// If the path is already absolute and starts with rootFS, use as-is
	if strings.HasPrefix(fsPath, s.rootFS) {
		return fsPath
	}

	// For containerized deployments, resolve the path within rootFS
	if strings.HasPrefix(fsPath, "/") {
		// Absolute path - join with rootFS
		return filepath.Join(s.rootFS, fsPath)
	}

	// Relative path - use as-is (though this is unusual for filesystem queries)
	return fsPath
}

// createDiskSpaceValue creates the appropriate value structure based on the requested field.
func (s *Server) createDiskSpaceValue(info *diskspace.DiskSpaceInfo, fsPath string, field string) (interface{}, error) {
	switch field {
	case "total":
		return info.TotalMB, nil
	case "available":
		return info.AvailableMB, nil
	case "both":
		return map[string]interface{}{
			"path":         fsPath,
			"total-mb":     info.TotalMB,
			"available-mb": info.AvailableMB,
		}, nil
	default:
		return nil, fmt.Errorf("unknown field: %s", field)
	}
}
