package gnmi

import (
	"fmt"
	log "github.com/golang/glog"
	ospb "github.com/openconfig/gnoi/os"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	json "google.golang.org/protobuf/encoding/protojson"
)

// Activate implements correspondig RPC
func (srv *OSServer) Activate(ctx context.Context, req *ospb.ActivateRequest) (*ospb.ActivateResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: os.Activate")
	// Front end marshals the request, and sends to the sonic-host-service.
	// Back end is expected to return the response in JSON format.
	reqStr, err := json.Marshal(req)
	if err != nil {
		// TODO(b/328077908) Alarms to be implemented later
		// raiseAlarm(err)
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Cannot marshal the Activate request: [%s].", req.String()))
	}
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, err
	}
	respStr, err := sc.ActivateOS(string(reqStr))
	if err != nil {
		// TODO(b/328077908) Alarms to be implemented later
		// raiseAlarm(err)
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	resp := &ospb.ActivateResponse{}
	if err := json.Unmarshal([]byte(respStr), resp); err != nil {
		// TODO(b/328077908) Alarms to be implemented later
		// raiseAlarm(err)
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Cannot unmarshal the Activate response: [%s].", respStr))
	}
	return resp, nil
}
