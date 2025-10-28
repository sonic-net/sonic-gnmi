package gnmi

import (
	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	gnoi_debug "github.com/sonic-net/sonic-gnmi/pkg/gnoi/debug"
	gnoi_debug_pb "github.com/sonic-net/sonic-gnmi/proto/gnoi/debug"
)

func (srv *DebugServer) Debug(req *gnoi_debug_pb.DebugRequest, stream gnoi_debug_pb.Debug_DebugServer) error {
	// Log the user and the contents of the request
	username := "invalid"
	common_utils.GetUsername(stream.Context(), &username)
	log.Infof("gNOI Debug RPC called by '%s': %+v", username, req)

	_, readAccessErr := authenticate(srv.config, stream.Context(), "gnoi", false)
	if readAccessErr != nil {
		// User cannot do anything, abort
		log.Errorf("authentication failed in Debug RPC: %v", readAccessErr)
		return readAccessErr
	}

	_, writeAccessErr := authenticate(srv.config, stream.Context(), "gnoi", true)
	if writeAccessErr != nil {
		// User has read-only access
		return gnoi_debug.HandleCommandRequest(req, stream, srv.readWhitelist)
	}

	// Otherwise, this user has write access
	return gnoi_debug.HandleCommandRequest(req, stream, srv.writeWhitelist)
}
