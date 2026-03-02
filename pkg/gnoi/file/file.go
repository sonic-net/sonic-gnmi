// Package file provides handlers for gNOI File service RPCs.
// This package is pure Go with no CGO or SONiC dependencies, enabling
// standalone testing and reuse across different components.
package file

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/golang/glog"
	common "github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"github.com/sonic-net/sonic-gnmi/internal/hash"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// Maximum time allowed for downloading a file (5 minutes for large firmware images)
	downloadTimeout = 5 * time.Minute

	// Maximum file size allowed (4GB - typical maximum firmware size)
	maxFileSize = 4 * 1024 * 1024 * 1024 // 4GB in bytes
)

// newFileClient wraps gnoi_file_pb.NewFileClient to allow test patching
// (the generated function is tiny and gets inlined, defeating gomonkey).
var newFileClient = gnoi_file_pb.NewFileClient

// HandleTransferToRemote implements the complete logic for the TransferToRemote RPC.
// It validates the request, checks for DPU metadata, and routes accordingly.
//
// This function handles:
//   - DPU metadata extraction and routing decisions
//   - Protocol validation (HTTP only for now)
//   - Container path translation (prepends /mnt/host when running in container)
//   - File download via HTTP (for NPU) or DPU streaming (for DPU targets)
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
	// Check for DPU headers (HandleOnNPU mode from DPU proxy)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		targetType := ""
		targetIndex := ""

		if vals := md.Get("x-sonic-ss-target-type"); len(vals) > 0 {
			targetType = vals[0]
		}
		if vals := md.Get("x-sonic-ss-target-index"); len(vals) > 0 {
			targetIndex = vals[0]
		}

		// If DPU headers are present, handle DPU transfer logic using efficient streaming
		if targetType == "dpu" && targetIndex != "" {
			log.Infof("[TransferToRemote] DPU routing detected: target-type=%s, target-index=%s", targetType, targetIndex)
			return HandleTransferToRemoteForDPUStreaming(ctx, req, targetIndex)
		}
	}

	// No DPU headers, handle normally for local device
	return handleTransferToRemoteLocal(ctx, req)
}

// handleTransferToRemoteLocal implements the local device logic for TransferToRemote RPC.
// This is the original HandleTransferToRemote logic extracted to a separate function.
func handleTransferToRemoteLocal(
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

// HandlePut implements the complete logic for the Put RPC with DPU routing support.
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
	// Step 0: Check for DPU headers (HandleOnNPU mode from DPU proxy)
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		targetType := ""
		targetIndex := ""

		if vals := md.Get("x-sonic-ss-target-type"); len(vals) > 0 {
			targetType = vals[0]
		}
		if vals := md.Get("x-sonic-ss-target-index"); len(vals) > 0 {
			targetIndex = vals[0]
		}

		// If DPU headers are present, handle DPU put logic
		if targetType == "dpu" && targetIndex != "" {
			log.Infof("[HandlePut] DPU routing detected: target-type=%s, target-index=%s", targetType, targetIndex)
			// For now, we'll use the same logic but could route to specific DPU endpoint
			// In the future, this could establish a connection to the specific DPU
		}
	}

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
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Errorf("Failed to close temp file %s: %v", tempPath, closeErr)
		}
		// Only remove if file still exists (indicates failure path)
		if _, err := os.Stat(tempPath); err == nil {
			if rmErr := os.Remove(tempPath); rmErr != nil {
				log.Errorf("Failed to cleanup temp file %s: %v", tempPath, rmErr)
			}
		}
	}()

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

// HandleTransferToRemoteForDPUStreaming implements efficient streaming proxy for DPU file transfers.
// This function streams data directly from HTTP source to DPU without intermediate disk storage
// or loading the entire file into memory. It calculates MD5 hash concurrently during streaming.
//
// The DPU connection is obtained directly via dpuproxy.GetDPUConnection, which returns a cached
// connection managed by the DPU proxy infrastructure. Callers must NOT close the connection.
func HandleTransferToRemoteForDPUStreaming(
	ctx context.Context,
	req *gnoi_file_pb.TransferToRemoteRequest,
	dpuIndex string,
) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	// Validate inputs
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if dpuIndex == "" {
		return nil, status.Error(codes.InvalidArgument, "dpuIndex cannot be empty")
	}

	remoteDownload := req.GetRemoteDownload()
	if remoteDownload == nil {
		return nil, status.Error(codes.InvalidArgument, "remote_download cannot be nil")
	}

	localPath := req.GetLocalPath()
	if localPath == "" {
		return nil, status.Error(codes.InvalidArgument, "local_path cannot be empty")
	}

	// Validate protocol - only HTTP supported
	protocol := remoteDownload.GetProtocol()
	if protocol != common.RemoteDownload_HTTP {
		return nil, status.Errorf(codes.Unimplemented,
			"only HTTP protocol is supported, got protocol %v", protocol)
	}

	url := remoteDownload.GetPath()
	if url == "" {
		return nil, status.Error(codes.InvalidArgument, "remote download path (URL) cannot be empty")
	}

	// Create context with timeout for streaming operation
	streamCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	// Step 1: Create HTTP streaming connection
	httpStream, _, err := download.DownloadHTTPStreaming(streamCtx, url, maxFileSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create HTTP stream: %v", err)
	}
	defer httpStream.Close()

	// Step 2: Get direct connection to DPU (cached, do NOT close)
	conn, err := dpuproxy.GetDPUConnection(streamCtx, dpuIndex)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get DPU connection: %v", err)
	}

	fileClient := newFileClient(conn)

	// Step 3: Create DPU Put stream
	putClient, err := fileClient.Put(streamCtx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create Put client: %v", err)
	}

	// Send Open request
	openReq := &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Open{
			Open: &gnoi_file_pb.PutRequest_Details{
				RemoteFile:  localPath,
				Permissions: 0644,
			},
		},
	}
	if err := putClient.Send(openReq); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to send open request: %v", err)
	}

	// Step 4: Set up concurrent hash calculation
	hashCalc := hash.NewStreamingMD5Calculator()
	teeReader := io.TeeReader(httpStream, hashCalc)

	// Step 5: Stream file contents in chunks
	chunkSize := 64 * 1024 // 64KB chunks
	buffer := make([]byte, chunkSize)

	for {
		select {
		case <-streamCtx.Done():
			return nil, status.Error(codes.DeadlineExceeded, "streaming operation timed out")
		default:
		}

		// Read next chunk from HTTP stream (via TeeReader for concurrent hashing)
		n, err := teeReader.Read(buffer)
		if n > 0 {
			// Send chunk to DPU
			contentReq := &gnoi_file_pb.PutRequest{
				Request: &gnoi_file_pb.PutRequest_Contents{
					Contents: buffer[:n],
				},
			}
			if err := putClient.Send(contentReq); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to send content chunk: %v", err)
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to read from HTTP stream: %v", err)
		}
	}

	// Step 6: Send final hash
	hashBytes := hashCalc.Sum()
	hashReq := &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Hash{
			Hash: &types.HashType{
				Method: types.HashType_MD5,
				Hash:   hashBytes,
			},
		},
	}
	if err := putClient.Send(hashReq); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to send hash: %v", err)
	}

	// Step 7: Close and get response
	if _, err := putClient.CloseAndRecv(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to complete Put to DPU: %v", err)
	}

	// Build response with calculated hash
	return &gnoi_file_pb.TransferToRemoteResponse{
		Hash: &types.HashType{
			Method: types.HashType_MD5,
			Hash:   hashBytes,
		},
	}, nil
}

func HandleFileRemove(ctx context.Context, req *gnoi_file_pb.RemoveRequest) (*gnoi_file_pb.RemoveResponse, error) {
	log.Infof("HandleFileRemove called with request: %+v", req)

	if req == nil {
		log.Errorf("Nil request received")
		return nil, status.Error(codes.InvalidArgument, "Invalid nil request.")
	}

	remoteFile := req.GetRemoteFile()
	if remoteFile == "" {
		log.Errorf("Invalid request: remote_file field is empty")
		return nil, status.Error(codes.InvalidArgument, "Invalid request: remote_file field is empty.")
	}

	if err := validatePath(remoteFile); err != nil {
		log.Errorf("Denied: %v", err)
		return nil, status.Error(codes.PermissionDenied, "only files in /tmp/ or /var/tmp/ can be removed")
	}

	// NEW: map host path to container path if needed.
	// translatePathForContainer will prepend /mnt/host inside the gnmi container
	// when appropriate, so files created on the DUT in /tmp/ are visible.
	localPath := remoteFile
	translatedPath := translatePathForContainer(localPath)
	log.Infof("HandleFileRemove removing file: remote=%s translated=%s", remoteFile, translatedPath)

	// Attempt remove and map errors to gRPC status codes for testable behavior.
	if err := os.Remove(translatedPath); err != nil {
		log.Errorf("Remove RPC failed: %v", err)

		lower := strings.ToLower(err.Error())

		// NotFound
		if os.IsNotExist(err) || strings.Contains(lower, "no such file") {
			return &gnoi_file_pb.RemoveResponse{}, status.Errorf(codes.NotFound, "%v", err)
		}

		// PermissionDenied — detect real OS permission errors or common test error strings
		if os.IsPermission(err) || errors.Is(err, os.ErrPermission) || strings.Contains(lower, "permission denied") {
			return &gnoi_file_pb.RemoveResponse{}, status.Errorf(codes.PermissionDenied, "%v", err)
		}

		// Fallback to Internal for other errors.
		return &gnoi_file_pb.RemoveResponse{}, status.Errorf(codes.Internal, "%v", err)
	}

	log.Infof("Successfully removed file: %s", remoteFile)
	return &gnoi_file_pb.RemoveResponse{}, nil
}
