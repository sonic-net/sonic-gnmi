package gnmi

import (
	"context"

	"github.com/Azure/sonic-mgmt-common/translib/transformer"
	"github.com/openconfig/gnoi/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GNOIFileServer implements the gnoi.file.File service.
type GNOIFileServer struct {
	*Server
}

// NewGNOIFileServer returns initialized GNOIFileServer structure.
func NewGNOIFileServer(srv *Server) *GNOIFileServer {
	return &GNOIFileServer{
		Server: srv,
	}
}

// Get RPC is unimplemented.
func (srv *GNOIFileServer) Get(req *file.GetRequest, stream file.File_GetServer) error {
	return status.Errorf(codes.Unimplemented, "Method file.Get is unimplemented.")
}

// TransferToRemote RPC is unimplemented.
func (srv *GNOIFileServer) TransferToRemote(ctx context.Context, req *file.TransferToRemoteRequest) (*file.TransferToRemoteResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "Method file.TransferToRemote is unimplemented.")
}

// Put RPC is unimplemented.
func (srv *GNOIFileServer) Put(stream file.File_PutServer) error {
	return status.Errorf(codes.Unimplemented, "Method file.Put is unimplemented.")
}

// Stat RPC is unimplemented.
func (srv *GNOIFileServer) Stat(ctx context.Context, req *file.StatRequest) (*file.StatResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "Method file.Stat is unimplemented.")
}

// Remove implements the corresponding RPC.
func (srv *GNOIFileServer) Remove(ctx context.Context, req *file.RemoveRequest) (*file.RemoveResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid nil request.")
	}
	if req.GetRemoteFile() == "" {
		return nil, status.Error(codes.InvalidArgument, "Invalid request: remote_file field is empty.")
	}
	if _, err := transformer.FileRemove(req.GetRemoteFile()); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &file.RemoveResponse{}, nil
}
