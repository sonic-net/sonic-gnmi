package loopback

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
	clientGnmi "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnmi"
	clientGnoi "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnoi"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
	serverConfig "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

// TestServerConfig holds configuration for test server setup.
type TestServerConfig struct {
	RootFS    string
	TLSCert   string
	TLSKey    string
	TLSCACert string
	UseTLS    bool
	UseMTLS   bool
	Services  []string
}

// TestServer wraps a gRPC server with test-specific helpers.
type TestServer struct {
	Server *server.Server
	Addr   string
	t      *testing.T
}

// Stop gracefully stops the test server.
func (ts *TestServer) Stop() {
	if ts.Server != nil {
		ts.Server.Stop()
	}
}

// SetupTestServer creates and starts a gRPC server for testing.
// It automatically finds an available port and starts the server in a goroutine.
func SetupTestServer(t *testing.T, tempDir string, cfg *TestServerConfig) *TestServer {
	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Set global server config
	serverConfig.Global = &serverConfig.Config{
		Addr:          addr,
		RootFS:        cfg.RootFS,
		TLSEnabled:    cfg.UseTLS,
		MTLSEnabled:   cfg.UseMTLS,
		TLSCertFile:   cfg.TLSCert,
		TLSKeyFile:    cfg.TLSKey,
		TLSCACertFile: cfg.TLSCACert,
	}

	// Create server builder
	builder := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(cfg.RootFS)

	// Configure TLS
	if !cfg.UseTLS {
		builder = builder.WithoutTLS()
	} else if cfg.UseMTLS {
		builder = builder.WithMTLS(cfg.TLSCert, cfg.TLSKey, cfg.TLSCACert)
	} else if cfg.UseTLS {
		builder = builder.WithTLS(cfg.TLSCert, cfg.TLSKey)
	}

	// Enable services
	for _, service := range cfg.Services {
		switch service {
		case "gnoi.system":
			builder = builder.EnableGNOISystem()
		case "gnmi":
			builder = builder.EnableGNMI()
		}
	}

	// Build server
	srv, err := builder.Build()
	require.NoError(t, err, "Failed to create server")

	// Start server in background
	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(200 * time.Millisecond)

	testServer := &TestServer{
		Server: srv,
		Addr:   addr,
		t:      t,
	}

	// Verify server is responding
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "Failed to connect to gRPC server")
	conn.Close()

	return testServer
}

// SetupInsecureTestServer creates a test server without TLS.
func SetupInsecureTestServer(t *testing.T, tempDir string, services []string) *TestServer {
	cfg := &TestServerConfig{
		RootFS:   tempDir,
		UseTLS:   false,
		Services: services,
	}
	return SetupTestServer(t, tempDir, cfg)
}

// SetupTLSTestServer creates a test server with TLS.
func SetupTLSTestServer(t *testing.T, tempDir string, certFile, keyFile string, services []string) *TestServer {
	cfg := &TestServerConfig{
		RootFS:   tempDir,
		UseTLS:   true,
		TLSCert:  certFile,
		TLSKey:   keyFile,
		Services: services,
	}
	return SetupTestServer(t, tempDir, cfg)
}

// SetupMTLSTestServer creates a test server with mutual TLS.
func SetupMTLSTestServer(
	t *testing.T, tempDir string, certFile, keyFile, caCertFile string, services []string,
) *TestServer {
	cfg := &TestServerConfig{
		RootFS:    tempDir,
		UseTLS:    true,
		UseMTLS:   true,
		TLSCert:   certFile,
		TLSKey:    keyFile,
		TLSCACert: caCertFile,
		Services:  services,
	}
	return SetupTestServer(t, tempDir, cfg)
}

// SetupGNOIClient creates a gNOI System client for testing.
func SetupGNOIClient(t *testing.T, addr string, useTLS bool) *clientGnoi.SystemClient {
	clientConfig := &config.Config{
		Address: addr,
		TLS:     useTLS,
	}

	client, err := clientGnoi.NewSystemClient(clientConfig)
	require.NoError(t, err, "Failed to create gNOI client")

	return client
}

// SetupHTTPTestServer creates an HTTP test server with the given content.
func SetupHTTPTestServer(content []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(content)
	}))
}

// SetupGNMIClient creates a gNMI client for testing.
func SetupGNMIClient(t *testing.T, addr string, timeout time.Duration) *clientGnmi.Client {
	client, err := clientGnmi.NewClient(&clientGnmi.ClientConfig{
		Target:  addr,
		Timeout: timeout,
	})
	require.NoError(t, err, "Failed to create gNMI client")

	return client
}

// WithTestTimeout creates a context with timeout for testing.
func WithTestTimeout(duration time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), duration)
}
