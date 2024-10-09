package gnmi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sonic-net/sonic-gnmi/common_utils"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"github.com/go-redis/redis"
	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	syspb "github.com/openconfig/gnoi/system"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pjson "google.golang.org/protobuf/encoding/protojson"
)

const (
	rebootKey           = "Reboot"
	rebootStatusKey     = "RebootStatus"
	rebootCancelKey     = "CancelReboot"
	rebootReqCh         = "Reboot_Request_Channel"
	rebootRespCh        = "Reboot_Response_Channel"
	dataMsgFld          = "MESSAGE"
	notificationTimeout = 10 * time.Second
)

// Vaild reboot method map.
var validRebootMap = map[syspb.RebootMethod]bool{
	syspb.RebootMethod_COLD:      true,
	syspb.RebootMethod_WARM:      true,
	syspb.RebootMethod_POWERDOWN: true,
	syspb.RebootMethod_NSF:       true,
}

// Validates reboot request.
func validRebootReq(req *syspb.RebootRequest) error {
	if _, ok := validRebootMap[req.GetMethod()]; !ok {
		log.Error("Invalid request: reboot method is not supported.")
		return fmt.Errorf("Invalid request: reboot method is not supported.")
	}
	// Back end does not support delayed reboot request.
	if req.GetDelay() > 0 {
		log.Error("Invalid request: reboot is not immediate.")
		return fmt.Errorf("Invalid request: reboot is not immediate.")
	}
	if req.GetMessage() == "" {
		log.Error("Invalid request: message is empty.")
		return fmt.Errorf("Invalid request: message is empty.")
	}

	if len(req.GetSubcomponents()) == 0 {
		return nil
	}

	return nil
}

func KillOrRestartProcess(restart bool, serviceName string) error {
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return err
	}
	if restart {
		log.V(2).Infof("Restarting service %s...", serviceName)
		err = sc.RestartService(serviceName)
		if err != nil {
			log.V(2).Infof("Failed to restart service %s: %v", serviceName, err)
		}
	} else {
		log.V(2).Infof("Stopping service %s...", serviceName)
		err = sc.StopService(serviceName)
		if err != nil {
			log.V(2).Infof("Failed to stop service %s: %v", serviceName, err)
		}
	}
	return err
}

func (srv *Server) KillProcess(ctx context.Context, req *syspb.KillProcessRequest) (*syspb.KillProcessResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}

	serviceName := req.GetName()
	restart := req.GetRestart()
        if req.GetPid() != 0 {
            return nil, status.Errorf(codes.Unimplemented, "Pid option is not implemented")
        }
        if req.GetSignal() != syspb.KillProcessRequest_SIGNAL_TERM {
            return nil, status.Errorf(codes.Unimplemented, "KillProcess only supports SIGNAL_TERM (option 1) for graceful process termination. Please specify SIGNAL_TERM")
        }
	log.V(1).Info("gNOI: KillProcess with optional restart")
	log.V(1).Info("Request: ", req)
	err = KillOrRestartProcess(restart, serviceName)
	if err != nil {
		return nil, err
	}
	var resp syspb.KillProcessResponse
	return &resp, nil
}

// Processes message payload as op, data, field-value pairs.
func processMsgPayload(pload string) (string, string, map[string]string, error) {
	var payload []string
	if err := json.Unmarshal([]byte(pload), &payload); err != nil {
		log.V(0).Info(err.Error())
		return "", "", nil, err
	}

	if len(payload) < 2 || len(payload)%2 != 0 {
		return "", "", nil, fmt.Errorf("Payload is malformed: %v\n", strings.Join(payload, ","))
	}

	op := payload[0]
	data := payload[1]
	fvs := map[string]string{}
	for i := 2; i < len(payload); i += 2 {
		fvs[payload[i]] = payload[i+1]
	}
	return op, data, fvs, nil
}

// Converts a SWSS error code string into a gRPC code.
func swssToErrorCode(statusStr string) codes.Code {
	switch statusStr {
	case "SWSS_RC_SUCCESS":
		return codes.OK
	case "SWSS_RC_UNKNOWN":
		return codes.Unknown
	case "SWSS_RC_IN_USE", "SWSS_RC_INVALID_PARAM":
		return codes.InvalidArgument
	case "SWSS_RC_DEADLINE_EXCEEDED":
		return codes.DeadlineExceeded
	case "SWSS_RC_NOT_FOUND":
		return codes.NotFound
	case "SWSS_RC_EXISTS":
		return codes.AlreadyExists
	case "SWSS_RC_PERMISSION_DENIED":
		return codes.PermissionDenied
	case "SWSS_RC_FULL", "SWSS_RC_NO_MEMORY":
		return codes.ResourceExhausted
	case "SWSS_RC_UNIMPLEMENTED":
		return codes.Unimplemented
	case "SWSS_RC_INTERNAL":
		return codes.Internal
	case "SWSS_RC_NOT_EXECUTED", "SWSS_RC_FAILED_PRECONDITION":
		return codes.FailedPrecondition
	case "SWSS_RC_UNAVAIL":
		return codes.Unavailable
	}
	return codes.Internal
}

func sendRebootReqOnNotifCh(ctx context.Context, req proto.Message, sc *redis.Client, rebootNotifKey string) (resp proto.Message, err error, msgDataStr string) {
	np, err := common_utils.NewNotificationProducer(rebootReqCh)
	if err != nil {
		log.V(1).Infof("[Reboot_Log] Error in setting up NewNotificationProducer: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}
	defer np.Close()

	// Subscribe to the response channel.
	sub := sc.Subscribe(rebootRespCh)
	if _, err = sub.Receive(); err != nil {
		log.V(1).Infof("[Reboot_Log] Error in setting up subscription to response channel: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}
	defer sub.Close()
	channel := sub.Channel()

	switch rebootNotifKey {
	case rebootKey:
		req = req.(*syspb.RebootRequest)
		resp = &syspb.RebootResponse{}
	case rebootStatusKey:
		req = req.(*syspb.RebootStatusRequest)
		resp = &syspb.RebootStatusResponse{}
	case rebootCancelKey:
		req = req.(*syspb.CancelRebootRequest)
		resp = &syspb.CancelRebootResponse{}
	}

	reqStr, err := json.Marshal(req)
	if err != nil {
		log.V(1).Infof("[Reboot_Log] Error in marshalling JSON: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}
	// Publish to notification channel.
	if err := np.Send(rebootNotifKey, "", map[string]string{dataMsgFld: string(reqStr)}); err != nil {
		log.V(1).Infof("[Reboot_Log] Error in publishing to notification channel: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}

	// Wait for response on Reboot_Response_Channel.
	tc := time.After(notificationTimeout)
	for {
		select {
		case msg := <-channel:
			op, data, fvs, err := processMsgPayload(msg.Payload)
			if err != nil {
				log.V(1).Infof("[Reboot_Log] Error while receiving Response Notification = [%v] for message [%v]", err.Error(), msg)
				return nil, status.Errorf(codes.Internal, fmt.Sprintf("Error while receiving Response Notification: [%s] for message [%s]", err.Error(), msg)), msgDataStr
			}
			log.V(1).Infof("[Reboot_Log] Received on the Reboot notification channel: op = [%v], data = [%v], fvs = [%v]", op, data, fvs)

			if op != rebootNotifKey {
				log.V(1).Infof("[Reboot_Log] Op: [%v] doesn't match for `%v`!", op, rebootNotifKey)
				continue
			}
			if fvs != nil {
				if _, ok := fvs[dataMsgFld]; ok {
					msgDataStr = fvs[dataMsgFld]
				}
			}
			if swssCode := swssToErrorCode(data); swssCode != codes.OK {
				log.V(1).Infof("[Reboot_Log] Response Notification returned SWSS Error code: %v, error = %v", swssCode, msgDataStr)
				return nil, status.Errorf(swssCode, "Response Notification returned SWSS Error code: "+msgDataStr), msgDataStr
			}
			return resp, nil, msgDataStr

		case <-tc:
			// Crossed the reboot response notification timeout.
			log.V(1).Infof("[Reboot_Log] Response Notification timeout from Warmboot Manager!")
			return nil, status.Errorf(codes.Internal, "Response Notification timeout from Warmboot Manager!"), msgDataStr
		}
	}
}

// Reboot implements the corresponding RPC.
func (srv *Server) Reboot(ctx context.Context, req *syspb.RebootRequest) (*syspb.RebootResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(2).Info("gNOI: Reboot")
	if err := validRebootReq(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, err.Error())
	}
	// Initialize State DB.
	rclient, err := common_utils.GetRedisDBClient()
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	defer rclient.Close()

	// Module reset.
	if len(req.GetSubcomponents()) > 0 {
		return &syspb.RebootResponse{}, nil
	}

	// System reboot.
	resp, err, _ := sendRebootReqOnNotifCh(ctx, req, rclient, rebootKey)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		log.V(2).Info("Reboot request received empty response from Warmboot Manager.")
		return &syspb.RebootResponse{}, nil
	}
	return resp.(*syspb.RebootResponse), nil
}

// RebootStatus implements the corresponding RPC.
func (srv *Server) RebootStatus(ctx context.Context, req *syspb.RebootStatusRequest) (*syspb.RebootStatusResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: RebootStatus")
	resp := &syspb.RebootStatusResponse{}
	// Initialize State DB.
	rclient, err := common_utils.GetRedisDBClient()
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	defer rclient.Close()

	respStr, err, msgData := sendRebootReqOnNotifCh(ctx, req, rclient, rebootStatusKey)
	if err != nil {
		log.V(1).Infof("gNOI: Received error for RebootStatusResponse: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	if msgData == "" || respStr == nil {
		log.V(1).Info("gNOI: Received empty RebootStatusResponse")
		return nil, status.Errorf(codes.Internal, "Received empty RebootStatusResponse")
	}
	if err := pjson.Unmarshal([]byte(msgData), resp); err != nil {
		log.V(1).Infof("gNOI: Cannot unmarshal the response: [%v]; err: [%v]", msgData, err)
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Cannot unmarshal the response: [%s]; err: [%s]", msgData, err.Error()))
	}
	log.V(1).Infof("gNOI: Returning RebootStatusResponse: resp = [%v]\n, msgData = [%v]", resp, msgData)
	return resp, nil
}

// CancelReboot RPC implements the corresponding RPC.
func (srv *Server) CancelReboot(ctx context.Context, req *syspb.CancelRebootRequest) (*syspb.CancelRebootResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: CancelReboot")
	if req.GetMessage() == "" {
		log.V(1).Info("Invalid CancelReboot request: message is empty.")
		return nil, status.Errorf(codes.Internal, "Invalid CancelReboot request: message is empty.")
	}
	// Initialize State DB.
	rclient, err := common_utils.GetRedisDBClient()
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	defer rclient.Close()

	resp, err, _ := sendRebootReqOnNotifCh(ctx, req, rclient, rebootCancelKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	if resp == nil {
		return &syspb.CancelRebootResponse{}, nil
	}
	return resp.(*syspb.CancelRebootResponse), err
}

// Ping is unimplemented.
func (srv *Server) Ping(req *syspb.PingRequest, stream syspb.System_PingServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: Ping")
	return status.Errorf(codes.Unimplemented, "Method system.Ping is unimplemented.")
}

// Traceroute is unimplemented.
func (srv *Server) Traceroute(req *syspb.TracerouteRequest, stream syspb.System_TracerouteServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: Traceroute")
	return status.Errorf(codes.Unimplemented, "Method system.Traceroute is unimplemented.")
}

// SetPackage is unimplemented.
func (srv *Server) SetPackage(stream syspb.System_SetPackageServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: SetPackage")
	return status.Errorf(codes.Unimplemented, "Method system.SetPackage is unimplemented.")
}

// SwitchControlProcessor implements the corresponding RPC.
func (srv *Server) SwitchControlProcessor(ctx context.Context, req *syspb.SwitchControlProcessorRequest) (*syspb.SwitchControlProcessorResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: SwitchControlProcessor")
	return &syspb.SwitchControlProcessorResponse{}, nil
}

// Time implements the corresponding RPC.
func (srv *Server) Time(ctx context.Context, req *syspb.TimeRequest) (*syspb.TimeResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Time")
	var tm syspb.TimeResponse
	tm.Time = uint64(time.Now().UnixNano())
	return &tm, nil
}