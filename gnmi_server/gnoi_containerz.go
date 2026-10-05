// Based on gnoi v0.3.0. The latest upstream v0.6.0 has updated many service names. TODO: Upgrade accordingly.

package gnmi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"

	log "github.com/golang/glog"
	gnoi_common_pb "github.com/openconfig/gnoi/common"
	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	gnoi_types_pb "github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type containerzTemporaryFile interface {
	io.Writer
	Close() error
	Name() string
}

type containerzImageLoader interface {
	LoadDockerImage(string) error
	Close() error
}

type containerzCountingWriter struct {
	containerzTemporaryFile
	bytesWritten uint64
}

func (w *containerzCountingWriter) Write(data []byte) (int, error) {
	n, err := w.containerzTemporaryFile.Write(data)
	w.bytesWritten += uint64(n)
	return n, err
}

type containerzDeployDependencies struct {
	authenticate      func(*Config, context.Context, string, bool) (context.Context, error)
	createTempFile    func(string, string) (containerzTemporaryFile, error)
	downloadRemote    func(context.Context, download.Request, io.Writer) error
	newImageLoader    func() (containerzImageLoader, error)
	removeFile        func(string) error
	translateHostPath func(string) string
}

func (c *ContainerzServer) resolvedDeployDependencies() containerzDeployDependencies {
	if c.deployDependencies != nil {
		return *c.deployDependencies
	}
	return defaultContainerzDeployDependencies()
}

// Deploy downloads a container image and asks HostService to load it.
func (c *ContainerzServer) Deploy(
	stream gnoi_containerz_pb.Containerz_DeployServer,
) (result error) {
	log.V(2).Info("gNOI: Containerz Deploy called")

	ctx := stream.Context()
	dependencies := c.resolvedDeployDependencies()

	// Authenticate the client using the server's config.
	_, err := dependencies.authenticate(c.server.config, ctx, "gnoi", true)
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

	downloadRequest, err := newContainerzDownloadRequest(imageTransfer)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid remote download request: %v", err)
	}
	log.V(2).Infof(
		"gNOI: Containerz downloading image with protocol %s",
		downloadRequest.Protocol,
	)

	const hostTempDirectory = "/tmp"
	tempFile, err := dependencies.createTempFile(
		dependencies.translateHostPath(hostTempDirectory),
		"containerz-image-*.tar",
	)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create temporary image file: %v", err)
	}
	localPath := tempFile.Name()
	hostVisiblePath := filepath.Join(hostTempDirectory, filepath.Base(localPath))
	fileClosed := false
	cleanupPending := true
	defer func() {
		if !cleanupPending {
			return
		}
		if cleanupErr := dependencies.removeFile(localPath); cleanupErr != nil {
			result = appendContainerzInternalFailure(
				result,
				"failed to remove temporary image file",
				cleanupErr,
			)
		}
	}()
	defer func() {
		if fileClosed {
			return
		}
		if closeErr := tempFile.Close(); closeErr != nil {
			result = appendContainerzInternalFailure(
				result,
				"failed to close temporary image file",
				closeErr,
			)
		}
	}()

	downloadWriter := &containerzCountingWriter{containerzTemporaryFile: tempFile}
	if err := dependencies.downloadRemote(ctx, downloadRequest, downloadWriter); err != nil {
		code := codes.Internal
		if errors.Is(err, download.ErrInvalidRequest) {
			code = codes.InvalidArgument
		}
		return status.Errorf(code, "failed to download image: %v", err)
	}
	err = tempFile.Close()
	fileClosed = true
	if err != nil {
		return status.Errorf(codes.Internal, "failed to close temporary image file: %v", err)
	}
	log.V(2).Info("gNOI: Containerz image download completed")

	imageLoader, err := dependencies.newImageLoader()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create D-Bus client: %v", err)
	}
	imageLoaderClosed := false
	defer func() {
		if imageLoaderClosed {
			return
		}
		if closeErr := imageLoader.Close(); closeErr != nil {
			result = appendContainerzInternalFailure(
				result,
				"failed to close D-Bus client",
				closeErr,
			)
		}
	}()
	if err := imageLoader.LoadDockerImage(hostVisiblePath); err != nil {
		return status.Errorf(codes.Internal, "failed to load docker image: %v", err)
	}
	closeErr := imageLoader.Close()
	imageLoaderClosed = true
	if closeErr != nil {
		return status.Errorf(codes.Internal, "failed to close D-Bus client: %v", closeErr)
	}
	log.V(2).Info("gNOI: Containerz image load completed")

	if err := dependencies.removeFile(localPath); err != nil {
		cleanupPending = false
		return status.Errorf(codes.Internal, "failed to remove temporary image file: %v", err)
	}
	cleanupPending = false

	resp := &gnoi_containerz_pb.DeployResponse{
		Response: &gnoi_containerz_pb.DeployResponse_ImageTransferSuccess{
			ImageTransferSuccess: &gnoi_containerz_pb.ImageTransferSuccess{
				Name:      imageTransfer.Name,
				Tag:       imageTransfer.Tag,
				ImageSize: downloadWriter.bytesWritten,
			},
		},
	}
	if err := stream.Send(resp); err != nil {
		return status.Errorf(codes.Internal, "failed to send DeployResponse: %v", err)
	}

	return nil
}

func newContainerzDownloadRequest(
	imageTransfer *gnoi_containerz_pb.ImageTransfer,
) (download.Request, error) {
	remoteDownload := imageTransfer.GetRemoteDownload()
	if remoteDownload == nil {
		return download.Request{}, errors.New("RemoteDownload is required")
	}

	var protocol download.Protocol
	switch remoteDownload.GetProtocol() {
	case gnoi_common_pb.RemoteDownload_SFTP:
		protocol = download.ProtocolSFTP
	case gnoi_common_pb.RemoteDownload_HTTP:
		protocol = download.ProtocolHTTP
	case gnoi_common_pb.RemoteDownload_HTTPS:
		protocol = download.ProtocolHTTPS
	case gnoi_common_pb.RemoteDownload_SCP:
		protocol = download.ProtocolSCP
	default:
		return download.Request{}, errors.New("unsupported protocol")
	}

	if imageTransfer.GetImageSize() > math.MaxInt64 {
		return download.Request{}, errors.New("image size exceeds supported range")
	}

	request := download.Request{
		Protocol: protocol,
		Path:     remoteDownload.GetPath(),
		MaxSize:  int64(imageTransfer.GetImageSize()),
	}
	if credentials := remoteDownload.GetCredentials(); credentials != nil {
		request.Username = credentials.GetUsername()
		switch password := credentials.Password.(type) {
		case nil:
		case *gnoi_types_pb.Credentials_Cleartext:
			if password != nil {
				request.Password = password.Cleartext
			}
		case *gnoi_types_pb.Credentials_Hashed:
			return download.Request{}, errors.New("hashed credentials are not supported")
		default:
			return download.Request{}, errors.New("unsupported credential type")
		}
	}

	if err := download.ValidateRemoteRequest(request); err != nil {
		return download.Request{}, err
	}
	return request, nil
}

func appendContainerzInternalFailure(current error, message string, cause error) error {
	failure := fmt.Sprintf("%s: %v", message, cause)
	if current == nil {
		return status.Error(codes.Internal, failure)
	}
	if currentStatus, ok := status.FromError(current); ok {
		return status.Errorf(codes.Internal, "%s; %s", currentStatus.Message(), failure)
	}
	return status.Errorf(codes.Internal, "%v; %s", current, failure)
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
