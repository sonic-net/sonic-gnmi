package gnmi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis"
	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	syspb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
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

// Validates reboot request.
func ValidateRebootRequest(req *syspb.RebootRequest) error {
	// Supported Reboot methods are: COLD (1), POWERDOWN (2), HALT (3), WARM (4), NSF (5).
	// Not suppoted are: UNKNOWN (0), POWERUP (7)
	if req.GetMethod() == syspb.RebootMethod_UNKNOWN || req.GetMethod() == syspb.RebootMethod_POWERUP {
		log.Error("Invalid request: reboot method is not supported.")
		return fmt.Errorf("Invalid request: reboot method is not supported.")
	}
	// Only the COLD method with a delay of 0 is guaranteed to be accepted for all target types.
	// From https://github.com/openconfig/gnoi/blob/main/system/system.proto#L105
	if req.GetDelay() > 0 {
		log.Error("Invalid request: reboot is not immediate.")
		return fmt.Errorf("Invalid request: reboot is not immediate.")
	}

	return nil
}

func KillOrRestartProcess(restart bool, serviceName string) error {
	sc, err := ssc.NewDbusClient(dbusCaller)
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
	_, err := authenticate(srv.config, ctx, "gnoi", true)
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

func sendRebootReqOnNotifCh(ctx context.Context, req proto.Message, sc *redis.Client, rebootNotifKey string) (resp proto.Message, err error, msgDataStr string) {
	np, err := common_utils.NewNotificationProducer(rebootReqCh)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}
	defer np.Close()

	// Subscribe to the response channel.
	sub := sc.Subscribe(rebootRespCh)
	if _, err = sub.Receive(); err != nil {
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
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}
	// Publish to notification channel.
	if err := np.Send(rebootNotifKey, "", map[string]string{dataMsgFld: string(reqStr)}); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error()), msgDataStr
	}

	// Wait for response on Reboot_Response_Channel.
	tc := time.After(notificationTimeout)
	var tErr error
	for {
		select {
		case msg := <-channel:
			op, data, fvs, err := processMsgPayload(msg.Payload)
			if err != nil {
				return nil, status.Errorf(codes.Internal, fmt.Sprintf("Error while receiving Response Notification: [%s] for message [%s]", err.Error(), msg)), msgDataStr
			}
			log.V(1).Infof("[Reboot_Log] Received on the Reboot notification channel: op = [%v], data = [%v], fvs = [%v]", op, data, fvs)

			if op != rebootNotifKey {
				log.V(1).Infof("[Reboot_Log] Op: %v doesn't match for %v!", op, rebootNotifKey)
				tErr = status.Errorf(codes.Internal, fmt.Sprintf("Op: %v doesn't match for %v!", op, rebootNotifKey))
				continue
			}
			if fvs != nil {
				if _, ok := fvs[dataMsgFld]; ok {
					msgDataStr = fvs[dataMsgFld]
				}
			}
			if swssCode := SwssToErrorCode(data); swssCode != codes.OK {
				errStr := fmt.Sprintf("Response Notification returned SWSS Error code: %v, error = %v", swssCode, msgDataStr)
				log.V(1).Infof(errStr)
				return nil, status.Errorf(swssCode, errStr), msgDataStr
			}
			return resp, nil, msgDataStr

		case <-tc:
			// Crossed the reboot response notification timeout.
			log.V(1).Infof("[Reboot_Log] Response Notification timeout from Reboot Backend!")
			if tErr == nil {
				tErr = status.Errorf(codes.Internal, "Response Notification timeout from Reboot Backend!")
			}
			return nil, tErr, msgDataStr
		}
	}
}

// Reboot implements the corresponding RPC.
func (srv *Server) Reboot(ctx context.Context, req *syspb.RebootRequest) (*syspb.RebootResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(2).Info("gNOI: Reboot")
	if err := ValidateRebootRequest(req); err != nil {
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
		log.V(2).Info("Reboot request received empty response from Reboot Backend.")
		resp = &syspb.RebootResponse{}
	}
	return resp.(*syspb.RebootResponse), nil
}

// RebootStatus implements the corresponding RPC.
func (srv *Server) RebootStatus(ctx context.Context, req *syspb.RebootStatusRequest) (*syspb.RebootStatusResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi", true)
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
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	if msgData == "" || respStr == nil {
		return nil, status.Errorf(codes.Internal, "Received empty RebootStatusResponse")
	}
	if err := pjson.Unmarshal([]byte(msgData), resp); err != nil {
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("Cannot unmarshal the response: [%s]; err: [%s]", msgData, err.Error()))
	}
	log.V(1).Infof("gNOI: Returning RebootStatusResponse: resp = [%v]\n, msgData = [%v]", resp, msgData)
	return resp, nil
}

// CancelReboot RPC implements the corresponding RPC.
func (srv *Server) CancelReboot(ctx context.Context, req *syspb.CancelRebootRequest) (*syspb.CancelRebootResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: CancelReboot")
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
		log.V(2).Info("CancelReboot request received empty response from Reboot Backend.")
		resp = &syspb.CancelRebootResponse{}
	}
	return resp.(*syspb.CancelRebootResponse), nil
}

// Ping is unimplemented.
func (srv *Server) Ping(req *syspb.PingRequest, stream syspb.System_PingServer) error {
	ctx := stream.Context()
	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: Ping")
	return status.Errorf(codes.Unimplemented, "Method system.Ping is unimplemented.")
}

// Traceroute is unimplemented.
func (srv *Server) Traceroute(req *syspb.TracerouteRequest, stream syspb.System_TracerouteServer) error {
	ctx := stream.Context()
	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: Traceroute")
	return status.Errorf(codes.Unimplemented, "Method system.Traceroute is unimplemented.")
}

func (srv *Server) SetPackage(rs syspb.System_SetPackageServer) error {
	ctx := rs.Context()

	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		log.Errorf("Authentication failed: %v", err)
		return status.Errorf(codes.PermissionDenied, "authentication failed: %v", err)
	}
	log.V(1).Info("gNOI: SetPackage request received")

	// Create D-Bus client
	dbus, err := ssc.NewDbusClient(dbusCaller)
	if err != nil {
		log.Errorf("Failed to create D-Bus client: %v", err)
		return status.Errorf(codes.Internal, "failed to create D-Bus client: %v", err)
	}
	defer dbus.Close()

	// Receive the package request
	req, err := rs.Recv()
	if err != nil {
		log.Errorf("Failed to receive package request: %v", err)
		return err
	}

	// Validate request type
	pkg, ok := req.GetRequest().(*syspb.SetPackageRequest_Package)
	if !ok {
		errMsg := fmt.Sprintf("invalid request type: %T, expected SetPackageRequest_Package", req.GetRequest())
		log.Errorf(errMsg)
		return status.Errorf(codes.InvalidArgument, errMsg)
	}

	// A filename and a version must be provided
	if pkg.Package.Filename == "" {
		log.Errorf("Filename is missing in package request")
		return status.Errorf(codes.InvalidArgument, "filename is missing in package request")
	}
	if pkg.Package.Version == "" {
		log.Errorf("Version is missing in package request")
		return status.Errorf(codes.InvalidArgument, "version is missing in package request")
	}
	// Log the package filename and version
	log.V(1).Infof("Package filename: %s, version: %s", pkg.Package.Filename, pkg.Package.Version)

	// Download the package if RemoteDownload is provided
	if pkg.Package.RemoteDownload != nil {
		// Validate RemoteDownload
		log.V(1).Infof("RemoteDownload provided")
		// Check if the path is provided
		if pkg.Package.RemoteDownload.Path == "" {
			log.Errorf("RemoteDownload path is missing")
			return status.Errorf(codes.InvalidArgument, "remote download path is missing")
		}
		log.V(1).Infof("RemoteDownload path: %s", pkg.Package.RemoteDownload.Path)

		// Download the package
		err = dbus.DownloadImage(pkg.Package.RemoteDownload.Path, pkg.Package.Filename)
		if err != nil {
			log.Errorf("Failed to download image: %v", err)
			return status.Errorf(codes.Internal, "failed to download image: %v", err)
		}
		log.V(1).Infof("Package %s downloaded successfully to %s", pkg.Package.Version, pkg.Package.Filename)
	}

	// If activate is requested, install the package and set it to be the next boot image
	if pkg.Package.Activate {
		log.V(1).Infof("Activate is requested")
		// Install the package
		err = dbus.InstallImage(pkg.Package.Filename)
		if err != nil {
			log.Errorf("Failed to install image: %v", err)
			return status.Errorf(codes.Internal, "failed to install image: %v", err)
		}
		log.V(1).Infof("Package %s installed successfully", pkg.Package.Filename)
		// Currently, Installing the image will automatically set it as the next boot image
		log.V(1).Infof("Package %s set as next boot image", pkg.Package.Filename)
	}

	// Send response to client
	if err := rs.SendAndClose(&syspb.SetPackageResponse{}); err != nil {
		log.Errorf("Failed to send response: %v", err)
		return err
	}

	log.V(1).Infof("SetPackage completed successfully for %s", pkg.Package.Filename)
	return nil
}

// SwitchControlProcessor implements the corresponding RPC.
func (srv *Server) SwitchControlProcessor(ctx context.Context, req *syspb.SwitchControlProcessorRequest) (*syspb.SwitchControlProcessorResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: SwitchControlProcessor")
	return &syspb.SwitchControlProcessorResponse{}, nil
}

// Time implements the corresponding RPC.
func (srv *Server) Time(ctx context.Context, req *syspb.TimeRequest) (*syspb.TimeResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Time")
	var tm syspb.TimeResponse
	tm.Time = uint64(time.Now().UnixNano())
	return &tm, nil
}
