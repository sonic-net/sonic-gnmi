package server

import (
	"context"

	"github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/hostinfo"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

// SystemInfoServer implements the SystemInfo gRPC service
type SystemInfoServer struct {
	pb.UnimplementedSystemInfoServer
	platformProvider hostinfo.PlatformInfoProvider
}

// NewSystemInfoServer creates a new instance of SystemInfoServer
func NewSystemInfoServer() *SystemInfoServer {
	return &SystemInfoServer{
		platformProvider: hostinfo.NewPlatformInfoProvider(),
	}
}

// NewSystemInfoServerWithProvider creates a new instance of SystemInfoServer with a custom provider
// This is useful for testing with mock providers
func NewSystemInfoServerWithProvider(provider hostinfo.PlatformInfoProvider) *SystemInfoServer {
	return &SystemInfoServer{
		platformProvider: provider,
	}
}

// GetPlatformType implements the GetPlatformType RPC method
func (s *SystemInfoServer) GetPlatformType(ctx context.Context, req *pb.GetPlatformTypeRequest) (*pb.GetPlatformTypeResponse, error) {
	glog.V(1).Info("GetPlatformType request received")

	// Get platform information from the host
	glog.V(2).Info("Retrieving platform information from host")
	platformInfo, err := s.platformProvider.GetPlatformInfo(ctx)
	if err != nil {
		glog.Errorf("Error getting platform info: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get platform information: %v", err)
	}

	// Get the platform identifier, vendor and model as strings
	glog.V(2).Info("Extracting platform identifier from platform info")
	platformIdentifier, vendor, model := s.platformProvider.GetPlatformIdentifier(ctx, platformInfo)

	glog.V(1).Infof("GetPlatformType response: platform=%s, vendor=%s, model=%s", platformIdentifier, vendor, model)
	return &pb.GetPlatformTypeResponse{
		PlatformIdentifier: platformIdentifier,
		Vendor:             vendor,
		Model:              model,
	}, nil
}
