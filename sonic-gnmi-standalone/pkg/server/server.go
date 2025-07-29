// Package server provides the main gRPC server implementation for the SONiC services.
// It provides a minimal server with configurable TLS support and reflection capabilities
// as a foundation for adding specific services.
//
// The server supports both secure (TLS) and insecure connections depending on deployment
// requirements. It enables gRPC reflection for development tools like grpcurl.
//
// Key features:
//   - Configurable TLS with certificate validation
//   - gRPC reflection support for development and testing
//   - Graceful shutdown handling
//   - Comprehensive logging and error handling
package server

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net"
	"os"

	"github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

// Server represents the gRPC server and its resources, providing a unified interface
// for managing the lifecycle of the SONiC gRPC services. It encapsulates the
// underlying gRPC server instance and network listener for clean resource management.
type Server struct {
	grpcServer *grpc.Server // The underlying gRPC server instance
	listener   net.Listener // Network listener for incoming connections
}

// NewServer creates a new Server instance using global configuration.
// This is the convenience function that reads TLS settings from the global config
// and delegates to NewServerWithTLS for actual server creation.
func NewServer(addr string) (*Server, error) {
	return NewServerWithTLS(
		addr,
		config.Global.TLSEnabled,
		config.Global.TLSCertFile,
		config.Global.TLSKeyFile,
		config.Global.MTLSEnabled,
		config.Global.TLSCACertFile,
	)
}

// NewServerWithTLS creates a new Server instance with configurable TLS support.
// This function handles the complete server setup including:
//   - Network listener creation on the specified address
//   - TLS certificate validation and loading (if enabled)
//   - Mutual TLS (mTLS) client certificate verification (if enabled)
//   - gRPC server instantiation with appropriate security settings
//   - gRPC reflection setup for development tools
//
// Parameters:
//   - addr: Network address to bind to (e.g., ":8080", "localhost:50051")
//   - useTLS: Whether to enable TLS encryption
//   - certFile: Path to TLS certificate file (required if useTLS is true)
//   - keyFile: Path to TLS private key file (required if useTLS is true)
//   - useMTLS: Whether to enable mutual TLS (client certificate verification)
//   - caCertFile: Path to CA certificate file (required if useMTLS is true)
func NewServerWithTLS(
	addr string,
	useTLS bool,
	certFile, keyFile string,
	useMTLS bool,
	caCertFile string,
) (*Server, error) {
	glog.V(1).Infof("Creating new server listening on %s (TLS: %t, mTLS: %t)", addr, useTLS, useMTLS)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		glog.Errorf("Failed to listen on %s: %v", addr, err)
		return nil, err
	}

	glog.V(2).Info("Initializing gRPC server")
	var grpcServer *grpc.Server

	grpcServer, err = createGRPCServer(useTLS, useMTLS, certFile, keyFile, caCertFile)
	if err != nil {
		return nil, err
	}

	// Register reflection service for grpcurl functionality
	glog.V(2).Info("Registering reflection service")
	reflection.Register(grpcServer)

	glog.V(1).Infof("Server created successfully, listening on %s", lis.Addr().String())
	return &Server{
		grpcServer: grpcServer,
		listener:   lis,
	}, nil
}

// GRPCServer returns the underlying gRPC server instance.
// This is useful for registering additional services.
func (s *Server) GRPCServer() *grpc.Server {
	return s.grpcServer
}

// Start begins serving requests on the configured listener.
// This method blocks until the server is stopped or encounters an error.
// It should typically be called in a goroutine if non-blocking operation is needed.
func (s *Server) Start() error {
	glog.Infof("Starting gRPC server on %s", s.listener.Addr().String())
	return s.grpcServer.Serve(s.listener)
}

// Stop gracefully stops the server, allowing active requests to complete.
// This method waits for all active RPCs to finish before returning, ensuring
// clean shutdown without dropping client connections.
func (s *Server) Stop() {
	glog.Info("Gracefully stopping server...")
	s.grpcServer.GracefulStop()
	glog.Info("Server stopped")
}

// createGRPCServer creates a gRPC server with the appropriate security configuration.
func createGRPCServer(useTLS, useMTLS bool, certFile, keyFile, caCertFile string) (*grpc.Server, error) {
	if !useTLS {
		return createInsecureServer(), nil
	}

	if err := validateCertificateFiles(certFile, keyFile); err != nil {
		return nil, err
	}

	if useMTLS {
		return createMTLSServer(certFile, keyFile, caCertFile)
	}

	return createTLSServer(certFile, keyFile)
}

// validateCertificateFiles checks if the required certificate files exist.
func validateCertificateFiles(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		glog.Errorf("TLS certificate file not found: %s", certFile)
		return err
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		glog.Errorf("TLS key file not found: %s", keyFile)
		return err
	}
	return nil
}

// createTLSServer creates a gRPC server with regular TLS (server-side only).
func createTLSServer(certFile, keyFile string) (*grpc.Server, error) {
	creds, err := credentials.NewServerTLSFromFile(certFile, keyFile)
	if err != nil {
		glog.Errorf("Failed to load TLS credentials: %v", err)
		return nil, err
	}

	server := grpc.NewServer(grpc.Creds(creds))
	glog.V(1).Infof("TLS enabled with cert: %s, key: %s", certFile, keyFile)
	return server, nil
}

// createMTLSServer creates a gRPC server with mutual TLS (client certificate verification).
func createMTLSServer(certFile, keyFile, caCertFile string) (*grpc.Server, error) {
	// Check if CA certificate file exists for mTLS
	if _, err := os.Stat(caCertFile); os.IsNotExist(err) {
		glog.Errorf("TLS CA certificate file not found: %s", caCertFile)
		return nil, err
	}

	// Load server certificate and key
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		glog.Errorf("Failed to load server certificate and key: %v", err)
		return nil, err
	}

	// Load CA certificate for client verification
	caCert, err := ioutil.ReadFile(caCertFile)
	if err != nil {
		glog.Errorf("Failed to read CA certificate: %v", err)
		return nil, err
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		glog.Errorf("Failed to parse CA certificate")
		return nil, err
	}

	// Configure TLS for mutual authentication
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS12, // Use TLS 1.2 minimum for compatibility
	}

	creds := credentials.NewTLS(tlsConfig)
	server := grpc.NewServer(grpc.Creds(creds))
	glog.V(1).Infof("mTLS enabled with cert: %s, key: %s, ca: %s", certFile, keyFile, caCertFile)
	return server, nil
}

// createInsecureServer creates a gRPC server without TLS for development/testing.
func createInsecureServer() *grpc.Server {
	// Intentionally insecure for development/testing when --no-tls flag is used
	// nosemgrep: go.grpc.security.grpc-server-insecure-connection.grpc-server-insecure-connection
	server := grpc.NewServer()
	glog.V(1).Info("TLS disabled - using insecure connection")
	return server
}
