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

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
)

func TestServerConnectionModes(t *testing.T) {
	t.Run("InsecureConnection", testInsecureConnection)
	t.Run("TLSConnection", testTLSConnection)
	t.Run("MTLSConnection", testMTLSConnection)
}

func TestServerBuilderCertificateIntegration(t *testing.T) {
	t.Run("ServerBuilderWithCertificateFiles", testServerBuilderWithCertificateFiles)
	t.Run("ServerBuilderWithSONiCCertificates", testServerBuilderWithSONiCCertificates)
	t.Run("ServerBuilderClientAuthPolicies", testServerBuilderClientAuthPolicies)
}

func testInsecureConnection(t *testing.T) {
	// Setup test server
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{})
	defer testServer.Stop()

	// Create insecure client connection
	conn, err := grpc.Dial(testServer.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
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
	// Setup test certificates and server
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile := generateTestCertificates(t, certDir, false)
	testServer := SetupTLSTestServer(t, tempDir, serverCertFile, serverKeyFile, []string{})
	defer testServer.Stop()

	// Create TLS client connection with insecure credentials (for self-signed cert)
	config := &tls.Config{
		InsecureSkipVerify: true,
	}
	creds := credentials.NewTLS(config)

	conn, err := grpc.Dial(testServer.Addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
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
	// Setup test certificates and server
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)
	testServer := SetupMTLSTestServer(t, tempDir, serverCertFile, serverKeyFile, caCertFile, []string{})
	defer testServer.Stop()

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

	conn, err := grpc.Dial(testServer.Addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
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

// testServerBuilderWithCertificateFiles tests the server builder with file-based certificates.
func testServerBuilderWithCertificateFiles(t *testing.T) {
	// Create temp directory and generate certificates
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server using builder pattern with certificate files
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithCertificateFiles(serverCertFile, serverKeyFile, caCertFile).
		Build()
	require.NoError(t, err, "Failed to build server with certificate files")
	defer srv.Stop()

	// Start server
	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(200 * time.Millisecond)

	// Test mTLS connection with the builder-created server
	testMTLSConnectionToAddress(t, addr, clientCertFile, clientKeyFile, caCertFile)
}

// testServerBuilderWithSONiCCertificates tests the server builder with SONiC ConfigDB certificates.
func testServerBuilderWithSONiCCertificates(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Create temp directory and generate certificates
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)

	// Populate ConfigDB with certificate paths
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   4, // ConfigDB database
	})
	defer client.Close()

	err = client.HSet(ctx, "GNMI|certs",
		"server_crt", serverCertFile,
		"server_key", serverKeyFile,
		"ca_crt", caCertFile,
	).Err()
	require.NoError(t, err, "Failed to populate ConfigDB")

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server using builder pattern with SONiC certificates
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithSONiCCertificates(mr.Addr(), 4).
		WithClientCertPolicy(true, false). // Require client certs
		Build()
	require.NoError(t, err, "Failed to build server with SONiC certificates")
	defer srv.Stop()

	// Start server
	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(200 * time.Millisecond)

	// Test mTLS connection with the builder-created server
	testMTLSConnectionToAddress(t, addr, clientCertFile, clientKeyFile, caCertFile)
}

// testServerBuilderClientAuthPolicies tests different client authentication policies.
func testServerBuilderClientAuthPolicies(t *testing.T) {
	tests := []struct {
		name              string
		requireClientCert bool
		allowNoClientCert bool
		expectClientAuth  bool
	}{
		{
			name:              "RequireClientCerts",
			requireClientCert: true,
			allowNoClientCert: false,
			expectClientAuth:  true,
		},
		{
			name:              "OptionalClientCerts",
			requireClientCert: false,
			allowNoClientCert: true,
			expectClientAuth:  false,
		},
		{
			name:              "NoClientCerts",
			requireClientCert: false,
			allowNoClientCert: false,
			expectClientAuth:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory and generate certificates
			tempDir := t.TempDir()
			certDir := filepath.Join(tempDir, "certs")
			err := os.MkdirAll(certDir, 0o755)
			require.NoError(t, err)

			serverCertFile, serverKeyFile, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)

			// Find available port
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			addr := fmt.Sprintf("127.0.0.1:%d", port)

			// Create server with specific client auth policy
			srv, err := server.NewServerBuilder().
				WithAddress(addr).
				WithRootFS(tempDir).
				WithCertificateFiles(serverCertFile, serverKeyFile, caCertFile).
				WithClientCertPolicy(tt.requireClientCert, tt.allowNoClientCert).
				Build()
			require.NoError(t, err, "Failed to build server")
			defer srv.Stop()

			// Start server
			go func() {
				if err := srv.Start(); err != nil {
					t.Logf("Server error: %v", err)
				}
			}()

			// Give server time to start
			time.Sleep(200 * time.Millisecond)

			if tt.expectClientAuth {
				// Should require client certificates
				testMTLSConnectionToAddress(t, addr, clientCertFile, clientKeyFile, caCertFile)
			} else {
				// Should work with TLS but no client cert
				testTLSConnectionToAddress(t, addr, caCertFile)
			}
		})
	}
}

// testMTLSConnectionToAddress tests mTLS connection to a specific address.
func testMTLSConnectionToAddress(t *testing.T, addr, clientCertFile, clientKeyFile, caCertFile string) {
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

	// Test basic gRPC reflection
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	resp, err := stream.Recv()
	require.NoError(t, err)

	services := resp.GetListServicesResponse()
	assert.NotNil(t, services)
	assert.Greater(t, len(services.Service), 0)
}

// testTLSConnectionToAddress tests TLS connection (no client cert) to a specific address.
func testTLSConnectionToAddress(t *testing.T, addr, caCertFile string) {
	// Load CA certificate for server verification
	caCertBytes, err := ioutil.ReadFile(caCertFile)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertBytes)

	// Create TLS client configuration (no client cert)
	config := &tls.Config{
		RootCAs:    caCertPool,
		ServerName: "localhost", // Must match certificate
	}
	creds := credentials.NewTLS(config)

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	// Test basic gRPC reflection
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	resp, err := stream.Recv()
	require.NoError(t, err)

	services := resp.GetListServicesResponse()
	assert.NotNil(t, services)
	assert.Greater(t, len(services.Service), 0)
}
