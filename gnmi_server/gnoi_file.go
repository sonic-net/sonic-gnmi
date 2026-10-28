package gnmi

import (
	"context"

	log "github.com/golang/glog"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	gnoifile "github.com/sonic-net/sonic-gnmi/pkg/gnoi/file"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func authorizeFileRead(config *Config, ctx context.Context) (context.Context, error) {
	ctx, err := authenticate(config, ctx, "gnoi", false)
	if err != nil {
		return ctx, err
	}
	if isUnixPeer(ctx) {
		return ctx, nil
	}

	rc, ctx := common_utils.GetContext(ctx)
	if !rc.Auth.AuthEnabled {
		return ctx, status.Error(codes.Unauthenticated, "File read RPCs require application authentication over TCP")
	}
	if err := checkRequiredRoleAccess(&rc.Auth, "gnoi", false); err != nil {
		return ctx, status.Error(codes.PermissionDenied, err.Error())
	}
	return ctx, nil
}

func (srv *FileServer) Stat(ctx context.Context, req *gnoi_file_pb.StatRequest) (*gnoi_file_pb.StatResponse, error) {
	log.Infof("GNOI File Stat RPC called with request: %+v", req)
	ctx, err := authorizeFileRead(srv.config, ctx)
	if err != nil {
		log.Errorf("File Stat access denied: %v", err)
		return nil, err
	}
	return gnoifile.HandleStat(ctx, req)
}

// Get streams a host file after authenticating and authorizing the caller.
func (srv *FileServer) Get(req *gnoi_file_pb.GetRequest, stream gnoi_file_pb.File_GetServer) error {
	log.Infof("GNOI File Get RPC called with request: %+v", req)
	_, err := authorizeFileRead(srv.config, stream.Context())
	if err != nil {
		log.Errorf("File Get access denied: %v", err)
		return err
	}
	return gnoifile.HandleGet(req, stream)
}

// TransferToRemote downloads a file from a remote URL.
// If DPU headers are present (HandleOnNPU mode), it downloads to NPU then uploads to the specified DPU.
func (srv *FileServer) TransferToRemote(ctx context.Context, req *gnoi_file_pb.TransferToRemoteRequest) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	log.Infof("GNOI File TransferToRemote RPC called with request: %+v", req)
	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		log.Errorf("authentication failed in TransferToRemote RPC: %v", err)
		return nil, err
	}

	// Delegate all logic to the pure handler function
	return gnoifile.HandleTransferToRemote(ctx, req)
}

// Put implements the gNOI File.Put RPC.
// It authenticates the request and delegates to the pure Go handler.
func (srv *FileServer) Put(stream gnoi_file_pb.File_PutServer) error {
	log.Infof("GNOI File Put RPC called")
	_, err := authenticate(srv.config, stream.Context(), "gnoi", true)
	if err != nil {
		log.Errorf("authentication failed in Put RPC: %v", err)
		return err
	}
	return gnoifile.HandlePut(stream)
}

// Remove implements the corresponding RPC.
func (srv *FileServer) Remove(ctx context.Context, req *gnoi_file_pb.RemoveRequest) (*gnoi_file_pb.RemoveResponse, error) {
	log.Infof("GNOI File Remove RPC called with request: %+v", req)
	if req == nil {
		log.Errorf("Nil request received")
		return nil, status.Error(codes.InvalidArgument, "Invalid nil request.")
	}
	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		log.Errorf("authentication failed in Remove RPC: %v", err)
		return nil, err
	}
	// Delegate to handler (all logic except authentication is in the handler)
	return gnoifile.HandleFileRemove(ctx, req)
}
