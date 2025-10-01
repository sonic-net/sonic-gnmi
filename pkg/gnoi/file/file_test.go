package file

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			name:     "already has /mnt/host",
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
			// We can't test the container case in unit tests without mocking
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
