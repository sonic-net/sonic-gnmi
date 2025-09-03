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
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
	serverConfig "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

func TestServerConnectionModes(t *testing.T) {
	t.Run("InsecureConnection", testInsecureConnection)
	t.Run("TLSConnection", testTLSConnection)
	t.Run("MTLSConnection", testMTLSConnection)
}

func TestServerBuilderClientAuthPolicies(t *testing.T) {
	// Initialize minimal global config for tests that don't set explicit builder values
	if serverConfig.Global == nil {
		serverConfig.Global = &serverConfig.Config{}
	}

	t.Run("RequireClientCerts", testRequireClientCerts)
	t.Run("OptionalClientCerts", testOptionalClientCerts)
	t.Run("NoClientCerts", testNoClientCerts)
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

// testRequireClientCerts tests server that requires client certificates.
func testRequireClientCerts(t *testing.T) {
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

	// Create server requiring client certificates
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithCertificateFiles(serverCertFile, serverKeyFile, caCertFile).
		WithClientCertPolicy(true, false). // Require client certs
		Build()
	require.NoError(t, err, "Failed to build server")
	defer srv.Stop()

	// Start server
	serverStarted := make(chan error, 1)
	go func() {
		serverStarted <- srv.Start()
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Check if server had an error starting
	select {
	case err := <-serverStarted:
		if err != nil {
			t.Fatalf("Server failed to start: %v", err)
		}
	default:
		// Server is still running, which is expected
	}

	// Should require client certificates
	validateMTLSConnection(t, addr, clientCertFile, clientKeyFile, caCertFile)
}

// testOptionalClientCerts tests server with optional client certificates.
func testOptionalClientCerts(t *testing.T) {
	// Create temp directory and generate certificates
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, _, _, caCertFile := generateMTLSCertificates(t, certDir)

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server with optional client certificates
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithCertificateFiles(serverCertFile, serverKeyFile, caCertFile).
		WithClientCertPolicy(false, true). // Optional client certs
		Build()
	require.NoError(t, err, "Failed to build server")
	defer srv.Stop()

	// Start server
	serverStarted := make(chan error, 1)
	go func() {
		serverStarted <- srv.Start()
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Check if server had an error starting
	select {
	case err := <-serverStarted:
		if err != nil {
			t.Fatalf("Server failed to start: %v", err)
		}
	default:
		// Server is still running, which is expected
	}

	// Should work with TLS but no client cert
	validateTLSConnection(t, addr, caCertFile)
}

// testNoClientCerts tests server that doesn't use client certificates.
func testNoClientCerts(t *testing.T) {
	// Create temp directory and generate certificates
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, _, _, caCertFile := generateMTLSCertificates(t, certDir)

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server with no client certificate requirement
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithCertificateFiles(serverCertFile, serverKeyFile, caCertFile).
		WithClientCertPolicy(false, false). // No client certs
		Build()
	require.NoError(t, err, "Failed to build server")
	defer srv.Stop()

	// Start server
	serverStarted := make(chan error, 1)
	go func() {
		serverStarted <- srv.Start()
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Check if server had an error starting
	select {
	case err := <-serverStarted:
		if err != nil {
			t.Fatalf("Server failed to start: %v", err)
		}
	default:
		// Server is still running, which is expected
	}

	// Should work with TLS but no client cert
	validateTLSConnection(t, addr, caCertFile)
}

// validateMTLSConnection validates that mTLS connection works to a specific address.
func validateMTLSConnection(t *testing.T, addr, clientCertFile, clientKeyFile, caCertFile string) {
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

// validateTLSConnection validates that TLS connection works (no client cert) to a specific address.
func validateTLSConnection(t *testing.T, addr, caCertFile string) {
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
