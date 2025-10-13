package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	log "github.com/golang/glog"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var allowedPrefixes = []string{"/tmp/", "/var/tmp/"}
var blacklistedFiles = []string{"/etc/sonic/config_db.json", "/etc/passwd"}

func isWhitelisted(path string) bool {
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isBlacklisted(path string) bool {
	for _, b := range blacklistedFiles {
		if filepath.Clean(path) == b {
			return true
		}
	}
	return false
}

func hasPathTraversal(path string) bool {
	clean := filepath.Clean(path)
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(clean, prefix) {
			return false
		}
	}
	return true
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

	if isBlacklisted(remoteFile) {
		log.Errorf("Denied: blacklisted file removal attempt: %s", remoteFile)
		return nil, status.Error(codes.PermissionDenied, "removal of critical system files is forbidden")
	}
	if !isWhitelisted(remoteFile) {
		log.Errorf("Denied: file not in allowed directory: %s", remoteFile)
		return nil, status.Error(codes.PermissionDenied, "only files in /tmp/ or /var/tmp/ can be removed")
	}
	if hasPathTraversal(remoteFile) {
		log.Errorf("Denied: path traversal detected in: %s", remoteFile)
		return nil, status.Error(codes.PermissionDenied, "path traversal detected")
	}

	err := os.Remove(remoteFile)
	if err != nil {
		log.Errorf("Remove RPC failed: %v", err)
		return &gnoi_file_pb.RemoveResponse{}, err
	}

	log.Infof("Successfully removed file: %s", remoteFile)
	return &gnoi_file_pb.RemoveResponse{}, nil
}
