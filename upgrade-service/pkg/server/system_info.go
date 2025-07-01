package server

import (
	"context"
	"os"

	"github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/diskspace"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/hostinfo"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/paths"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

// SystemInfoServer implements the SystemInfo gRPC service.
type SystemInfoServer struct {
	pb.UnimplementedSystemInfoServer
}

// NewSystemInfoServer creates a new instance of SystemInfoServer.
func NewSystemInfoServer() *SystemInfoServer {
	return &SystemInfoServer{}
}

// GetPlatformType implements the GetPlatformType RPC method.
func (s *SystemInfoServer) GetPlatformType(
	ctx context.Context, req *pb.GetPlatformTypeRequest,
) (*pb.GetPlatformTypeResponse, error) {
	glog.V(1).Info("GetPlatformType request received")

	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	default:
	}

	// Get platform information from the host
	glog.V(2).Info("Retrieving platform information from host")
	platformInfo, err := hostinfo.GetPlatformInfo()
	if err != nil {
		glog.Errorf("Error getting platform info: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get platform information: %v", err)
	}

	// Get the platform identifier, vendor and model as strings
	glog.V(2).Info("Extracting platform identifier from platform info")
	platformIdentifier, vendor, model := hostinfo.GetPlatformIdentifierString(platformInfo)

	glog.V(1).Infof("GetPlatformType response: platform=%s, vendor=%s, model=%s", platformIdentifier, vendor, model)
	return &pb.GetPlatformTypeResponse{
		PlatformIdentifier: platformIdentifier,
		Vendor:             vendor,
		Model:              model,
	}, nil
}

// GetDiskSpace implements the GetDiskSpace RPC method.
func (s *SystemInfoServer) GetDiskSpace(
	ctx context.Context, req *pb.GetDiskSpaceRequest,
) (*pb.GetDiskSpaceResponse, error) {
	glog.V(1).Info("GetDiskSpace request received")

	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	default:
	}

	// Determine paths to check
	var pathsToCheck []string
	if len(req.Paths) > 0 {
		// Use custom paths provided in request
		pathsToCheck = req.Paths
		glog.V(2).Infof("Using custom paths: %v", pathsToCheck)
	} else {
		// Use default paths
		pathsToCheck = []string{"/", "/host", "/tmp"}
		glog.V(2).Info("Using default paths: /, /host, /tmp")
	}

	filesystems := make([]*pb.GetDiskSpaceResponse_DiskSpaceInfo, 0, len(pathsToCheck))

	for _, path := range pathsToCheck {
		diskInfo := &pb.GetDiskSpaceResponse_DiskSpaceInfo{
			Path: path,
		}

		// Resolve path if we have config available
		resolvedPath := path
		if config.Global != nil && config.Global.RootFS != "" {
			resolvedPath = paths.ToHost(path, config.Global.RootFS)
		}

		// Check if path exists before trying to get disk space
		if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
			glog.V(2).Infof("Path %s does not exist (resolved: %s), skipping", path, resolvedPath)
			diskInfo.ErrorMessage = "path does not exist"
			filesystems = append(filesystems, diskInfo)
			continue
		}

		// Get total space
		totalMB, err := diskspace.GetDiskTotalSpaceMB(resolvedPath)
		if err != nil {
			glog.V(2).Infof("Failed to get total space for %s: %v", path, err)
			diskInfo.ErrorMessage = err.Error()
			filesystems = append(filesystems, diskInfo)
			continue
		}

		// Get free space
		freeMB, err := diskspace.GetDiskFreeSpaceMB(resolvedPath)
		if err != nil {
			glog.V(2).Infof("Failed to get free space for %s: %v", path, err)
			diskInfo.ErrorMessage = err.Error()
			filesystems = append(filesystems, diskInfo)
			continue
		}

		// Calculate used space
		usedMB := totalMB - freeMB

		// Populate the successful result
		diskInfo.TotalMb = totalMB
		diskInfo.FreeMb = freeMB
		diskInfo.UsedMb = usedMB
		diskInfo.ErrorMessage = "" // Clear any error message

		glog.V(2).Infof("Disk space for %s: total=%dMB, free=%dMB, used=%dMB",
			path, totalMB, freeMB, usedMB)

		filesystems = append(filesystems, diskInfo)
	}

	response := &pb.GetDiskSpaceResponse{
		Filesystems: filesystems,
	}

	glog.V(1).Infof("GetDiskSpace response: checked %d filesystems", len(filesystems))
	return response, nil
}
