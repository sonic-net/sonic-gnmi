package gnmi

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_common "github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	gnoifile "github.com/sonic-net/sonic-gnmi/pkg/gnoi/file"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// === Test Setup Helpers ===
func createFileServer(t *testing.T, port int) (*grpc.Server, string) {
	var listener net.Listener
	var err error

	if port == 0 {
		// Use dynamic port
		listener, err = net.Listen("tcp", ":0")
	} else {
		listener, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
	}
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	fileServer := &FileServer{
		Server: &Server{
			config: &Config{}, // Add config fields if required
		},
	}
	gnoi_file_pb.RegisterFileServer(s, fileServer)

	go func() {
		if err := s.Serve(listener); err != nil {
			t.Errorf("Failed to serve: %v", err)
		}
	}()

	return s, listener.Addr().String()
}

// === Actual Tests ===
func TestGnoiFileServer(t *testing.T) {
	s, addr := createFileServer(t, 0) // Use dynamic port
	defer s.Stop()

	//tlsConfig := &tls.Config{InsecureSkipVerify: true}
	//opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	conn, err := grpc.Dial(addr, opts...)
	if err != nil {
		t.Fatalf("Failed to dial server: %v", err)
	}
	defer conn.Close()

	client := gnoi_file_pb.NewFileClient(conn)

	// Behavior coverage for HandleStat lives in pkg/gnoi/file/stat_test.go.
	// The gnmi_server tests below only verify the server wiring: that the
	// authenticate hook fires before the handler, and that handler errors
	// surface as gRPC status codes through the server stack.
	t.Run("Stat Fails with Auth Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, status.Error(codes.Unauthenticated, "unauth"))
		defer patch.Reset()

		req := &gnoi_file_pb.StatRequest{Path: "/tmp/test.txt"}
		_, err := client.Stat(context.Background(), req)
		if err == nil || status.Code(err) != codes.Unauthenticated {
			t.Fatalf("Expected unauthenticated error, got: %v", err)
		}
	})

	t.Run("Stat Delegates to Handler", func(t *testing.T) {
		// Smoke test: an authenticated request reaches HandleStat and a
		// handler-level error (empty path -> InvalidArgument) propagates
		// through the server stack as the matching gRPC status code.
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, nil)
		defer patch.Reset()

		req := &gnoi_file_pb.StatRequest{Path: ""}
		_, err := client.Stat(context.Background(), req)
		if err == nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("Expected InvalidArgument from handler, got: %v", err)
		}
	})

	t.Run("Put Fails with Auth Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, status.Error(codes.Unauthenticated, "unauthenticated"))
		defer patch.Reset()

		putStream, err := client.Put(context.Background())
		if err != nil {
			t.Fatalf("Failed to create Put stream: %v", err)
		}

		_, err = putStream.CloseAndRecv()
		// This is expected because authentication fails before stream is fully established
		if err == nil || status.Code(err) != codes.Unauthenticated {
			t.Fatalf("Expected Unauthenticated error, got: %v", err)
		}
	})

	t.Run("TransferToRemote Fails with Unimplemented Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, nil)
		defer patch.Reset()

		req := &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath: "/tmp/test.txt",
			RemoteDownload: &gnoi_common.RemoteDownload{
				Path:     "https://example.com/file",
				Protocol: gnoi_common.RemoteDownload_HTTPS,
			},
		}
		_, err := client.TransferToRemote(context.Background(), req)
		if err == nil || status.Code(err) != codes.Unimplemented {
			t.Fatalf("Expected Unimplemented error, got: %v", err)
		}
	})

	t.Run("TransferToRemote DPU Success", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		// Mock authenticate to succeed
		patches.ApplyFuncReturn(authenticate, nil, nil)

		// Mock HandleTransferToRemoteForDPUStreaming to succeed
		patches.ApplyFunc(gnoifile.HandleTransferToRemoteForDPUStreaming,
			func(ctx context.Context, req *gnoi_file_pb.TransferToRemoteRequest, dpuIndex string) (*gnoi_file_pb.TransferToRemoteResponse, error) {
				return &gnoi_file_pb.TransferToRemoteResponse{}, nil
			})

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}

		// Create context with DPU metadata (lines 117, 120, 125-126)
		md := metadata.New(map[string]string{
			"x-sonic-ss-target-type":  "dpu",
			"x-sonic-ss-target-index": "0",
		})
		ctx := metadata.NewIncomingContext(context.Background(), md)

		req := &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath: "/tmp/test.txt",
		}

		resp, err := fs.TransferToRemote(ctx, req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("TransferToRemote NPU Success", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		// Mock authenticate to succeed
		patches.ApplyFuncReturn(authenticate, nil, nil)

		// Mock HandleTransferToRemote to succeed
		patches.ApplyFunc(gnoifile.HandleTransferToRemote,
			func(ctx context.Context, req *gnoi_file_pb.TransferToRemoteRequest) (*gnoi_file_pb.TransferToRemoteResponse, error) {
				return &gnoi_file_pb.TransferToRemoteResponse{}, nil
			})

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}

		ctx := context.Background() // No DPU metadata - should call regular function

		req := &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath: "/tmp/test.txt",
		}

		resp, err := fs.TransferToRemote(ctx, req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("TransferToRemote Fails with Auth Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, status.Error(codes.Unauthenticated, "unauthenticated"))
		defer patch.Reset()

		req := &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath: "/tmp/test.txt",
			RemoteDownload: &gnoi_common.RemoteDownload{
				Path:     "https://example.com/file",
				Protocol: gnoi_common.RemoteDownload_HTTPS,
			},
		}
		_, err := client.TransferToRemote(context.Background(), req)
		if err == nil || status.Code(err) != codes.Unauthenticated {
			t.Fatalf("Expected Unauthenticated error, got: %v", err)
		}
	})

	t.Run("Remove_Success", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		// Patch authenticate to succeed
		patches.ApplyFuncReturn(authenticate, nil, nil)

		// Patch NewDbusClient to return FakeClient
		patches.ApplyFuncReturn(ssc.NewDbusClient, &ssc.FakeClient{}, nil)

		// create a real temporary file so Remove has something to delete
		tmpf, err := os.CreateTemp("", "gnoi-remove-success-*.txt")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		tmpPath := tmpf.Name()
		if cerr := tmpf.Close(); cerr != nil {
			t.Fatalf("failed to close temp file: %v", cerr)
		}
		// ensure cleanup if the handler didn't remove it for any reason
		defer func() { _ = os.Remove(tmpPath) }()

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}
		req := &gnoi_file_pb.RemoveRequest{RemoteFile: tmpPath}
		resp, err := fs.Remove(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// verify file was actually removed
		if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
			t.Fatalf("expected file to be removed, stat error: %v", statErr)
		}
	})

	t.Run("Remove_Fails_NilRequest", func(t *testing.T) {
		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}
		_, err := fs.Remove(context.Background(), nil)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Remove_Fails_EmptyRemoteFile", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFuncReturn(authenticate, nil, nil)

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}
		req := &gnoi_file_pb.RemoveRequest{RemoteFile: ""}
		_, err := fs.Remove(context.Background(), req)

		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Remove_Fails_With_Auth_Error", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		// Simulate auth failure
		patches.ApplyFuncReturn(authenticate, nil, status.Error(codes.PermissionDenied, "unauthenticated"))

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}
		req := &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/test.txt"}
		_, err := fs.Remove(context.Background(), req)

		assert.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})

	t.Run("Remove_Fails_With_RemoveFile_Error", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		// Patch authenticate to succeed
		patches.ApplyFuncReturn(authenticate, nil, nil)

		// Make os.Remove return a simulated error so the handler reports it.
		patches.ApplyFuncReturn(os.Remove, fmt.Errorf("simulated failure"))

		// Patch NewDbusClient to return an erring client if needed by the handler branch later.
		patches.ApplyFuncReturn(ssc.NewDbusClient, &ssc.FakeClientWithError{}, nil)

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}
		req := &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/bad.txt"}
		_, err := fs.Remove(context.Background(), req)

		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Equal(t, "simulated failure", status.Convert(err).Message())
	})

	t.Run("Get_Fails_With_Auth_Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, status.Error(codes.Unauthenticated, "unauthenticated"))
		defer patch.Reset()

		stream, err := client.Get(context.Background(), &gnoi_file_pb.GetRequest{})
		if err == nil {
			// since Get returns Unimplemented after auth, this should be hit
			_, err = stream.Recv()
		}

		if err == nil || status.Code(err) != codes.Unauthenticated {
			t.Fatalf("Expected Unauthenticated error, got: %v", err)
		}
	})

	t.Run("Get_Fails_With_Unimplemented_Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, nil)
		defer patch.Reset()

		stream, err := client.Get(context.Background(), &gnoi_file_pb.GetRequest{})
		if err == nil {
			_, err = stream.Recv()
		}

		if err == nil || status.Code(err) != codes.Unimplemented {
			t.Fatalf("Expected Unimplemented error, got: %v", err)
		}
	})

	// Test Put function success path (line 143)
	t.Run("Put_Success", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFuncReturn(authenticate, nil, nil)

		// Mock the gnoifile.HandlePut function to return success
		patches.ApplyFunc(gnoifile.HandlePut,
			func(stream gnoi_file_pb.File_PutServer) error {
				return nil
			})

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}

		// Create a mock stream
		mockStream := &mockPutStream{
			ctx: context.Background(),
		}

		err := fs.Put(mockStream)
		assert.NoError(t, err)
	})

}

// Mock stream for Put testing
type mockPutStream struct {
	ctx context.Context
}

func (m *mockPutStream) Context() context.Context {
	return m.ctx
}

func (m *mockPutStream) SendMsg(msg interface{}) error {
	return nil
}

func (m *mockPutStream) RecvMsg(msg interface{}) error {
	return nil
}

func (m *mockPutStream) Recv() (*gnoi_file_pb.PutRequest, error) {
	return nil, nil
}

func (m *mockPutStream) SendAndClose(resp *gnoi_file_pb.PutResponse) error {
	return nil
}

func (m *mockPutStream) SendHeader(metadata.MD) error {
	return nil
}

func (m *mockPutStream) SetTrailer(metadata.MD) {
}

func (m *mockPutStream) SetHeader(metadata.MD) error {
	return nil
}
