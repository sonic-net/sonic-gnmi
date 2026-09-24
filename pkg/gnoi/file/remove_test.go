package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHandleFileRemoveValidation(t *testing.T) {
	tests := []struct {
		name string
		req  *gnoi_file_pb.RemoveRequest
		code codes.Code
	}{
		{name: "nil request", code: codes.InvalidArgument},
		{name: "empty path", req: &gnoi_file_pb.RemoveRequest{}, code: codes.InvalidArgument},
		{name: "persistent path", req: &gnoi_file_pb.RemoveRequest{RemoteFile: "/etc/sonic/config_db.json"}, code: codes.PermissionDenied},
		{name: "traversal", req: &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/a/../b"}, code: codes.PermissionDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := HandleFileRemove(context.Background(), test.req)
			if response != nil || status.Code(err) != test.code {
				t.Fatalf("response=%v code=%v, want nil/%v; err=%v", response, status.Code(err), test.code, err)
			}
		})
	}
}

func TestHandleFileRemoveDLDDInbox(t *testing.T) {
	root := setTempHostRoot(t)
	destination := root + dlddRulesInboxPath
	if err := os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("rules"), dlddRulesInboxMode); err != nil {
		t.Fatal(err)
	}
	response, err := HandleFileRemove(context.Background(), &gnoi_file_pb.RemoveRequest{RemoteFile: dlddRulesInboxPath})
	if response != nil || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("response=%v code=%v, want nil/PermissionDenied; err=%v", response, status.Code(err), err)
	}
	if content, readErr := os.ReadFile(destination); readErr != nil || string(content) != "rules" {
		t.Fatalf("protected inbox changed: content=%q err=%v", content, readErr)
	}
}

func TestHandleFileRemovePrimaryWorkflow(t *testing.T) {
	root := setTempHostRoot(t)
	destination := filepath.Join(root, "tmp", "remove.txt")
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("remove me"), 0644); err != nil {
		t.Fatal(err)
	}
	response, err := HandleFileRemove(context.Background(), &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/remove.txt"})
	if err != nil || response == nil {
		t.Fatalf("HandleFileRemove: response=%v err=%v", response, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination still exists: %v", err)
	}
}

func TestHandleFileRemoveFilesystemFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "missing", err: os.ErrNotExist, code: codes.NotFound},
		{name: "permission", err: os.ErrPermission, code: codes.PermissionDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			patch := gomonkey.ApplyFunc(os.Remove, func(string) error { return test.err })
			defer patch.Reset()
			response, err := HandleFileRemove(context.Background(), &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/file"})
			if response == nil || status.Code(err) != test.code {
				t.Fatalf("response=%v code=%v, want non-nil/%v; err=%v", response, status.Code(err), test.code, err)
			}
		})
	}
}
