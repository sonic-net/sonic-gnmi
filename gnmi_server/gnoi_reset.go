package gnmi

import (
	"context"
	"fmt"

	log "github.com/golang/glog"
	"github.com/openconfig/gnoi/factory_reset"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	json "google.golang.org/protobuf/encoding/protojson"
)

// Start implements the corresponding RPC.
func (srv *Server) Start(ctx context.Context, req *factory_reset.StartRequest) (*factory_reset.StartResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: factory_reset.Start")

	// Front end marshals the request, and sends to the sonic-host-service.
	// Back end is expected to return the response in JSON format.
	reqStr, err := json.Marshal(req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Cannot marshal the StartRequest: [%s], err %v", req.String(), err))
	}

	sc, err := ssc.NewDbusClient(&ssc.DbusCaller{})
	if err != nil {
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Error creating dbus client: [%s]", err.Error()))
	}

	respStr, err := sc.FactoryReset(string(reqStr))
	log.V(1).Infof("gNOI: factory_reset.Start Response %s", respStr)
	if err != nil {
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Backend error: %v", err))
	}

	resp := &factory_reset.StartResponse{}
	if err := json.Unmarshal([]byte(respStr), resp); err != nil {
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Cannot unmarshal backend response: [%s], error: %v", respStr, err))
	}
	return resp, nil
}
