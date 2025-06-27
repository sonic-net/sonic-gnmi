package e2e

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/pkg/server"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

const bufSize = 1024 * 1024

// setupGRPCServer creates an in-memory gRPC server using bufconn
// and registers the SystemInfoServer.
func setupGRPCServer(t *testing.T) (*grpc.Server, *bufconn.Listener) {
	listener := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()

	// Register SystemInfoServer
	systemInfoServer := server.NewSystemInfoServer()
	pb.RegisterSystemInfoServer(grpcServer, systemInfoServer)

	// Start server in background
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			t.Logf("Server exited with error: %v", err)
		}
	}()

	return grpcServer, listener
}

// createClient creates a gRPC client connected to the bufconn listener.
func createClient(ctx context.Context, listener *bufconn.Listener) (pb.SystemInfoClient, *grpc.ClientConn, error) {
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, err
	}

	client := pb.NewSystemInfoClient(conn)
	return client, conn, nil
}

func TestGetPlatformType_E2E(t *testing.T) {
	tests := []struct {
		name                       string
		machineConfContent         string
		expectedPlatformIdentifier string
		expectedVendor             string
		expectedModel              string
	}{
		{
			name: "Success - Mellanox SN4600",
			machineConfContent: `onie_platform=x86_64-mlnx_msn4600c-r0
onie_machine=mlnx_msn4600c
onie_arch=x86_64
onie_switch_asic=mlnx`,
			expectedPlatformIdentifier: "mellanox_sn4600",
			expectedVendor:             "Mellanox",
			expectedModel:              "sn4600",
		},
		{
			name: "Success - Arista 7060",
			machineConfContent: `aboot_vendor=arista
aboot_platform=x86_64-arista_7060x6_64pe
aboot_machine=arista_7060x6_64pe
aboot_arch=x86_64`,
			expectedPlatformIdentifier: "arista_7060",
			expectedVendor:             "arista",
			expectedModel:              "7060",
		},
		{
			name: "Unknown Platform",
			machineConfContent: `onie_platform=x86_64-generic_platform-r0
onie_machine=generic_platform
onie_arch=x86_64
onie_switch_asic=unknown`,
			expectedPlatformIdentifier: "unknown_generic_platform",
			expectedVendor:             "unknown",
			expectedModel:              "generic_platform",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Set up temporary directory and machine.conf
			tempDir := t.TempDir()
			machineConfPath := filepath.Join(tempDir, "host", "machine.conf")

			// Create directory
			err := os.MkdirAll(filepath.Dir(machineConfPath), 0755)
			require.NoError(t, err)

			// Write machine.conf
			err = os.WriteFile(machineConfPath, []byte(test.machineConfContent), 0644)
			require.NoError(t, err)

			// Mock config
			originalConfig := config.Global
			config.Global = &config.Config{RootFS: tempDir}
			defer func() { config.Global = originalConfig }()

			// Setup gRPC server
			grpcServer, listener := setupGRPCServer(t)
			defer grpcServer.Stop()

			// Create client
			ctx := context.Background()
			client, conn, err := createClient(ctx, listener)
			require.NoError(t, err)
			defer conn.Close()

			// Make request
			resp, err := client.GetPlatformType(ctx, &pb.GetPlatformTypeRequest{})
			require.NoError(t, err)
			require.NotNil(t, resp)

			// Verify response
			assert.Equal(t, test.expectedPlatformIdentifier, resp.PlatformIdentifier)
			assert.Equal(t, test.expectedVendor, resp.Vendor)
			assert.Equal(t, test.expectedModel, resp.Model)
		})
	}
}
