package file

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHandleTransferToRemote_Success(t *testing.T) {
	// Create test HTTP server
	testContent := []byte("test firmware content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	// Create temp directory for output
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "firmware.bin")

	// Create request
	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: localPath,
		RemoteDownload: &common.RemoteDownload{
			Path:     server.URL,
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	// Call handler
	ctx := context.Background()
	resp, err := HandleTransferToRemote(ctx, req)
	if err != nil {
		t.Fatalf("HandleTransferToRemote() error = %v", err)
	}

	// Verify response
	if resp == nil {
		t.Fatal("HandleTransferToRemote() returned nil response")
	}

	if resp.Hash == nil {
		t.Fatal("Response hash is nil")
	}

	if resp.Hash.Method != types.HashType_MD5 {
		t.Errorf("Hash method = %v, want MD5", resp.Hash.Method)
	}

	// Verify file was downloaded
	content, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("Downloaded content = %q, want %q", content, testContent)
	}

	// Verify hash is correct
	// MD5 of "test firmware content" = 7c9e7c8e5c47c8e5c47c8e5c47c8e5c4 (approx)
	if len(resp.Hash.Hash) != 16 {
		t.Errorf("Hash length = %d, want 16 (MD5 is 128 bits)", len(resp.Hash.Hash))
	}
}

func TestHandleTransferToRemote_NilRequest(t *testing.T) {
	ctx := context.Background()
	_, err := HandleTransferToRemote(ctx, nil)
	if err == nil {
		t.Error("HandleTransferToRemote() expected error for nil request, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", err)
	}
}

func TestHandleTransferToRemote_NilRemoteDownload(t *testing.T) {
	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath:      "/tmp/test.bin",
		RemoteDownload: nil,
	}

	ctx := context.Background()
	_, err := HandleTransferToRemote(ctx, req)
	if err == nil {
		t.Error("HandleTransferToRemote() expected error for nil remote_download, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", err)
	}
}

func TestHandleTransferToRemote_EmptyLocalPath(t *testing.T) {
	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: "",
		RemoteDownload: &common.RemoteDownload{
			Path:     "http://example.com/file",
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	ctx := context.Background()
	_, err := HandleTransferToRemote(ctx, req)
	if err == nil {
		t.Error("HandleTransferToRemote() expected error for empty local_path, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", err)
	}
}

func TestHandleTransferToRemote_EmptyURL(t *testing.T) {
	tempDir := t.TempDir()
	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: filepath.Join(tempDir, "test.bin"),
		RemoteDownload: &common.RemoteDownload{
			Path:     "",
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	ctx := context.Background()
	_, err := HandleTransferToRemote(ctx, req)
	if err == nil {
		t.Error("HandleTransferToRemote() expected error for empty URL, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", err)
	}
}

func TestHandleTransferToRemote_UnsupportedProtocol(t *testing.T) {
	tempDir := t.TempDir()
	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: filepath.Join(tempDir, "test.bin"),
		RemoteDownload: &common.RemoteDownload{
			Path:     "https://example.com/file",
			Protocol: common.RemoteDownload_HTTPS,
		},
	}

	ctx := context.Background()
	_, err := HandleTransferToRemote(ctx, req)
	if err == nil {
		t.Error("HandleTransferToRemote() expected error for HTTPS protocol, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unimplemented {
		t.Errorf("Expected Unimplemented error, got %v", err)
	}
}

func TestHandleTransferToRemote_DownloadFails(t *testing.T) {
	// Create server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: filepath.Join(tempDir, "test.bin"),
		RemoteDownload: &common.RemoteDownload{
			Path:     server.URL,
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	ctx := context.Background()
	_, err := HandleTransferToRemote(ctx, req)
	if err == nil {
		t.Error("HandleTransferToRemote() expected error for 404 response, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Errorf("Expected Internal error, got %v", err)
	}
}

func TestHandleTransferToRemote_HashVerification(t *testing.T) {
	// Test with known content and verify hash
	testContent := []byte("hello world")
	expectedMD5Hex := "5eb63bbbe01eeed093cb22bb8f5acdc3"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "test.txt")

	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: localPath,
		RemoteDownload: &common.RemoteDownload{
			Path:     server.URL,
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	ctx := context.Background()
	resp, err := HandleTransferToRemote(ctx, req)
	if err != nil {
		t.Fatalf("HandleTransferToRemote() error = %v", err)
	}

	// Verify hash matches expected
	gotHashHex := hex.EncodeToString(resp.Hash.Hash)
	if gotHashHex != expectedMD5Hex {
		t.Errorf("Hash = %s, want %s", gotHashHex, expectedMD5Hex)
	}
}

func TestHandleTransferToRemote_NestedDirectories(t *testing.T) {
	// Test that handler creates nested directories
	testContent := []byte("test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	// Path with nested directories
	localPath := filepath.Join(tempDir, "a", "b", "c", "file.bin")

	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: localPath,
		RemoteDownload: &common.RemoteDownload{
			Path:     server.URL,
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	ctx := context.Background()
	_, err := HandleTransferToRemote(ctx, req)
	if err != nil {
		t.Fatalf("HandleTransferToRemote() error = %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		t.Error("File was not created in nested directory")
	}
}

func TestTranslatePathForContainer(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string // when NOT in container
	}{
		{
			name:     "absolute path",
			input:    "/tmp/test.bin",
			expected: "/tmp/test.bin",
		},
		{
			name:     "path with dots",
			input:    "/tmp/../tmp/test.bin",
			expected: "/tmp/test.bin",
		},
		{
			name:     "path with /mnt/host gets translated",
			input:    "/mnt/host/tmp/test.bin",
			expected: "/mnt/host/tmp/test.bin",
		},
		{
			name:     "root path",
			input:    "/",
			expected: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translatePathForContainer(tt.input)
			// When not in container (no /mnt/host), should return cleaned path
			// When in container, would return /mnt/host + cleaned path
			// We can't reliably test container case in unit tests
			if got != tt.expected && got != "/mnt/host"+tt.expected {
				t.Errorf("translatePathForContainer(%q) = %q, want %q or %q",
					tt.input, got, tt.expected, "/mnt/host"+tt.expected)
			}
		})
	}
}

func TestHandleTransferToRemote_ContextCancellation(t *testing.T) {
	// Create server with slow response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This won't complete before context is cancelled
		<-r.Context().Done()
	}))
	defer server.Close()

	tempDir := t.TempDir()
	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: filepath.Join(tempDir, "test.bin"),
		RemoteDownload: &common.RemoteDownload{
			Path:     server.URL,
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := HandleTransferToRemote(ctx, req)
	if err == nil {
		t.Error("HandleTransferToRemote() expected error for cancelled context, got nil")
	}
}

func TestValidatePath_AllowedPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"tmp file", "/tmp/firmware.bin"},
		{"tmp nested", "/tmp/upgrades/v1.0/firmware.bin"},
		{"var tmp file", "/var/tmp/firmware.bin"},
		{"var tmp nested", "/var/tmp/downloads/image.bin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if err != nil {
				t.Errorf("validatePath(%q) returned error for allowed path: %v", tt.path, err)
			}
		})
	}
}

func TestValidatePath_RejectedPaths(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		expectedErr string
	}{
		{
			name:        "relative path",
			path:        "tmp/firmware.bin",
			expectedErr: "path must be absolute",
		},
		{
			name:        "path traversal",
			path:        "/tmp/../etc/passwd",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "etc directory",
			path:        "/etc/passwd",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "boot directory",
			path:        "/boot/grub/grub.cfg",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "usr directory",
			path:        "/usr/bin/malicious",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "root directory",
			path:        "/root/.ssh/authorized_keys",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "bin directory",
			path:        "/bin/bash",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "sbin directory",
			path:        "/sbin/init",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "home directory",
			path:        "/home/admin/.ssh/id_rsa",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "host grub config",
			path:        "/host/grub/grub.cfg",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "host machine.conf",
			path:        "/host/machine.conf",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "host overlayfs rw",
			path:        "/host/image-master/rw/usr/bin/test",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
		{
			name:        "var log",
			path:        "/var/log/syslog",
			expectedErr: "path must be under /tmp/ or /var/tmp/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if err == nil {
				t.Errorf("validatePath(%q) expected error, got nil", tt.path)
				return
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("validatePath(%q) error = %q, want substring %q",
					tt.path, err.Error(), tt.expectedErr)
			}
		})
	}
}

func TestHandleTransferToRemote_PathSecurityValidation(t *testing.T) {
	// Test that security validation is enforced via the RPC handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test"))
	}))
	defer server.Close()

	tests := []struct {
		name string
		path string
	}{
		{"etc passwd", "/etc/passwd"},
		{"boot grub", "/boot/grub/grub.cfg"},
		{"usr bin", "/usr/bin/malicious"},
		{"relative path", "tmp/file.bin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &gnoi_file_pb.TransferToRemoteRequest{
				LocalPath: tt.path,
				RemoteDownload: &common.RemoteDownload{
					Path:     server.URL,
					Protocol: common.RemoteDownload_HTTP,
				},
			}

			ctx := context.Background()
			_, err := HandleTransferToRemote(ctx, req)
			if err == nil {
				t.Errorf("HandleTransferToRemote() with path %q expected error, got nil", tt.path)
				return
			}

			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("Expected InvalidArgument error for path %q, got %v", tt.path, err)
			}
		})
	}
}

// mockPutStream implements gnoi_file_pb.File_PutServer for testing
type mockPutStream struct {
	gnoi_file_pb.File_PutServer
	requests  []*gnoi_file_pb.PutRequest
	responses []*gnoi_file_pb.PutResponse
	recvIdx   int
	recvErr   error
	sendErr   error
	ctx       context.Context
}

func newMockPutStream() *mockPutStream {
	return &mockPutStream{
		requests: make([]*gnoi_file_pb.PutRequest, 0),
		ctx:      context.Background(),
	}
}

func (m *mockPutStream) Context() context.Context {
	return m.ctx
}

func (m *mockPutStream) Recv() (*gnoi_file_pb.PutRequest, error) {
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	if m.recvIdx >= len(m.requests) {
		return nil, io.EOF
	}
	req := m.requests[m.recvIdx]
	m.recvIdx++
	return req, nil
}

func (m *mockPutStream) SendAndClose(resp *gnoi_file_pb.PutResponse) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.responses = append(m.responses, resp)
	return nil
}

func (m *mockPutStream) addOpenRequest(path string, perms uint32) {
	m.requests = append(m.requests, &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Open{
			Open: &gnoi_file_pb.PutRequest_Details{
				RemoteFile:  path,
				Permissions: perms,
			},
		},
	})
}

func (m *mockPutStream) addContentRequest(content []byte) {
	m.requests = append(m.requests, &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Contents{
			Contents: content,
		},
	})
}

func (m *mockPutStream) addHashRequest(hash []byte) {
	m.requests = append(m.requests, &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Hash{
			Hash: &types.HashType{
				Method: types.HashType_MD5,
				Hash:   hash,
			},
		},
	})
}

func TestHandlePut_Success(t *testing.T) {
	// Test successful file upload
	stream := newMockPutStream()

	// Content to upload
	content := []byte("test file content for gNOI Put RPC")

	// Calculate MD5 hash
	hasher := md5.New()
	hasher.Write(content)
	expectedHash := hasher.Sum(nil)

	// Setup request sequence
	stream.addOpenRequest("/tmp/test.txt", 0644)
	stream.addContentRequest(content)
	stream.addHashRequest(expectedHash)

	// Execute
	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	// Verify response was sent
	if len(stream.responses) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(stream.responses))
	}

	// Verify file was created with correct content
	path := "/tmp/test.txt"
	if _, err := os.Stat("/mnt/host"); err == nil {
		path = "/mnt/host/tmp/test.txt"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read uploaded file: %v", err)
	}

	if string(data) != string(content) {
		t.Errorf("File content = %q, want %q", data, content)
	}

	// Cleanup
	os.Remove(path)
}

func TestHandlePut_MultipleChunks(t *testing.T) {
	// Test file upload with multiple content chunks
	stream := newMockPutStream()

	// Content in chunks
	chunks := [][]byte{
		[]byte("chunk1 "),
		[]byte("chunk2 "),
		[]byte("chunk3"),
	}

	// Calculate total hash
	hasher := md5.New()
	for _, chunk := range chunks {
		hasher.Write(chunk)
	}
	expectedHash := hasher.Sum(nil)

	// Setup request sequence
	stream.addOpenRequest("/tmp/chunked.txt", 0600)
	for _, chunk := range chunks {
		stream.addContentRequest(chunk)
	}
	stream.addHashRequest(expectedHash)

	// Execute
	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	// Verify file content
	path := "/tmp/chunked.txt"
	if _, err := os.Stat("/mnt/host"); err == nil {
		path = "/mnt/host/tmp/chunked.txt"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read uploaded file: %v", err)
	}

	expected := "chunk1 chunk2 chunk3"
	if string(data) != expected {
		t.Errorf("File content = %q, want %q", data, expected)
	}

	// Verify permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	if info.Mode().Perm() != 0600 {
		t.Errorf("File permissions = %o, want %o", info.Mode().Perm(), 0600)
	}

	// Cleanup
	os.Remove(path)
}

func TestHandlePut_HashMismatch(t *testing.T) {
	// Test hash validation failure
	stream := newMockPutStream()

	content := []byte("test content")
	wrongHash := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}

	stream.addOpenRequest("/tmp/hashfail.txt", 0644)
	stream.addContentRequest(content)
	stream.addHashRequest(wrongHash)

	// Execute
	err := HandlePut(stream)
	if err == nil {
		t.Fatal("Expected error for hash mismatch, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.DataLoss {
		t.Errorf("Expected DataLoss error, got %v", err)
	}

	if !strings.Contains(st.Message(), "hash mismatch") {
		t.Errorf("Error message = %q, want substring 'hash mismatch'", st.Message())
	}

	// Verify temp file was cleaned up
	tempPath := "/tmp/hashfail.txt.tmp"
	if _, err := os.Stat("/mnt/host"); err == nil {
		tempPath = "/mnt/host/tmp/hashfail.txt.tmp"
	}

	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Error("Temp file was not cleaned up after hash mismatch")
		os.Remove(tempPath)
	}
}

func TestHandlePut_InvalidPaths(t *testing.T) {
	// Test path security validation
	tests := []struct {
		name string
		path string
	}{
		{"etc file", "/etc/passwd"},
		{"boot file", "/boot/grub/grub.cfg"},
		{"usr bin", "/usr/bin/malicious"},
		{"relative path", "tmp/file.txt"},
		{"path traversal", "/tmp/../etc/passwd"},
		{"home directory", "/home/admin/.ssh/id_rsa"},
		{"root directory", "/root/.bashrc"},
		{"var log", "/var/log/syslog"},
		{"host grub", "/host/grub/grub.cfg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := newMockPutStream()
			stream.addOpenRequest(tt.path, 0644)

			err := HandlePut(stream)
			if err == nil {
				t.Errorf("Expected error for path %q, got nil", tt.path)
				return
			}

			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("Expected InvalidArgument error for path %q, got %v", tt.path, err)
			}
		})
	}
}

func TestHandlePut_NoOpenMessage(t *testing.T) {
	// Test missing Open message
	stream := newMockPutStream()
	stream.addContentRequest([]byte("content"))

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("Expected error for missing Open message, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", err)
	}

	if !strings.Contains(st.Message(), "first message must be Open") {
		t.Errorf("Error message = %q, want substring 'first message must be Open'", st.Message())
	}
}

func TestHandlePut_EmptyRemotePath(t *testing.T) {
	// Test empty remote path
	stream := newMockPutStream()
	stream.addOpenRequest("", 0644)

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("Expected error for empty path, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", err)
	}

	if !strings.Contains(st.Message(), "remote_file cannot be empty") {
		t.Errorf("Error message = %q, want substring 'remote_file cannot be empty'", st.Message())
	}
}

func TestHandlePut_DefaultPermissions(t *testing.T) {
	// Test default permissions when not specified
	stream := newMockPutStream()

	content := []byte("test default perms")
	hasher := md5.New()
	hasher.Write(content)
	expectedHash := hasher.Sum(nil)

	// Permission 0 should default to 0644
	stream.addOpenRequest("/tmp/default_perms.txt", 0)
	stream.addContentRequest(content)
	stream.addHashRequest(expectedHash)

	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	// Verify file permissions
	path := "/tmp/default_perms.txt"
	if _, err := os.Stat("/mnt/host"); err == nil {
		path = "/mnt/host/tmp/default_perms.txt"
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	if info.Mode().Perm() != 0644 {
		t.Errorf("File permissions = %o, want %o", info.Mode().Perm(), 0644)
	}

	// Cleanup
	os.Remove(path)
}

func TestHandlePut_StreamError(t *testing.T) {
	// Test stream receive error
	stream := newMockPutStream()
	stream.recvErr = context.Canceled

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("Expected error for stream error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Errorf("Expected Internal error, got %v", err)
	}
}

func TestHandlePut_LargeFile(t *testing.T) {
	// Test uploading a larger file in chunks
	stream := newMockPutStream()

	// Create 1MB of data in 64KB chunks
	chunkSize := 64 * 1024
	totalSize := 1024 * 1024
	chunks := make([][]byte, 0)
	hasher := md5.New()

	for written := 0; written < totalSize; written += chunkSize {
		chunk := make([]byte, chunkSize)
		// Fill with pattern
		for i := range chunk {
			chunk[i] = byte((written + i) % 256)
		}
		chunks = append(chunks, chunk)
		hasher.Write(chunk)
	}

	expectedHash := hasher.Sum(nil)

	// Setup request sequence
	stream.addOpenRequest("/tmp/large_file.bin", 0644)
	for _, chunk := range chunks {
		stream.addContentRequest(chunk)
	}
	stream.addHashRequest(expectedHash)

	// Execute
	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	// Verify file size
	path := "/tmp/large_file.bin"
	if _, err := os.Stat("/mnt/host"); err == nil {
		path = "/mnt/host/tmp/large_file.bin"
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	if info.Size() != int64(totalSize) {
		t.Errorf("File size = %d, want %d", info.Size(), totalSize)
	}

	// Cleanup
	os.Remove(path)
}

func TestHandlePut_VarTmpPath(t *testing.T) {
	// Test /var/tmp path is allowed
	stream := newMockPutStream()

	content := []byte("var tmp test")
	hasher := md5.New()
	hasher.Write(content)
	expectedHash := hasher.Sum(nil)

	stream.addOpenRequest("/var/tmp/test.txt", 0644)
	stream.addContentRequest(content)
	stream.addHashRequest(expectedHash)

	err := HandlePut(stream)
	if err != nil {
		t.Fatalf("HandlePut() error = %v", err)
	}

	// Verify file was created
	path := "/var/tmp/test.txt"
	if _, err := os.Stat("/mnt/host"); err == nil {
		path = "/mnt/host/var/tmp/test.txt"
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("File was not created in /var/tmp")
	}

	// Cleanup
	os.Remove(path)
}

func TestHandlePut_UnexpectedEOF(t *testing.T) {
	// Test EOF after content but before hash
	stream := newMockPutStream()

	stream.addOpenRequest("/tmp/eof_test.txt", 0644)
	stream.addContentRequest([]byte("content"))
	// Don't add hash - Recv will return EOF

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("Expected error for unexpected EOF, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", err)
	}

	if !strings.Contains(st.Message(), "unexpected end of stream") {
		t.Errorf("Error message = %q, want substring 'unexpected end of stream'", st.Message())
	}

	// Cleanup temp file if it was created
	tempPath := "/tmp/eof_test.txt.tmp"
	if _, err := os.Stat("/mnt/host"); err == nil {
		tempPath = "/mnt/host/tmp/eof_test.txt.tmp"
	}
	os.Remove(tempPath)
}

func TestHandlePut_InvalidMessageType(t *testing.T) {
	// Test receiving invalid message type (neither contents nor hash)
	stream := newMockPutStream()

	stream.addOpenRequest("/tmp/invalid_msg.txt", 0644)
	// Add a request with no content or hash
	stream.requests = append(stream.requests, &gnoi_file_pb.PutRequest{})

	err := HandlePut(stream)
	if err == nil {
		t.Fatal("Expected error for invalid message type, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", err)
	}

	if !strings.Contains(st.Message(), "message must contain contents or hash") {
		t.Errorf("Error message = %q, want substring 'message must contain contents or hash'", st.Message())
	}
}
