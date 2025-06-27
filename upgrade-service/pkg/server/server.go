package server

import (
	"net"
	"os"

	"github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

// Server represents the gRPC server and its resources.
type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener
}

// NewServer creates a new Server instance using global configuration.
func NewServer(addr string) (*Server, error) {
	return NewServerWithTLS(addr, config.Global.TLSEnabled, config.Global.TLSCertFile, config.Global.TLSKeyFile)
}

// NewServerWithTLS creates a new Server instance with configurable TLS.
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
		// Intentionally insecure for development/testing when DISABLE_TLS=true is explicitly set
		// nosemgrep: go.grpc.security.grpc-server-insecure-connection.grpc-server-insecure-connection
		grpcServer = grpc.NewServer()
		glog.V(1).Info("TLS disabled - using insecure connection")
	}
	systemInfoServer := NewSystemInfoServer()
	pb.RegisterSystemInfoServer(grpcServer, systemInfoServer)

	firmwareManagementServer := NewFirmwareManagementServer()
	pb.RegisterFirmwareManagementServer(grpcServer, firmwareManagementServer)

	// Register reflection service for grpcurl functionality
	glog.V(2).Info("Registering reflection service")
	reflection.Register(grpcServer)

	glog.V(1).Infof("Server created successfully, listening on %s", lis.Addr().String())
	return &Server{
		grpcServer: grpcServer,
		listener:   lis,
	}, nil
}

// Start begins serving requests.
func (s *Server) Start() error {
	glog.Infof("Starting gRPC server on %s", s.listener.Addr().String())
	return s.grpcServer.Serve(s.listener)
}

// Stop gracefully stops the server.
func (s *Server) Stop() {
	glog.Info("Gracefully stopping server...")
	s.grpcServer.GracefulStop()
	glog.Info("Server stopped")
}
