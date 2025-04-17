package gnmi

import (
	"context"

	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Deploy is a placeholder implementation for the Deploy RPC.
func (c *ContainerzServer) Deploy(stream gnoi_containerz_pb.Containerz_DeployServer) error {
	return status.Error(codes.Unimplemented, "Deploy is not implemented")
}

// Remove is a placeholder implementation for the Remove RPC.
func (c *ContainerzServer) Remove(ctx context.Context, req *gnoi_containerz_pb.RemoveRequest) (*gnoi_containerz_pb.RemoveResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Remove is not implemented")
}

// List is a placeholder implementation for the List RPC.
func (c *ContainerzServer) List(req *gnoi_containerz_pb.ListRequest, stream gnoi_containerz_pb.Containerz_ListServer) error {
	return status.Error(codes.Unimplemented, "List is not implemented")
}

// Start is a placeholder implementation for the Start RPC.
func (c *ContainerzServer) Start(ctx context.Context, req *gnoi_containerz_pb.StartRequest) (*gnoi_containerz_pb.StartResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Start is not implemented")
}

// Stop is a placeholder implementation for the Stop RPC.
func (c *ContainerzServer) Stop(ctx context.Context, req *gnoi_containerz_pb.StopRequest) (*gnoi_containerz_pb.StopResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Stop is not implemented")
}

// Log is a placeholder implementation for the Log RPC.
func (c *ContainerzServer) Log(req *gnoi_containerz_pb.LogRequest, stream gnoi_containerz_pb.Containerz_LogServer) error {
	return status.Error(codes.Unimplemented, "Log is not implemented")
}
