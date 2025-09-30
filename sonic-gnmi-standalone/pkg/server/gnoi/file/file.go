package file

import (
	"context"
	"github.com/openconfig/gnoi/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
)

// FileServer implements the gNOI File service.
type FileServer struct {
	file.UnimplementedFileServer
}

// Remove deletes the specified file from the filesystem.
func (s *FileServer) Remove(ctx context.Context, req *file.RemoveRequest) (*file.RemoveResponse, error) {
	path := req.RemoteFile
	if path == "" {
		return nil, status.Error(codes.InvalidArgument, "path must not be empty")
	}
	err := os.Remove(path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to remove file: %v", err)
	}
	return &file.RemoveResponse{}, nil
}

func (s *FileServer) Get(req *file.GetRequest, stream grpc.ServerStreamingServer[file.GetResponse]) error {
	// Not implemented yet
	return nil
}

func (s *FileServer) TransferToRemote(
	ctx context.Context,
	req *file.TransferToRemoteRequest,
) (*file.TransferToRemoteResponse, error) {
	// Not implemented yet
	return nil, nil
}

func (s *FileServer) Put(stream grpc.ClientStreamingServer[file.PutRequest, file.PutResponse]) error {
	// Not implemented yet
	return nil
}

func (s *FileServer) Stat(ctx context.Context, req *file.StatRequest) (*file.StatResponse, error) {
	// Not implemented yet
	return nil, nil
}
