package server

import (
	"log"
	"net"

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
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	grpcServer := grpc.NewServer()
	systemInfoServer := NewSystemInfoServer()
	pb.RegisterSystemInfoServer(grpcServer, systemInfoServer)

	// Register reflection service for grpcurl functionality
	reflection.Register(grpcServer)

	return &Server{
		grpcServer: grpcServer,
		listener:   lis,
	}, nil
}

// Start begins serving requests
func (s *Server) Start() error {
	log.Printf("Starting gRPC server on %s", s.listener.Addr().String())
	return s.grpcServer.Serve(s.listener)
}

// Stop gracefully stops the server
func (s *Server) Stop() {
	log.Println("Gracefully stopping server...")
	s.grpcServer.GracefulStop()
	log.Println("Server stopped")
}
