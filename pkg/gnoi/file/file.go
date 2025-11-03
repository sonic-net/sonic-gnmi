// Package file provides handlers for gNOI File service RPCs.
// This package is pure Go with no CGO or SONiC dependencies, enabling
// standalone testing and reuse across different components.
package file

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	common "github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"github.com/sonic-net/sonic-gnmi/internal/hash"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// Maximum time allowed for downloading a file (5 minutes for large firmware images)
	downloadTimeout = 5 * time.Minute

	// Maximum file size allowed (4GB - typical maximum firmware size)
	maxFileSize = 4 * 1024 * 1024 * 1024 // 4GB in bytes
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

	// Create context with timeout for download operation
	downloadCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	// Download file from URL with timeout and size limit
	if err := download.DownloadHTTP(downloadCtx, url, translatedPath, maxFileSize); err != nil {
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

// HandlePut implements the complete logic for the Put RPC.
// It receives a file stream from the client, validates the path, writes the file
// to the filesystem, and verifies the hash.
//
// This function handles:
//   - Receiving Open message with file path and permissions
//   - Path validation (only /tmp/ and /var/tmp/)
//   - Container path translation (prepends /mnt/host when running in container)
//   - Receiving file contents in chunks
//   - MD5 hash verification
//   - Atomic file write (write to temp, then rename)
//
// Protocol sequence:
//  1. Client sends Open message with remote_file and permissions
//  2. Client sends multiple Contents messages with file chunks
//  3. Client sends Hash message with MD5 hash
//  4. Server verifies hash and renames temp file to final path
//
// Returns:
//   - PutResponse on success
//   - Error with appropriate gRPC status code on failure
func HandlePut(stream gnoi_file_pb.File_PutServer) error {
	// Step 1: Receive the first message (must be Open)
	firstReq, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to receive first message: %v", err)
	}

	openMsg := firstReq.GetOpen()
	if openMsg == nil {
		return status.Error(codes.InvalidArgument, "first message must be Open")
	}

	remotePath := openMsg.GetRemoteFile()
	if remotePath == "" {
		return status.Error(codes.InvalidArgument, "remote_file cannot be empty")
	}

	permissions := openMsg.GetPermissions()
	if permissions == 0 {
		// Default to 0644 if not specified
		permissions = 0644
	}

	// Step 2: Validate path is in allowed directories
	if err := validatePath(remotePath); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid remote_file: %v", err)
	}

	// Step 3: Container path translation
	translatedPath := translatePathForContainer(remotePath)

	// Step 4: Create temp file for atomic write
	tempPath := translatedPath + ".tmp"
	f, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create temp file: %v", err)
	}
	defer f.Close()
	defer os.Remove(tempPath) // Clean up temp file on error or success

	// Step 5: Receive chunks and write to temp file
	hasher := md5.New() // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5
	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return status.Error(codes.InvalidArgument, "unexpected end of stream before hash")
			}
			if err == context.Canceled || err == context.DeadlineExceeded {
				return status.Errorf(codes.Canceled, "stream canceled: %v", err)
			}
			return status.Errorf(codes.Internal, "failed to receive chunk: %v", err)
		}

		if contents := req.GetContents(); contents != nil {
			// Write chunk to file
			if _, err := f.Write(contents); err != nil {
				return status.Errorf(codes.Internal, "failed to write chunk: %v", err)
			}
			// Update hash
			hasher.Write(contents)
		} else if hashMsg := req.GetHash(); hashMsg != nil {
			// Step 6: Verify hash
			calculatedHash := hasher.Sum(nil)
			receivedHash := hashMsg.GetHash()

			if !bytes.Equal(calculatedHash, receivedHash) {
				return status.Error(codes.DataLoss, "hash mismatch: file corrupted during transfer")
			}

			// Hash verified, proceed to finalize
			break
		} else {
			return status.Error(codes.InvalidArgument, "message must contain contents or hash")
		}
	}

	// Step 7: Close the temp file before renaming
	if err := f.Close(); err != nil {
		return status.Errorf(codes.Internal, "failed to close temp file: %v", err)
	}

	// Step 8: Set permissions on temp file
	if err := os.Chmod(tempPath, os.FileMode(permissions)); err != nil {
		return status.Errorf(codes.Internal, "failed to set permissions: %v", err)
	}

	// Step 9: Atomic rename to final path
	if err := os.Rename(tempPath, translatedPath); err != nil {
		return status.Errorf(codes.Internal, "failed to rename file: %v", err)
	}

	// Step 10: Send success response
	return stream.SendAndClose(&gnoi_file_pb.PutResponse{})
}

// HandleTransferToRemoteForDPU handles TransferToRemote when DPU headers are present.
// It downloads the file to NPU first, then uploads it to the specified DPU using File.Put.
//
// This function implements the complete DPU transfer workflow:
//   - Downloads file to a temporary location on NPU
//   - Reads the downloaded file with container path translation
//   - Creates a gRPC connection with DPU routing metadata
//   - Uploads the file to DPU using File.Put streaming RPC
//   - Cleans up temporary files
//
// Parameters:
//   - ctx: Context for the operation
//   - req: Original TransferToRemoteRequest with target DPU path
//   - dpuIndex: Target DPU index (e.g., "0", "1")
//   - proxyAddress: Address of the gNOI proxy server (e.g., "localhost:8080")
//
// Returns:
//   - TransferToRemoteResponse with MD5 hash on success
//   - Error with appropriate gRPC status code on failure
func HandleTransferToRemoteForDPU(
	ctx context.Context,
	req *gnoi_file_pb.TransferToRemoteRequest,
	dpuIndex string,
	proxyAddress string,
) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	// Validate inputs
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if dpuIndex == "" {
		return nil, status.Error(codes.InvalidArgument, "dpuIndex cannot be empty")
	}
	if proxyAddress == "" {
		return nil, status.Error(codes.InvalidArgument, "proxyAddress cannot be empty")
	}

	// Step 1: Download file to NPU temp location
	tempPath := fmt.Sprintf("/tmp/dpu_transfer_%s_%d", dpuIndex, time.Now().UnixNano())

	// Create a modified request with temp path
	npuReq := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath:      tempPath,
		RemoteDownload: req.GetRemoteDownload(),
	}

	// Download to NPU
	resp, err := HandleTransferToRemote(ctx, npuReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to download to NPU: %v", err)
	}

	// Ensure cleanup of temp file (use the same path translation as reading)
	defer func() {
		cleanupPath := tempPath
		if _, err := os.Stat("/mnt/host"); err == nil {
			cleanupPath = "/mnt/host" + tempPath
		}
		if err := os.Remove(cleanupPath); err != nil {
			// Log warning but don't fail the operation
		}
	}()

	// Step 2: Read the downloaded file (apply container path translation)
	// The file was downloaded to host filesystem via HandleTransferToRemote, so we need to read from /mnt/host prefix
	translatedTempPath := tempPath
	if _, err := os.Stat("/mnt/host"); err == nil {
		translatedTempPath = "/mnt/host" + tempPath
	}

	fileData, err := os.ReadFile(translatedTempPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read downloaded file: %v", err)
	}

	// Step 3: Connect to DPU via proxy
	// Create metadata for DPU routing
	md := metadata.New(map[string]string{
		"x-sonic-ss-target-type":  "dpu",
		"x-sonic-ss-target-index": dpuIndex,
	})
	outCtx := metadata.NewOutgoingContext(ctx, md)

	// Connect to local gNOI server which will proxy to DPU
	conn, err := grpc.Dial(proxyAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to connect to proxy: %v", err)
	}
	defer conn.Close()

	fileClient := gnoi_file_pb.NewFileClient(conn)

	// Step 4: Use File.Put to upload to DPU
	putClient, err := fileClient.Put(outCtx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create Put client: %v", err)
	}

	// Send Open request
	openReq := &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Open{
			Open: &gnoi_file_pb.PutRequest_Details{
				RemoteFile:  req.GetLocalPath(), // Use original target path
				Permissions: 0644,
			},
		},
	}
	if err := putClient.Send(openReq); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to send open request: %v", err)
	}

	// Send file contents in chunks
	chunkSize := 64 * 1024 // 64KB chunks
	for i := 0; i < len(fileData); i += chunkSize {
		end := i + chunkSize
		if end > len(fileData) {
			end = len(fileData)
		}

		contentReq := &gnoi_file_pb.PutRequest{
			Request: &gnoi_file_pb.PutRequest_Contents{
				Contents: fileData[i:end],
			},
		}
		if err := putClient.Send(contentReq); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to send content: %v", err)
		}
	}

	// Send hash (reuse the hash from download response)
	hashReq := &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Hash{
			Hash: resp.GetHash(),
		},
	}
	if err := putClient.Send(hashReq); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to send hash: %v", err)
	}

	// Close and get response
	if _, err := putClient.CloseAndRecv(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to complete Put to DPU: %v", err)
	}

	return resp, nil
}
