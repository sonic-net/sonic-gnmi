// Package file provides handlers for gNOI File service RPCs.
// This package is pure Go with no CGO or SONiC dependencies, enabling
// standalone testing and reuse across different components.
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	common "github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"github.com/sonic-net/sonic-gnmi/internal/hash"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// HandleTransferToRemote implements the complete logic for the TransferToRemote RPC.
// It validates the request, downloads the file from the remote URL, calculates the MD5 hash,
// and returns the response.
//
// This function handles:
//   - Protocol validation (HTTP only for now)
//   - Container path translation (prepends /mnt/host when running in container)
//   - File download via HTTP
//   - MD5 hash calculation
//   - Response construction
//
// Returns:
//   - TransferToRemoteResponse with MD5 hash on success
//   - Error with appropriate gRPC status code on failure
func HandleTransferToRemote(
	ctx context.Context,
	req *gnoi_file_pb.TransferToRemoteRequest,
) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	// Validate request
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}

	remoteDownload := req.GetRemoteDownload()
	if remoteDownload == nil {
		return nil, status.Error(codes.InvalidArgument, "remote_download cannot be nil")
	}

	localPath := req.GetLocalPath()
	if localPath == "" {
		return nil, status.Error(codes.InvalidArgument, "local_path cannot be empty")
	}

	// Validate path is in allowed directories for security
	if err := validatePath(localPath); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid local_path: %v", err)
	}

	// Validate protocol - only HTTP supported initially
	protocol := remoteDownload.GetProtocol()
	if protocol != common.RemoteDownload_HTTP {
		return nil, status.Errorf(codes.Unimplemented,
			"only HTTP protocol is supported, got protocol %v", protocol)
	}

	url := remoteDownload.GetPath()
	if url == "" {
		return nil, status.Error(codes.InvalidArgument, "remote download path (URL) cannot be empty")
	}

	// Container path translation: prepend /mnt/host to access host filesystem
	// Only apply if /mnt/host exists (running in container) and path doesn't already have it
	translatedPath := translatePathForContainer(localPath)

	// Download file from URL
	if err := download.DownloadHTTP(ctx, url, translatedPath); err != nil {
		return nil, status.Errorf(codes.Internal, "download failed: %v", err)
	}

	// Calculate MD5 hash of downloaded file
	hashBytes, err := hash.CalculateMD5(translatedPath)
	if err != nil {
		// Clean up the downloaded file since we can't verify it
		os.Remove(translatedPath)
		return nil, status.Errorf(codes.Internal, "hash calculation failed: %v", err)
	}

	// Build response with MD5 hash
	return &gnoi_file_pb.TransferToRemoteResponse{
		Hash: &types.HashType{
			Method: types.HashType_MD5,
			Hash:   hashBytes,
		},
	}, nil
}

// translatePathForContainer handles path translation for container environments.
// If the code is running in a container with /mnt/host mount (host filesystem access),
// it prepends /mnt/host to the path. This follows the same pattern as the diskspace package.
//
// Example:
//   - Input: "/tmp/firmware.bin"
//   - Running in container: "/mnt/host/tmp/firmware.bin"
//   - Running on host: "/tmp/firmware.bin"
func translatePathForContainer(path string) string {
	// Clean the path first
	cleanPath := filepath.Clean(path)

	// Check if /mnt/host exists (indicates we're running in a container)
	if _, err := os.Stat("/mnt/host"); err == nil {
		return "/mnt/host" + cleanPath
	}

	// Not in container, return original path
	return cleanPath
}

// validatePath checks if the requested path is within allowed directories.
// This prevents security issues like overwriting critical system files.
//
// Allowed directories for SONiC devices:
//   - /tmp/      - Temporary files, firmware images
//   - /var/tmp/  - Temporary files that persist across reboots
//
// Rejected paths include:
//   - /etc/, /boot/, /usr/, /bin/, /sbin/ - Critical system directories
//   - /host/ - Contains grub config, overlayfs layers, machine.conf
//   - /var/log/ - System logs
//   - /home/, /root/ - User home directories with SSH keys
//   - Relative paths or paths with .. traversal
//
// Rationale: Only temporary directories are safe for firmware downloads.
// Writing to /host/ risks:
//   - Overwriting /host/grub/grub.cfg (brick device on reboot)
//   - Corrupting /host/image-*/rw/ (overlayfs upperdir, kernel panic)
//   - Modifying /host/machine.conf (platform detection failure)
func validatePath(path string) error {
	// Clean the path to resolve . and .. components
	cleanPath := filepath.Clean(path)

	// Must be absolute path
	if !filepath.IsAbs(cleanPath) {
		return fmt.Errorf("path must be absolute, got: %s", path)
	}

	// Check if path contains .. after cleaning (path traversal attempt)
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path traversal not allowed: %s", path)
	}

	// Whitelist of allowed directory prefixes
	allowedPrefixes := []string{
		"/tmp/",
		"/var/tmp/",
	}

	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(cleanPath, prefix) {
			return nil
		}
	}

	return fmt.Errorf("path must be under /tmp/ or /var/tmp/, got: %s", cleanPath)
}
