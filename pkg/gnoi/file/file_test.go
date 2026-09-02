package file

import (
	"bytes"
	"context"
	"crypto/md5"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/pkg/hostfs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func transferTestPath(t *testing.T, name string) (logical, physical string) {
	t.Helper()
	logicalDir, physicalDir := useTempHostRoot(t)
	return filepath.Join(logicalDir, name), filepath.Join(physicalDir, name)
}

func TestHandleTransferToRemotePrimaryWorkflow(t *testing.T) {
	payload := []byte("test firmware content")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	logical, physical := transferTestPath(t, filepath.Join("nested", "firmware.bin"))
	request := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: logical,
		RemoteDownload: &common.RemoteDownload{
			Path:     server.URL,
			Protocol: common.RemoteDownload_HTTP,
		},
	}
	response, err := HandleTransferToRemote(context.Background(), request)
	if err != nil {
		t.Fatalf("HandleTransferToRemote: %v", err)
	}
	wantHash := md5.Sum(payload)
	if response.Hash.Method != types.HashType_MD5 || !bytes.Equal(response.Hash.Hash, wantHash[:]) {
		t.Fatalf("response hash = %v, want MD5 %x", response.Hash, wantHash)
	}
	if got, err := os.ReadFile(physical); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("downloaded content = %q, err=%v", got, err)
	}
}

func TestHandleTransferToRemoteValidation(t *testing.T) {
	validDownload := &common.RemoteDownload{Path: "http://example.test/file", Protocol: common.RemoteDownload_HTTP}
	tests := []struct {
		name string
		req  *gnoi_file_pb.TransferToRemoteRequest
		code codes.Code
	}{
		{name: "nil request", code: codes.InvalidArgument},
		{name: "missing remote download", req: &gnoi_file_pb.TransferToRemoteRequest{LocalPath: "/tmp/file"}, code: codes.InvalidArgument},
		{name: "empty local path", req: &gnoi_file_pb.TransferToRemoteRequest{RemoteDownload: validDownload}, code: codes.InvalidArgument},
		{name: "empty URL", req: &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath:      "/tmp/file",
			RemoteDownload: &common.RemoteDownload{Protocol: common.RemoteDownload_HTTP},
		}, code: codes.InvalidArgument},
		{name: "unsupported protocol", req: &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath:      "/tmp/file",
			RemoteDownload: &common.RemoteDownload{Path: "https://example.test/file", Protocol: common.RemoteDownload_HTTPS},
		}, code: codes.Unimplemented},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := HandleTransferToRemote(context.Background(), test.req)
			if status.Code(err) != test.code {
				t.Fatalf("code = %v, want %v; err=%v", status.Code(err), test.code, err)
			}
		})
	}
}

func TestHandleTransferToRemoteSecurityBoundary(t *testing.T) {
	tests := []struct {
		name string
		path string
		code codes.Code
	}{
		{name: "persistent path", path: "/boot/image.bin", code: codes.InvalidArgument},
		{name: "traversal", path: "/tmp/a/../b", code: codes.InvalidArgument},
		{name: "DLDD inbox", path: dlddRulesInboxPath, code: codes.PermissionDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &gnoi_file_pb.TransferToRemoteRequest{
				LocalPath: test.path,
				RemoteDownload: &common.RemoteDownload{
					Path:     "http://example.test/file",
					Protocol: common.RemoteDownload_HTTP,
				},
			}
			_, err := HandleTransferToRemote(context.Background(), request)
			if status.Code(err) != test.code {
				t.Fatalf("code = %v, want %v; err=%v", status.Code(err), test.code, err)
			}
		})
	}
}

func TestHandleTransferToRemoteDownloadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	logical, _ := transferTestPath(t, "missing.bin")
	request := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: logical,
		RemoteDownload: &common.RemoteDownload{
			Path:     server.URL,
			Protocol: common.RemoteDownload_HTTP,
		},
	}
	if _, err := HandleTransferToRemote(context.Background(), request); status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal; err=%v", status.Code(err), err)
	}
}

func TestHandleTransferToRemoteDPURouting(t *testing.T) {
	var routedIndex string
	patch := gomonkey.ApplyFunc(HandleTransferToRemoteForDPUStreaming,
		func(_ context.Context, _ *gnoi_file_pb.TransferToRemoteRequest, index string) (*gnoi_file_pb.TransferToRemoteResponse, error) {
			routedIndex = index
			return &gnoi_file_pb.TransferToRemoteResponse{}, nil
		})
	defer patch.Reset()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
		"x-sonic-ss-target-type":  "dpu",
		"x-sonic-ss-target-index": "3",
	}))
	request := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath:      "/tmp/file",
		RemoteDownload: &common.RemoteDownload{Path: "http://example.test/file", Protocol: common.RemoteDownload_HTTP},
	}
	if _, err := HandleTransferToRemote(ctx, request); err != nil || routedIndex != "3" {
		t.Fatalf("DPU route index=%q err=%v, want 3/nil", routedIndex, err)
	}
}

type mockPutStream struct {
	gnoi_file_pb.File_PutServer
	requests  []*gnoi_file_pb.PutRequest
	responses []*gnoi_file_pb.PutResponse
	recvIndex int
}

func newMockPutStream() *mockPutStream {
	return &mockPutStream{}
}

func (stream *mockPutStream) Context() context.Context {
	return context.Background()
}

func (stream *mockPutStream) Recv() (*gnoi_file_pb.PutRequest, error) {
	if stream.recvIndex == len(stream.requests) {
		return nil, io.EOF
	}
	request := stream.requests[stream.recvIndex]
	stream.recvIndex++
	return request, nil
}

func (stream *mockPutStream) SendAndClose(response *gnoi_file_pb.PutResponse) error {
	stream.responses = append(stream.responses, response)
	return nil
}

func (stream *mockPutStream) addOpen(path string, permissions uint32) {
	stream.requests = append(stream.requests, &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Open{Open: &gnoi_file_pb.PutRequest_Details{
			RemoteFile: path, Permissions: permissions,
		}},
	})
}

func (stream *mockPutStream) addContents(content []byte) {
	stream.requests = append(stream.requests, &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Contents{Contents: content},
	})
}

func (stream *mockPutStream) addHash(method types.HashType_HashMethod, digest []byte) {
	stream.requests = append(stream.requests, &gnoi_file_pb.PutRequest{
		Request: &gnoi_file_pb.PutRequest_Hash{Hash: &types.HashType{Method: method, Hash: digest}},
	})
}

func successfulPut(path string, permissions uint32, chunks ...[]byte) *mockPutStream {
	stream := newMockPutStream()
	stream.addOpen(path, permissions)
	for _, chunk := range chunks {
		stream.addContents(chunk)
	}
	digest := md5.Sum(bytes.Join(chunks, nil))
	stream.addHash(types.HashType_MD5, digest[:])
	return stream
}

func TestHandlePutPrimaryStagingRoots(t *testing.T) {
	tests := []string{
		"/tmp/primary.bin",
		"/var/tmp/primary.bin",
		"/host/vendor-anything/rw/primary.bin",
	}
	for _, logical := range tests {
		t.Run(logical, func(t *testing.T) {
			root := setTempHostRoot(t)
			chunks := [][]byte{[]byte("first-"), []byte("second")}
			stream := successfulPut(logical, 0, chunks...)
			if err := HandlePut(stream); err != nil {
				t.Fatalf("HandlePut: %v", err)
			}
			if len(stream.responses) != 1 {
				t.Fatalf("got %d responses, want 1", len(stream.responses))
			}
			destination := root + logical
			got, err := os.ReadFile(destination)
			if err != nil || !bytes.Equal(got, bytes.Join(chunks, nil)) {
				t.Fatalf("uploaded content = %q, err=%v", got, err)
			}
			if info, err := os.Stat(destination); err != nil || info.Mode().Perm() != 0644 {
				t.Fatalf("uploaded mode = %v, err=%v; want 0644", info, err)
			}
		})
	}
}

func TestFileWritePolicyDLDDExceptionIsLocal(t *testing.T) {
	if err := fileWritePolicy.Validate(dlddRulesInboxPath); err != nil {
		t.Fatalf("File policy rejected exact DLDD inbox: %v", err)
	}
	if err := hostfs.Validate(dlddRulesInboxPath); err == nil {
		t.Fatal("global hostfs policy unexpectedly allows the DLDD inbox")
	}
}

func TestHandlePutDLDDRulesInbox(t *testing.T) {
	root := setTempHostRoot(t)
	payload := []byte("schema_version: 0.0.1\nsignatures: []\n")
	stream := successfulPut(dlddRulesInboxPath, 0777, payload)
	if err := HandlePut(stream); err != nil {
		t.Fatalf("HandlePut: %v", err)
	}
	destination := root + dlddRulesInboxPath
	if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("staged rules = %q, err=%v", got, err)
	}
	if info, err := os.Stat(destination); err != nil || info.Mode().Perm() != dlddRulesInboxMode {
		t.Fatalf("rules mode = %v, err=%v; want %o", info, err, dlddRulesInboxMode)
	}
	parent := filepath.Dir(destination)
	if info, err := os.Stat(parent); err != nil || info.Mode().Perm() != 0750 {
		t.Fatalf("inbox mode = %v, err=%v; want 0750", info, err)
	}
}

func TestHandlePutRejectsUnauthorizedPaths(t *testing.T) {
	paths := []string{
		"/boot/file", "/usr/file", "/root/file", "/home/file", "/var/log/file", "/TMP/file",
		"/etc/file", "/tmp2/file", "/tmp/a/../b", "../tmp/file", "/tmp/bad\x00name",
		filepath.Dir(dlddRulesInboxPath) + "/other.yaml",
		dlddRulesInboxPath + ".bak",
		"/var/lib/sonic/dldd/rules/dld_rules.active.yaml",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			root := setTempHostRoot(t)
			stream := newMockPutStream()
			stream.addOpen(path, 0644)
			err := HandlePut(stream)
			if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "invalid remote_file") {
				t.Fatalf("code=%v err=%v, want policy InvalidArgument diagnostic", status.Code(err), err)
			}
			entries, readErr := os.ReadDir(root)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("rejected path created filesystem state: entries=%v err=%v", entries, readErr)
			}
		})
	}
}

func assertEmptyUploadDirectory(t *testing.T, root, logical string) {
	t.Helper()
	destination := root + logical
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination was published: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil {
		t.Fatalf("read upload directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("upload left temporary files: %v", entries)
	}
}

func TestHandlePutSizeLimitsAndCleanup(t *testing.T) {
	t.Run("total size", func(t *testing.T) {
		root := setTempHostRoot(t)
		previous := maxFileSize
		maxFileSize = 5
		t.Cleanup(func() { maxFileSize = previous })
		stream := newMockPutStream()
		stream.addOpen("/tmp/large.bin", 0600)
		stream.addContents([]byte("1234"))
		stream.addContents([]byte("56"))
		if err := HandlePut(stream); status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("code=%v, want ResourceExhausted; err=%v", status.Code(err), err)
		}
		assertEmptyUploadDirectory(t, root, "/tmp/large.bin")
	})
	t.Run("chunk size", func(t *testing.T) {
		root := setTempHostRoot(t)
		stream := newMockPutStream()
		stream.addOpen("/tmp/large-chunk.bin", 0600)
		stream.addContents(make([]byte, maxPutChunkSize+1))
		if err := HandlePut(stream); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code=%v, want InvalidArgument; err=%v", status.Code(err), err)
		}
		assertEmptyUploadDirectory(t, root, "/tmp/large-chunk.bin")
	})
}

func TestHandlePutProtocolAndIntegrityFailures(t *testing.T) {
	t.Run("open must be first", func(t *testing.T) {
		stream := newMockPutStream()
		stream.addContents([]byte("content"))
		if err := HandlePut(stream); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code=%v, want InvalidArgument; err=%v", status.Code(err), err)
		}
	})
	t.Run("hash is required", func(t *testing.T) {
		root := setTempHostRoot(t)
		stream := newMockPutStream()
		stream.addOpen("/tmp/no-hash", 0644)
		stream.addContents([]byte("content"))
		if err := HandlePut(stream); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code=%v, want InvalidArgument; err=%v", status.Code(err), err)
		}
		assertEmptyUploadDirectory(t, root, "/tmp/no-hash")
	})
	t.Run("hash method", func(t *testing.T) {
		root := setTempHostRoot(t)
		stream := newMockPutStream()
		stream.addOpen("/tmp/hash-method", 0644)
		stream.addContents([]byte("content"))
		stream.addHash(types.HashType_SHA256, make([]byte, 32))
		if err := HandlePut(stream); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code=%v, want InvalidArgument; err=%v", status.Code(err), err)
		}
		assertEmptyUploadDirectory(t, root, "/tmp/hash-method")
	})
	t.Run("hash mismatch", func(t *testing.T) {
		root := setTempHostRoot(t)
		destination := root + "/tmp/hash-mismatch"
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte("original"), 0644); err != nil {
			t.Fatal(err)
		}
		stream := newMockPutStream()
		stream.addOpen("/tmp/hash-mismatch", 0644)
		stream.addContents([]byte("content"))
		stream.addHash(types.HashType_MD5, make([]byte, md5.Size))
		if err := HandlePut(stream); status.Code(err) != codes.DataLoss {
			t.Fatalf("code=%v, want DataLoss; err=%v", status.Code(err), err)
		}
		if got, err := os.ReadFile(destination); err != nil || string(got) != "original" {
			t.Fatalf("failed upload changed destination: content=%q err=%v", got, err)
		}
		entries, err := os.ReadDir(filepath.Dir(destination))
		if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(destination) {
			t.Fatalf("failed upload left temporary files: %v, err=%v", entries, err)
		}
	})
}

func TestHandlePutConcurrentAtomicReplacement(t *testing.T) {
	root := setTempHostRoot(t)
	const uploads = 4
	payloads := make([][]byte, uploads)
	results := make(chan error, uploads)
	start := make(chan struct{})
	for index := range payloads {
		payload := bytes.Repeat([]byte{byte('A' + index)}, 16*1024)
		payloads[index] = payload
		stream := successfulPut(dlddRulesInboxPath, dlddRulesInboxMode, payload)
		go func() {
			<-start
			results <- HandlePut(stream)
		}()
	}
	close(start)
	for range payloads {
		if err := <-results; err != nil {
			t.Fatalf("concurrent HandlePut: %v", err)
		}
	}
	destination := root + dlddRulesInboxPath
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	matchesPayload := false
	for _, payload := range payloads {
		matchesPayload = matchesPayload || bytes.Equal(got, payload)
	}
	if !matchesPayload {
		t.Fatal("concurrent uploads produced interleaved or truncated content")
	}
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(destination) {
		t.Fatalf("atomic upload left unexpected files: %v, err=%v", entries, err)
	}
}

func TestHandlePutDoesNotFollowLegacyTempSymlink(t *testing.T) {
	root := setTempHostRoot(t)
	destination := root + dlddRulesInboxPath
	if err := os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, destination+".tmp"); err != nil {
		t.Fatal(err)
	}
	payload := []byte("schema_version: 0.0.1\n")
	if err := HandlePut(successfulPut(dlddRulesInboxPath, 0640, payload)); err != nil {
		t.Fatalf("HandlePut: %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "unchanged" {
		t.Fatalf("symlink target changed: content=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("destination content=%q err=%v", got, err)
	}
}
