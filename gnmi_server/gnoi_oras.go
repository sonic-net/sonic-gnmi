package gnmi

import (
	log "github.com/golang/glog"

	gnoioras "github.com/sonic-net/sonic-gnmi/pkg/gnoi/oras"
	gnoi_oras_pb "github.com/sonic-net/sonic-gnmi/proto/gnoi/oras"
)

// Pull is the entry point for the SONiC ORAS Pull RPC. Authentication is
// applied the same way as the other gNOI services on this server; the actual
// pull logic lives in pkg/gnoi/oras.
func (srv *OrasServer) Pull(req *gnoi_oras_pb.PullRequest, stream gnoi_oras_pb.Oras_PullServer) error {
	log.Infof("GNOI Oras Pull RPC called with registry=%s repository=%s ref=%s",
		req.GetRegistry(), req.GetRepository(), pullRefDescription(req))
	if _, err := authenticate(srv.config, stream.Context(), "gnoi", true); err != nil {
		log.Errorf("authentication failed in Oras.Pull RPC: %v", err)
		return err
	}
	return gnoioras.HandlePull(req, stream)
}

func pullRefDescription(req *gnoi_oras_pb.PullRequest) string {
	if d := req.GetDigest(); d != "" {
		return d
	}
	return req.GetTag()
}
