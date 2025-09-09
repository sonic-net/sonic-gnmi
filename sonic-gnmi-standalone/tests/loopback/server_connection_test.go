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

	// Test the 2x2 certificate authentication matrix:
	// Server modes: Required, No requirement
	// Client scenarios: Has cert, No cert
	t.Run("RequiredClientCert_ClientHasCert", testRequiredClientCert_ClientHasCert)
	t.Run("RequiredClientCert_ClientNoCert", testRequiredClientCert_ClientNoCert)
	t.Run("NoClientCertRequirement_ClientHasCert", testNoClientCertRequirement_ClientHasCert)
	t.Run("NoClientCertRequirement_ClientNoCert", testNoClientCertRequirement_ClientNoCert)
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

// testRequiredClientCert_ClientHasCert tests server requiring client certs with client providing cert.
func testRequiredClientCert_ClientHasCert(t *testing.T) {
	addr, clientCertFile, clientKeyFile, caCertFile, srv := createTestServerWithClientAuth(t, true)
	defer srv.Stop()

	// Should succeed with client certificate
	validateMTLSConnection(t, addr, clientCertFile, clientKeyFile, caCertFile)
}

// testRequiredClientCert_ClientNoCert tests server requiring client certs with client providing no cert.
func testRequiredClientCert_ClientNoCert(t *testing.T) {
	addr, _, _, caCertFile, srv := createTestServerWithClientAuth(t, true)
	defer srv.Stop()

	// Should fail without client certificate
	validateTLSConnectionFailure(t, addr, caCertFile)
}

// testNoClientCertRequirement_ClientHasCert tests server with no client cert requirement where client provides cert.
func testNoClientCertRequirement_ClientHasCert(t *testing.T) {
	addr, clientCertFile, clientKeyFile, caCertFile, srv := createTestServerWithClientAuth(t, false)
	defer srv.Stop()

	// Should succeed with client certificate
	validateMTLSConnection(t, addr, clientCertFile, clientKeyFile, caCertFile)
}

// testNoClientCertRequirement_ClientNoCert tests server with no client cert requirement where client provides no cert.
func testNoClientCertRequirement_ClientNoCert(t *testing.T) {
	addr, _, _, caCertFile, srv := createTestServerWithClientAuth(t, false)
	defer srv.Stop()

	// Should succeed without client certificate
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

// validateTLSConnectionFailure validates that TLS connection fails when client cert is required but not provided.
func validateTLSConnectionFailure(t *testing.T, addr, caCertFile string) {
	// Load CA certificate for server verification
	caCertBytes, err := ioutil.ReadFile(caCertFile)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertBytes)

	// Create TLS client configuration (no client cert)
	config := &tls.Config{
		RootCAs:    caCertPool,
		ServerName: "localhost",
	}
	creds := credentials.NewTLS(config)

	// Connection should fail at TLS handshake level
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		// Connection failed as expected
		return
	}
	defer conn.Close()

	// If connection succeeded, the RPC should fail
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	if err != nil {
		// RPC failed as expected
		return
	}

	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
	})

	// Either Send or Recv should fail for missing client certificate
	if err == nil {
		_, err = stream.Recv()
	}

	// We expect some kind of authentication error
	assert.Error(t, err, "Expected connection or RPC to fail when client cert is required but not provided")
}

// validateInvalidClientCertConnection validates that connection fails with invalid client certificate.
func validateInvalidClientCertConnection(t *testing.T, addr, invalidClientCert, invalidClientKey, caCertFile string) {
	// Load invalid client certificate and key
	clientCert, err := tls.LoadX509KeyPair(invalidClientCert, invalidClientKey)
	require.NoError(t, err)

	// Load server CA certificate (for server verification)
	caCertBytes, err := ioutil.ReadFile(caCertFile)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertBytes)

	// Create TLS client configuration with invalid client cert
	config := &tls.Config{
		Certificates: []tls.Certificate{clientCert}, // Invalid cert (different CA)
		RootCAs:      caCertPool,                    // Valid server CA
		ServerName:   "localhost",
	}
	creds := credentials.NewTLS(config)

	// Connection should fail due to invalid client certificate
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		// Connection failed as expected (TLS handshake failure)
		return
	}
	defer conn.Close()

	// If connection succeeded, the RPC should fail due to invalid cert
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	if err != nil {
		// RPC failed as expected
		return
	}

	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
	})

	// Either Send or Recv should fail for invalid client certificate
	if err == nil {
		_, err = stream.Recv()
	}

	// We expect some kind of certificate validation error
	assert.Error(t, err, "Expected connection or RPC to fail with invalid client certificate")
}

// createTestServerWithClientAuth creates a test server with specified client auth policy.
func createTestServerWithClientAuth(
	t *testing.T, requireClient bool,
) (addr, clientCertFile, clientKeyFile, caCertFile string, srv *server.Server) {
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
	addr = fmt.Sprintf("127.0.0.1:%d", port)

	// Create server with specified client certificate policy
	srv, err = server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithCertificateFiles(serverCertFile, serverKeyFile, caCertFile).
		WithClientCertPolicy(requireClient).
		Build()
	require.NoError(t, err, "Failed to build server")

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

	return addr, clientCertFile, clientKeyFile, caCertFile, srv
}
