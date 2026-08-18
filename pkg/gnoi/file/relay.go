package file

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const relayMaxFileSize = 64 * 1024

var relayFsync = unix.Fsync

var relayFstat = unix.Fstat

var relayFchmod = unix.Fchmod

var relayTrustedOwnerUID uint32

func managedDirectoryComponent(path string, index int) bool {
	if strings.HasPrefix(path, "/var/tmp/") {
		return index >= 2
	}
	if strings.HasPrefix(path, "/host/") {
		return index >= 1
	}
	return false
}

func secureManagedDirectory(fd int) error {
	var info unix.Stat_t
	if err := relayFstat(fd, &info); err != nil {
		return status.Errorf(codes.Internal, "failed to inspect relay directory: %v", err)
	}
	if info.Uid != relayTrustedOwnerUID {
		return status.Errorf(codes.PermissionDenied, "relay directory owner %d is not trusted", info.Uid)
	}
	mode := os.FileMode(info.Mode).Perm()
	if mode&0022 != 0 || mode&0700 != 0700 {
		if err := relayFchmod(fd, 0750); err != nil {
			return status.Errorf(codes.PermissionDenied, "failed to correct root-owned relay directory mode %04o: %v", mode, err)
		}
		var corrected unix.Stat_t
		if err := relayFstat(fd, &corrected); err != nil {
			return status.Errorf(codes.Internal, "failed to verify corrected relay directory mode: %v", err)
		}
		if os.FileMode(corrected.Mode).Perm() != 0750 {
			return status.Errorf(codes.PermissionDenied, "relay directory mode correction did not take effect")
		}
	}
	if err := relayFsync(fd); err != nil {
		return status.Errorf(codes.Internal, "failed to sync relay directory metadata: %v", err)
	}
	return nil
}

func createRelayTemp(parentFD int, base string) (*os.File, string, error) {
	for attempts := 0; attempts < 10; attempts++ {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return nil, "", err
		}
		name := "." + base + ".tmp-" + hex.EncodeToString(random)
		fd, err := unix.Openat(parentFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if err == nil {
			return os.NewFile(uintptr(fd), name), name, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("could not allocate a unique relay temporary file")
}

// CanonicalHostVisiblePath validates the exact path spelling used by File RPCs.
// Clients must not use container-internal, traversal, dot, or repeated-slash aliases.
func CanonicalHostVisiblePath(path string) (string, error) {
	cleanPath := filepath.Clean(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", status.Error(codes.InvalidArgument, "File path must be an absolute host-visible path")
	}
	if cleanPath != path || hasParentSegment(path) {
		return "", status.Error(codes.InvalidArgument, "File path must use its canonical host-visible spelling")
	}
	if cleanPath == "/mnt/host" || strings.HasPrefix(cleanPath, "/mnt/host/") {
		return "", status.Error(codes.InvalidArgument, "File path must not use the container-internal /mnt/host prefix")
	}
	return cleanPath, nil
}

func openRestrictedParent(path string, create bool) (int, string, error) {
	cleanPath, err := CanonicalHostVisiblePath(path)
	if err != nil {
		return -1, "", err
	}

	rootFD, err := unix.Open(hostRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, "", status.Errorf(codes.FailedPrecondition, "host root %s is unavailable or invalid: %v", hostRoot, err)
	}
	currentFD := rootFD
	components := strings.Split(strings.TrimPrefix(filepath.Dir(cleanPath), "/"), "/")
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			unix.Close(currentFD)
			return -1, "", status.Error(codes.InvalidArgument, "invalid relay path component")
		}
		nextFD, openErr := unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil && create && errors.Is(openErr, unix.ENOENT) {
			mode := uint32(0755)
			if managedDirectoryComponent(cleanPath, index) {
				mode = 0750
			}
			if mkdirErr := unix.Mkdirat(currentFD, component, mode); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				unix.Close(currentFD)
				return -1, "", status.Errorf(codes.Internal, "failed to create relay directory: %v", mkdirErr)
			}
			if syncErr := relayFsync(currentFD); syncErr != nil {
				unix.Close(currentFD)
				return -1, "", status.Errorf(codes.Internal, "failed to sync relay directory parent: %v", syncErr)
			}
			nextFD, openErr = unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			unix.Close(currentFD)
			if errors.Is(openErr, unix.ENOENT) {
				return -1, "", status.Error(codes.NotFound, "relay file directory does not exist")
			}
			if errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, unix.ENOTDIR) {
				return -1, "", status.Error(codes.PermissionDenied, "relay path contains a symlink or non-directory component")
			}
			return -1, "", status.Errorf(codes.Internal, "failed to open relay directory: %v", openErr)
		}
		if managedDirectoryComponent(cleanPath, index) {
			if secureErr := secureManagedDirectory(nextFD); secureErr != nil {
				unix.Close(nextFD)
				unix.Close(currentFD)
				return -1, "", secureErr
			}
		}
		unix.Close(currentFD)
		currentFD = nextFD
	}
	return currentFD, filepath.Base(cleanPath), nil
}

// PrepareRestrictedPaths creates and durably records parent directories for fixed relay files.
func PrepareRestrictedPaths(paths ...string) error {
	for _, path := range paths {
		parentFD, _, err := openRestrictedParent(path, true)
		if err != nil {
			return err
		}
		if err := relayFsync(parentFD); err != nil {
			unix.Close(parentFD)
			return status.Errorf(codes.Internal, "failed to sync relay directory: %v", err)
		}
		if err := unix.Close(parentFD); err != nil {
			return status.Errorf(codes.Internal, "failed to close relay directory: %v", err)
		}
	}
	return nil
}

func hasParentSegment(path string) bool {
	for _, component := range strings.Split(path, "/") {
		if component == ".." {
			return true
		}
	}
	return false
}

// HandleRestrictedPut durably replaces one fixed file without following symlinks.
func HandleRestrictedPut(stream gnoi_file_pb.File_PutServer, allowedPath string) (retErr error) {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to receive File.Put open message: %v", err)
	}
	open := first.GetOpen()
	if open == nil {
		return status.Error(codes.InvalidArgument, "first message must be Open")
	}
	requestPath, err := CanonicalHostVisiblePath(open.GetRemoteFile())
	if err != nil {
		return err
	}
	canonicalAllowedPath, err := CanonicalHostVisiblePath(allowedPath)
	if err != nil {
		return status.Errorf(codes.Internal, "invalid configured relay path: %v", err)
	}
	if requestPath != canonicalAllowedPath {
		return status.Error(codes.PermissionDenied, "relay File.Put path is not authorized")
	}

	parentFD, base, err := openRestrictedParent(canonicalAllowedPath, true)
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)

	temp, tempName, err := createRelayTemp(parentFD, base)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create relay temporary file: %v", err)
	}
	renamed := false
	closed := false
	defer func() {
		if !closed {
			closeErr := temp.Close()
			closed = true
			if closeErr != nil && retErr == nil {
				retErr = status.Errorf(codes.Internal, "failed to close relay temporary file: %v", closeErr)
			}
		}
		if !renamed {
			_ = unix.Unlinkat(parentFD, tempName, 0)
		}
	}()
	if err := temp.Chmod(0600); err != nil {
		return status.Errorf(codes.Internal, "failed to set relay file permissions: %v", err)
	}

	hasher := md5.New() // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5 -- gNOI File requires MD5 wire integrity.
	written := 0
	for {
		req, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return status.Error(codes.InvalidArgument, "unexpected end of stream before hash")
			}
			if errors.Is(recvErr, context.Canceled) || errors.Is(recvErr, context.DeadlineExceeded) {
				return status.FromContextError(recvErr).Err()
			}
			return status.Errorf(codes.Internal, "failed to receive relay file data: %v", recvErr)
		}
		if contents := req.GetContents(); contents != nil {
			if len(contents) > relayMaxFileSize || written > relayMaxFileSize-len(contents) {
				return status.Errorf(codes.ResourceExhausted, "relay file exceeds maximum size of %d bytes", relayMaxFileSize)
			}
			if _, err := temp.Write(contents); err != nil {
				return status.Errorf(codes.Internal, "failed to write relay file: %v", err)
			}
			_, _ = hasher.Write(contents)
			written += len(contents)
			continue
		}

		hashMessage := req.GetHash()
		if hashMessage == nil {
			return status.Error(codes.InvalidArgument, "message must contain contents or hash")
		}
		if hashMessage.GetMethod() != types.HashType_MD5 {
			return status.Error(codes.InvalidArgument, "relay File.Put hash method must be MD5")
		}
		received := hashMessage.GetHash()
		calculated := hasher.Sum(nil)
		if len(received) != md5.Size || subtle.ConstantTimeCompare(calculated, received) != 1 {
			return status.Error(codes.DataLoss, "relay File.Put MD5 digest mismatch")
		}
		if _, recvErr := stream.Recv(); !errors.Is(recvErr, io.EOF) {
			if recvErr == nil {
				return status.Error(codes.InvalidArgument, "hash must be the final File.Put message")
			}
			return status.Errorf(codes.Internal, "failed to finish relay File.Put stream: %v", recvErr)
		}
		break
	}

	if err := relayFsync(int(temp.Fd())); err != nil {
		return status.Errorf(codes.Internal, "failed to sync relay temporary file: %v", err)
	}
	if err := temp.Close(); err != nil {
		return status.Errorf(codes.Internal, "failed to close relay temporary file: %v", err)
	}
	closed = true
	if err := unix.Renameat(parentFD, tempName, parentFD, base); err != nil {
		return status.Errorf(codes.Internal, "failed to replace relay file: %v", err)
	}
	renamed = true
	if err := relayFsync(parentFD); err != nil {
		return status.Errorf(codes.Internal, "failed to sync relay directory: %v", err)
	}
	return stream.SendAndClose(&gnoi_file_pb.PutResponse{})
}

// HandleRelayPut keeps the relay API name used by existing callers.
func HandleRelayPut(stream gnoi_file_pb.File_PutServer, allowedPath string) error {
	return HandleRestrictedPut(stream, allowedPath)
}

// HandleRestrictedGet streams one fixed file without following symlinks.
func HandleRestrictedGet(req *gnoi_file_pb.GetRequest, stream gnoi_file_pb.File_GetServer, allowedPath string) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "relay File.Get request is required")
	}
	requestPath, err := CanonicalHostVisiblePath(req.GetRemoteFile())
	if err != nil {
		return err
	}
	canonicalAllowedPath, err := CanonicalHostVisiblePath(allowedPath)
	if err != nil {
		return status.Errorf(codes.Internal, "invalid configured relay path: %v", err)
	}
	if requestPath != canonicalAllowedPath {
		return status.Error(codes.PermissionDenied, "relay File.Get path is not authorized")
	}
	parentFD, base, err := openRestrictedParent(canonicalAllowedPath, false)
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)

	fd, err := unix.Openat(parentFD, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return status.Error(codes.NotFound, "relay status file does not exist")
		}
		if errors.Is(err, unix.ELOOP) {
			return status.Error(codes.PermissionDenied, "relay status file must not be a symlink")
		}
		return status.Errorf(codes.Internal, "failed to open relay status file: %v", err)
	}
	file := os.NewFile(uintptr(fd), canonicalAllowedPath)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to stat relay status file: %v", err)
	}
	if !info.Mode().IsRegular() {
		return status.Error(codes.FailedPrecondition, "relay status path is not a regular file")
	}
	if info.Size() > relayMaxFileSize {
		return status.Errorf(codes.ResourceExhausted, "relay status file exceeds maximum size of %d bytes", relayMaxFileSize)
	}
	data, err := io.ReadAll(io.LimitReader(file, relayMaxFileSize+1))
	if err != nil {
		return status.Errorf(codes.Internal, "failed to read relay status file: %v", err)
	}
	if len(data) > relayMaxFileSize {
		return status.Errorf(codes.ResourceExhausted, "relay status file exceeds maximum size of %d bytes", relayMaxFileSize)
	}
	digest := md5.Sum(data) // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5 -- gNOI File requires MD5 wire integrity.
	for len(data) > 0 {
		chunkSize := len(data)
		if chunkSize > relayMaxFileSize {
			chunkSize = relayMaxFileSize
		}
		if err := stream.Send(&gnoi_file_pb.GetResponse{
			Response: &gnoi_file_pb.GetResponse_Contents{Contents: data[:chunkSize]},
		}); err != nil {
			return status.Errorf(codes.Internal, "failed to send relay status data: %v", err)
		}
		data = data[chunkSize:]
	}
	return stream.Send(&gnoi_file_pb.GetResponse{
		Response: &gnoi_file_pb.GetResponse_Hash{Hash: &types.HashType{
			Method: types.HashType_MD5,
			Hash:   digest[:],
		}},
	})
}

// HandleRelayGet keeps the relay API name used by existing callers.
func HandleRelayGet(req *gnoi_file_pb.GetRequest, stream gnoi_file_pb.File_GetServer, allowedPath string) error {
	return HandleRestrictedGet(req, stream, allowedPath)
}
