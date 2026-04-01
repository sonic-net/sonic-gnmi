package file

import (
	"context"
	"crypto/md5"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- copyFile tests ---

func TestCopyFile_Success(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcPath := filepath.Join(srcDir, "src.bin")
	dstPath := filepath.Join(dstDir, "dst.bin")

	content := []byte("hello copyFile")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestCopyFile_CreatesParentDirs(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcPath := filepath.Join(srcDir, "src.bin")
	dstPath := filepath.Join(dstDir, "a", "b", "c", "dst.bin")

	if err := os.WriteFile(srcPath, []byte("nested"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "nested" {
		t.Errorf("content = %q, want %q", got, "nested")
	}
}

func TestCopyFile_SourceNotFound(t *testing.T) {
	err := copyFile("/tmp/definitely_nonexistent_src_12345", "/tmp/dst_12345")
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Logf("got error: %v", err)
	}
}

func TestCopyFile_DestinationNotWritable(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "src.bin")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// /proc is read-only on Linux
	err := copyFile(srcPath, "/proc/fakedir/dst.bin")
	if err == nil {
		t.Fatal("expected error for unwritable destination")
	}
}

// --- dropPageCache tests ---

func TestDropPageCache_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	if err := os.WriteFile(path, []byte("page cache test data"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	// Should not panic or error
	dropPageCache(f)
}

func TestDropPageCache_ClosedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "closed.bin")
	if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.Close()

	// Stat on closed file fails — should return early without panic
	dropPageCache(f)
}

// --- mockPutStreamWithErrors: mid-stream error injection ---

type mockPutStreamWithErrors struct {
	gnoi_file_pb.File_PutServer
	requests  []*gnoi_file_pb.PutRequest
	responses []*gnoi_file_pb.PutResponse
	recvIdx   int
	// errAfter injects an error after this many successful Recv calls.
	// -1 means no injection (use normal EOF behavior).
	errAfter int
	injErr   error
	sendErr  error
	ctx      context.Context
}

func (m *mockPutStreamWithErrors) Context() context.Context {
	return m.ctx
}

func (m *mockPutStreamWithErrors) Recv() (*gnoi_file_pb.PutRequest, error) {
	if m.errAfter >= 0 && m.recvIdx >= m.errAfter {
		return nil, m.injErr
	}
	if m.recvIdx >= len(m.requests) {
		return nil, io.EOF
	}
	req := m.requests[m.recvIdx]
	m.recvIdx++
	return req, nil
}

func (m *mockPutStreamWithErrors) SendAndClose(resp *gnoi_file_pb.PutResponse) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.responses = append(m.responses, resp)
	return nil
}

// --- HandlePut: context canceled mid-stream (line 407) ---

func TestHandlePut_ContextCanceledMidStream(t *testing.T) {
	content := []byte("some data before cancel")
	hasher := md5.New()
	hasher.Write(content)

	stream := &mockPutStreamWithErrors{
		ctx: context.Background(),
		requests: []*gnoi_file_pb.PutRequest{
			{Request: &gnoi_file_pb.PutRequest_Open{
				Open: &gnoi_file_pb.PutRequest_Details{
					RemoteFile:  "/tmp/cancel_test.txt",
					Permissions: 0644,
				},
			}},
			{Request: &gnoi_file_pb.PutRequest_Contents{Contents: content}},
		},
		// After receiving Open + 1 content chunk (2 calls), inject context.Canceled
		errAfter: 2,
		injErr:   context.Canceled,
	}

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("expected error")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.Canceled {
		t.Errorf("code = %v, want Canceled", st.Code())
	}

	// Cleanup
	os.Remove("/tmp/cancel_test.txt.tmp")
}

// --- HandlePut: generic recv error mid-stream (line 410) ---

func TestHandlePut_GenericRecvError(t *testing.T) {
	content := []byte("data before error")
	stream := &mockPutStreamWithErrors{
		ctx: context.Background(),
		requests: []*gnoi_file_pb.PutRequest{
			{Request: &gnoi_file_pb.PutRequest_Open{
				Open: &gnoi_file_pb.PutRequest_Details{
					RemoteFile:  "/tmp/recv_err_test.txt",
					Permissions: 0644,
				},
			}},
			{Request: &gnoi_file_pb.PutRequest_Contents{Contents: content}},
		},
		errAfter: 2,
		injErr:   errors.New("transport closed"),
	}

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("expected error")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}

	// Cleanup
	os.Remove("/tmp/recv_err_test.txt.tmp")
}

// --- HandlePut: deadline exceeded mid-stream (also hits line 407) ---

func TestHandlePut_DeadlineExceeded(t *testing.T) {
	stream := &mockPutStreamWithErrors{
		ctx: context.Background(),
		requests: []*gnoi_file_pb.PutRequest{
			{Request: &gnoi_file_pb.PutRequest_Open{
				Open: &gnoi_file_pb.PutRequest_Details{
					RemoteFile:  "/tmp/deadline_test.txt",
					Permissions: 0644,
				},
			}},
			{Request: &gnoi_file_pb.PutRequest_Contents{Contents: []byte("data")}},
		},
		errAfter: 2,
		injErr:   context.DeadlineExceeded,
	}

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("expected error")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.Canceled {
		t.Errorf("code = %v, want Canceled", st.Code())
	}

	// Cleanup
	os.Remove("/tmp/deadline_test.txt.tmp")
}

// --- HandlePut: two-phase write via gomonkey (lines 466-471) ---
// We simulate the container environment by patching translatePathForContainer
// to return a different path than the cleaned input.

func TestHandlePut_TwoPhaseWrite(t *testing.T) {
	// Use gomonkey to make translatePathForContainer return a different path,
	// triggering the twoPhase branch.
	// We can't import gomonkey here without adding it to this file's imports,
	// so we use a simpler approach: create a file at a path where
	// /mnt/host exists (if in container) or test the single-phase path
	// and separately test copyFile.
	//
	// The two-phase code path is: copyFile(tempPath, finalPath) + os.Remove(tempPath)
	// copyFile is already tested above. The integration through HandlePut
	// depends on translatePathForContainer returning a different path, which
	// requires /mnt/host to exist.
	//
	// Instead, let's test this by creating a temp /mnt/host if possible.

	// Skip if we can't create /mnt/host (non-root)
	if err := os.MkdirAll("/mnt/host/tmp", 0755); err != nil {
		t.Skip("cannot create /mnt/host/tmp (need root), skipping two-phase test")
	}
	defer os.RemoveAll("/mnt/host/tmp/twophase_test.txt")

	content := []byte("two phase content")
	hasher := md5.New()
	hasher.Write(content)
	expectedHash := hasher.Sum(nil)

	stream := newMockPutStream()
	stream.addOpenRequest("/tmp/twophase_test.txt", 0644)
	stream.addContentRequest(content)
	stream.addHashRequest(expectedHash)

	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	// In container mode, file should be at /mnt/host/tmp/twophase_test.txt
	data, err := os.ReadFile("/mnt/host/tmp/twophase_test.txt")
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content = %q, want %q", data, content)
	}

	// Staging file should be cleaned up
	if _, err := os.Stat("/tmp/twophase_test.txt.tmp"); !os.IsNotExist(err) {
		t.Error("staging temp file was not removed")
	}
}

// --- copyFile: large file to exercise the full path including page cache ---

func TestCopyFile_LargeFile(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcPath := filepath.Join(srcDir, "large.bin")
	dstPath := filepath.Join(dstDir, "large_copy.bin")

	// 2 MB file
	size := 2 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != size {
		t.Errorf("size = %d, want %d", len(got), size)
	}

	srcHash := md5.Sum(data)
	gotHash := md5.Sum(got)
	if srcHash != gotHash {
		t.Error("hash mismatch between source and copy")
	}
}

// --- HandlePut: rename failure in single-phase path (line 475) ---
// This is hard to trigger without fs manipulation. The typical single-phase
// path (same filesystem rename) doesn't fail. We exercise it implicitly
// through existing success tests. Instead, test that HandlePut works with
// /var/tmp paths too (exercises line 371 log path for non-twoPhase).

func TestHandlePut_VarTmpSinglePhase(t *testing.T) {
	content := []byte("var tmp single phase test")
	hasher := md5.New()
	hasher.Write(content)
	expectedHash := hasher.Sum(nil)

	stream := newMockPutStream()
	stream.addOpenRequest("/var/tmp/single_phase_test.txt", 0755)
	stream.addContentRequest(content)
	stream.addHashRequest(expectedHash)

	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	path := "/var/tmp/single_phase_test.txt"
	if _, err := os.Stat("/mnt/host"); err == nil {
		path = "/mnt/host/var/tmp/single_phase_test.txt"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content = %q, want %q", data, content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("perms = %o, want %o", info.Mode().Perm(), 0755)
	}

	os.Remove(path)
}

// --- HandlePut: hash with hex string format ---

func TestHandlePut_MD5HashTypeUnspecified(t *testing.T) {
	content := []byte("hash type test")
	hasher := md5.New()
	hasher.Write(content)
	hash := hasher.Sum(nil)

	stream := newMockPutStream()
	stream.addOpenRequest("/tmp/hash_type_test.txt", 0644)
	stream.addContentRequest(content)
	// Use UNSPECIFIED hash method (default)
	stream.requests = append(stream.requests, &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Hash{
			Hash: &types.HashType{
				Method: types.HashType_UNSPECIFIED,
				Hash:   hash,
			},
		},
	})

	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	path := "/tmp/hash_type_test.txt"
	if _, err := os.Stat("/mnt/host"); err == nil {
		path = "/mnt/host/tmp/hash_type_test.txt"
	}
	os.Remove(path)
}

// --- HandlePut: two-phase write via gomonkey patching translatePathForContainer ---

func TestHandlePut_TwoPhaseWriteMonkey(t *testing.T) {
	// Patch translatePathForContainer to simulate container environment
	// by returning a different path than the cleaned input.
	dstDir := t.TempDir()

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(translatePathForContainer, func(path string) string {
		return filepath.Join(dstDir, filepath.Clean(path))
	})

	content := []byte("two-phase monkey test content")
	hasher := md5.New()
	hasher.Write(content)
	expectedHash := hasher.Sum(nil)

	stream := newMockPutStream()
	stream.addOpenRequest("/tmp/monkey_twophase.txt", 0644)
	stream.addContentRequest(content)
	stream.addHashRequest(expectedHash)

	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	// File should be at the translated destination
	finalPath := filepath.Join(dstDir, "tmp", "monkey_twophase.txt")
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read final path: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content = %q, want %q", data, content)
	}

	// Staging temp file should have been removed
	if _, err := os.Stat("/tmp/monkey_twophase.txt.tmp"); !os.IsNotExist(err) {
		t.Error("staging temp file was not cleaned up")
		os.Remove("/tmp/monkey_twophase.txt.tmp")
	}

	// Also clean up any file at the original path
	os.Remove("/tmp/monkey_twophase.txt")
}

// --- HandlePut: two-phase write with copyFile error ---

func TestHandlePut_TwoPhaseWriteCopyError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Make translatePathForContainer return an unwritable destination
	patches.ApplyFunc(translatePathForContainer, func(path string) string {
		return "/proc/fake_destination" + filepath.Clean(path)
	})

	content := []byte("copy error test")
	hasher := md5.New()
	hasher.Write(content)
	expectedHash := hasher.Sum(nil)

	stream := newMockPutStream()
	stream.addOpenRequest("/tmp/copy_error.txt", 0644)
	stream.addContentRequest(content)
	stream.addHashRequest(expectedHash)

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("expected error when copyFile fails")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}

	// Cleanup staging
	os.Remove("/tmp/copy_error.txt.tmp")
	os.Remove("/tmp/copy_error.txt")
}
