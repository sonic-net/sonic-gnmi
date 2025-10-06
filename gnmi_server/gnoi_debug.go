package gnmi

import (
	log "github.com/golang/glog"
	gnoi_debug "github.com/sonic-net/sonic-gnmi/pkg/gnoi/debug"
	gnoi_debug_pb "github.com/sonic-net/sonic-gnmi/proto/gnoi/debug"
)

func (srv *DebugServer) Debug(req *gnoi_debug_pb.DebugRequest, stream gnoi_debug_pb.Debug_DebugServer) error {
	log.Infof("GNOI Debug RPC called with request: %+v", req)
	_, err := authenticate(srv.config, stream.Context(), "gnoi", true)
	if err != nil {
		log.Errorf("authentication failed in Debug RPC: %v", err)
		return err
	}

	return gnoi_debug.HandleCommandRequest(req, stream)
}
