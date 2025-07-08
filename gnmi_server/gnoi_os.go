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
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return "", err
	}
	defer sc.Close()
	return sc.InstallOS(req)
}

func (srv *OSServer) processTransferReq(req *ospb.InstallRequest) *ospb.InstallResponse {
	log.Infof("processTransferReq for %v", req)
	transferReq := req.GetTransferRequest()
	if transferReq.GetVersion() == "" {
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
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to marshal TransferReady JSON: err: %v, req: %v, reqStr: %v", err, req, reqStr),
				},
			},
		}

	}
	respStr, err := srv.config.OSCfg.ProcessTransferReady(string(reqStr))
	log.Infof("Response string from backend= %s", respStr)
	if err != nil {
		// If the error is about unimplemented OS install, return nil to trigger gRPC Unimplemented code
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.Unimplemented {
			log.Infof("Backend returned unimplemented error.")
			return nil // signal caller to return codes.Unimplemented
		}
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Received error from OSServer.TransferReady: err: %v, reqStr: %v, respStr: %v", err, reqStr, respStr),
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
	log.Infof("Successfully processed TransferRequest and returning %v", resp)
	return resp
}
func (srv *OSServer) processTransferEnd(req *ospb.InstallRequest) *ospb.InstallResponse {
	// Front end marshals the request, and sends to the sonic-host-service.
	// Back end is expected to return the response in JSON format.

	log.Infof("Received TransferEnd event for InstallRequest: %v", req)
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
	if err != nil {
		log.Errorf("Received error from OSServer.TransferEnd: err: %v, reqStr: %v, respStr: %v", err, reqStr, respStr)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Received error from OSServer.TransferEnd: err: %v, reqStr: %v, respStr: %v", err, reqStr, respStr),
				},
			},
		}

	}
	resp := &ospb.InstallResponse{}
	log.Infof("processTransferEnd: InstallResponse = resp: %v", resp)
	if err := json.Unmarshal([]byte(respStr), resp); err != nil {
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to unmarshal TransferEnd JSON: err: %v, respStr: %v", err, respStr),
				},
			},
		}

	}
	log.Infof("Successfully processed TransferEnd and returning %v", resp)
	return resp
}

func (srv *OSServer) processTransferContent(transferContent []byte, imgPath string) *ospb.InstallResponse {
	log.Infof("processTransferContent to %v", imgPath)

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
		log.Errorf("Path traversal attempt detected: %s", imgPath)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Invalid image path: %s", imgPath),
				},
			},
		}
	}

	log.Infof("Processing transfer content for file: %s", safePath)

	// Ensure the directory exists before writing the file
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Errorf("Failed to create directory for file: %s, error: %v", dir, err)
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
		log.Errorf("Failed to open or create file for writing: %s, error: %v", safePath, err)
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
		log.Errorf("Failed to write to file: %s, error: %v", safePath, err)
		return &ospb.InstallResponse{
			Response: &ospb.InstallResponse_InstallError{
				InstallError: &ospb.InstallError{
					Detail: fmt.Sprintf("Failed to write to file [%s].", safePath),
				},
			},
		}
	}

	if err := f.Close(); err != nil {
		log.Errorf("Failed to close file after writing: %s, error: %v", safePath, err)
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
	log.Infof("Successfully wrote %d bytes to file: %s. Total bytes received: %d", len(transferContent), safePath, totalBytesReceived[safePath])

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
			log.Errorf("InstallResponse: %v", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
		return status.Errorf(codes.Aborted, "Concurrent Install RPCs are not allowed.")
	}
	defer sem.Unlock()

	// Receive TransferReq message.
	req, err := stream.Recv()
	log.Infof("Received TransferRequest")
	if err == io.EOF {
		log.V(1).Infoln("Received EOF instead of TransferRequest!")
		return nil
	}
	if err != nil {
		log.V(1).Infoln("Received error: ", err)
		return status.Errorf(codes.Aborted, err.Error())
	}
	transferReq := req.GetTransferRequest()
	if transferReq == nil {
		log.V(1).Infoln("Did not receive a TransferRequest.")
		err = status.Errorf(codes.InvalidArgument, "Expected TransferRequest.")
		return err
	}
	resp := srv.processTransferReq(req)
	log.Infof("Processed TransferRequest=%v", resp)

	if resp == nil {
		log.V(1).Infof("Returning codes.Unimplemented as backend not supported")
		return status.Errorf(codes.Unimplemented, "OS Install not supported")
	}

	if resp != nil {
		if err := stream.Send(resp); err != nil {
			log.V(1).Infoln("Error while sending TransferRequest response: ", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}

	if resp == nil || resp.GetInstallError() != nil {
		log.Infof("Failed to process TransferRequest =%v", resp.GetInstallError())
		err = status.Errorf(codes.Aborted, "Failed to process TransferRequest.")
		return err
	}

	imgPath := srv.getVersionPath(transferReq.GetVersion())
	imgTransferInitiated := false
	for {
		req, err = stream.Recv()
		log.Infof("Received TransferContent request: req=%v err=%v", req, err)
		if err == io.EOF {
			log.Errorf("Received EOF instead of TransferContent request!")
			// Handle cleanup for incomplete transfer
			if imgTransferInitiated {
				srv.removeIncompleteTransfer(imgPath)
			}
			return status.Errorf(codes.Aborted, "Stream closed prematurely during transfer.")
		}
		if err != nil {
			log.Errorf("Error with TransferContent request=%v", err)
			if imgTransferInitiated {
				srv.removeIncompleteTransfer(imgPath)
			}
			return status.Errorf(codes.Aborted, err.Error())
		}
		if transferReq := req.GetTransferRequest(); transferReq != nil {
			log.Errorf("Received a TransferReq out-of-sequence.")
			if imgTransferInitiated {
				srv.removeIncompleteTransfer(imgPath)
			}
			err = status.Errorf(codes.InvalidArgument, "Expected TransferContent, or TransferEnd.")
			return err
		}
		// Transferring content is complete.
		if trfEnd := req.GetTransferEnd(); trfEnd != nil {
			log.Infof("Transferring content is complete.")
			break
		}
		// Process content transfer.
		// If image exists, target should have sent Validated | InstallError on TransferRequest.
		if !imgTransferInitiated && srv.imageExists(imgPath) {
			resp := &ospb.InstallResponse{
				Response: &ospb.InstallResponse_InstallError{
					InstallError: &ospb.InstallError{
						Detail: fmt.Sprintf("File exists [%s]!", imgPath),
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				log.Errorf("InstallResponse error for TransferContent=%v", err)
			}
			err = status.Errorf(codes.Aborted, "Failed as image exists!")
			log.Errorf("TransferContent failed as image %v exists", imgPath)
			return err
		}
		imgTransferInitiated = true
		resp := srv.processTransferContent(req.GetTransferContent(), imgPath)
		log.Infof("processTransferContent response=%v", resp)
		if resp != nil {
			if err := stream.Send(resp); err != nil {
				log.Errorf("Error while sending processTransferContent response: %v", err)
				srv.removeIncompleteTransfer(imgPath)
				return status.Errorf(codes.Aborted, err.Error())
			}
		}
		if resp == nil || resp.GetInstallError() != nil {
			srv.removeIncompleteTransfer(imgPath)
			err = status.Errorf(codes.Aborted, "Failed to process TransferContent.")
			log.Errorf("Failed to process TransferContent=%v", err)
			return err
		}
	}
	// Receive TransferEnd message.
	trfEnd := req.GetTransferEnd()
	log.Infof("Received TransferEnd")
	if trfEnd == nil {
		log.V(1).Infoln("Did not receive a TransferEnd")
		srv.removeIncompleteTransfer(imgPath)
		err = status.Errorf(codes.InvalidArgument, "Expected TransferEnd")
		return err
	}
	resp = srv.processTransferEnd(req)
	log.Infof("Processed TransferEnd=%v", resp)
	if resp != nil {
		if err := stream.Send(resp); err != nil {
			log.Errorf("Error while sending TransferEnd response:%v. Aborting..", err)
			srv.removeIncompleteTransfer(imgPath)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
	if resp == nil || resp.GetInstallError() != nil {
		srv.removeIncompleteTransfer(imgPath)
		err = status.Errorf(codes.Aborted, "Failed to process TransferEnd.")
		log.Errorf("Failed to process TransferEnd=%v", resp.GetInstallError())
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
