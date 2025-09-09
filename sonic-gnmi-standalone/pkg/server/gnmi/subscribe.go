package gnmi

import (
	"github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Subscribe enables a client to subscribe to updates of particular paths within the data tree.
// This implementation returns Unimplemented as streaming subscriptions are not yet supported.
func (s *Server) Subscribe(stream gnmi.GNMI_SubscribeServer) error {
	return status.Error(codes.Unimplemented, "Subscribe RPC not implemented - use Get RPC for current state")
}
