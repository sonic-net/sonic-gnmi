// Package gnmi provides gNMI service implementation for SONiC operations.
package gnmi

import (
	"github.com/openconfig/gnmi/proto/gnmi"
)

// Server implements the gNMI gRPC service for SONiC operations.
// It provides capabilities discovery and read-only access to system information
// such as filesystem disk space metrics.
type Server struct {
	gnmi.UnimplementedGNMIServer
	rootFS string
}

// NewServer creates a new gNMI server instance.
//
// The rootFS parameter specifies the root filesystem path for containerized
// deployments. Common values:
// - "/" for bare metal deployments
// - "/mnt/host" for containerized deployments where host filesystem is mounted
//
// This allows the server to resolve filesystem paths correctly regardless
// of the deployment environment.
func NewServer(rootFS string) *Server {
	return &Server{
		rootFS: rootFS,
	}
}
