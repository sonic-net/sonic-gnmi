package server

import (
	"context"
	"log"

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
	// Get platform information from the host
	platformInfo, err := s.platformProvider.GetPlatformInfo(ctx)
	if err != nil {
		log.Printf("Error getting platform info: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get platform information: %v", err)
	}

	// Get the platform identifier, vendor and model as strings
	platformIdentifier, vendor, model := s.platformProvider.GetPlatformIdentifier(ctx, platformInfo)

	return &pb.GetPlatformTypeResponse{
		PlatformIdentifier: platformIdentifier,
		Vendor:             vendor,
		Model:              model,
	}, nil
}
