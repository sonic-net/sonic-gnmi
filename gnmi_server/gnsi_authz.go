package gnmi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	log "github.com/golang/glog"
	"github.com/openconfig/gnsi/authz"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var (
	authzMu sync.Mutex
)

const (
	authzP4rtTbl      string = "AUTHZ_POLICY|p4rt"
	authzGnxiTbl      string = "AUTHZ_POLICY|gnxi"
	authzVersionFld   string = "authz_version"
	authzCreatedOnFld string = "authz_created_on"
	backupExt         string = ".bak"
)

type GNSIAuthzServer struct {
	*Server
	authzMetadata     *AuthzMetadata
	authzMetadataCopy AuthzMetadata
	authz.UnimplementedAuthzServer
}

func (srv *GNSIAuthzServer) Probe(context.Context, *authz.ProbeRequest) (*authz.ProbeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Probe not implemented")
}
func (srv *GNSIAuthzServer) Get(context.Context, *authz.GetRequest) (*authz.GetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Get not implemented")
}
func NewGNSIAuthzServer(srv *Server) *GNSIAuthzServer {
	ret := &GNSIAuthzServer{
		Server:        srv,
		authzMetadata: NewAuthzMetadata(),
	}
	log.V(2).Infof("gnsi: loading authz metadata from %s", srv.config.AuthzMetaFile)
	log.V(2).Infof("gnsi: loading authz policy from %s", srv.config.AuthzPolicyFile)
	if err := ret.loadAuthzFreshness(srv.config.AuthzMetaFile); err != nil {
		log.V(0).Info(err)
	}
	ret.authzMetadataCopy = *ret.authzMetadata
	ret.writeAuthzMetadataToDB(authzVersionFld, ret.authzMetadata.AuthzVersion)
	ret.writeAuthzMetadataToDB(authzCreatedOnFld, ret.authzMetadata.AuthzCreatedOn)
	return ret
}

// Rotate implements the gNSI.authz.Rotate RPC.
func (srv *GNSIAuthzServer) Rotate(stream authz.Authz_RotateServer) error {
	ctx := stream.Context()
	log.Infof("GNSI Authz Rotate RPC")
	_, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		log.Errorf("authentication failed in Rotate RPC: %v", err)
		return err
	}
	session := time.Now().Nanosecond()
	// Concurrent Authz RPCs are not allowed.
	if !authzMu.TryLock() {
		log.V(0).Infof("[%v]gNSI: authz.Rotate already in use", session)
		return status.Errorf(codes.Aborted, "concurrent authz.Rotate RPCs are not allowed")
	}
	defer authzMu.Unlock()

	log.V(2).Infof("[%v]gNSI: Begin authz.Rotate", session)
	defer log.V(2).Infof("[%v]gNSI: End authz.Rotate", session)

	srv.checkpointAuthzFreshness()
	if err := srv.checkpointAuthzFile(); err != nil {
		log.V(0).Infof("Failure during Authz checkpoint: %v", err)
	}
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			log.V(0).Infof("[%v]gNSI: Received unexpected EOF", session)
			// Connection closed without Finalize message. Revert all changes made until now.
			if err := copyFile(srv.config.AuthzPolicyFile+backupExt, srv.config.AuthzPolicyFile); err != nil {
				log.V(0).Infof("[%v]gnsi: failed to revert authz policy file (%v): %v", session, srv.config.AuthzPolicyFile, err)
			}
			srv.revertAuthzFileFreshness()
			return status.Errorf(codes.Aborted, "No Finalize message")
		}
		if err != nil {
			log.V(0).Infof("[%v]gnsi: while processing a rotate request got error: `%v`. Reverting to last good state.", session, err)
			// Connection closed without Finalize message. Revert all changes made until now.
			if err := copyFile(srv.config.AuthzPolicyFile+backupExt, srv.config.AuthzPolicyFile); err != nil {
				log.V(0).Infof("[%v]gnsi: failed to revert authz policy file (%v): %v", session, srv.config.AuthzPolicyFile, err)
			}
			srv.revertAuthzFileFreshness()
			return status.Errorf(codes.Aborted, err.Error())
		}
		if endReq := req.GetFinalizeRotation(); endReq != nil {
			// This is the last message. All changes are final.
			log.V(2).Infof("[%v]gNSI: Received Finalize: %v", session, endReq)
			srv.commitAuthzFileChanges()
			srv.saveAuthzFileFreshess(srv.config.AuthzMetaFile)
			return nil
		}
		resp, err := srv.processRotateRequest(req)
		if err != nil {
			log.V(0).Infof("[%v]gnsi: while processing a rotate request got error: `%v`. Reverting to last good state.", session, err)
			// Connection closed without Finalize message. Revert all changes made until now.
			if err := copyFile(srv.config.AuthzPolicyFile+backupExt, srv.config.AuthzPolicyFile); err != nil {
				log.V(0).Infof("[%v]gnsi: failed to revert authz policy file (%v): %v", session, srv.config.AuthzPolicyFile, err)
			}
			srv.revertAuthzFileFreshness()
			return err
		}
		if err := stream.Send(resp); err != nil {
			log.V(0).Infof("[%v]gnsi: while processing a rotate request got error: `%v`. Reverting to last good state.", session, err)
			// Connection closed without Finalize message. Revert all changes made until now.
			if err := copyFile(srv.config.AuthzPolicyFile+backupExt, srv.config.AuthzPolicyFile); err != nil {
				log.V(0).Infof("[%v]gnsi: failed to revert authz policy file (%v): %v", session, srv.config.AuthzPolicyFile, err)
			}
			srv.revertAuthzFileFreshness()
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
}

func (srv *GNSIAuthzServer) processRotateRequest(req *authz.RotateAuthzRequest) (*authz.RotateAuthzResponse, error) {
	policyReq := req.GetUploadRequest()
	if policyReq == nil {
		return nil, status.Errorf(codes.Aborted, `Unknown request: "%v"`, req)
	}
	log.V(2).Infof("received a gNSI.Authz UploadRequest request message")
	log.V(3).Infof("request message: %v", policyReq)
	if len(policyReq.GetPolicy()) == 0 {
		return nil, status.Errorf(codes.Aborted, "Authz policy cannot be empty!")
	}
	if len(policyReq.GetVersion()) == 0 {
		return nil, status.Errorf(codes.Aborted, "Authz policy version cannot be empty!")
	}
	if !json.Valid([]byte(policyReq.GetPolicy())) {
		return nil, status.Errorf(codes.Aborted, "Authz policy `%v` is malformed", policyReq.GetPolicy())
	}
	if err := fileCheck(srv.config.AuthzPolicyFile); err != nil {
		return nil, status.Errorf(codes.NotFound, "Error in reading file %s: %v. Please try Install.", srv.config.AuthzPolicyFile, err)
	}
	if srv.gnsiAuthz.authzMetadata.AuthzVersion == policyReq.GetVersion() && !req.GetForceOverwrite() {
		return nil, status.Errorf(codes.AlreadyExists, "Authz with version `%v` already exists", policyReq.GetVersion())
	}
	if err := srv.writeAuthzMetadataToDB(authzVersionFld, policyReq.GetVersion()); err != nil {
		return nil, status.Errorf(codes.Aborted, err.Error())
	}
	if err := srv.writeAuthzMetadataToDB(authzCreatedOnFld, strconv.FormatUint(policyReq.GetCreatedOn(), 10)); err != nil {
		return nil, status.Errorf(codes.Aborted, err.Error())
	}
	if err := srv.saveToAuthzFile(policyReq.GetPolicy()); err != nil {
		return nil, status.Errorf(codes.Aborted, err.Error())
	}
	resp := &authz.RotateAuthzResponse{
		RotateResponse: &authz.RotateAuthzResponse_UploadResponse{},
	}
	return resp, nil
}

func (srv *GNSIAuthzServer) saveToAuthzFile(p string) error {
	tmpDst, err := os.CreateTemp(filepath.Dir(srv.config.AuthzPolicyFile), filepath.Base(srv.config.AuthzPolicyFile))
	if err != nil {
		return err
	}
	if _, err := tmpDst.Write([]byte(p)); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(1).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	if err := tmpDst.Close(); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(1).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	if err := os.Rename(tmpDst.Name(), srv.config.AuthzPolicyFile); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(1).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	return os.Chmod(srv.config.AuthzPolicyFile, 0600)
}

func (srv *GNSIAuthzServer) checkpointAuthzFile() error {
	log.V(2).Infof("Checkpoint authz file: %v", srv.config.AuthzPolicyFile)
	return copyFile(srv.config.AuthzPolicyFile, srv.config.AuthzPolicyFile+backupExt)
}

func (srv *GNSIAuthzServer) commitAuthzFileChanges() error {
	// Check if the active policy file exists.
	srcStat, err := os.Stat(srv.config.AuthzPolicyFile)
	if err != nil {
		return err
	}
	if !srcStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", srv.config.AuthzPolicyFile)
	}
	// OK. Now the backup can be deleted.
	backup := srv.config.AuthzPolicyFile + backupExt
	backupStat, err := os.Stat(backup)
	if err != nil {
		// Already does not exist.
		return nil
	}
	if !backupStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file; did not remove it.", backup)
	}
	return os.Remove(backup)
}

// writeAuthzMetadataToDB writes the credentials freshness data to the DB.
func (srv *GNSIAuthzServer) writeAuthzMetadataToDB(fld, val string) error {
	if err := writeCredentialsMetadataToDB(authzP4rtTbl, "", fld, val); err != nil {
		return err
	}
	if err := writeCredentialsMetadataToDB(authzGnxiTbl, "", fld, val); err != nil {
		return err
	}
	switch fld {
	case authzVersionFld:
		srv.authzMetadata.AuthzVersion = val
	case authzCreatedOnFld:
		srv.authzMetadata.AuthzCreatedOn = val
	}
	return nil
}

type AuthzMetadata struct {
	AuthzVersion   string `json:"authz_version"`
	AuthzCreatedOn string `json:"authz_created_on"`
}

func NewAuthzMetadata() *AuthzMetadata {
	return &AuthzMetadata{
		AuthzVersion:   "unknown",
		AuthzCreatedOn: "0",
	}
}

func (srv *GNSIAuthzServer) checkpointAuthzFreshness() {
	log.V(2).Infof("checkpoint authz freshness")
	srv.authzMetadataCopy = *srv.authzMetadata
}

func (srv *GNSIAuthzServer) revertAuthzFileFreshness() {
	log.V(2).Infof("revert authz freshness")
	srv.writeAuthzMetadataToDB(authzVersionFld, srv.authzMetadataCopy.AuthzVersion)
	srv.writeAuthzMetadataToDB(authzCreatedOnFld, srv.authzMetadataCopy.AuthzCreatedOn)
}

func (srv *GNSIAuthzServer) saveAuthzFileFreshess(path string) error {
	log.V(2).Infof("save authz metadata to file: %v", path)
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(*srv.authzMetadata); err != nil {
		log.V(0).Info(err)
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		if e := os.Remove(path); e != nil {
			err = fmt.Errorf("Write %s failed: %w; Cleanup failed", path, err)
		}
		return err
	}
	return nil
}

func (srv *GNSIAuthzServer) loadAuthzFreshness(path string) error {
	log.V(2).Infof("load authz metadata from file: %v", path)
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, srv.authzMetadata)
}
