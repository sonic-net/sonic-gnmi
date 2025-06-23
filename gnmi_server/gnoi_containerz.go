// Based on gnoi v0.3.0. The latest upstream v0.6.0 has updated many service names. TODO: Upgrade accordingly.

package gnmi

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	log "github.com/golang/glog"
	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	gnoi_types_pb "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Deploy receives the image and download information and downloads the file using sonic_service_client.
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

	var reqDump strings.Builder
	reqDump.WriteString("Received DeployRequest:\n")
	reqDump.WriteString("  Name: " + imageTransfer.Name + "\n")
	reqDump.WriteString("  Tag: " + imageTransfer.Tag + "\n")
	var hostname, remotePath, username, password, protocol string
	if rd := imageTransfer.RemoteDownload; rd != nil {
		reqDump.WriteString("  RemoteDownload:\n")
		reqDump.WriteString("    Path: " + rd.Path + "\n")
		reqDump.WriteString("    Protocol: " + rd.Protocol.String() + "\n")
		protocol = rd.Protocol.String()
		if rd.Credentials != nil {
			username = rd.Credentials.Username
			reqDump.WriteString("    Username: " + username + "\n")
			if clear, ok := rd.Credentials.Password.(*gnoi_types_pb.Credentials_Cleartext); ok {
				password = clear.Cleartext
			}
		}
		// Parse <host>:<remote-path>
		parts := strings.SplitN(rd.Path, ":", 2)
		if len(parts) == 2 {
			hostname = parts[0]
			remotePath = parts[1]
		} else {
			return status.Errorf(codes.InvalidArgument, "invalid remote download path: %s", rd.Path)
		}
	}
	log.V(2).Info(reqDump.String())

	// Use sonic_service_client to download the file to a local path (e.g., /tmp/<name>-<random>.tar)
	// Use crypto/rand for a random suffix if common_utils.RandString is not available
	randomBytes := make([]byte, 6)
	_, err = rand.Read(randomBytes)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to generate random suffix: %v", err)
	}
	randomSuffix := fmt.Sprintf("%x", randomBytes)
	localPath := "/tmp/" + imageTransfer.Name + "-" + randomSuffix + ".tar"

	dbusClient, err := ssc.NewDbusClient(dbusCaller)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create dbus client: %v", err)
	}
	err = dbusClient.DownloadFile(hostname, username, password, remotePath, localPath, protocol)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to download file: %v", err)
	}
	log.V(2).Infof("Downloaded file to %s", localPath)

	// After download, load the docker image using dbusClient.LoadDockerImage
	err = dbusClient.LoadDockerImage(localPath)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to load docker image: %v", err)
	}
	log.V(2).Infof("Loaded docker image from %s", localPath)

	// Clean up the local file after loading the image
	err = dbusClient.RemoveFile(localPath)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to remove local file: %v", err)
	}
	log.V(2).Infof("Removed local file %s", localPath)

	// Respond with success (dummy, real implementation should load the image, etc.)
	resp := &gnoi_containerz_pb.DeployResponse{
		Response: &gnoi_containerz_pb.DeployResponse_ImageTransferSuccess{
			ImageTransferSuccess: &gnoi_containerz_pb.ImageTransferSuccess{
				Name:      imageTransfer.Name,
				Tag:       imageTransfer.Tag,
				ImageSize: 0, // You can fill this with the actual file size if needed
			},
		},
	}
	if err := stream.Send(resp); err != nil {
		return status.Errorf(codes.Internal, "failed to send DeployResponse: %v", err)
	}

	return nil
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
