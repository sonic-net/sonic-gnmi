// Package system implements the gNOI System service for SONiC.
// This package provides system-level operations including package management,
// reboot, and other system administrative functions.
package system

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
	"github.com/openconfig/gnoi/common"
	"github.com/openconfig/gnoi/system"
	"github.com/openconfig/gnoi/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/internal/checksum"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/internal/download"
)

// Server implements the gNOI System service.
// It provides system-level operations for SONiC devices.
type Server struct {
	system.UnimplementedSystemServer
	rootFS string // Root filesystem path for containerized deployments
}

// NewServer creates a new System service server instance.
func NewServer(rootFS string) *Server {
	return &Server{
		rootFS: rootFS,
	}
}

// SetPackage installs a software package on the target device.
// Current implementation supports:
// - Remote download via HTTP protocol
// - MD5 hash verification
// - Package metadata with remote_download field
//
// The RPC follows this sequence:
// 1. Receive package metadata with remote_download info
// 2. Skip any content messages (not yet supported)
// 3. Receive hash for verification
// 4. Download the package and verify checksum
func (s *Server) SetPackage(stream system.System_SetPackageServer) error {
	glog.Info("SetPackage RPC called")

	var packageInfo *system.Package
	var hashInfo *types.HashType
	
	// Read all messages from the stream
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			glog.Errorf("Error receiving SetPackage request: %v", err)
			return status.Errorf(codes.Internal, "failed to receive request: %v", err)
		}

		switch request := req.GetRequest().(type) {
		case *system.SetPackageRequest_Package:
			if packageInfo != nil {
				return status.Error(codes.InvalidArgument, "package info already received")
			}
			packageInfo = request.Package
			glog.Infof("Received package info: filename=%s, version=%s", 
				packageInfo.GetFilename(), packageInfo.GetVersion())

		case *system.SetPackageRequest_Contents:
			return status.Error(codes.Unimplemented, "direct package transfer not yet supported")

		case *system.SetPackageRequest_Hash:
			if hashInfo != nil {
				return status.Error(codes.InvalidArgument, "hash info already received")
			}
			hashInfo = request.Hash
			glog.Info("Received hash info")

		default:
			return status.Error(codes.InvalidArgument, "unknown request type")
		}
	}

	// Validate we have all required info
	if packageInfo == nil {
		return status.Error(codes.InvalidArgument, "package info not provided")
	}
	if hashInfo == nil {
		return status.Error(codes.InvalidArgument, "hash info not provided")
	}

	// Validate remote download is provided
	if packageInfo.GetRemoteDownload() == nil {
		return status.Error(codes.Unimplemented, "local package installation not yet supported")
	}

	// Validate protocol is HTTP
	if packageInfo.GetRemoteDownload().GetProtocol() != common.RemoteDownload_HTTP {
		return status.Errorf(codes.Unimplemented, "only HTTP protocol is currently supported (received %s)", 
			packageInfo.GetRemoteDownload().GetProtocol())
	}

	// Validate hash type is MD5
	if hashInfo.GetMethod() != types.HashType_MD5 {
		return status.Errorf(codes.Unimplemented, "only MD5 hash verification is currently supported (received %s)",
			hashInfo.GetMethod())
	}

	// Download the package
	downloadURL := packageInfo.GetRemoteDownload().GetPath()
	if downloadURL == "" {
		return status.Error(codes.InvalidArgument, "remote download path is empty")
	}

	// Create temp file for download
	tempDir := os.TempDir()
	tempFile := filepath.Join(tempDir, fmt.Sprintf("setpackage_%s.tmp", filepath.Base(packageInfo.GetFilename())))
	
	glog.Infof("Downloading package from %s to %s", downloadURL, tempFile)
	
	// Use download package to fetch the file
	ctx := context.Background()
	session, result, err := download.DownloadFile(ctx, downloadURL, tempFile)
	if err != nil {
		glog.Errorf("Failed to download package: %v", err)
		// Clean up temp file if it exists
		os.Remove(tempFile)
		return status.Errorf(codes.Internal, "failed to download package: %v", err)
	}

	// Log download result
	glog.Infof("Download completed: %d bytes in %v", result.FileSize, result.Duration)

	// Verify MD5 checksum
	expectedMD5 := fmt.Sprintf("%x", hashInfo.GetHash())
	validator := checksum.NewMD5Validator()
	
	if err := validator.ValidateFile(tempFile, expectedMD5); err != nil {
		glog.Errorf("MD5 validation failed: %v", err)
		os.Remove(tempFile)
		return status.Errorf(codes.FailedPrecondition, "MD5 validation failed: %v", err)
	}

	// Prepare final destination path
	finalPath := packageInfo.GetFilename()
	if finalPath == "" {
		os.Remove(tempFile)
		return status.Error(codes.InvalidArgument, "package filename is empty")
	}

	// Apply rootFS prefix if path is absolute and rootFS is set
	if s.rootFS != "" && filepath.IsAbs(finalPath) {
		// Ensure the path doesn't already start with rootFS to avoid double prefixing
		if !strings.HasPrefix(finalPath, s.rootFS) {
			finalPath = filepath.Join(s.rootFS, finalPath)
		}
	}

	glog.Infof("Installing package to: %s", finalPath)

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(finalPath), 0755); err != nil {
		os.Remove(tempFile)
		return status.Errorf(codes.Internal, "failed to create directory: %v", err)
	}

	// Move temp file to final location
	if err := os.Rename(tempFile, finalPath); err != nil {
		// If rename fails (e.g., cross-device), try copy and delete
		if err := copyFile(tempFile, finalPath); err != nil {
			os.Remove(tempFile)
			return status.Errorf(codes.Internal, "failed to move package to final location: %v", err)
		}
		os.Remove(tempFile)
	}

	glog.Infof("Package successfully installed at %s (session: %s)", finalPath, session.ID)
	
	// Send empty response
	return stream.SendAndClose(&system.SetPackageResponse{})
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}