package gnmi

import (
	"context"
	"encoding/json"
	"fmt"
	log "github.com/golang/glog"
	ospb "github.com/openconfig/gnoi/os"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"os"
	"strings"
	"sync"
)

var (
	sem             sync.Mutex
	imgTrfInitiated = false
)

func (srv *OSServer) processTransferReq(trfReq *ospb.TransferRequest) *ospb.InstallResponse {
	if trfReq.GetVersion() == "" {
		log.Errorln("TransferRequest must contain a valid OS version.")
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Type:   ospb.InstallError_PARSE_FAIL,
					Detail: "TransferRequest must contain a valid OS version.",
				},
			},
		}
	}

	return &ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferReady{},
	}
}

func (srv *OSServer) processTransferEnd(trfReq *ospb.TransferEnd) *ospb.InstallResponse {
	return &ospb.InstallResponse{
		Response: &ospb.InstallResponse_Validated{},
	}
}

func (srv *OSServer) processTransferContent(trfCnt []byte, imgPath string) *ospb.InstallResponse {
	// If image exists, target should have sent Validated | InstallError on TransferRequest.
	if !imgTrfInitiated && srv.imageExists(imgPath) {
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("File exists [%s]!", imgPath),
				},
			},
		}
	}
	imgTrfInitiated = true

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
		log.Errorln(err)
		return errResp
	}
	if _, err := f.Write(trfCnt); err != nil {
		f.Close()
		log.Errorln(err)
		return errResp
	}
	if err := f.Close(); err != nil {
		log.Errorln(err)
		return errResp
	}

	return &ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferProgress{},
	}
}

func (srv *OSServer) getVersionPath(version string) string {
	return srv.config.ImgDir + "/" + version
}

func (srv *OSServer) imageExists(path string) bool {
	if _, err := os.Lstat(path); err == nil {
		return true
	}
	return false
}

// Install implements correspondig RPC
func (srv *OSServer) Install(stream ospb.OS_InstallServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: os.Install")

	defer func() {
		imgTrfInitiated = false
	}()

	// Concurrent Install RPCs are not allowed.
	if !sem.TryLock() {
		log.Errorln("Concurrent Install RPCs are not allowed.")

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
			log.Errorln("Error while sending InstallError response: ", err)
			return status.Errorf(codes.Aborted, err.Error())
		}

		return status.Errorf(codes.Aborted, "Concurrent Install RPCs are not allowed.")
	}
	defer sem.Unlock()

	// Receive TransferReq message.
	req, err := stream.Recv()
	if err == io.EOF {
		log.Errorln("Received EOF instead of TransferRequest!")
		return nil
	}
	if err != nil {
		log.Errorln("Received error: ", err)
		return status.Errorf(codes.Aborted, err.Error())
	}

	trfReq := req.GetTransferRequest()
	if trfReq == nil {
		log.Errorln("Did not receive a TransferRequest.")
		return status.Errorf(codes.InvalidArgument, "Expected TransferRequest.")
	}

	resp := srv.processTransferReq(trfReq)
	if resp != nil {
		if err := stream.Send(resp); err != nil {
			log.Errorln("Error while sending response: ", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
	if resp == nil || resp.GetInstallError() != nil {
		return status.Errorf(codes.Aborted, "Failed to process TransferRequest.")
	}

	imgPath := srv.getVersionPath(trfReq.GetVersion())
	for {
		req, err = stream.Recv()
		if err == io.EOF {
			log.Errorln("Received EOF instead of TransferContent request!")
			return nil
		}
		if err != nil {
			log.Errorln("Received error: ", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
		if trfReq := req.GetTransferRequest(); trfReq != nil {
			log.Errorln("Received a TransferReq out-of-sequence.")
			return status.Errorf(codes.InvalidArgument, "Expected TransferContent, or TransferEnd.")
		}
		// Transferring content is complete.
		if trfEnd := req.GetTransferEnd(); trfEnd != nil {
			break
		}
		// Process content transfer.
		resp := srv.processTransferContent(req.GetTransferContent(), imgPath)
		if resp != nil {
			if err := stream.Send(resp); err != nil {
				log.Errorln("Error while sending response: ", err)
				return status.Errorf(codes.Aborted, err.Error())
			}
		}
		if resp == nil || resp.GetInstallError() != nil {
			return status.Errorf(codes.Aborted, "Failed to process TransferContent.")
		}
	}

	// Receive TransferEnd message.
	trfEnd := req.GetTransferEnd()
	if trfEnd == nil {
		log.Errorln("Did not receive a TransferEnd")
		return status.Errorf(codes.InvalidArgument, "Expected TransferEnd")
	}

	resp = srv.processTransferEnd(trfEnd)
	if resp != nil {
		if err := stream.Send(resp); err != nil {
			log.Errorln("Error while sending response: ", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
	if resp == nil || resp.GetInstallError() != nil {
		return status.Errorf(codes.Aborted, "Failed to process TransferEnd.")
	}

	return nil
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
	err = json.Unmarshal([]byte(image_json), &images)
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
