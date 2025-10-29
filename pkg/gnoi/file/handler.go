package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	log "github.com/golang/glog"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var allowedPrefixes = []string{"/tmp/", "/var/tmp/"}

func isWhitelisted(path string) bool {
	// Use absolute paths for proper comparison
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	for _, prefix := range allowedPrefixes {
		absPrefix, _ := filepath.Abs(prefix)
		// Ensure path stays within directory (not just starts with it)
		if strings.HasPrefix(abs, absPrefix+string(filepath.Separator)) {
			return true
		}
		// Also allow exact match (e.g., "/tmp" itself)
		if abs == absPrefix {
			return true
		}
	}
	return false
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

	if !isWhitelisted(remoteFile) {
		log.Errorf("Denied: file not in allowed directory: %s", remoteFile)
		return nil, status.Error(codes.PermissionDenied, "only files in /tmp/ or /var/tmp/ can be removed")
	}

	// Attempt remove and map errors to gRPC status codes for testable behavior.
	if err := os.Remove(remoteFile); err != nil {
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
