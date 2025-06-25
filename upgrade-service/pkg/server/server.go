package server

import (
	"net"

	"github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

// Server represents the gRPC server and its resources
type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener
}

// NewServer creates a new Server instance
func NewServer(addr string) (*Server, error) {
	glog.V(1).Infof("Creating new server listening on %s", addr)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		glog.Errorf("Failed to listen on %s: %v", addr, err)
		return nil, err
	}

	glog.V(2).Info("Initializing gRPC server")
	grpcServer := grpc.NewServer()
	systemInfoServer := NewSystemInfoServer()
	pb.RegisterSystemInfoServer(grpcServer, systemInfoServer)

	// Register reflection service for grpcurl functionality
	glog.V(2).Info("Registering reflection service")
	reflection.Register(grpcServer)

	glog.V(1).Infof("Server created successfully, listening on %s", lis.Addr().String())
	return &Server{
		grpcServer: grpcServer,
		listener:   lis,
	}, nil
}

// Start begins serving requests
func (s *Server) Start() error {
	glog.Infof("Starting gRPC server on %s", s.listener.Addr().String())
	return s.grpcServer.Serve(s.listener)
}

// Stop gracefully stops the server
func (s *Server) Stop() {
	glog.Info("Gracefully stopping server...")
	s.grpcServer.GracefulStop()
	glog.Info("Server stopped")
}
