package gnmi

import (
	"fmt"
	"io"
	"os"
	"sync"

	ospb "github.com/openconfig/gnoi/os"

	log "github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
