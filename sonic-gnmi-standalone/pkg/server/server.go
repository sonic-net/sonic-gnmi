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
	"net"
	"os"

	"github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/internal/config"
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
	return NewServerWithTLS(addr, config.Global.TLSEnabled, config.Global.TLSCertFile, config.Global.TLSKeyFile)
}

// NewServerWithTLS creates a new Server instance with configurable TLS support.
// This function handles the complete server setup including:
//   - Network listener creation on the specified address
//   - TLS certificate validation and loading (if enabled)
//   - gRPC server instantiation with appropriate security settings
//   - gRPC reflection setup for development tools
//
// Parameters:
//   - addr: Network address to bind to (e.g., ":8080", "localhost:50051")
//   - useTLS: Whether to enable TLS encryption
//   - certFile: Path to TLS certificate file (required if useTLS is true)
//   - keyFile: Path to TLS private key file (required if useTLS is true)
func NewServerWithTLS(addr string, useTLS bool, certFile, keyFile string) (*Server, error) {
	glog.V(1).Infof("Creating new server listening on %s (TLS: %t)", addr, useTLS)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		glog.Errorf("Failed to listen on %s: %v", addr, err)
		return nil, err
	}

	glog.V(2).Info("Initializing gRPC server")
	var grpcServer *grpc.Server

	if useTLS {
		// Check if certificate files exist
		if _, err := os.Stat(certFile); os.IsNotExist(err) {
			glog.Errorf("TLS certificate file not found: %s", certFile)
			return nil, err
		}
		if _, err := os.Stat(keyFile); os.IsNotExist(err) {
			glog.Errorf("TLS key file not found: %s", keyFile)
			return nil, err
		}

		// Load TLS credentials
		creds, err := credentials.NewServerTLSFromFile(certFile, keyFile)
		if err != nil {
			glog.Errorf("Failed to load TLS credentials: %v", err)
			return nil, err
		}

		grpcServer = grpc.NewServer(grpc.Creds(creds))
		glog.V(1).Infof("TLS enabled with cert: %s, key: %s", certFile, keyFile)
	} else {
		// Intentionally insecure for development/testing when --no-tls flag is used
		// nosemgrep: go.grpc.security.grpc-server-insecure-connection.grpc-server-insecure-connection
		grpcServer = grpc.NewServer()
		glog.V(1).Info("TLS disabled - using insecure connection")
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
