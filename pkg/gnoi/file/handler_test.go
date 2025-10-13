package file

import (
	"context"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
)

func TestHandleFileRemove_NilRequest(t *testing.T) {
	resp, err := HandleFileRemove(context.Background(), nil)
	if err == nil {
		t.Error("Expected error for nil request, got nil")
	}
	if resp != nil {
		t.Error("Expected nil response for nil request, got non-nil")
	}
}

func TestRemove_DangerousFile(t *testing.T) {
	// /etc/sonic/config_db.json should be blocked
	resp, err := HandleFileRemove(testCtx, &RemoveRequest{RemoteFile: "/etc/sonic/config_db.json"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dangerous") // adapt to your error message
	assert.Nil(t, resp)
}

func TestRemove_PathTraversal(t *testing.T) {
	resp, err := HandleFileRemove(testCtx, &RemoveRequest{RemoteFile: "../../etc/passwd"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "traversal") // adapt as needed
	assert.Nil(t, resp)
}

func TestRemove_NonExistentFile(t *testing.T) {
	patch := gomonkey.ApplyFunc(os.Remove, func(path string) error {
		return os.ErrNotExist
	})
	defer patch.Reset()

	resp, err := HandleFileRemove(testCtx, &RemoveRequest{RemoteFile: "/tmp/notfound.txt"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
	assert.Nil(t, resp)
}

func TestRemove_RelativePath(t *testing.T) {
	resp, err := HandleFileRemove(testCtx, &RemoveRequest{RemoteFile: "./somefile.txt"})
	// Decide if you allow or block relative path
	assert.NoError(t, err) // or assert.Error if you block
	assert.NotNil(t, resp)
}

func TestRemove_EmptyPath(t *testing.T) {
	resp, err := HandleFileRemove(testCtx, &RemoveRequest{RemoteFile: ""})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	assert.Nil(t, resp)
}

func TestRemove_SpecialCharFile(t *testing.T) {
	patch := gomonkey.ApplyFunc(os.Remove, func(path string) error {
		return nil // simulate success
	})
	defer patch.Reset()

	resp, err := HandleFileRemove(testCtx, &RemoveRequest{RemoteFile: "filé.txt"})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestRemove_PermissionDenied(t *testing.T) {
	patch := gomonkey.ApplyFunc(os.Remove, func(path string) error {
		return os.ErrPermission
	})
	defer patch.Reset()

	resp, err := HandleFileRemove(testCtx, &RemoveRequest{RemoteFile: "/tmp/forbidden.txt"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrPermission))
	assert.Nil(t, resp)
}
