package file

import (
	"context"
	"crypto/md5"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// fakeGetServer captures GetResponse messages emitted by HandleGet. It
// satisfies gnoi_file_pb.File_GetServer (grpc.ServerStream + Send).
type fakeGetServer struct {
	grpc.ServerStream
	ctx       context.Context
	responses []*gnoi_file_pb.GetResponse
	sendErr   error
}

func newFakeGetServer() *fakeGetServer {
	return &fakeGetServer{ctx: context.Background()}
}

func (s *fakeGetServer) Send(r *gnoi_file_pb.GetResponse) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	// Real gRPC Send marshals the message synchronously, so the
	// handler is allowed to reuse its buffer after Send returns. For
	// the fake, copy any Contents to mimic that contract.
	if c := r.GetContents(); c != nil {
		dup := make([]byte, len(c))
		copy(dup, c)
		r = &gnoi_file_pb.GetResponse{
			Response: &gnoi_file_pb.GetResponse_Contents{Contents: dup},
		}
	}
	s.responses = append(s.responses, r)
	return nil
}
func (s *fakeGetServer) Context() context.Context     { return s.ctx }
func (s *fakeGetServer) SetHeader(metadata.MD) error  { return nil }
func (s *fakeGetServer) SendHeader(metadata.MD) error { return nil }
func (s *fakeGetServer) SetTrailer(metadata.MD)       {}

// getTestRoot mirrors statTestRoot from stat_test.go: builds fixtures
// under the *physical* root (/mnt/host/tmp/... when /mnt/host is
// present) and returns the matching *logical* root that should be sent
// in the request.
func getTestRoot(t *testing.T) (logical, physical string) {
	t.Helper()
	physBase := "/tmp"
	if _, err := os.Stat("/mnt/host"); err == nil {
		physBase = "/mnt/host/tmp"
		if err := os.MkdirAll(physBase, 0755); err != nil {
			t.Fatalf("ensure /mnt/host/tmp: %v", err)
		}
	}
	phys, err := os.MkdirTemp(physBase, "get-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(phys) })

	logi := phys
	if strings.HasPrefix(phys, "/mnt/host") {
		logi = strings.TrimPrefix(phys, "/mnt/host")
	}
	return logi, phys
}

// collectStream concatenates the contents from every Contents message
// and returns (data, finalHash). Asserts that the last message is a
// HashType message.
func collectStream(t *testing.T, srv *fakeGetServer) ([]byte, *types.HashType) {
	t.Helper()
	if len(srv.responses) == 0 {
		t.Fatal("no responses captured")
	}
	var data []byte
	for i, r := range srv.responses[:len(srv.responses)-1] {
		c := r.GetContents()
		if c == nil {
			t.Fatalf("response %d: expected Contents, got %T", i, r.GetResponse())
		}
		data = append(data, c...)
	}
	last := srv.responses[len(srv.responses)-1]
	h := last.GetHash()
	if h == nil {
		t.Fatalf("final response is not a Hash, got %T", last.GetResponse())
	}
	return data, h
}

func TestHandleGet_NilRequest(t *testing.T) {
	err := HandleGet(nil, newFakeGetServer())
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestHandleGet_EmptyPath(t *testing.T) {
	err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: ""}, newFakeGetServer())
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestHandleGet_RelativePath(t *testing.T) {
	err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: "rel/path"}, newFakeGetServer())
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestHandleGet_RejectsMntHostPrefix(t *testing.T) {
	for _, p := range []string{"/mnt/host", "/mnt/host/tmp/foo"} {
		err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: p}, newFakeGetServer())
		if err == nil || status.Code(err) != codes.InvalidArgument {
			t.Errorf("path %q: expected InvalidArgument, got %v", p, err)
		}
	}
}

func TestHandleGet_NotFound(t *testing.T) {
	logi, _ := getTestRoot(t)
	err := HandleGet(&gnoi_file_pb.GetRequest{
		RemoteFile: filepath.Join(logi, "no-such-file"),
	}, newFakeGetServer())
	if err == nil || status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestHandleGet_DirectoryRejected(t *testing.T) {
	logi, _ := getTestRoot(t)
	err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: logi}, newFakeGetServer())
	if err == nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", err)
	}
}

func TestHandleGet_EmptyFile(t *testing.T) {
	logi, phys := getTestRoot(t)
	if err := os.WriteFile(filepath.Join(phys, "empty.bin"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	srv := newFakeGetServer()
	if err := HandleGet(&gnoi_file_pb.GetRequest{
		RemoteFile: filepath.Join(logi, "empty.bin"),
	}, srv); err != nil {
		t.Fatalf("HandleGet: %v", err)
	}
	// Empty file: no Contents messages, only the final Hash.
	if len(srv.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(srv.responses))
	}
	h := srv.responses[0].GetHash()
	if h == nil {
		t.Fatalf("expected Hash, got %T", srv.responses[0].GetResponse())
	}
	if h.Method != types.HashType_MD5 {
		t.Errorf("hash method = %v, want MD5", h.Method)
	}
	want := md5.Sum(nil)
	if string(h.Hash) != string(want[:]) {
		t.Errorf("hash mismatch for empty file")
	}
}

func TestHandleGet_SmallFile(t *testing.T) {
	logi, phys := getTestRoot(t)
	payload := []byte("hello sonic gnmi")
	if err := os.WriteFile(filepath.Join(phys, "small.bin"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	srv := newFakeGetServer()
	if err := HandleGet(&gnoi_file_pb.GetRequest{
		RemoteFile: filepath.Join(logi, "small.bin"),
	}, srv); err != nil {
		t.Fatalf("HandleGet: %v", err)
	}
	got, h := collectStream(t, srv)
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q want %q", got, payload)
	}
	want := md5.Sum(payload)
	if string(h.Hash) != string(want[:]) {
		t.Errorf("hash mismatch")
	}
	// Small file should fit in a single chunk.
	if n := len(srv.responses); n != 2 {
		t.Errorf("expected 2 responses (1 chunk + hash), got %d", n)
	}
}

func TestHandleGet_LargeFileChunks(t *testing.T) {
	// 200 KiB file forces >1 chunk (chunk size is 64 KiB).
	logi, phys := getTestRoot(t)
	payload := make([]byte, 200*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(phys, "large.bin"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	srv := newFakeGetServer()
	if err := HandleGet(&gnoi_file_pb.GetRequest{
		RemoteFile: filepath.Join(logi, "large.bin"),
	}, srv); err != nil {
		t.Fatalf("HandleGet: %v", err)
	}
	got, h := collectStream(t, srv)
	if string(got) != string(payload) {
		t.Errorf("payload mismatch (lengths got=%d want=%d)", len(got), len(payload))
	}
	want := md5.Sum(payload)
	if string(h.Hash) != string(want[:]) {
		t.Errorf("hash mismatch on chunked stream")
	}
	// 200 KiB / 64 KiB = 4 chunks (3 full + 1 partial), plus the final hash.
	if n := len(srv.responses); n != 5 {
		t.Errorf("expected 5 responses (4 chunks + hash), got %d", n)
	}
	// Every Contents message except possibly the last must be exactly 64 KiB.
	for i := 0; i < len(srv.responses)-2; i++ {
		c := srv.responses[i].GetContents()
		if len(c) != 64*1024 {
			t.Errorf("response %d size = %d, want %d", i, len(c), 64*1024)
		}
	}
}

func TestHandleGet_StreamSendError(t *testing.T) {
	logi, phys := getTestRoot(t)
	if err := os.WriteFile(filepath.Join(phys, "x"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := newFakeGetServer()
	srv.sendErr = io.ErrClosedPipe
	err := HandleGet(&gnoi_file_pb.GetRequest{
		RemoteFile: filepath.Join(logi, "x"),
	}, srv)
	if err == nil || status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal on send failure, got %v", err)
	}
}

func TestHandleGet_ContextCancelled(t *testing.T) {
	logi, phys := getTestRoot(t)
	if err := os.WriteFile(filepath.Join(phys, "x"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := newFakeGetServer()
	srv.ctx = ctx
	err := HandleGet(&gnoi_file_pb.GetRequest{
		RemoteFile: filepath.Join(logi, "x"),
	}, srv)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if c := status.Code(err); c != codes.Canceled && c != codes.DeadlineExceeded {
		t.Errorf("expected Canceled/DeadlineExceeded, got %v", err)
	}
}
