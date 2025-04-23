// Based on gnoi v0.3.0. The latest upstream v0.6.0 has updated many service names. TODO: Upgrade accordingly.

package gnmi

import (
	"context"
	"strings"

	log "github.com/golang/glog"
	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Deploy receives the image and download information and prints it out.
func (c *ContainerzServer) Deploy(stream gnoi_containerz_pb.Containerz_DeployServer) error {
	log.V(2).Info("gNOI: Containerz Deploy called")

	ctx := stream.Context()

	// Authenticate the client using the server's config.
	_, err := authenticate(c.server.config, ctx, "gnoi", true)
	if err != nil {
		return err
	}

	// Read the first request from the stream.
	req, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to receive DeployRequest: %v", err)
	}

	imageTransfer := req.GetImageTransfer()
	if imageTransfer == nil {
		return status.Errorf(codes.InvalidArgument, "first DeployRequest must be ImageTransfer")
	}

	var b strings.Builder
	b.WriteString("Received DeployRequest:\n")
	b.WriteString("  Name: " + imageTransfer.Name + "\n")
	b.WriteString("  Tag: " + imageTransfer.Tag + "\n")
	if rd := imageTransfer.RemoteDownload; rd != nil {
		b.WriteString("  RemoteDownload:\n")
		b.WriteString("    Path: " + rd.Path + "\n")
		b.WriteString("    Protocol: " + rd.Protocol.String() + "\n")
		if rd.Credentials != nil {
			b.WriteString("    Username: " + rd.Credentials.Username + "\n")
		}
	}
	log.V(2).Info(b.String())

	return status.Error(codes.Unimplemented, "Deploy is not fully implemented")
}

// Remove is a placeholder implementation for the Remove RPC.
func (c *ContainerzServer) Remove(ctx context.Context, req *gnoi_containerz_pb.RemoveRequest) (*gnoi_containerz_pb.RemoveResponse, error) {
	log.V(2).Info("gNOI: Containerz Remove called")
	return nil, status.Error(codes.Unimplemented, "Remove is not implemented")
}

// List is a placeholder implementation for the List RPC.
func (c *ContainerzServer) List(req *gnoi_containerz_pb.ListRequest, stream gnoi_containerz_pb.Containerz_ListServer) error {
	log.V(2).Info("gNOI: Containerz List called")
	return status.Error(codes.Unimplemented, "List is not implemented")
}

// Start is a placeholder implementation for the Start RPC.
func (c *ContainerzServer) Start(ctx context.Context, req *gnoi_containerz_pb.StartRequest) (*gnoi_containerz_pb.StartResponse, error) {
	log.V(2).Info("gNOI: Containerz Start called")
	return nil, status.Error(codes.Unimplemented, "Start is not implemented")
}

// Stop is a placeholder implementation for the Stop RPC.
func (c *ContainerzServer) Stop(ctx context.Context, req *gnoi_containerz_pb.StopRequest) (*gnoi_containerz_pb.StopResponse, error) {
	log.V(2).Info("gNOI: Containerz Stop called")
	return nil, status.Error(codes.Unimplemented, "Stop is not implemented")
}

// Log is a placeholder implementation for the Log RPC.
func (c *ContainerzServer) Log(req *gnoi_containerz_pb.LogRequest, stream gnoi_containerz_pb.Containerz_LogServer) error {
	log.V(2).Info("gNOI: Containerz Log called")
	return status.Error(codes.Unimplemented, "Log is not implemented")
}
