package gnmi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"

	"github.com/sonic-net/sonic-gnmi/pathz_authorizer"

	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	"github.com/openconfig/gnsi/pathz"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	pathzMu sync.Mutex
)

const (
	pathzTbl          string        = "PATHZ_POLICY|"
	pathzVersionFld   string        = "pathz_version"
	pathzCreatedOnFld string        = "pathz_created_on"
	pathzPolicyActive pathzInstance = "ACTIVE"
	// support for sandbox not yet implemented
	pathzPolicySandbox pathzInstance = "SANDBOX"
)

type pathzInstance string
type PathzMetadata struct {
	PathzVersion   string `json:"pathz_version"`
	PathzCreatedOn string `json:"pathz_created_on"`
}

type GNSIPathzServer struct {
	*Server
	pathzProcessor      pathz_authorizer.GnmiAuthzProcessorInterface
	pathzMetadata       *PathzMetadata
	pathzMetadataCopy   *PathzMetadata
	policyCopy          *pathz.AuthorizationPolicy
	policyUpdated       bool
	pathzV1Policy       string
	pathzV1PolicyBackup string
	pathz.UnimplementedPathzServer
}

func NewPathzMetadata() *PathzMetadata {
	return &PathzMetadata{
		PathzVersion:   "unknown",
		PathzCreatedOn: "0",
	}
}

func (srv *GNSIPathzServer) Probe(context.Context, *pathz.ProbeRequest) (*pathz.ProbeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Probe not implemented")
}

func (srv *GNSIPathzServer) Get(context.Context, *pathz.GetRequest) (*pathz.GetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Get not implemented")
}
func NewGNSIPathzServer(srv *Server) *GNSIPathzServer {
	ret := &GNSIPathzServer{
		Server:              srv,
		pathzProcessor:      &pathz_authorizer.GnmiAuthzProcessor{},
		pathzMetadata:       NewPathzMetadata(),
		pathzV1Policy:       srv.config.PathzPolicyFile,
		pathzV1PolicyBackup: srv.config.PathzPolicyFile + ".backup",
	}
	if err := ret.loadPathzFreshness(srv.config.PathzMetaFile); err != nil {
		log.V(0).Info(err)
	}
	ret.writePathzMetadataToDB(pathzPolicyActive)
	if srv.config.PathzPolicy {
		if err := ret.pathzProcessor.UpdatePolicyFromFile(ret.pathzV1Policy); err != nil {
			log.V(0).Infof("Failed to load gNMI pathz file %s: %v", ret.pathzV1Policy, err)
		}
	}
	return ret
}

func (srv *GNSIPathzServer) savePathzFileFreshess(path string) error {
	log.V(2).Infof("Saving pathz metadata to file: %s", path)
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(*srv.pathzMetadata); err != nil {
		log.V(0).Info(err)
		return err
	}
	return attemptWrite(path, buf.Bytes(), 0o644)
}

func (srv *GNSIPathzServer) loadPathzFreshness(path string) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, srv.pathzMetadata)
}

func (srv *GNSIPathzServer) savePathzPolicyToFile(p *pathz.AuthorizationPolicy) (string, error) {
	content := proto.MarshalTextString(p)
	log.V(3).Infof("Saving pathz policy to file: %s", srv.pathzV1Policy)
	return content, attemptWrite(srv.pathzV1Policy, []byte(content), 0o644)
}

func (srv *GNSIPathzServer) verifyPathzFile(c string) error {
	content, err := os.ReadFile(srv.pathzV1Policy)
	if err != nil {
		return err
	}
	if c != string(content) {
		return fmt.Errorf("Pathz file %s contains error.", srv.pathzV1Policy)
	}
	return nil
}

func (srv *GNSIPathzServer) writePathzMetadataToDB(instance pathzInstance) error {
	id := string(instance)
	log.V(2).Infof("Writing pathz metadata to DB: %s Version: %s CreatedOn: %s", id, srv.pathzMetadata.PathzVersion, srv.pathzMetadata.PathzCreatedOn)
	if err := writeCredentialsMetadataToDB(pathzTbl+id, "", pathzVersionFld, srv.pathzMetadata.PathzVersion); err != nil {
		return err
	}
	return writeCredentialsMetadataToDB(pathzTbl+id, "", pathzCreatedOnFld, srv.pathzMetadata.PathzCreatedOn)
}

func (srv *GNSIPathzServer) updatePolicy(p *pathz.AuthorizationPolicy) error {
	log.V(2).Info("Updating gNMI pathz policy")
	log.V(3).Infof("Policy: %v", p.String())
	c, err := srv.savePathzPolicyToFile(p)
	if err != nil {
		return err
	}
	if err := srv.verifyPathzFile(c); err != nil {
		log.V(0).Infof("Failed to verify gNMI pathz policy: %v", err)
		return err
	}
	err = srv.pathzProcessor.UpdatePolicyFromProto(p)
	if err != nil {
		log.V(0).Infof("Failed to update gNMI pathz policy: %v", err)
	}
	return err
}

func (srv *GNSIPathzServer) createCheckpoint() error {
	log.V(2).Info("Creating gNMI pathz policy checkpoint")
	srv.policyCopy = srv.pathzProcessor.GetPolicy()
	srv.policyUpdated = false
	srv.pathzMetadataCopy = srv.pathzMetadata
	return copyFile(srv.pathzV1Policy, srv.pathzV1PolicyBackup)
}

func (srv *GNSIPathzServer) revertPolicy() error {
	log.V(2).Info("Reverting gNMI pathz policy")
	if srv.policyUpdated {
		srv.policyUpdated = false
		if err := srv.pathzProcessor.UpdatePolicyFromProto(srv.policyCopy); err != nil {
			log.V(0).Infof("Failed to revert gNMI pathz policy: %v", err)
			os.Remove(srv.pathzV1PolicyBackup)
			return err
		}
	}
	srv.pathzMetadata = srv.pathzMetadataCopy
	return os.Rename(srv.pathzV1PolicyBackup, srv.pathzV1Policy)
}

func (srv *GNSIPathzServer) commitChanges() error {
	log.V(2).Info("Committing gNMI pathz policy changes")
	if err := srv.writePathzMetadataToDB(pathzPolicyActive); err != nil {
		return err
	}
	return srv.savePathzFileFreshess(srv.config.PathzMetaFile)
}

// Rotate implements the gNSI.pathz.Rotate RPC.
func (srv *GNSIPathzServer) Rotate(stream pathz.Pathz_RotateServer) error {
	log.V(2).Info("gNSI pathz Rotate RPC")
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return err
	}
	// Concurrent Pathz RPCs are not allowed.
	if !pathzMu.TryLock() {
		log.V(0).Infoln("Concurrent Pathz RPCs are not allowed")
		return status.Errorf(codes.Aborted, "Concurrent Pathz RPCs are not allowed")
	}
	defer pathzMu.Unlock()
	if err := fileCheck(srv.pathzV1Policy); err != nil {
		log.V(0).Infof("Error in reading file %s: %v", srv.pathzV1Policy, err)
		return status.Errorf(codes.NotFound, "Error in reading file %s: %v", srv.pathzV1Policy, err)
	}
	if err := srv.createCheckpoint(); err != nil {
		log.V(0).Infof("Error in creating checkpoint: %v", err)
		return status.Errorf(codes.Aborted, "Error in creating checkpoint: %v", err)
	}
	for {
		req, err := stream.Recv()
		log.V(3).Infof("Received a Rotate request message: %v", req.String())
		if err == io.EOF {
			log.V(0).Infoln("Received EOF instead of a UploadRequest/Finalize request! Reverting to last good state")
			// Connection closed without Finalize message. Revert all changes made until now.
			if err := srv.revertPolicy(); err != nil {
				return status.Errorf(codes.Aborted, "Error in reverting policy: %v", err)
			}
			return status.Errorf(codes.Aborted, "No Finalize message")
		}
		if err != nil {
			log.V(0).Infof("Reverting to last good state Received error: %v", err)
			// Connection closed without Finalize message. Revert all changes made until now.
			srv.revertPolicy()
			return status.Errorf(codes.Aborted, err.Error())
		}
		if endReq := req.GetFinalizeRotation(); endReq != nil {
			// This is the last message. All changes are final.
			log.V(2).Infof("Received a Finalize request message: %v", endReq)
			if !srv.policyUpdated {
				log.V(0).Infoln("Received finalize message without successful rotation")
				srv.revertPolicy()
				return status.Errorf(codes.Aborted, "Received finalize message without successful rotation")
			}
			if err := srv.commitChanges(); err != nil {
				// Revert won't be called if the final commit fails.
				return status.Errorf(codes.Aborted, "Final policy commit fails: %v", err)
			}
			os.Remove(srv.pathzV1PolicyBackup)
			return nil
		}
		resp, err := srv.processRotateRequest(req)
		if err != nil {
			log.V(0).Infof("Reverting to last good state; While processing a rotate request got error: %v", err)
			// Connection closed without Finalize message. Revert all changes made until now.
			srv.revertPolicy()
			return err
		}
		if err := stream.Send(resp); err != nil {
			log.V(0).Infof("Reverting to last good state; While sending a confirmation got error: %v", err)
			// Connection closed without Finalize message. Revert all changes made until now.
			srv.revertPolicy()
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
}

func (srv *GNSIPathzServer) processRotateRequest(req *pathz.RotateRequest) (*pathz.RotateResponse, error) {
	policyReq := req.GetUploadRequest()
	if policyReq == nil {
		return nil, status.Errorf(codes.Aborted, "Unknown request: %v", req)
	}
	log.V(2).Infof("Received a gNSI.Pathz UploadRequest request message")
	if len(policyReq.GetVersion()) == 0 {
		return nil, status.Errorf(codes.Aborted, "Pathz policy version cannot be empty")
	}
	if srv.pathzMetadata.PathzVersion == policyReq.GetVersion() && !req.GetForceOverwrite() {
		return nil, status.Errorf(codes.AlreadyExists, "Pathz with version `%v` already exists", policyReq.GetVersion())
	}
	srv.pathzMetadata.PathzVersion = policyReq.GetVersion()
	srv.pathzMetadata.PathzCreatedOn = strconv.FormatUint(policyReq.GetCreatedOn(), 10)
	if err := srv.updatePolicy(policyReq.GetPolicy()); err != nil {
		return nil, status.Errorf(codes.Aborted, err.Error())
	}
	srv.policyUpdated = true
	resp := &pathz.RotateResponse{
		Response: &pathz.RotateResponse_Upload{},
	}
	return resp, nil
}
func attemptWrite(name string, data []byte, perm os.FileMode) error {
	log.V(2).Infof("Writing: %s", name)
	err := os.WriteFile(name, data, perm)
	if err != nil {
		if e := os.Remove(name); e != nil {
			err = fmt.Errorf("Write %s failed: %w; Cleanup failed", name, err)
		}
	}
	return err
}
