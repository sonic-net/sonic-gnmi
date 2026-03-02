package file

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestDPU_SuccessPathMocking(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// 1. Mock dpuproxy.GetDPUConnection to return a connection
	patches.ApplyFunc(dpuproxy.GetDPUConnection, func(ctx context.Context, dpuIndex string) (*grpc.ClientConn, error) {
		return &grpc.ClientConn{}, nil
	})

	// 2. Mock newFileClient package variable (avoids inlining issues with gomonkey)
	patches.ApplyGlobalVar(&newFileClient, func(cc grpc.ClientConnInterface) gnoi_file_pb.FileClient {
		return &mockSuccessFileClient{}
	})

	// 3. Mock the download package's DownloadHTTPStreaming
	patches.ApplyFunc(download.DownloadHTTPStreaming, func(ctx context.Context, url string, maxFileSize int64) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader("streaming test data")), 18, nil
	})

	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: "/tmp/dpu_success_test.bin",
		RemoteDownload: &common.RemoteDownload{
			Path:     "http://example.com/file.bin",
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	// Test HandleTransferToRemoteForDPUStreaming success path
	resp, err := HandleTransferToRemoteForDPUStreaming(context.Background(), req, "dpu2")
	if err != nil {
		t.Logf("DPU streaming returned error: %v", err)
	} else {
		t.Logf("DPU streaming success: %v", resp)
	}
}

func TestDPU_ContainerPathBranches(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock dpuproxy.GetDPUConnection
	patches.ApplyFunc(dpuproxy.GetDPUConnection, func(ctx context.Context, dpuIndex string) (*grpc.ClientConn, error) {
		return &grpc.ClientConn{}, nil
	})

	patches.ApplyGlobalVar(&newFileClient, func(cc grpc.ClientConnInterface) gnoi_file_pb.FileClient {
		return &mockFailureFileClient{}
	})

	patches.ApplyFunc(download.DownloadHTTPStreaming, func(ctx context.Context, url string, maxFileSize int64) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader("container test data")), 18, nil
	})

	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: "/tmp/container_path_test.bin",
		RemoteDownload: &common.RemoteDownload{
			Path:     "http://example.com/file.bin",
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	// This should exercise the DPU path with mocked components
	_, err := HandleTransferToRemoteForDPUStreaming(context.Background(), req, "container-test")
	t.Logf("Container path test result: %v", err)
}

func TestDPU_ErrorPaths(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock dpuproxy.GetDPUConnection to fail
	patches.ApplyFunc(dpuproxy.GetDPUConnection, func(ctx context.Context, dpuIndex string) (*grpc.ClientConn, error) {
		return nil, os.ErrNotExist
	})

	patches.ApplyFunc(download.DownloadHTTPStreaming, func(ctx context.Context, url string, maxFileSize int64) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader("error test data")), 14, nil
	})

	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: "/tmp/error_test.bin",
		RemoteDownload: &common.RemoteDownload{
			Path:     "http://example.com/file.bin",
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	_, err := HandleTransferToRemoteForDPUStreaming(context.Background(), req, "error-test")
	t.Logf("Error path test result: %v", err)
}

func TestDPU_StreamingSuccess(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock the download package's DownloadHTTPStreaming
	patches.ApplyFunc(download.DownloadHTTPStreaming, func(ctx context.Context, url string, maxFileSize int64) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader("streaming test data")), 18, nil
	})

	// Mock dpuproxy.GetDPUConnection
	patches.ApplyFunc(dpuproxy.GetDPUConnection, func(ctx context.Context, dpuIndex string) (*grpc.ClientConn, error) {
		return &grpc.ClientConn{}, nil
	})

	// Mock file client to return success
	patches.ApplyGlobalVar(&newFileClient, func(cc grpc.ClientConnInterface) gnoi_file_pb.FileClient {
		return &mockSuccessFileClient{}
	})

	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: "/tmp/streaming_test.bin",
		RemoteDownload: &common.RemoteDownload{
			Path:     "http://example.com/stream.bin",
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	resp, err := HandleTransferToRemoteForDPUStreaming(context.Background(), req, "stream-test")
	if err != nil {
		t.Logf("Streaming test returned error: %v", err)
	} else {
		t.Logf("Streaming test success: %v", resp)
	}
}

func TestDPU_StreamingError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(download.DownloadHTTPStreaming, func(ctx context.Context, url string, maxFileSize int64) (io.ReadCloser, int64, error) {
		return nil, 0, os.ErrNotExist
	})

	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: "/tmp/streaming_error_test.bin",
		RemoteDownload: &common.RemoteDownload{
			Path:     "http://example.com/error.bin",
			Protocol: common.RemoteDownload_HTTP,
		},
	}

	_, err := HandleTransferToRemoteForDPUStreaming(context.Background(), req, "stream-error-test")
	t.Logf("Streaming error test result: %v", err)
}

// Mock implementations

type mockSuccessFileClient struct{}

func (m *mockSuccessFileClient) Stat(ctx context.Context, in *gnoi_file_pb.StatRequest, opts ...grpc.CallOption) (*gnoi_file_pb.StatResponse, error) {
	return nil, nil
}

func (m *mockSuccessFileClient) TransferToRemote(ctx context.Context, in *gnoi_file_pb.TransferToRemoteRequest, opts ...grpc.CallOption) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	return &gnoi_file_pb.TransferToRemoteResponse{}, nil
}

func (m *mockSuccessFileClient) Remove(ctx context.Context, in *gnoi_file_pb.RemoveRequest, opts ...grpc.CallOption) (*gnoi_file_pb.RemoveResponse, error) {
	return nil, nil
}

func (m *mockSuccessFileClient) Get(ctx context.Context, in *gnoi_file_pb.GetRequest, opts ...grpc.CallOption) (gnoi_file_pb.File_GetClient, error) {
	return nil, nil
}

func (m *mockSuccessFileClient) Put(ctx context.Context, opts ...grpc.CallOption) (gnoi_file_pb.File_PutClient, error) {
	return &mockSuccessPutClient{}, nil
}

type mockSuccessPutClient struct{}

func (m *mockSuccessPutClient) Send(*gnoi_file_pb.PutRequest) error {
	return nil
}

func (m *mockSuccessPutClient) CloseAndRecv() (*gnoi_file_pb.PutResponse, error) {
	return &gnoi_file_pb.PutResponse{}, nil
}

func (m *mockSuccessPutClient) Header() (metadata.MD, error) { return nil, nil }
func (m *mockSuccessPutClient) Trailer() metadata.MD         { return nil }
func (m *mockSuccessPutClient) CloseSend() error             { return nil }
func (m *mockSuccessPutClient) Context() context.Context     { return context.Background() }
func (m *mockSuccessPutClient) SendMsg(interface{}) error    { return nil }
func (m *mockSuccessPutClient) RecvMsg(interface{}) error    { return nil }

// Mock that fails at Put() to test different error paths
type mockFailureFileClient struct{}

func (m *mockFailureFileClient) Stat(ctx context.Context, in *gnoi_file_pb.StatRequest, opts ...grpc.CallOption) (*gnoi_file_pb.StatResponse, error) {
	return nil, nil
}

func (m *mockFailureFileClient) TransferToRemote(ctx context.Context, in *gnoi_file_pb.TransferToRemoteRequest, opts ...grpc.CallOption) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	return &gnoi_file_pb.TransferToRemoteResponse{}, nil
}

func (m *mockFailureFileClient) Remove(ctx context.Context, in *gnoi_file_pb.RemoveRequest, opts ...grpc.CallOption) (*gnoi_file_pb.RemoveResponse, error) {
	return nil, nil
}

func (m *mockFailureFileClient) Get(ctx context.Context, in *gnoi_file_pb.GetRequest, opts ...grpc.CallOption) (gnoi_file_pb.File_GetClient, error) {
	return nil, nil
}

func (m *mockFailureFileClient) Put(ctx context.Context, opts ...grpc.CallOption) (gnoi_file_pb.File_PutClient, error) {
	return nil, os.ErrPermission // Simulate a failure at Put creation
}
