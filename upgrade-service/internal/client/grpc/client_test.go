package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/client/config"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MockSystemInfoServer implements the SystemInfo service for testing.
type MockSystemInfoServer struct {
	pb.UnimplementedSystemInfoServer
	mock.Mock
}

func (m *MockSystemInfoServer) GetPlatformType(
	ctx context.Context, req *pb.GetPlatformTypeRequest,
) (*pb.GetPlatformTypeResponse, error) {
	args := m.Called(ctx, req)
	if resp, ok := args.Get(0).(*pb.GetPlatformTypeResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockSystemInfoServer) GetDiskSpace(
	ctx context.Context, req *pb.GetDiskSpaceRequest,
) (*pb.GetDiskSpaceResponse, error) {
	args := m.Called(ctx, req)
	if resp, ok := args.Get(0).(*pb.GetDiskSpaceResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

// MockFirmwareManagementServer implements the FirmwareManagement service for testing.
type MockFirmwareManagementServer struct {
	pb.UnimplementedFirmwareManagementServer
	mock.Mock
}

func (m *MockFirmwareManagementServer) DownloadFirmware(
	ctx context.Context, req *pb.DownloadFirmwareRequest,
) (*pb.DownloadFirmwareResponse, error) {
	args := m.Called(ctx, req)
	if resp, ok := args.Get(0).(*pb.DownloadFirmwareResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockFirmwareManagementServer) GetDownloadStatus(
	ctx context.Context, req *pb.GetDownloadStatusRequest,
) (*pb.GetDownloadStatusResponse, error) {
	args := m.Called(ctx, req)
	if resp, ok := args.Get(0).(*pb.GetDownloadStatusResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockFirmwareManagementServer) ListFirmwareImages(
	ctx context.Context, req *pb.ListFirmwareImagesRequest,
) (*pb.ListFirmwareImagesResponse, error) {
	args := m.Called(ctx, req)
	if resp, ok := args.Get(0).(*pb.ListFirmwareImagesResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockFirmwareManagementServer) CleanupOldFirmware(
	ctx context.Context, req *pb.CleanupOldFirmwareRequest,
) (*pb.CleanupOldFirmwareResponse, error) {
	args := m.Called(ctx, req)
	if resp, ok := args.Get(0).(*pb.CleanupOldFirmwareResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockFirmwareManagementServer) ConsolidateImages(
	ctx context.Context, req *pb.ConsolidateImagesRequest,
) (*pb.ConsolidateImagesResponse, error) {
	args := m.Called(ctx, req)
	if resp, ok := args.Get(0).(*pb.ConsolidateImagesResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockFirmwareManagementServer) ListImages(
	ctx context.Context, req *pb.ListImagesRequest,
) (*pb.ListImagesResponse, error) {
	args := m.Called(ctx, req)
	if resp, ok := args.Get(0).(*pb.ListImagesResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

// setupTestServer creates a test gRPC server with mock services.
func setupTestServer(
	t *testing.T, systemInfo *MockSystemInfoServer, firmwareMgmt *MockFirmwareManagementServer,
) (string, func()) {
	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	if systemInfo != nil {
		pb.RegisterSystemInfoServer(server, systemInfo)
	}
	if firmwareMgmt != nil {
		pb.RegisterFirmwareManagementServer(server, firmwareMgmt)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("Server exited: %v", err)
		}
	}()

	cleanup := func() {
		server.Stop()
	}

	return lis.Addr().String(), cleanup
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  *config.Config
		wantErr string
	}{
		{
			name: "valid config",
			config: &config.Config{
				Spec: config.Spec{
					Server: config.ServerSpec{
						Address:    "localhost:50051",
						TLSEnabled: boolPtr(false),
					},
					Download: config.DownloadSpec{
						ConnectTimeout: 1, // 1 second for fast test failure
					},
				},
			},
			wantErr: "failed to create gRPC connection", // Will fail to connect
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.config)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestClient_SystemInfoMethods(t *testing.T) {
	t.Run("GetPlatformType", func(t *testing.T) {
		// Setup mock server for this specific test
		mockSysInfo := &MockSystemInfoServer{}
		addr, cleanup := setupTestServer(t, mockSysInfo, nil)
		defer cleanup()

		// Create client
		cfg := &config.Config{
			Spec: config.Spec{
				Server: config.ServerSpec{
					Address:    addr,
					TLSEnabled: boolPtr(false),
				},
				Download: config.DownloadSpec{
					ConnectTimeout: 5, // 5 seconds
				},
			},
		}

		client, err := NewClient(cfg)
		require.NoError(t, err)
		defer client.Close()

		expectedResp := &pb.GetPlatformTypeResponse{
			PlatformIdentifier: "mellanox_sn4600",
		}

		mockSysInfo.On("GetPlatformType", mock.Anything, mock.Anything).Return(expectedResp, nil)

		resp, err := client.GetPlatformType(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, expectedResp.PlatformIdentifier, resp.PlatformIdentifier)

		mockSysInfo.AssertExpectations(t)
	})

	t.Run("GetDiskSpace", func(t *testing.T) {
		// Setup mock server for this specific test
		mockSysInfo := &MockSystemInfoServer{}
		addr, cleanup := setupTestServer(t, mockSysInfo, nil)
		defer cleanup()

		// Create client
		cfg := &config.Config{
			Spec: config.Spec{
				Server: config.ServerSpec{
					Address:    addr,
					TLSEnabled: boolPtr(false),
				},
				Download: config.DownloadSpec{
					ConnectTimeout: 5, // 5 seconds
				},
			},
		}

		client, err := NewClient(cfg)
		require.NoError(t, err)
		defer client.Close()

		expectedResp := &pb.GetDiskSpaceResponse{
			Filesystems: []*pb.GetDiskSpaceResponse_DiskSpaceInfo{
				{
					Path:    "/host",
					TotalMb: 1000,
					FreeMb:  500,
					UsedMb:  500,
				},
			},
		}

		mockSysInfo.On("GetDiskSpace", mock.Anything, mock.MatchedBy(func(req *pb.GetDiskSpaceRequest) bool {
			return len(req.Paths) == 1 && req.Paths[0] == "/host"
		})).Return(expectedResp, nil)

		resp, err := client.GetDiskSpace(context.Background(), []string{"/host"})
		assert.NoError(t, err)
		assert.Len(t, resp.Filesystems, 1)
		assert.Equal(t, "/host", resp.Filesystems[0].Path)

		mockSysInfo.AssertExpectations(t)
	})

	t.Run("GetPlatformType_Error", func(t *testing.T) {
		// Setup mock server for this specific test
		mockSysInfo := &MockSystemInfoServer{}
		addr, cleanup := setupTestServer(t, mockSysInfo, nil)
		defer cleanup()

		// Create client
		cfg := &config.Config{
			Spec: config.Spec{
				Server: config.ServerSpec{
					Address:    addr,
					TLSEnabled: boolPtr(false),
				},
				Download: config.DownloadSpec{
					ConnectTimeout: 5, // 5 seconds
				},
			},
		}

		client, err := NewClient(cfg)
		require.NoError(t, err)
		defer client.Close()

		mockSysInfo.On("GetPlatformType", mock.Anything, mock.Anything).
			Return(nil, status.Error(codes.Internal, "test error"))

		_, err = client.GetPlatformType(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "GetPlatformType failed")

		mockSysInfo.AssertExpectations(t)
	})
}

func TestClient_FirmwareManagementMethods(t *testing.T) {
	// Setup mock server
	mockFwMgmt := &MockFirmwareManagementServer{}
	addr, cleanup := setupTestServer(t, nil, mockFwMgmt)
	defer cleanup()

	// Create client config
	cfg := &config.Config{
		Spec: config.Spec{
			Server: config.ServerSpec{
				Address:    addr,
				TLSEnabled: boolPtr(false),
			},
			Download: config.DownloadSpec{
				ConnectTimeout: 5, // 5 seconds
			},
		},
	}

	client, err := NewClient(cfg)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	t.Run("DownloadFirmware", func(t *testing.T) {
		expectedResp := &pb.DownloadFirmwareResponse{
			SessionId:  "test-session-123",
			Status:     "Download started",
			OutputPath: "/host/firmware.bin",
		}

		mockFwMgmt.On("DownloadFirmware", mock.Anything, mock.MatchedBy(func(req *pb.DownloadFirmwareRequest) bool {
			return req.Url == "http://test.com/firmware.bin" && req.OutputPath == "/host/firmware.bin"
		})).Return(expectedResp, nil)

		opts := &DownloadOptions{
			ConnectTimeout: 30 * time.Second,
			TotalTimeout:   300 * time.Second,
			ExpectedMD5:    "abc123",
		}

		resp, err := client.DownloadFirmware(ctx, "http://test.com/firmware.bin", "/host/firmware.bin", opts)
		assert.NoError(t, err)
		assert.Equal(t, expectedResp.SessionId, resp.SessionId)

		mockFwMgmt.AssertExpectations(t)
	})

	t.Run("GetDownloadStatus_Progress", func(t *testing.T) {
		expectedResp := &pb.GetDownloadStatusResponse{
			SessionId: "test-session-123",
			State: &pb.GetDownloadStatusResponse_Progress{
				Progress: &pb.DownloadProgress{
					DownloadedBytes:  500,
					TotalBytes:       1000,
					SpeedBytesPerSec: 1024,
					Percentage:       50.0,
					CurrentMethod:    "direct",
					AttemptCount:     1,
					StartTime:        "2024-01-01T00:00:00Z",
					LastUpdate:       "2024-01-01T00:00:01Z",
				},
			},
		}

		mockFwMgmt.On("GetDownloadStatus", mock.Anything, mock.MatchedBy(func(req *pb.GetDownloadStatusRequest) bool {
			return req.SessionId == "test-session-123"
		})).Return(expectedResp, nil)

		resp, err := client.GetDownloadStatus(ctx, "test-session-123")
		assert.NoError(t, err)
		assert.Equal(t, expectedResp.SessionId, resp.SessionId)

		mockFwMgmt.AssertExpectations(t)
	})

	t.Run("ListFirmwareImages", func(t *testing.T) {
		expectedResp := &pb.ListFirmwareImagesResponse{
			Images: []*pb.FirmwareImageInfo{
				{
					FilePath:      "/host/sonic-firmware.bin",
					Version:       "202311.1",
					FullVersion:   "SONiC-OS-202311.1-build123",
					ImageType:     "onie",
					FileSizeBytes: 1024000,
				},
			},
		}

		mockFwMgmt.On("ListFirmwareImages", mock.Anything, mock.MatchedBy(func(req *pb.ListFirmwareImagesRequest) bool {
			return len(req.SearchDirectories) == 1 && req.SearchDirectories[0] == "/host"
		})).Return(expectedResp, nil)

		resp, err := client.ListFirmwareImages(ctx, []string{"/host"}, "202311.*")
		assert.NoError(t, err)
		assert.Len(t, resp.Images, 1)
		assert.Equal(t, "202311.1", resp.Images[0].Version)

		mockFwMgmt.AssertExpectations(t)
	})

	t.Run("CleanupOldFirmware", func(t *testing.T) {
		expectedResp := &pb.CleanupOldFirmwareResponse{
			FilesDeleted:    3,
			DeletedFiles:    []string{"old1.bin", "old2.bin", "old3.bin"},
			SpaceFreedBytes: 3072000,
		}

		mockFwMgmt.On("CleanupOldFirmware", mock.Anything, mock.Anything).Return(expectedResp, nil)

		resp, err := client.CleanupOldFirmware(ctx)
		assert.NoError(t, err)
		assert.Equal(t, int32(3), resp.FilesDeleted)
		assert.Equal(t, int64(3072000), resp.SpaceFreedBytes)

		mockFwMgmt.AssertExpectations(t)
	})

	t.Run("ConsolidateImages", func(t *testing.T) {
		expectedResp := &pb.ConsolidateImagesResponse{
			CurrentImage:    "SONiC-OS-202311.1",
			RemovedImages:   []string{"SONiC-OS-202305.1", "SONiC-OS-202211.1"},
			SpaceFreedBytes: 2048000,
			Executed:        false, // dry run
		}

		mockFwMgmt.On("ConsolidateImages", mock.Anything, mock.MatchedBy(func(req *pb.ConsolidateImagesRequest) bool {
			return req.DryRun == true
		})).Return(expectedResp, nil)

		resp, err := client.ConsolidateImages(ctx, true)
		assert.NoError(t, err)
		assert.Equal(t, "SONiC-OS-202311.1", resp.CurrentImage)
		assert.Len(t, resp.RemovedImages, 2)
		assert.False(t, resp.Executed)

		mockFwMgmt.AssertExpectations(t)
	})

	t.Run("ListImages", func(t *testing.T) {
		expectedResp := &pb.ListImagesResponse{
			Images:       []string{"SONiC-OS-202311.1", "SONiC-OS-202305.1"},
			CurrentImage: "SONiC-OS-202311.1",
			NextImage:    "SONiC-OS-202311.1",
		}

		mockFwMgmt.On("ListImages", mock.Anything, mock.Anything).Return(expectedResp, nil)

		resp, err := client.ListImages(ctx)
		assert.NoError(t, err)
		assert.Len(t, resp.Images, 2)
		assert.Equal(t, "SONiC-OS-202311.1", resp.CurrentImage)

		mockFwMgmt.AssertExpectations(t)
	})
}

func TestClient_ConnectionManagement(t *testing.T) {
	t.Run("Close", func(t *testing.T) {
		client := &Client{}
		err := client.Close()
		assert.NoError(t, err)
	})

	t.Run("IsConnected", func(t *testing.T) {
		client := &Client{}
		assert.False(t, client.IsConnected())
	})

	t.Run("Reconnect_NoConnection", func(t *testing.T) {
		client := &Client{}
		err := client.Reconnect()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no connection to reconnect")
	})
}

func boolPtr(b bool) *bool {
	return &b
}
