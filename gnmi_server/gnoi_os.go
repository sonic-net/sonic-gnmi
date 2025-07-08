package gnmi

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	log "github.com/golang/glog"
	ospb "github.com/openconfig/gnoi/os"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	json "google.golang.org/protobuf/encoding/protojson"
	"io"
	"os"
	"strings"
	"sync"
)

var (
	sem sync.Mutex
)

// ProcessInstallFromBackEnd makes call via the sonic-host-service.
func ProcessInstallFromBackEnd(req string) (string, error) {
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return "", err
	}
	return sc.InstallOS(req)
}
func handleErrorResponse(f string, a ...any) *ospb.InstallResponse {
	errStr := fmt.Sprintf(f, a...)
	log.V(1).Infoln(errStr)
	return &ospb.InstallResponse{
		Response: &ospb.InstallResponse_InstallError{
			InstallError: &ospb.InstallError{
				Detail: errStr,
			},
		},
	}
}
func (srv *OSServer) processTransferReq(req *ospb.InstallRequest) *ospb.InstallResponse {
	trfReq := req.GetTransferRequest()
	if trfReq.GetVersion() == "" {
		log.V(1).Infoln("TransferRequest must contain a valid OS version.")
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Type:   ospb.InstallError_PARSE_FAIL,
					Detail: "TransferRequest must contain a valid OS version.",
				},
			},
		}
	}
	// Front end marshals the request, and sends to the sonic-host-service.
	// Back end is expected to return the response in JSON format.
	reqStr, err := json.Marshal(req)
	if err != nil {
		return handleErrorResponse("Failed to marshal TransferReady JSON: err: %v, req: %v, reqStr: %v", err, req, reqStr)
	}
	respStr, err := srv.config.OSCfg.ProcessTrfReady(string(reqStr))
	if err != nil {
		return handleErrorResponse("Received error from OSServer.TransferReady: err: %v, reqStr: %v, respStr: %v", err, reqStr, respStr)
	}
	resp := &ospb.InstallResponse{}
	if err := json.Unmarshal([]byte(respStr), resp); err != nil {
		return handleErrorResponse("Failed to unmarshal TransferReady JSON: err: %v, respStr: %v", err, respStr)
	}
	return resp
}
func (srv *OSServer) processTransferEnd(req *ospb.InstallRequest) *ospb.InstallResponse {
	// Front end marshals the request, and sends to the sonic-host-service.
	// Back end is expected to return the response in JSON format.
	reqStr, err := json.Marshal(req)
	if err != nil {
		return handleErrorResponse("Failed to marshal TransferEnd JSON: err: %v, req: %v, reqStr: %v", err, req, reqStr)
	}
	respStr, err := srv.config.OSCfg.ProcessTrfEnd(string(reqStr))
	if err != nil {
		return handleErrorResponse("Received error from OSServer.TransferEnd: err: %v, reqStr: %v, respStr: %v", err, reqStr, respStr)
	}
	resp := &ospb.InstallResponse{}
	if err := json.Unmarshal([]byte(respStr), resp); err != nil {
		return handleErrorResponse("Failed to unmarshal TransferEnd JSON: err: %v, respStr: %v", err, respStr)
	}
	return resp
}
func (srv *OSServer) processTransferContent(trfCnt []byte, imgPath string) *ospb.InstallResponse {
	errResp := &ospb.InstallResponse{
		Response: &ospb.InstallResponse_InstallError{
			InstallError: &ospb.InstallError{
				Detail: fmt.Sprintf("Failed to open file [%s].", imgPath),
			},
		},
	}
	// If the file doesn't exist, create it, or append to the file
	f, err := os.OpenFile(imgPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.V(1).Infoln(err)
		return errResp
	}
	if _, err := f.Write(trfCnt); err != nil {
		f.Close()
		log.V(1).Infoln(err)
		return errResp
	}
	if err := f.Close(); err != nil {
		log.V(1).Infoln(err)
		return errResp
	}
	return &ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferProgress{
			TransferProgress: &ospb.TransferProgress{
				BytesReceived: uint64(len(trfCnt)),
			},
		},
	}
}
func (srv *OSServer) getVersionPath(version string) string {
	return srv.config.OSCfg.ImgDir + "/" + version
}
func (srv *OSServer) imageExists(path string) bool {
	if _, err := os.Lstat(path); err == nil {
		return true
	}
	return false
}
func (srv *OSServer) removeIncompleteTrf(imgPath string) {
	if !srv.imageExists(imgPath) {
		return
	}
	log.V(1).Infoln("Remove incomplete image: ", imgPath)
	// Now remove the file.
	if err := os.Remove(imgPath); err != nil {
		log.V(1).Infoln("Failed to remove incomplete image: ", err)
	}
}

// TODO(b/328077908) Alarms to be implemented later
//
//	func raiseAlarm(err error) {
//		csh, cshErr := common_utils.NewComponentStateHelper(common_utils.Telemetry)
//		if cshErr != nil {
//			log.V(lvl.ERROR).Infof("gNOI OS: failed to create new ComponentStateHelper - %v", cshErr)
//			return
//		}
//		defer csh.Close()
//		if rcsErr := csh.ReportComponentState(common_utils.ComponentMinor, err.Error()); rcsErr != nil {
//			log.V(lvl.ERROR).Infof("Failed to raise ComponentMinor Alarm: %v", rcsErr)
//		}
//	}
//
// Install implements correspondig RPC
func (srv *OSServer) Install(stream ospb.OS_InstallServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: os.Install")
	// Concurrent Install RPCs are not allowed.
	if !sem.TryLock() {
		log.V(1).Infoln("Concurrent Install RPCs are not allowed.")
		// Send InstallError response.
		err = stream.Send(&ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Type:   ospb.InstallError_INSTALL_IN_PROGRESS,
					Detail: "Concurrent Install RPCs are not allowed.",
				},
			},
		})
		if err != nil {
			log.V(1).Infoln("Error while sending InstallError response: ", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
		return status.Errorf(codes.Aborted, "Concurrent Install RPCs are not allowed.")
	}
	defer sem.Unlock()
	// Receive TransferReq message.
	req, err := stream.Recv()
	if err == io.EOF {
		log.V(1).Infoln("Received EOF instead of TransferRequest!")
		return nil
	}
	if err != nil {
		log.V(1).Infoln("Received error: ", err)
		// TODO(b/328077908) Alarms to be implemented later
		// raiseAlarm(err)
		return status.Errorf(codes.Aborted, err.Error())
	}
	trfReq := req.GetTransferRequest()
	if trfReq == nil {
		log.V(1).Infoln("Did not receive a TransferRequest.")
		err = status.Errorf(codes.InvalidArgument, "Expected TransferRequest.")
		// TODO(b/328077908) Alarms to be implemented later
		// raiseAlarm(err)
		return err
	}
	resp := srv.processTransferReq(req)
	if resp != nil {
		if err := stream.Send(resp); err != nil {
			log.V(1).Infoln("Error while sending response: ", err)
			// TODO(b/328077908) Alarms to be implemented later
			// raiseAlarm(err)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
	if resp == nil || resp.GetInstallError() != nil {
		err = status.Errorf(codes.Aborted, "Failed to process TransferRequest.")
		// TODO(b/328077908) Alarms to be implemented later
		// raiseAlarm(err)
		return err
	}
	imgPath := srv.getVersionPath(trfReq.GetVersion())
	imgTrfInitiated := false
	for {
		req, err = stream.Recv()
		if err == io.EOF {
			log.V(1).Infoln("Received EOF instead of TransferContent request!")
			if imgTrfInitiated {
				srv.removeIncompleteTrf(imgPath)
			}
			return nil
		}
		if err != nil {
			log.V(1).Infoln("Received error: ", err)
			if imgTrfInitiated {
				srv.removeIncompleteTrf(imgPath)
			}
			// TODO(b/328077908) Alarms to be implemented later
			// raiseAlarm(err)
			return status.Errorf(codes.Aborted, err.Error())
		}
		if trfReq := req.GetTransferRequest(); trfReq != nil {
			log.V(1).Infoln("Received a TransferReq out-of-sequence.")
			if imgTrfInitiated {
				srv.removeIncompleteTrf(imgPath)
			}
			err = status.Errorf(codes.InvalidArgument, "Expected TransferContent, or TransferEnd.")
			// TODO(b/328077908) Alarms to be implemented later
			// raiseAlarm(err)
			return err
		}
		// Transferring content is complete.
		if trfEnd := req.GetTransferEnd(); trfEnd != nil {
			break
		}
		// Process content transfer.
		// If image exists, target should have sent Validated | InstallError on TransferRequest.
		if !imgTrfInitiated && srv.imageExists(imgPath) {
			resp := &ospb.InstallResponse{
				Response: &ospb.InstallResponse_InstallError{
					InstallError: &ospb.InstallError{
						Detail: fmt.Sprintf("File exists [%s]!", imgPath),
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				log.V(1).Infoln("Error while sending response: ", err)
			}
			err = status.Errorf(codes.Aborted, "Failed as image exists!")
			// TODO(b/328077908) Alarms to be implemented later
			// raiseAlarm(err)
			return err
		}
		imgTrfInitiated = true
		resp := srv.processTransferContent(req.GetTransferContent(), imgPath)
		if resp != nil {
			if err := stream.Send(resp); err != nil {
				log.V(1).Infoln("Error while sending response: ", err)
				srv.removeIncompleteTrf(imgPath)
				// TODO(b/328077908) Alarms to be implemented later
				// raiseAlarm(err)
				return status.Errorf(codes.Aborted, err.Error())
			}
		}
		if resp == nil || resp.GetInstallError() != nil {
			srv.removeIncompleteTrf(imgPath)
			err = status.Errorf(codes.Aborted, "Failed to process TransferContent.")
			// TODO(b/328077908) Alarms to be implemented later
			// raiseAlarm(err)
			return err
		}
	}
	// Receive TransferEnd message.
	trfEnd := req.GetTransferEnd()
	if trfEnd == nil {
		log.V(1).Infoln("Did not receive a TransferEnd")
		srv.removeIncompleteTrf(imgPath)
		err = status.Errorf(codes.InvalidArgument, "Expected TransferEnd")
		// TODO(b/328077908) Alarms to be implemented later
		// raiseAlarm(err)
		return err
	}
	resp = srv.processTransferEnd(req)
	if resp != nil {
		if err := stream.Send(resp); err != nil {
			log.V(1).Infoln("Error while sending response: ", err)
			srv.removeIncompleteTrf(imgPath)
			// TODO(b/328077908) Alarms to be implemented later
			// raiseAlarm(err)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
	if resp == nil || resp.GetInstallError() != nil {
		srv.removeIncompleteTrf(imgPath)
		err = status.Errorf(codes.Aborted, "Failed to process TransferEnd.")
		// TODO(b/328077908) Alarms to be implemented later
		// raiseAlarm(err)
		return err
	}
	log.V(1).Info("OS.Install is complete.")
	return nil
}

func (srv *OSServer) Activate(ctx context.Context, req *ospb.ActivateRequest) (*ospb.ActivateResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi" /*writeAccess=*/, true)
	if err != nil {
		log.Errorf("Failed to authenticate: %v", err)
		return nil, err
	}

	log.Infof("gNOI: Activate")
	image := req.GetVersion()
	log.Infof("Requested to activate image %s", image)

	dbus, err := ssc.NewDbusClient()
	if err != nil {
		log.Errorf("Failed to create dbus client: %v", err)
		return nil, err
	}
	defer dbus.Close()

	var resp ospb.ActivateResponse
	err = dbus.ActivateImage(image)
	if err != nil {
		log.Errorf("Failed to activate image %s: %v", image, err)
		image_not_exists := os.IsNotExist(err) ||
			(strings.Contains(strings.ToLower(err.Error()), "not") &&
				strings.Contains(strings.ToLower(err.Error()), "exist"))
		if image_not_exists {
			// Image does not exist.
			resp.Response = &ospb.ActivateResponse_ActivateError{
				ActivateError: &ospb.ActivateError{
					Type:   ospb.ActivateError_NON_EXISTENT_VERSION,
					Detail: err.Error(),
				},
			}
		} else {
			// Other error.
			resp.Response = &ospb.ActivateResponse_ActivateError{
				ActivateError: &ospb.ActivateError{
					Type:   ospb.ActivateError_UNSPECIFIED,
					Detail: err.Error(),
				},
			}
		}
		return &resp, nil
	}

	log.Infof("Successfully activated image %s", image)
	resp.Response = &ospb.ActivateResponse_ActivateOk{}
	return &resp, nil
}

func (srv *OSServer) Verify(ctx context.Context, req *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		log.V(2).Infof("Failed to authenticate: %v", err)
		return nil, err
	}

	log.V(1).Info("gNOI: Verify")
	dbus, err := ssc.NewDbusClient()
	if err != nil {
		log.V(2).Infof("Failed to create dbus client: %v", err)
		return nil, err
	}
	defer dbus.Close()

	image_json, err := dbus.ListImages()
	if err != nil {
		log.V(2).Infof("Failed to list images: %v", err)
		return nil, err
	}

	images := make(map[string]interface{})
	err = stdjson.Unmarshal([]byte(image_json), &images)
	if err != nil {
		log.V(2).Infof("Failed to unmarshal images: %v", err)
		return nil, err
	}

	current, exists := images["current"]
	if !exists {
		return nil, status.Errorf(codes.Internal, "Key 'current' not found in images")
	}
	current_image, ok := current.(string)
	if !ok {
		return nil, status.Errorf(codes.Internal, "Failed to assert current image as string")
	}
	resp := &ospb.VerifyResponse{
		Version: current_image,
	}
	return resp, nil
}
