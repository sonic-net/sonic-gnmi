package gnmi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sonic-net/sonic-gnmi/common_utils"

	log "github.com/golang/glog"
	syspb "github.com/openconfig/gnoi/system"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pjson "google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
}

// Validates reboot request.
func validRebootReq(req *syspb.RebootRequest) error {
	if _, ok := validRebootMap[req.GetMethod()]; !ok {
		log.V(0).Info("Invalid request: reboot method is not supported.")
		return fmt.Errorf("Invalid request: reboot method is not supported.")
	}
	// Back end does not support delayed reboot request.
	if req.GetDelay() > 0 {
		log.V(0).Info("Invalid request: reboot is not immediate.")
		return fmt.Errorf("Invalid request: reboot is not immediate.")
	}
	if req.GetMessage() == "" {
		log.V(0).Info("Invalid request: message is empty.")
		return fmt.Errorf("Invalid request: message is empty.")
	}

	return nil
}

func sendRebootReqOnNotifCh(ctx context.Context, req proto.Message, sc *redis.Client, rebootNotifKey string) (resp proto.Message, err error, msgDataStr string) {
	np, err := common_utils.NewNotificationProducer(rebootReqCh)
	if err != nil {
		log.V(2).Infof("[Reboot_Log] Error in setting up NewNotificationProducer: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}
	defer np.Close()

	// Subscribe to the response channel.
	sub := sc.Subscribe(context.Background(), rebootRespCh)
	if _, err = sub.Receive(context.Background()); err != nil {
		log.V(2).Infof("[Reboot_Log] Error in setting up subscription to response channel: %v", err)
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
		log.V(2).Infof("[Reboot_Log] Error in marshalling JSON: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}
	// Publish to notification channel.
	if err := np.Send(rebootNotifKey, "", map[string]string{dataMsgFld: string(reqStr)}); err != nil {
		log.V(2).Infof("[Reboot_Log] Error in publishing to notification channel: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}

	// Wait for response on Reboot_Response_Channel.
	tc := time.After(notificationTimeout)
	for {
		select {
		case msg := <-channel:
			op, data, fvs, err := processMsgPayload(msg.Payload)
			if err != nil {
				log.V(2).Infof("[Reboot_Log] Error while receiving Response Notification = [%v] for message [%v]", err.Error(), msg)
				return nil, status.Errorf(codes.Internal, fmt.Sprintf("Error while receiving Response Notification: [%s] for message [%s]", err.Error(), msg)), msgDataStr
			}
			log.V(2).Infof("[Reboot_Log] Received on the Reboot notification channel: op = [%v], data = [%v], fvs = [%v]", op, data, fvs)

			if op != rebootNotifKey {
				log.V(2).Infof("[Reboot_Log] Op: [%v] doesn't match for `%v`!", op, rebootNotifKey)
				continue
			}
			if fvs != nil {
				if _, ok := fvs[dataMsgFld]; ok {
					msgDataStr = fvs[dataMsgFld]
				}
			}
			if swssCode := swssToErrorCode(data); swssCode != codes.OK {
				log.V(2).Infof("[Reboot_Log] Response Notification returned SWSS Error code: %v, error = %v", swssCode, msgDataStr)
				return nil, status.Errorf(swssCode, "Response Notification returned SWSS Error code: "+msgDataStr), msgDataStr
			}
			return resp, nil, msgDataStr

		case <-tc:
			// Crossed the reboot response notification timeout.
			log.V(2).Infof("[Reboot_Log] Response Notification timeout from NSF Manager!")
			return nil, status.Errorf(codes.Internal, "Response Notification timeout from NSF Manager!"), msgDataStr
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
	rclient, err := getRedisDBClient(stateDB)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	defer rclient.Close()

	// NSF WARM BOOT.
	if req.GetMethod() == syspb.RebootMethod_NSF {
		// NSF pre-check validation before sending to NSF Manager.
		return precheckNSFReboot(ctx, srv, req, rclient)
	}

	// System reboot (COLD BOOT)
	resp, err, _ := sendRebootReqOnNotifCh(ctx, req, rclient, rebootKey)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		log.V(2).Info("NSF request received empty response from NSF Manager.")
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
	log.V(2).Info("gNOI: RebootStatus")
	resp := &syspb.RebootStatusResponse{}
	// Initialize State DB.
	rclient, err := getRedisDBClient(stateDB)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	defer rclient.Close()

	respStr, err, msgData := sendRebootReqOnNotifCh(ctx, req, rclient, rebootStatusKey)
	if err != nil {
		log.V(2).Infof("gNOI: Received error for RebootStatusResponse: %v", err)
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	if msgData == "" || respStr == nil {
		log.V(2).Info("gNOI: Received empty RebootStatusResponse")
		return nil, status.Errorf(codes.Internal, "Received empty RebootStatusResponse")
	}
	if err := pjson.Unmarshal([]byte(msgData), resp); err != nil {
		log.V(2).Infof("gNOI: Cannot unmarshal the response: [%v]; err: [%v]", msgData, err)
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Cannot unmarshal the response: [%s]; err: [%s]", msgData, err.Error()))
	}
	log.V(2).Infof("gNOI: Returning RebootStatusResponse: resp = [%v]\n, msgData = [%v]", resp, msgData)
	return resp, nil
}

// CancelReboot RPC implements the corresponding RPC.
func (srv *Server) CancelReboot(ctx context.Context, req *syspb.CancelRebootRequest) (*syspb.CancelRebootResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(3).Info("gNOI: CancelReboot")
	if req.GetMessage() == "" {
		log.V(0).Info("Invalid CancelReboot request: message is empty.")
		return nil, status.Errorf(codes.Internal, "Invalid CancelReboot request: message is empty.")
	}
	// Initialize State DB.
	rclient, err := getRedisDBClient(stateDB)
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

// Ping implements the corresponding RPC.
func (srv *Server) Ping(req *syspb.PingRequest, stream syspb.System_PingServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return err
	}
	log.V(3).Info("gNOI: Ping")
	return status.Errorf(codes.Unimplemented, "Method system.Ping is unimplemented.")
}

// Traceroute implements the corresponding RPC.
func (srv *Server) Traceroute(req *syspb.TracerouteRequest, stream syspb.System_TracerouteServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return err
	}
	log.V(3).Info("gNOI: Traceroute")
	return status.Errorf(codes.Unimplemented, "Method system.Traceroute is unimplemented.")
}

// SetPackage implements the corresponding RPC.
func (srv *Server) SetPackage(stream syspb.System_SetPackageServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return err
	}
	log.V(3).Info("gNOI: SetPackage")
	return status.Errorf(codes.Unimplemented, "Method system.SetPackage is unimplemented.")
}

// SwitchControlProcessor implements the corresponding RPC.
func (srv *Server) SwitchControlProcessor(ctx context.Context, req *syspb.SwitchControlProcessorRequest) (*syspb.SwitchControlProcessorResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(3).Info("gNOI: SwitchControlProcessor")
	return &syspb.SwitchControlProcessorResponse{}, nil
}

// Time implements the corresponding RPC.
func (srv *Server) Time(ctx context.Context, req *syspb.TimeRequest) (*syspb.TimeResponse, error) {
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(3).Info("gNOI: Time")
	var tm syspb.TimeResponse
	tm.Time = uint64(time.Now().UnixNano())
	return &tm, nil
}

func precheckNSFReboot(ctx context.Context, srv *Server, req *syspb.RebootRequest, rclient *redis.Client) (*syspb.RebootResponse, error) {
	// Reject NSF if system is in critical state.
	if srv.SsHelper.IsSystemCritical() {
		log.V(2).Info("NSF request rejected since system is in critical state")
		return nil, status.Errorf(codes.FailedPrecondition, "NSF request rejected since system is in critical state: %s", srv.SsHelper.GetSystemCriticalReason())
	}
	// Reject NSF if NSF is already in progress.
	if srv.WarmRestartHelper.IsNSFOngoing() {
		log.V(2).Info("NSF request rejected since NSF is already in progress!")
		return nil, status.Errorf(codes.Unavailable, "NSF request rejected since NSF is already in progress!")
	}
	// Reject NSF if LinkQual is in progress.
	result, err := rclient.HGet(context.Background(), "LINKQUAL_RESULT|LINKQUAL_ACTIVE_SESSIONS", "count").Result()
	// Valid case if table entry is not present, i.e. no LinkQual is ongoing.
	if err != nil {
		resp, err, _ := sendRebootReqOnNotifCh(ctx, req, rclient, rebootKey)
		if err != nil {
			return nil, err
		}
		if resp == nil {
			log.V(2).Info("NSF request received empty response from NSF Manager.")
			return &syspb.RebootResponse{}, nil
		}
		return resp.(*syspb.RebootResponse), err
	}
	linkQualRes, err := strconv.Atoi(result)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	if linkQualRes > 0 {
		return nil, status.Errorf(codes.Unavailable, "NSF request rejected since LinkQual is in progress: LinkQual active session count %v", result)
	}
	resp, err, _ := sendRebootReqOnNotifCh(ctx, req, rclient, rebootKey)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		log.V(2).Info("NSF request received empty response from NSF Manager.")
		return &syspb.RebootResponse{}, nil
	}
	return resp.(*syspb.RebootResponse), nil
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
