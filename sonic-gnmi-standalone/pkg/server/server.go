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
	"fmt"
	"io/ioutil"
	"net"
	"os"

	"github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/cert"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

// Server represents the gRPC server and its resources, providing a unified interface
// for managing the lifecycle of the SONiC gRPC services. It encapsulates the
// underlying gRPC server instance and network listener for clean resource management.
type Server struct {
	grpcServer *grpc.Server            // The underlying gRPC server instance
	listener   net.Listener            // Network listener for incoming connections
	certMgr    cert.CertificateManager // Certificate manager for TLS and monitoring
}

// NewServer creates a new Server instance using global configuration.
// This is the convenience function that reads TLS settings from the global config
// and creates appropriate certificate manager for actual server creation.
func NewServer(addr string) (*Server, error) {
	if !config.Global.TLSEnabled {
		return NewInsecureServer(addr)
	}

	// Create certificate configuration from global config
	certConfig := &cert.CertConfig{
		CertFile:          config.Global.TLSCertFile,
		KeyFile:           config.Global.TLSKeyFile,
		CAFile:            config.Global.TLSCACertFile,
		RequireClientCert: config.Global.MTLSEnabled,
		MinTLSVersion:     tls.VersionTLS12,
		EnableMonitoring:  true,
	}

	// Use default production TLS settings
	if certConfig.CipherSuites == nil {
		defaultConfig := cert.NewDefaultConfig()
		certConfig.CipherSuites = defaultConfig.CipherSuites
		certConfig.CurvePreferences = defaultConfig.CurvePreferences
	}

	return NewServerWithCertManager(addr, cert.NewCertificateManager(certConfig))
}

// NewServerWithCertManager creates a new Server instance with certificate manager.
// This is the primary server creation function that uses the certificate manager
// for production-grade TLS configuration and monitoring.
func NewServerWithCertManager(addr string, certMgr cert.CertificateManager) (*Server, error) {
	glog.V(1).Infof("Creating new server with certificate manager listening on %s", addr)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		glog.Errorf("Failed to listen on %s: %v", addr, err)
		return nil, err
	}

	// Load certificates
	if err := certMgr.LoadCertificates(); err != nil {
		return nil, err
	}

	// Start certificate monitoring
	if err := certMgr.StartMonitoring(); err != nil {
		glog.Warningf("Failed to start certificate monitoring: %v", err)
	}

	// Create gRPC server with certificate manager
	grpcServer, err := createGRPCServerWithCertManager(certMgr)
	if err != nil {
		certMgr.StopMonitoring()
		return nil, err
	}

	// Register reflection service for grpcurl functionality
	glog.V(2).Info("Registering reflection service")
	reflection.Register(grpcServer)

	glog.V(1).Infof("Server created successfully, listening on %s", lis.Addr().String())
	return &Server{
		grpcServer: grpcServer,
		listener:   lis,
		certMgr:    certMgr,
	}, nil
}

// NewInsecureServer creates a new Server instance without TLS for development/testing.
func NewInsecureServer(addr string) (*Server, error) {
	glog.V(1).Infof("Creating insecure server listening on %s", addr)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		glog.Errorf("Failed to listen on %s: %v", addr, err)
		return nil, err
	}

	// Create insecure gRPC server
	// nosemgrep: go.grpc.security.grpc-server-insecure-connection.grpc-server-insecure-connection
	grpcServer := grpc.NewServer()
	reflection.Register(grpcServer)

	glog.V(1).Infof("Insecure server created successfully, listening on %s", lis.Addr().String())
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

	// Stop certificate monitoring if enabled
	if s.certMgr != nil {
		s.certMgr.StopMonitoring()
	}

	glog.Info("Server stopped")
}

// createGRPCServerWithCertManager creates a gRPC server using the certificate manager.
func createGRPCServerWithCertManager(certMgr cert.CertificateManager) (*grpc.Server, error) {
	glog.V(2).Info("Creating gRPC server with certificate manager")

	// Get TLS configuration from certificate manager
	tlsConfig, err := certMgr.GetTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get TLS config: %w", err)
	}

	// Create gRPC credentials from TLS config
	creds := credentials.NewTLS(tlsConfig)
	server := grpc.NewServer(grpc.Creds(creds))

	glog.V(1).Infof("gRPC server created with TLS: MinVersion=%x, ClientAuth=%v, CipherSuites=%d",
		tlsConfig.MinVersion, tlsConfig.ClientAuth, len(tlsConfig.CipherSuites))

	return server, nil
}

// createGRPCServer creates a gRPC server with the appropriate security configuration (DEPRECATED).
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
