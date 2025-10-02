package gnmi

import (
	"context"
	"os"
	"strconv"
	"strings"

	log "github.com/golang/glog"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (srv *FileServer) Stat(ctx context.Context, req *gnoi_file_pb.StatRequest) (*gnoi_file_pb.StatResponse, error) {
	log.Infof("GNOI File Stat RPC called with request: %+v", req)
	_, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		log.Errorf("authentication failed in Stat RPC: %v", err)
		return nil, err
	}
	path := req.GetPath()
	log.V(1).Info("Request: ", req)
	statInfo, err := readFileStat(path)
	if err != nil {
		log.Errorf("readFileStat error: %v", err)
		return nil, err
	}
	resp := &gnoi_file_pb.StatResponse{
		Stats: []*gnoi_file_pb.StatInfo{statInfo},
	}
	return resp, nil
}

func readFileStat(path string) (*gnoi_file_pb.StatInfo, error) {
	sc, err := ssc.NewDbusClient()
	if err != nil {
		log.Errorf("DbusClient init failed: %v", err)
		return nil, status.Errorf(codes.Internal, "DBus client init failed: %v", err)
	}
	defer sc.Close()
	data, err := sc.GetFileStat(path)
	if err != nil {
		log.V(2).Infof("Failed to read file stat at path %s: %v. Error ", path, err)
		return nil, err
	}
	// Parse the data and populate StatInfo
	lastModified, err := strconv.ParseUint(data["last_modified"], 10, 64)
	if err != nil {
		log.Errorf("Stat Fails on Invalid last_modified %v", err)
		return nil, err
	}

	permissions, err := strconv.ParseUint(data["permissions"], 8, 32)
	if err != nil {
		log.Errorf("Stat Fails on Invalid permissions: %v", err)
		return nil, err
	}

	size, err := strconv.ParseUint(data["size"], 10, 64)
	if err != nil {
		log.Errorf("Stat Fails on Invalid size: %v", err)
		return nil, err
	}

	umaskStr := data["umask"]
	if strings.HasPrefix(umaskStr, "o") {
		umaskStr = umaskStr[1:] // Remove leading "o"
	}
	umask, err := strconv.ParseUint(umaskStr, 8, 32)
	if err != nil {
		log.Errorf("Stat Fails on Invalid umaskStr: %v", err)
		return nil, err
	}

	statInfo := &gnoi_file_pb.StatInfo{
		Path:         data["path"],
		LastModified: lastModified,
		Permissions:  uint32(permissions),
		Size:         size,
		Umask:        uint32(umask),
	}
	return statInfo, nil
}

// Get RPC is unimplemented.
func (srv *FileServer) Get(req *gnoi_file_pb.GetRequest, stream gnoi_file_pb.File_GetServer) error {
	log.Infof("GNOI File Get RPC called with request: %+v", req)
	_, err := authenticate(srv.config, stream.Context(), "gnoi", false)
	if err != nil {
		log.Errorf("authentication failed in Get RPC: %v", err)
		return err
	}
	log.Warning("file.Get RPC is unimplemented")
	return status.Errorf(codes.Unimplemented, "Method file.Get is unimplemented.")
}

// TransferToRemote RPC is unimplemented.
func (srv *FileServer) TransferToRemote(ctx context.Context, req *gnoi_file_pb.TransferToRemoteRequest) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	log.Infof("GNOI File TransferToRemote RPC called with request: %+v", req)
	_, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		log.Errorf("authentication failed in TransferToRemote RPC: %v", err)
		return nil, err
	}
	log.Warning("file.TransferToRemote RPC is unimplemented")
	return nil, status.Errorf(codes.Unimplemented, "Method file.TransferToRemote is unimplemented.")
}

// Put RPC is unimplemented.
func (srv *FileServer) Put(stream gnoi_file_pb.File_PutServer) error {
	log.Infof("GNOI File Put RPC called")
	_, err := authenticate(srv.config, stream.Context(), "gnoi", false)
	if err != nil {
		log.Errorf("authentication failed in Put RPC: %v", err)
		return err
	}
	log.Warning("file.Put RPC is unimplemented")
	return status.Errorf(codes.Unimplemented, "Method file.Put is unimplemented.")
}

// Remove implements the corresponding RPC.
func (srv *FileServer) Remove(ctx context.Context, req *gnoi_file_pb.RemoveRequest) (*gnoi_file_pb.RemoveResponse, error) {
	log.Infof("GNOI File Remove RPC called with request: %+v", req)
	if req == nil {
		log.Errorf("Nil request received")
		return nil, status.Error(codes.InvalidArgument, "Invalid nil request.")
	}
	_, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		log.Errorf("authentication failed in Remove RPC: %v", err)
		return nil, err
	}
	if req.GetRemoteFile() == "" {
		log.Errorf("Invalid request: remote_file field is empty")
		return nil, status.Error(codes.InvalidArgument, "Invalid request: remote_file field is empty.")
	}

	// Use native Go to remove the file instead of Dbus client
	err = os.Remove(req.GetRemoteFile())
	if err != nil {
		log.Errorf("Remove RPC failed: %v", err)
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &gnoi_file_pb.RemoveResponse{}, nil
}
