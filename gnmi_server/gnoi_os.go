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
	"path/filepath"
	"strings"
	"sync"
)

var (
	sem                sync.Mutex
	totalBytesReceived = make(map[string]uint64)
)

// ProcessInstallFromBackEnd makes call via the sonic-host-service.
func ProcessInstallFromBackEnd(req string) (string, error) {
	log.Infof("ProcessInstallFromBackEnd: %v", req)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return "", err
	}
	defer sc.Close()
	return sc.InstallOS(req)
}

func (srv *OSServer) processTransferReq(req *ospb.InstallRequest) *ospb.InstallResponse {
	log.Infof("processTransferReq: %v", req)
	transferReq := req.GetTransferRequest()
	if transferReq.GetVersion() == "" {
		log.Errorf("processTransferReq: TransferRequest must contain a valid OS version.")
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
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to marshal TransferReady JSON: err: %v, req: %v, reqStr: %v", err, req, reqStr),
				},
			},
		}

	}
	respStr, err := srv.config.OSCfg.ProcessTransferReady(string(reqStr))
	log.Infof("processTransferReq: Backend response %s", respStr)
	if err != nil {
		// Convert the generic error to a gRPC status object
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.Unimplemented {
			log.Errorf("processTransferReq: Backend returned OS unimplemented error.")
			return nil // signal caller to return codes.Unimplemented
		}
		log.Errorf("processTransferReq: Error in processing install from backend")
		// Fallback to returning a detailed InstallError for other types of errors
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Error in TransferReady response: err: %v, reqStr: %v, respStr: %v", err, reqStr, respStr),
				},
			},
		}
	}

	resp := &ospb.InstallResponse{}
	if err := json.Unmarshal([]byte(respStr), resp); err != nil {
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to unmarshal TransferReady JSON: err: %v, respStr: %v", err, respStr),
				},
			},
		}

	}
	log.Infof("processTransferReq: complete.")
	return resp
}
func (srv *OSServer) processTransferEnd(req *ospb.InstallRequest) *ospb.InstallResponse {
	// Front end marshals the request, and sends to the sonic-host-service.
	// Back end is expected to return the response in JSON format.

	log.Infof("processTransferEnd: %v", req)
	reqStr, err := json.Marshal(req)
	if err != nil {
		log.Errorf("Failed to marshal TransferEnd JSON: err: %v, req: %v, reqStr: %v", err, req, reqStr)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to marshal TransferEnd JSON: err: %v, req: %v, reqStr: %v", err, req, reqStr),
				},
			},
		}

	}
	respStr, err := srv.config.OSCfg.ProcessTransferEnd(string(reqStr))
	log.Infof("processTransferEnd: Backend response %s", respStr)
	if err != nil {
		log.Errorf("Received error from OSServer.TransferEnd: err: %v, reqStr: %v, respStr: %v", err, reqStr, respStr)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Error in TransferEnd response: err: %v, reqStr: %v, respStr: %v", err, reqStr, respStr),
				},
			},
		}

	}
	resp := &ospb.InstallResponse{}
	if err := json.Unmarshal([]byte(respStr), resp); err != nil {
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to unmarshal TransferEnd JSON: err: %v, respStr: %v", err, respStr),
				},
			},
		}

	}
	log.Infof("processTransferEnd: complete.")
	return resp
}

func (srv *OSServer) processTransferContent(transferContent []byte, imgPath string) *ospb.InstallResponse {
	log.Infof("processTransferContent: file %v", imgPath)

	// Base directory where the images should reside (e.g., /tmp)
	baseDir := srv.config.OSCfg.ImgDir

	// Clean the user-provided imgPath to avoid directory traversal
	cleanPath := filepath.Clean(imgPath) // Clean any ".." or other traversal patterns

	// Ensure baseDir has a trailing slash for the HasPrefix check to work correctly.
	if baseDir[len(baseDir)-1] != '/' {
		baseDir += "/"
	}

	var safePath string

	// If cleanPath is absolute (starts with /), use it directly
	if filepath.IsAbs(cleanPath) {
		safePath = cleanPath
	} else {
		// If it's not absolute, join it with baseDir
		safePath = filepath.Join(baseDir, cleanPath)
	}

	// Validate that the safePath is still within the base directory (to avoid path traversal)
	if !strings.HasPrefix(safePath, baseDir) {
		log.Errorf("processTransferContent: Path traversal attempt detected: %s", imgPath)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Invalid image path: %s", imgPath),
				},
			},
		}
	}

	// Ensure the directory exists before writing the file
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Errorf("processTransferContent: Failed to create directory for file: %s, error: %v", dir, err)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to create directory for file [%s].", dir),
				},
			},
		}
	}

	// Open the file for writing (create or append)
	f, err := os.OpenFile(safePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Errorf("processTransferContent: Failed to open or create file for writing: %s, error: %v", safePath, err)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to open file [%s].", safePath),
				},
			},
		}
	}

	if _, err := f.Write(transferContent); err != nil {
		f.Close()
		log.Errorf("processTransferContent: Failed to write to file: %s, error: %v", safePath, err)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to write to file [%s].", safePath),
				},
			},
		}
	}

	if err := f.Close(); err != nil {
		log.Errorf("processTransferContent: Failed to close file after writing: %s, error: %v", safePath, err)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to close file after writing [%s].", safePath),
				},
			},
		}
	}

	// Update progress (total bytes received)
	totalBytesReceived[safePath] += uint64(len(transferContent))
	log.Infof("processTransferContent: complete. Wrote %d bytes to %s", totalBytesReceived[safePath], safePath)
	return &ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferProgress{
			TransferProgress: &ospb.TransferProgress{
				BytesReceived: totalBytesReceived[safePath],
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

func (srv *OSServer) removeIncompleteTransfer(imgPath string) {
	if !srv.imageExists(imgPath) {
		log.Errorf("Image not exist to removeIncompleteTransfer")
		return
	}
	log.V(1).Infoln("Remove incomplete image: ", imgPath)
	// Now remove the file.
	if err := os.Remove(imgPath); err != nil {
		log.Errorf("Failed to remove incomplete image: %v", err)
	}
}

// Install implements correspondig RPC
func (srv *OSServer) Install(stream ospb.OS_InstallServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return err
	}
	log.V(1).Info("=== [OS Install Start] ===")

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
			log.Errorf("InstallResponse: %v", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
		return status.Errorf(codes.Aborted, "Concurrent Install RPCs are not allowed.")
	}
	defer sem.Unlock()

	// Receive TransferReq message.
	req, err := stream.Recv()
	log.Infof("Install: Received TransferRequest version=%v", req.GetTransferRequest().GetVersion())
	if err == io.EOF {
		log.Errorf("Install: Received EOF while receiving TransferRequest!")
		return nil
	}
	if err != nil {
		log.Errorf("Install: Received error %v while receiving TransferRequest!", err)
		return status.Errorf(codes.Aborted, err.Error())
	}
	transferReq := req.GetTransferRequest()
	if transferReq == nil {
		log.Errorf("Install: TransferRequest is nil")
		err = status.Errorf(codes.InvalidArgument, "Expected TransferRequest.")
		return err
	}
	resp := srv.processTransferReq(req)
	log.Infof("Install: Response received %v", resp)

	if resp == nil {
		log.Errorf("Install: TransferReady not received. Returning OS Unimplemented (backend not supported)")
		return status.Errorf(codes.Unimplemented, "OS Install not supported")
	}

	if resp != nil {
		if err := stream.Send(resp); err != nil {
			log.Errorf("Install: Error %v in sending TransferReady response:", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}

	if resp == nil || resp.GetInstallError() != nil {
		log.Infof("Install: Failed to process TransferRequest =%v", resp.GetInstallError())
		err = status.Errorf(codes.Aborted, "Failed to process TransferRequest.")
		return err
	}

	imgPath := srv.getVersionPath(transferReq.GetVersion())
	imgTransferInitiated := false
	defer func() {
		if imgTransferInitiated {
			srv.removeIncompleteTransfer(imgPath)
		}
	}()

	for {
		req, err = stream.Recv()
		log.Infof("Install: Received TransferContent stream")
		if err == io.EOF {
			log.Errorf("Install: Received EOF instead of TransferContent request!")
			return status.Errorf(codes.Aborted, "Stream closed prematurely during transfer.")
		}
		if err != nil {
			log.Errorf("Install: Error %v in receiving TransferContent", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
		if transferReq := req.GetTransferRequest(); transferReq != nil {
			log.Errorf("Install: Received a TransferReq out-of-sequence.")
			err = status.Errorf(codes.InvalidArgument, "Expected TransferContent, or TransferEnd.")
			return err
		}
		// Transferring content is complete.
		if transferEnd := req.GetTransferEnd(); transferEnd != nil {
			log.Infof("Install: TransferContent is complete.")
			break
		}
		// Process content transfer.
		// If image exists, target should have sent Validated | InstallError on TransferRequest.
		if !imgTransferInitiated && srv.imageExists(imgPath) {
			log.Errorf("Install: Image %v already exists. Aborting TransferContent.", imgPath)
			return status.Errorf(codes.Aborted, "Image already exists: %s", imgPath)
		}
		imgTransferInitiated = true
		resp := srv.processTransferContent(req.GetTransferContent(), imgPath)
		log.Infof("Install: Response received %v", resp)
		if resp != nil {
			if err := stream.Send(resp); err != nil {
				log.Errorf("Install: Error %v in sending TransferContent response", err)
				srv.removeIncompleteTransfer(imgPath)
				return status.Errorf(codes.Aborted, err.Error())
			}
		}
		if resp == nil || resp.GetInstallError() != nil {
			srv.removeIncompleteTransfer(imgPath)
			err = status.Errorf(codes.Aborted, "Failed to process TransferContent.")
			log.Errorf("Install: Failed to process TransferContent=%v", err)
			return err
		}
	}
	// Receive TransferEnd message.
	transferEnd := req.GetTransferEnd()
	if transferEnd == nil {
		log.V(1).Infoln("Did not receive a TransferEnd")
		srv.removeIncompleteTransfer(imgPath)
		err = status.Errorf(codes.InvalidArgument, "Expected TransferEnd")
		return err
	}
	log.Infof("Install: Received TransferEnd")
	resp = srv.processTransferEnd(req)
	log.Infof("Install: Response received %v", resp)
	if resp != nil {
		if err := stream.Send(resp); err != nil {
			log.Errorf("Install: Error %v in sending TransferEnd response. Aborting..", err)
			srv.removeIncompleteTransfer(imgPath)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
	if resp == nil || resp.GetInstallError() != nil {
		srv.removeIncompleteTransfer(imgPath)
		err = status.Errorf(codes.Aborted, "Failed to process TransferEnd.")
		log.Errorf("Install: Failed to process TransferEnd=%v", resp.GetInstallError())
		return err
	}
	log.V(1).Info("=== [OS Install Complete] ===")
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
