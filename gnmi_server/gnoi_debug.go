package gnmi

import (
	"context"

	log "github.com/golang/glog"
	debugpb "github.com/openconfig/gnoi/debug"
	debugservice "github.com/sonic-net/sonic-gnmi/pkg/gnoi/debug"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var handleDebugRequest = debugservice.HandleCommandRequest

func (srv *DebugServer) Debug(req *debugpb.DebugRequest, stream debugpb.Debug_DebugServer) error {
	access, err := debugAccessLevel(stream.Context())
	if err != nil {
		log.Errorf("transport authorization failed in Debug RPC: %v", err)
		return err
	}

	log.Infof("gNOI Debug RPC called over Unix domain socket")
	return handleDebugRequest(req, stream, srv.policy, access)
}

func debugAccessLevel(ctx context.Context) (debugservice.AccessLevel, error) {
	if !isUnixPeer(ctx) {
		return 0, status.Error(codes.PermissionDenied, "Debug is available only over the Unix domain socket")
	}
	return debugservice.AccessReadWrite, nil
}
