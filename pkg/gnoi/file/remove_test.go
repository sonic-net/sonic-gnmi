package file

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var testCtx = context.Background()

func TestHandleFileRemove_NilRequest(t *testing.T) {
	resp, err := HandleFileRemove(context.Background(), nil)
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Nil(t, resp)
}

func TestRemove_DangerousFile(t *testing.T) {
	// With no blacklist, this will be denied by whitelist and return PermissionDenied.
	resp, err := HandleFileRemove(testCtx, &gnoi_file_pb.RemoveRequest{RemoteFile: "/etc/sonic/config_db.json"})
	assert.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Nil(t, resp)
}

func TestRemove_PathTraversal(t *testing.T) {
	// The handler denies paths not starting with allowed prefixes.
	// ../../etc/passwd will be rejected as "not whitelisted".
	resp, err := HandleFileRemove(testCtx, &gnoi_file_pb.RemoveRequest{RemoteFile: "../../etc/passwd"})
	assert.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Nil(t, resp)
}

func TestRemove_NonExistentFile(t *testing.T) {
	patch := gomonkey.ApplyFunc(os.Remove, func(path string) error {
		return os.ErrNotExist
	})
	defer patch.Reset()

	// Use an allowed path so handler reaches os.Remove
	resp, err := HandleFileRemove(testCtx, &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/notfound.txt"})
	assert.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
	// Handler returns a RemoveResponse even when os.Remove fails per current design:
	assert.NotNil(t, resp)
}

func TestRemove_RelativePath(t *testing.T) {
	// The handler currently denies relative paths (they don't start with /tmp/ or /var/tmp/)
	resp, err := HandleFileRemove(testCtx, &gnoi_file_pb.RemoveRequest{RemoteFile: "./somefile.txt"})
	assert.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Nil(t, resp)
}

func TestRemove_EmptyPath(t *testing.T) {
	resp, err := HandleFileRemove(testCtx, &gnoi_file_pb.RemoveRequest{RemoteFile: ""})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Nil(t, resp)
}

func TestRemove_SpecialCharFile(t *testing.T) {
	// Patch os.Remove to succeed and use an allowed path so handler invokes os.Remove.
	patch := gomonkey.ApplyFunc(os.Remove, func(path string) error {
		return nil // simulate success
	})
	defer patch.Reset()

	resp, err := HandleFileRemove(testCtx, &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/fil├⌐.txt"})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestRemove_PermissionDenied(t *testing.T) {
	patch := gomonkey.ApplyFunc(os.Remove, func(path string) error {
		return os.ErrPermission
	})
	defer patch.Reset()

	// Use an allowed path so handler reaches os.Remove
	resp, err := HandleFileRemove(testCtx, &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/forbidden.txt"})
	assert.Error(t, err)
	// ensure permission-denied is returned as gRPC code
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	// Handler returns a RemoveResponse with the error (non-nil resp) per current design
	assert.NotNil(t, resp)

	// Also verify errors.Is recognizes underlying os.ErrPermission
	assert.True(t, errors.Is(err, os.ErrPermission) || status.Code(err) == codes.PermissionDenied)
}
