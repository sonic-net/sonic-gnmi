package gnmi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_common "github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// === Fake Dbus Client ===
type fakeClient struct{}

func (f *fakeClient) Close() error                         { return nil }
func (f *fakeClient) ConfigReload(fileName string) error   { return nil }
func (f *fakeClient) ConfigReplace(fileName string) error  { return nil }
func (f *fakeClient) ConfigSave(fileName string) error     { return nil }
func (f *fakeClient) ApplyPatchYang(fileName string) error { return nil }
func (f *fakeClient) ApplyPatchDb(fileName string) error   { return nil }
func (f *fakeClient) CreateCheckPoint(cpName string) error { return nil }
func (f *fakeClient) DeleteCheckPoint(cpName string) error { return nil }
func (f *fakeClient) StopService(service string) error     { return nil }
func (f *fakeClient) RestartService(service string) error  { return nil }
func (f *fakeClient) GetFileStat(path string) (map[string]string, error) {
	return map[string]string{
		"path":          path,
		"last_modified": "1686999999", // any valid Unix timestamp
		"permissions":   "644",
		"size":          "100",
		"umask":         "022",
	}, nil
}
func (f *fakeClient) DownloadFile(host, username, password, remotePath, localPath, protocol string) error {
	return nil
}
func (f *fakeClient) RemoveFile(path string) error                   { return nil }
func (f *fakeClient) DownloadImage(url string, save_as string) error { return nil }
func (f *fakeClient) InstallImage(where string) error                { return nil }
func (f *fakeClient) ListImages() (string, error)                    { return "image1", nil }
func (f *fakeClient) ActivateImage(image string) error               { return nil }
func (f *fakeClient) LoadDockerImage(image string) error             { return nil }

type fakeClientWithError struct {
	fakeClient
}

func (f *fakeClientWithError) RemoveFile(path string) error {
	return errors.New("simulated failure")
}

// === Test Setup Helpers ===
func createFileServer(t *testing.T, port int) *grpc.Server {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
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

	return s
}

// === Actual Tests ===
func TestGnoiFileServer(t *testing.T) {
	s := createFileServer(t, 8081)
	defer s.Stop()

	//tlsConfig := &tls.Config{InsecureSkipVerify: true}
	//opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	conn, err := grpc.Dial("127.0.0.1:8081", opts...)
	if err != nil {
		t.Fatalf("Failed to dial server: %v", err)
	}
	defer conn.Close()

	client := gnoi_file_pb.NewFileClient(conn)

	t.Run("Stat Success", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(authenticate, nil, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &fakeClient{}, nil)
		defer patch1.Reset()
		defer patch2.Reset()

		req := &gnoi_file_pb.StatRequest{Path: "/tmp/test.txt"}
		resp, err := client.Stat(context.Background(), req)
		if err != nil {
			t.Fatalf("Expected success, got error: %v", err)
		}
		if len(resp.GetStats()) == 0 || resp.Stats[0].Path != "/tmp/test.txt" {
			t.Fatalf("Unexpected Stat response: %+v", resp)
		}
	})

	t.Run("Stat Fails with Auth Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, status.Error(codes.Unauthenticated, "unauth"))
		defer patch.Reset()

		req := &gnoi_file_pb.StatRequest{Path: "/tmp/test.txt"}
		_, err := client.Stat(context.Background(), req)
		if err == nil || status.Code(err) != codes.Unauthenticated {
			t.Fatalf("Expected unauthenticated error, got: %v", err)
		}
	})

	t.Run("Stat Fails with Dbus Error", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(authenticate, nil, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, nil, fmt.Errorf("dbus failure"))
		defer patch1.Reset()
		defer patch2.Reset()

		req := &gnoi_file_pb.StatRequest{Path: "/tmp/test.txt"}
		_, err := client.Stat(context.Background(), req)
		if err == nil || status.Code(err) != codes.Internal {
			t.Fatalf("Expected internal error, got: %v", err)
		}
	})

	t.Run("Put Fails with Unimplemented Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(authenticate, nil, nil)
		defer patch.Reset()

		putStream, err := client.Put(context.Background())
		if err != nil {
			t.Fatalf("Failed to create Put stream: %v", err)
		}

		// Expect Unimplemented error on CloseAndRecv
		_, err = putStream.CloseAndRecv()
		if err == nil || status.Code(err) != codes.Unimplemented {
			t.Fatalf("Expected Unimplemented error, got: %v", err)
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

		// Patch NewDbusClient to return fakeClient
		patches.ApplyFuncReturn(ssc.NewDbusClient, &fakeClient{}, nil)

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}
		req := &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/test.txt"}
		resp, err := fs.Remove(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
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

		patches.ApplyFuncReturn(authenticate, nil, nil)

		// Patch NewDbusClient to return an erroring client
		patches.ApplyFuncReturn(ssc.NewDbusClient, &fakeClientWithError{}, nil)

		fs := &FileServer{
			Server: &Server{
				config: &Config{},
			},
		}
		req := &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/bad.txt"}
		_, err := fs.Remove(context.Background(), req)

		assert.Error(t, err)
		assert.Equal(t, "simulated failure", err.Error())
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

}
