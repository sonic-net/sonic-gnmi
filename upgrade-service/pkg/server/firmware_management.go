package server

import (
	"context"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/firmware"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

type firmwareManagementServer struct {
	pb.UnimplementedFirmwareManagementServer
}

func NewFirmwareManagementServer() pb.FirmwareManagementServer {
	return &firmwareManagementServer{}
}

func (s *firmwareManagementServer) CleanupOldFirmware(
	ctx context.Context,
	req *pb.CleanupOldFirmwareRequest,
) (*pb.CleanupOldFirmwareResponse, error) {
	result := firmware.CleanupOldFirmware()

	return &pb.CleanupOldFirmwareResponse{
		FilesDeleted:    result.FilesDeleted,
		DeletedFiles:    result.DeletedFiles,
		Errors:          result.Errors,
		SpaceFreedBytes: result.SpaceFreedBytes,
	}, nil
}
