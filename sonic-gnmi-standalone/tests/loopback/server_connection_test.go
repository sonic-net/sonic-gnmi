package loopback

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
)

func TestServerConnectionModes(t *testing.T) {
	t.Run("InsecureConnection", testInsecureConnection)
	t.Run("TLSConnection", testTLSConnection)
	t.Run("MTLSConnection", testMTLSConnection)
}

func testInsecureConnection(t *testing.T) {
	// Create temporary directory for test environment
	tempDir := t.TempDir()

	// Get available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server with insecure connection
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithoutTLS().
		Build()
	require.NoError(t, err)

	// Start server in background
	go func() {
		err := srv.Start()
		if err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Ensure cleanup
	defer srv.Stop()

	// Create insecure client connection
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	// Receive response
	resp, err := stream.Recv()
	require.NoError(t, err)

	// Verify we get a list of services response
	services := resp.GetListServicesResponse()
	assert.NotNil(t, services)
	assert.Greater(t, len(services.Service), 0)
}

func testTLSConnection(t *testing.T) {
	// Create temporary directory for test environment
	tempDir := t.TempDir()

	// Generate test certificates
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile := generateTestCertificates(t, certDir, false)

	// Get available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server with TLS
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithTLS(serverCertFile, serverKeyFile).
		Build()
	require.NoError(t, err)

	// Start server in background
	go func() {
		err := srv.Start()
		if err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Ensure cleanup
	defer srv.Stop()

	// Create TLS client connection with insecure credentials (for self-signed cert)
	config := &tls.Config{
		InsecureSkipVerify: true,
	}
	creds := credentials.NewTLS(config)

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	// Receive response
	resp, err := stream.Recv()
	require.NoError(t, err)

	// Verify we get a list of services response
	services := resp.GetListServicesResponse()
	assert.NotNil(t, services)
	assert.Greater(t, len(services.Service), 0)
}

func testMTLSConnection(t *testing.T) {
	// Create temporary directory for test environment
	tempDir := t.TempDir()

	// Generate test certificates for mTLS
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)

	// Get available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server with mTLS
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithMTLS(serverCertFile, serverKeyFile, caCertFile).
		Build()
	require.NoError(t, err)

	// Start server in background
	go func() {
		err := srv.Start()
		if err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Ensure cleanup
	defer srv.Stop()

	// Load client certificate and key
	clientCert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
	require.NoError(t, err)

	// Load CA certificate
	caCertBytes, err := ioutil.ReadFile(caCertFile)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertBytes)

	// Create mTLS client configuration
	config := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caCertPool,
		ServerName:   "localhost", // Must match certificate
	}
	creds := credentials.NewTLS(config)

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	// Receive response
	resp, err := stream.Recv()
	require.NoError(t, err)

	// Verify we get a list of services response
	services := resp.GetListServicesResponse()
	assert.NotNil(t, services)
	assert.Greater(t, len(services.Service), 0)
}
