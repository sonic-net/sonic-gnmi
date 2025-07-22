// Package system implements the gNOI System service for SONiC.
// This package provides system-level operations including package management,
// reboot, and other system administrative functions.
package system

import (
	"github.com/golang/glog"
	"github.com/openconfig/gnoi/system"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements the gNOI System service.
// It provides system-level operations for SONiC devices.
type Server struct {
	system.UnimplementedSystemServer
}

// NewServer creates a new System service server instance.
func NewServer() *Server {
	return &Server{}
}

// SetPackage installs a software package on the target device.
// This is a bidirectional streaming RPC that handles package transfer and installation.
//
// The RPC follows this sequence:
// 1. Client sends initial request with package details
// 2. Client streams package contents
// 3. Server validates and installs the package
// 4. Server streams progress updates back to client
//
// Currently returns UNIMPLEMENTED as a skeleton implementation.
func (s *Server) SetPackage(stream system.System_SetPackageServer) error {
	glog.Info("SetPackage RPC called (not implemented)")
	
	// Return UNIMPLEMENTED error as this is a skeleton
	return status.Errorf(codes.Unimplemented, "SetPackage not implemented")
}