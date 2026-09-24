package file

import (
	"bytes"
	"context"
	"crypto/md5"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeGetServer struct {
	grpc.ServerStream
	ctx       context.Context
	responses []*gnoi_file_pb.GetResponse
	sendErr   error
}

func newFakeGetServer() *fakeGetServer {
	return &fakeGetServer{ctx: context.Background()}
}

func (s *fakeGetServer) Send(response *gnoi_file_pb.GetResponse) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	if chunk := response.GetContents(); chunk != nil {
		response = &gnoi_file_pb.GetResponse{Response: &gnoi_file_pb.GetResponse_Contents{
			Contents: append([]byte(nil), chunk...),
		}}
	}
	s.responses = append(s.responses, response)
	return nil
}

func (s *fakeGetServer) Context() context.Context     { return s.ctx }
func (s *fakeGetServer) SetHeader(metadata.MD) error  { return nil }
func (s *fakeGetServer) SendHeader(metadata.MD) error { return nil }
func (s *fakeGetServer) SetTrailer(metadata.MD)       {}

func setTempHostRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	previous := fileHostMapper
	fileHostMapper.Mount = root
	t.Cleanup(func() { fileHostMapper = previous })
	return root
}

func useTempHostRoot(t *testing.T) (logical, physical string) {
	t.Helper()
	root := setTempHostRoot(t)
	base := filepath.Join(root, "tmp")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create test root: %v", err)
	}
	physical, err := os.MkdirTemp(base, "file-test-*")
	if err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	return strings.TrimPrefix(physical, root), physical
}

func collectGetStream(t *testing.T, server *fakeGetServer) ([]byte, *types.HashType) {
	t.Helper()
	if len(server.responses) < 2 {
		t.Fatalf("got %d responses, want contents and hash", len(server.responses))
	}
	var data []byte
	for _, response := range server.responses[:len(server.responses)-1] {
		chunk := response.GetContents()
		if chunk == nil || len(chunk) > 64*1024 {
			t.Fatalf("invalid content frame size %d", len(chunk))
		}
		data = append(data, chunk...)
	}
	hash := server.responses[len(server.responses)-1].GetHash()
	if hash == nil {
		t.Fatal("final response is not a hash")
	}
	return data, hash
}

func TestHandleGetValidation(t *testing.T) {
	tests := []struct {
		name string
		req  *gnoi_file_pb.GetRequest
	}{
		{name: "nil request"},
		{name: "empty path", req: &gnoi_file_pb.GetRequest{}},
		{name: "relative path", req: &gnoi_file_pb.GetRequest{RemoteFile: "tmp/file"}},
		{name: "host mount", req: &gnoi_file_pb.GetRequest{RemoteFile: "/mnt/host/tmp/file"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := HandleGet(test.req, newFakeGetServer()); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument; err=%v", status.Code(err), err)
			}
		})
	}
}

func TestHandleGetFilesystemFailures(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	if err := syscall.Mkfifo(filepath.Join(physical, "pipe"), 0644); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	tests := []struct {
		name string
		path string
		code codes.Code
	}{
		{name: "missing", path: filepath.Join(logical, "missing"), code: codes.NotFound},
		{name: "directory", path: logical, code: codes.FailedPrecondition},
		{name: "non-regular file", path: filepath.Join(logical, "pipe"), code: codes.FailedPrecondition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: test.path}, newFakeGetServer())
			if status.Code(err) != test.code {
				t.Fatalf("code = %v, want %v; err=%v", status.Code(err), test.code, err)
			}
		})
	}
}

func TestHandleGetStreamsContentsAndHash(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	payload := bytes.Repeat([]byte("stream-data"), 20*1024)
	path := filepath.Join(physical, "payload.bin")
	if err := os.WriteFile(path, payload, 0644); err != nil {
		t.Fatal(err)
	}
	server := newFakeGetServer()
	if err := HandleGet(&gnoi_file_pb.GetRequest{
		RemoteFile: filepath.Join(logical, "payload.bin"),
	}, server); err != nil {
		t.Fatalf("HandleGet: %v", err)
	}
	got, hash := collectGetStream(t, server)
	wantHash := md5.Sum(payload)
	if !bytes.Equal(got, payload) || hash.Method != types.HashType_MD5 || !bytes.Equal(hash.Hash, wantHash[:]) {
		t.Fatal("stream did not preserve payload and MD5")
	}
	if len(server.responses) < 3 {
		t.Fatalf("got %d frames, want multiple content frames plus hash", len(server.responses))
	}
}

func TestHandleGetSizeLimit(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	previous := maxFileSize
	maxFileSize = 16
	t.Cleanup(func() { maxFileSize = previous })
	if err := os.WriteFile(filepath.Join(physical, "large.bin"), bytes.Repeat([]byte{'x'}, 17), 0644); err != nil {
		t.Fatal(err)
	}
	err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: filepath.Join(logical, "large.bin")}, newFakeGetServer())
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition; err=%v", status.Code(err), err)
	}
}

func TestHandleGetStreamFailure(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	if err := os.WriteFile(filepath.Join(physical, "payload"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	server := newFakeGetServer()
	server.sendErr = io.ErrClosedPipe
	err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: filepath.Join(logical, "payload")}, server)
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal; err=%v", status.Code(err), err)
	}
}
