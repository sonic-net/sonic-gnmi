package gnmi

import (
	"context"

	"github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Set modifies the state of data on the target device.
// This implementation returns Unimplemented as this server is read-only.
func (s *Server) Set(ctx context.Context, req *gnmi.SetRequest) (*gnmi.SetResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Set RPC not implemented - this is a read-only gNMI server")
}
