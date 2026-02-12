package gnmi

import (
	"bytes"
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"

	log "github.com/golang/glog"
	credz "github.com/openconfig/gnsi/credentialz"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	credzMu sync.Mutex
)

const (
	sshAccountTbl               string = "SSH_ACCOUNT"
	sshHostTbl                  string = "SSH_HOST"
	consoleAccountTbl           string = "CONSOLE_ACCOUNT"
	sshKeysVersionFld           string = "keys_version"
	sshKeysCreatedOnFld         string = "keys_created_on"
	sshPrincipalsVersionFld     string = "principals_version"
	sshPrincipalsCreatedOnFld   string = "principals_created_on"
	sshCaKeysVersionFld         string = "ca_keys_version"
	sshCaKeysCreatedOnFld       string = "ca_keys_created_on"
	consolePasswordVersionFld   string = "password_version"
	consolePasswordCreatedOnFld string = "password_created_on"

	// glomeConfigRedisKey is GLOME config's Redis key appended to credentialsTbl.
	glomeConfigRedisKey = "GLOME_CONFIG"
)

type cpType int

const (
	consoleCP cpType = iota
	sshCP
)

var (
	sshKeyTypePrefix = map[credz.KeyType]string{
		credz.KeyType_KEY_TYPE_UNSPECIFIED: "unspecified",
		credz.KeyType_KEY_TYPE_ECDSA_P_256: "ecdsa-sha2-nistp256",
		credz.KeyType_KEY_TYPE_ECDSA_P_521: "ecdsa-sha2-nistp521",
		credz.KeyType_KEY_TYPE_ED25519:     "ssh-ed25519",
		credz.KeyType_KEY_TYPE_RSA_2048:    "ssh-rsa",
		credz.KeyType_KEY_TYPE_RSA_4096:    "ssh-rsa",
	}
)

type GNSICredentialzServer struct {
	*Server
	sshCredMetadata         *SshCredMetadata
	sshCredMetadataCopy     SshCredMetadata
	consoleCredMetadata     *ConsoleCredMetadata
	consoleCredMetadataCopy ConsoleCredMetadata
	// glomeConfigMetadata is used to store rollback data for STATE_DB
	glomeConfigMetadata *GlomeConfigMetadata
	stateDbClient       *redis.Client

	credz.UnimplementedCredentialzServer
}

func NewGNSICredentialzServer(srv *Server) *GNSICredentialzServer {
	stateDbClient, err := NewStateDBClient()
	if err != nil {
		log.V(0).Infof("Failed to create STATE_DB client: %v", err)
	}
	defer stateDbClient.Close()

	ret := &GNSICredentialzServer{
		Server:              srv,
		sshCredMetadata:     NewSshCredMetadata(),
		consoleCredMetadata: NewConsoleCredMetadata(),
		stateDbClient:       stateDbClient,
	}
	// Load the SSH Creds from host OS to update the STATE_DB with the switch system's current state.
	// The switch needs to be supplied with the initial SSH keys (gnetch keys) before gNSI is up and running.
	if err := ret.loadCredentialFreshness(srv.config.SshCredMetaFile); err != nil {
		log.V(0).Infof("srv.config.SshCredMetaFile=%s error=%v", srv.config.SshCredMetaFile, err)
	}
	if err := ret.loadConsoleCredentialFreshness(srv.config.ConsoleCredMetaFile); err != nil {
		log.V(0).Infof("srv.config.ConsoleCredMetaFile=%s error=%v", srv.config.ConsoleCredMetaFile, err)
	}
	ret.sshCredMetadataCopy = *ret.sshCredMetadata
	ret.writeSshHostCredentialsMetadataToDB(sshCaKeysVersionFld, ret.sshCredMetadata.Host.CaKeysVersion)
	ret.writeSshHostCredentialsMetadataToDB(sshCaKeysCreatedOnFld, ret.sshCredMetadata.Host.CaKeysCreatedOn)
	for a, u := range ret.sshCredMetadata.Accounts {
		ret.writeSshAccountCredentialsMetadataToDB(a, sshKeysVersionFld, u.KeysVersion)
		ret.writeSshAccountCredentialsMetadataToDB(a, sshKeysCreatedOnFld, u.KeysCreatedOn)
		ret.writeSshAccountCredentialsMetadataToDB(a, sshPrincipalsVersionFld, u.UsersVersion)
		ret.writeSshAccountCredentialsMetadataToDB(a, sshPrincipalsCreatedOnFld, u.UsersCreatedOn)
	}
	ret.consoleCredMetadataCopy = *ret.consoleCredMetadata
	for a, u := range ret.consoleCredMetadata.Accounts {
		ret.writeConsoleAccountCredentialsMetadataToDB(a, consolePasswordVersionFld, u.PasswordVersion)
		ret.writeConsoleAccountCredentialsMetadataToDB(a, consolePasswordCreatedOnFld, u.PasswordCreatedOn)
	}

	// Initialize the Glome config metadata from STATE_DB data to use as rollback data.
	ret.InitGlomeConfigMetadata(context.Background())

	return ret
}

func (srv *GNSICredentialzServer) CanGenerateKey(context.Context, *credz.CanGenerateKeyRequest) (*credz.CanGenerateKeyResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CanGenerateKey not implemented")
}

func (srv *GNSICredentialzServer) GetPublicKeys(context.Context, *credz.GetPublicKeysRequest) (*credz.GetPublicKeysResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CanGenerateKey not implemented")
}

// RotateAccountCredentials implements corresponding RPC
func (srv *GNSICredentialzServer) RotateAccountCredentials(stream credz.Credentialz_RotateAccountCredentialsServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return err
	}
	// Concurrent Rotate{Account|Host} RPCs are not allowed.
	if !credzMu.TryLock() {
		log.V(0).Infoln("Concurrent Rotate{Account|Host} RPCs are not allowed.")
		return status.Errorf(codes.Aborted, "Concurrent Rotate{Account|Host} RPCs are not allowed.")
	}
	defer credzMu.Unlock()

	log.V(2).Info("gNSI: credz.RotateAccountCredentials")

	consoleBackup := false
	sshBackup := false

	for {
		req, err := stream.Recv()
		if err != nil {
			log.V(0).Infoln("Received error: ", err)
			if sshBackup {
				srv.checkpointRestore(sshCP)
			}
			if consoleBackup {
				srv.checkpointRestore(consoleCP)
			}
			return status.Errorf(codes.Aborted, err.Error())
		}

		log.V(3).Infof("Received: %T", req.GetRequest())
		var resp *credz.RotateAccountCredentialsResponse

		switch r := req.GetRequest().(type) {
		case *credz.RotateAccountCredentialsRequest_Finalize:
			if endReq := req.GetFinalize(); endReq != nil {
				// This is the last message. All changes are final.
				log.V(0).Infof("Received a Finalize request message %v", endReq)
				if sshBackup {
					srv.checkpointDelete(sshCP)
				}
				if consoleBackup {
					srv.checkpointDelete(consoleCP)
				}
				return nil
			}
			return status.Errorf(codes.Aborted, err.Error())
		case *credz.RotateAccountCredentialsRequest_Password:
			log.V(2).Info("Received a Password request")
			if !consoleBackup {
				log.V(3).Infof("Checkpoint create: %v", consoleCP)
				consoleBackup = true
				if err := srv.checkpointCreate(consoleCP); err != nil {
					return err
				}
				defer srv.saveConsoleCredentialsFreshness(srv.config.ConsoleCredMetaFile)
			}
			resp, err = srv.processConsolePassword(req)
		case *credz.RotateAccountCredentialsRequest_Credential:
			if !sshBackup {
				log.V(3).Infof("Checkpoint create: %v", sshCP)
				sshBackup = true
				if err := srv.checkpointCreate(sshCP); err != nil {
					return err
				}
				defer srv.saveCredentialsFreshness(srv.config.SshCredMetaFile)
			}
			resp, err = srv.processSshCred(req)
		case *credz.RotateAccountCredentialsRequest_User:
			if !sshBackup {
				log.V(3).Infof("Checkpoint create: %v", sshCP)
				sshBackup = true
				if err := srv.checkpointCreate(sshCP); err != nil {
					return err
				}
				defer srv.saveCredentialsFreshness(srv.config.SshCredMetaFile)
			}
			resp, err = srv.processSshUser(req)
		default:
			return status.Errorf(codes.Aborted, "Unknown Request: %+v", r)
		}
		if err != nil {
			log.V(0).Infof("Failed to process request: %v", err)
			if sshBackup {
				srv.checkpointRestore(sshCP)
			}
			if consoleBackup {
				srv.checkpointRestore(consoleCP)
			}
			return status.Errorf(codes.Aborted, err.Error())
		}
		log.V(2).Info("Finished process request")
		if err := stream.Send(resp); err != nil {
			if sshBackup {
				srv.checkpointRestore(sshCP)
			}
			if consoleBackup {
				srv.checkpointRestore(consoleCP)
			}
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
}

// RotateHostParameters implements corresponding RPC
func (srv *GNSICredentialzServer) RotateHostParameters(stream credz.Credentialz_RotateHostParametersServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return err
	}

	// Concurrent Rotate{Account|Host}Credentials RPCs are not allowed.
	if !credzMu.TryLock() {
		return status.Errorf(codes.Aborted, "Concurrent Mutate{Account|Host}Credentials RPCs are not allowed.")
	}
	defer credzMu.Unlock()
	log.V(2).Info("gNSI: credz.RotateHostParameters")

	// Main loop to process requests from the stream.
	// - Before request handler is called, no checkpoint is created thus there is
	//   nothing to rollback. The checkpoint is managed by each request handler.
	// - After request handler returns, it implies that the transaction is completed
	//   (request and Finalize are processed). Thus, there is nothing to rollback.
	// - After a valid flow of requests are processed, close the stream. The valid
	//   flows are defined in: https://github.com/openconfig/gnsi/blob/main/credentialz/credentialz.proto).
	for {
		req, err := stream.Recv()
		if err != nil {
			return status.Errorf(codes.Aborted, err.Error())
		}

		// handlerErr is the error returned by the handler functions (e.g. handleGlome, etc).
		var handlerErr error
		switch r := req.GetRequest().(type) {
		case *credz.RotateHostParametersRequest_SshCaPublicKey:
			handlerErr = srv.handleSshCaPublicKey(ctx, stream, req)
		case *credz.RotateHostParametersRequest_Glome:
			handlerErr = srv.handleGlome(ctx, stream, req)
		case *credz.RotateHostParametersRequest_ServerKeys:
			return status.Errorf(codes.Unimplemented, "ServerKeys Unimplemented")
		case *credz.RotateHostParametersRequest_GenerateKeys:
			return status.Errorf(codes.Unimplemented, "GenerateKeys Unimplemented")
		case *credz.RotateHostParametersRequest_AuthenticationAllowed:
			return status.Errorf(codes.Unimplemented, "AuthenticationAllowed Unimplemented")
		case *credz.RotateHostParametersRequest_AuthorizedPrincipalCheck:
			return status.Errorf(codes.Unimplemented, "AuthorizedPrincipalCheck Unimplemented")
		case *credz.RotateHostParametersRequest_Finalize:
			return status.Errorf(codes.Aborted, "Finalize cannot be the first message in a transaction.")
		default:
			return status.Errorf(codes.Aborted, "Unknown Request: %+v", r)
		}

		if handlerErr != nil {
			return handlerErr
		}
		log.V(2).Info("Rotation request is completed successfully.")
		return nil
	}
}

// handleSshCaPublicKey handles the SSH CA public key request.
// It creates a checkpoint, processes the SSH CA public key, sends the response
// to the client, and expects a Finalize message.
func (srv *GNSICredentialzServer) handleSshCaPublicKey(ctx context.Context, stream credz.Credentialz_RotateHostParametersServer, req *credz.RotateHostParametersRequest) error {
	if err := srv.checkpointCreate(sshCP); err != nil {
		return err
	}
	defer srv.saveCredentialsFreshness(srv.config.SshCredMetaFile)

	resp, err := srv.processSshCaPublicKey(req)
	if err != nil {
		srv.checkpointRestore(sshCP)
		return status.Errorf(codes.Aborted, err.Error())
	}

	// Send the response to the client.
	if err := stream.Send(resp); err != nil {
		srv.checkpointRestore(sshCP)
		return status.Errorf(codes.Aborted, err.Error())
	}

	// Expect a Finalize message.
	req, err = stream.Recv()
	if err != nil {
		srv.checkpointRestore(sshCP)
		return status.Errorf(codes.Aborted, err.Error())
	}
	if _, ok := req.GetRequest().(*credz.RotateHostParametersRequest_Finalize); !ok {
		srv.checkpointRestore(sshCP)
		return status.Errorf(codes.Aborted, "Expected Finalize message, but received %T", req.GetRequest())
	}
	log.V(2).Info("Received a Finalize request message for SshCaPublicKey.")
	srv.checkpointDelete(sshCP)
	return nil
}

// handleGlome handles the Glome request. It creates a checkpoint, processes the Glome config,
// sends the response to the client, and expects a Finalize message.
func (srv *GNSICredentialzServer) handleGlome(ctx context.Context, stream credz.Credentialz_RotateHostParametersServer, req *credz.RotateHostParametersRequest) error {
	lastUpdated := time.Now().UnixNano()
	resp, err := srv.processGlomeConfig(ctx, req, lastUpdated)
	if err != nil {
		srv.glomeCheckpointRestore(ctx)
		return status.Errorf(codes.Aborted, err.Error())
	}

	// Send the response to the client.
	if err := stream.Send(resp); err != nil {
		srv.glomeCheckpointRestore(ctx)
		return status.Errorf(codes.Aborted, err.Error())
	}

	// Expect a Finalize message.
	req, err = stream.Recv()
	if err != nil {
		srv.glomeCheckpointRestore(ctx)
		return status.Errorf(codes.Aborted, err.Error())
	}
	if _, ok := req.GetRequest().(*credz.RotateHostParametersRequest_Finalize); !ok {
		srv.glomeCheckpointRestore(ctx)
		return status.Errorf(codes.Aborted, "Expected Finalize message, but received %T", req.GetRequest())
	}
	log.V(2).Info("Received a Finalize request message for Glome.")
	return nil
}

func (srv *GNSICredentialzServer) checkpointCreate(source cpType) error {
	log.V(3).Infof("Checkpoint Create: %v", source)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return err
	}
	if source == sshCP {
		log.V(3).Info("Creating SSH Checkpoint")
		err := sc.SSHCheckpoint(ssc.CredzCPCreate)
		if err != nil {
			return status.Errorf(codes.Internal, "Cannot start the ssh transaction:%v", err)
		}
		srv.checkpointSshCredentialFreshness()
	}
	if source == consoleCP {
		err := sc.ConsoleCheckpoint(ssc.CredzCPCreate)
		if err != nil {
			return status.Errorf(codes.Internal, "Cannot start the console transaction:%v", err)
		}
		srv.checkpointConsoleFreshness()
	}
	return nil
}

func (srv *GNSICredentialzServer) checkpointRestore(source cpType) {
	log.V(3).Infof("Checkpoint Restore: %v", source)
	sc, _ := ssc.NewDbusClient()
	if source == sshCP {
		if err := sc.SSHCheckpoint(ssc.CredzCPRestore); err != nil {
			log.V(0).Infof("Could not restore from checkpoint: %v", err)
		}
		srv.revertSshCredentialFreshness()
	}
	if source == consoleCP {
		if err := sc.ConsoleCheckpoint(ssc.CredzCPRestore); err != nil {
			log.V(0).Infof("Could not restore from checkpoint: %v", err)
		}
		srv.revertConsoleCredentialFreshness()
	}
}

// glomeCheckpointRestore rolls back the Glome configuration file in the host OS system,
// and also rollbacks the STATE_DB to the checkpoint state.
func (srv *GNSICredentialzServer) glomeCheckpointRestore(ctx context.Context) {
	log.V(3).Infof("Glome Checkpoint Restore with context: %v", ctx)
	dbusClient, _ := ssc.NewDbusClient()
	if err := dbusClient.GLOMERestoreCheckpoint(ctx); err != nil {
		log.V(0).Infof("Could not restore from GLOME checkpoint: %v", err)
	}

	// Restore the Glome Config in STATE_DB to the checkpoint.
	srv.writeGlomeConfigMetadataToStateDB(ctx, srv.glomeConfigMetadata)
}

func (srv *GNSICredentialzServer) checkpointDelete(source cpType) {
	log.V(3).Infof("Checkpoint Delete: %v", source)
	sc, _ := ssc.NewDbusClient()
	if source == sshCP {
		if err := sc.SSHCheckpoint(ssc.CredzCPDelete); err != nil {
			log.V(0).Infof("Could not delete checkpoint: %v", err)
		}
	}
	if source == consoleCP {
		if err := sc.ConsoleCheckpoint(ssc.CredzCPDelete); err != nil {
			log.V(0).Infof("Could not delete checkpoint: %v", err)
		}
	}

}

func (srv *GNSICredentialzServer) processSshCred(req *credz.RotateAccountCredentialsRequest) (*credz.RotateAccountCredentialsResponse, error) {
	credReq := req.GetCredential()
	log.V(3).Infof("Received a RotateAccountCredentials.Credential request message! %v", credReq)

	// Sanity checks.
	if len(credReq.GetCredentials()) == 0 {
		return nil, fmt.Errorf("credentials cannot be empty")
	}
	for _, set := range credReq.GetCredentials() {
		if set.GetVersion() == "" {
			return nil, fmt.Errorf("version cannot be empty")
		}
		if set.GetCreatedOn() == 0 {
			return nil, fmt.Errorf("created_on cannot be empty")
		}
		if len(set.GetAccount()) == 0 {
			return nil, fmt.Errorf("account cannot be empty")
		}
		if len(set.GetAuthorizedKeys()) == 0 {
			return nil, fmt.Errorf("authorized_keys cannot be empty")
		}
	}
	// Build the message to be sent to the back-end.
	var b strings.Builder
	fmt.Fprintf(&b, `{ "SshAccountKeys": [ `)
	for i, set := range credReq.GetCredentials() {
		fmt.Fprintf(&b, `{ "account": "%s", "keys": [`, set.Account)
		for i, key := range set.AuthorizedKeys {
			fmt.Fprintf(&b, ` { "key" : "%s %s %s", "options" : [`, sshKeyTypePrefix[key.GetKeyType()], b64.StdEncoding.EncodeToString(key.AuthorizedKey), key.Description)
			for i, o := range key.Options {
				fmt.Fprintf(&b, ` { "name" : "%v", "value": "%v" }`, o.GetName(), o.GetValue())
				if i < len(key.Options)-1 {
					fmt.Fprintf(&b, `,`)
				}
			}
			fmt.Fprintf(&b, ` ] }`)
			if i < len(set.AuthorizedKeys)-1 {
				fmt.Fprintf(&b, `,`)
			}
		}
		fmt.Fprintf(&b, ` ] }`)
		if i < len(credReq.GetCredentials())-1 {
			fmt.Fprintf(&b, `,`)
		}
	}
	fmt.Fprintf(&b, ` ] }`)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, err
	}
	if err := sc.SSHMgmtSet(b.String()); err != nil {
		return nil, err
	}
	for _, set := range credReq.GetCredentials() {
		if err := srv.writeSshAccountCredentialsMetadataToDB(set.Account, sshKeysVersionFld, set.GetVersion()); err != nil {
			return nil, err
		}
		if err := srv.writeSshAccountCredentialsMetadataToDB(set.Account, sshKeysCreatedOnFld, strconv.FormatUint(set.GetCreatedOn(), 10)); err != nil {
			return nil, err
		}
	}
	resp := &credz.RotateAccountCredentialsResponse{
		Response: &credz.RotateAccountCredentialsResponse_Credential{},
	}
	return resp, nil
}

func (srv *GNSICredentialzServer) processSshUser(req *credz.RotateAccountCredentialsRequest) (*credz.RotateAccountCredentialsResponse, error) {
	usrReq := req.GetUser()
	log.V(3).Infof("Received a RotateAccountCredentials.User request message! %v", usrReq)

	// Sanity checks.
	if len(usrReq.GetPolicies()) == 0 {
		return nil, fmt.Errorf("policies cannot be empty")
	}
	for _, set := range usrReq.GetPolicies() {
		if set.GetVersion() == "" {
			return nil, fmt.Errorf("version cannot be empty")
		}
		if set.GetCreatedOn() == 0 {
			return nil, fmt.Errorf("created_on cannot be empty")
		}
		if len(set.GetAccount()) == 0 {
			return nil, fmt.Errorf("account cannot be empty")
		}
		if len(set.GetAuthorizedPrincipals().GetAuthorizedPrincipals()) == 0 {
			return nil, fmt.Errorf("authorized_principals cannot be empty")
		}
	}
	// Build the message to be sent to the back-end.
	var b strings.Builder
	fmt.Fprintf(&b, `{ "SshAccountUsers": [`)
	for i, set := range usrReq.GetPolicies() {
		fmt.Fprintf(&b, ` { "account": "%s", "users": [`, set.Account)
		for j, user := range set.GetAuthorizedPrincipals().GetAuthorizedPrincipals() {
			fmt.Fprintf(&b, ` { "name" : "%v", "options" : [`, user.GetAuthorizedUser())
			for k, o := range user.Options {
				fmt.Fprintf(&b, ` { "name" : "%v", "value": "%v" }`, o.GetName(), o.GetValue())
				if k < len(user.Options)-1 {
					fmt.Fprintf(&b, `,`)
				}
			}
			fmt.Fprintf(&b, ` ] }`)
			if j < len(set.GetAuthorizedPrincipals().GetAuthorizedPrincipals())-1 {
				fmt.Fprintf(&b, `,`)
			}
		}
		fmt.Fprintf(&b, ` ] }`)
		if i < len(usrReq.GetPolicies())-1 {
			fmt.Fprintf(&b, `,`)
		}
	}
	fmt.Fprintf(&b, ` ] }`)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, err
	}
	if err := sc.SSHMgmtSet(b.String()); err != nil {
		return nil, err
	}
	for _, set := range usrReq.GetPolicies() {
		if err := srv.writeSshAccountCredentialsMetadataToDB(set.Account, sshPrincipalsVersionFld, set.GetVersion()); err != nil {
			return nil, err
		}
		if err := srv.writeSshAccountCredentialsMetadataToDB(set.Account, sshPrincipalsCreatedOnFld, strconv.FormatUint(set.GetCreatedOn(), 10)); err != nil {
			return nil, err
		}
	}
	resp := &credz.RotateAccountCredentialsResponse{
		Response: &credz.RotateAccountCredentialsResponse_User{},
	}
	return resp, nil
}

func (srv *GNSICredentialzServer) processConsolePassword(req *credz.RotateAccountCredentialsRequest) (*credz.RotateAccountCredentialsResponse, error) {
	credReq := req.GetPassword()
	log.V(3).Infof("Received a Set Password request message! %v", credReq)

	// Sanity checks.
	if len(credReq.GetAccounts()) == 0 {
		return nil, fmt.Errorf("list of username/password pairs cannot be empty")
	}
	for _, set := range credReq.GetAccounts() {
		if set.GetVersion() == "" {
			return nil, fmt.Errorf("version cannot be empty")
		}
		if set.GetCreatedOn() == 0 {
			return nil, fmt.Errorf("created_on cannot be empty")
		}
		if set.Account == "" {
			return nil, fmt.Errorf("name cannot be empty")
		}
		pwd := set.GetPassword()
		if pwd == nil {
			return nil, fmt.Errorf("password cannot be empty")
		}
		if pwd.GetPlaintext() == "" {
			return nil, fmt.Errorf("password must be plaintext; CryptoHash unimplemented")
		}
	}

	// Build a message to be sent to the back-end.
	var b strings.Builder
	fmt.Fprintf(&b, `{ "ConsolePasswords": [ `)
	for i, set := range credReq.GetAccounts() {
		fmt.Fprintf(&b, `{ "name": "%s", "password" : "%s" }`, set.GetAccount(), set.GetPassword().GetPlaintext())
		if i < len(credReq.GetAccounts())-1 {
			fmt.Fprintf(&b, `,`)
		}
	}
	fmt.Fprintf(&b, ` ] }`)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, err
	}
	if err := sc.ConsoleSet(b.String()); err != nil {
		return nil, err
	}
	for _, set := range credReq.GetAccounts() {
		if err := srv.writeConsoleAccountCredentialsMetadataToDB(set.GetAccount(), consolePasswordVersionFld, set.GetVersion()); err != nil {
			return nil, err
		}
		if err := srv.writeConsoleAccountCredentialsMetadataToDB(set.GetAccount(), consolePasswordCreatedOnFld, strconv.FormatUint(set.GetCreatedOn(), 10)); err != nil {
			return nil, err
		}
	}
	resp := &credz.RotateAccountCredentialsResponse{
		Response: &credz.RotateAccountCredentialsResponse_Password{},
	}
	return resp, nil
}

func (srv *GNSICredentialzServer) processSshCaPublicKey(req *credz.RotateHostParametersRequest) (*credz.RotateHostParametersResponse, error) {
	credReq := req.GetSshCaPublicKey()
	if credReq == nil {
		return nil, fmt.Errorf(`Unknown request: "%v"`, req)
	}
	log.V(3).Infof("Received a RotateHostParameters request message! %v", credReq)

	// Sanity checks.
	if len(credReq.SshCaPublicKeys) == 0 {
		return nil, fmt.Errorf("CA keys cannot be empty")
	}
	if credReq.GetVersion() == "" {
		return nil, fmt.Errorf("version cannot be empty")
	}
	if credReq.GetCreatedOn() == 0 {
		return nil, fmt.Errorf("created_on cannot be empty")
	}
	for _, key := range credReq.GetSshCaPublicKeys() {
		if len(key.GetPublicKey()) == 0 {
			return nil, fmt.Errorf("CA public key cannot be empty")
		}
	}
	// Build the message to be sent to the back-end.
	var b strings.Builder
	fmt.Fprintf(&b, `{ "SshCaPublicKey": [`)
	for i, key := range credReq.GetSshCaPublicKeys() {
		fmt.Fprintf(&b, ` "%s %s %s"`, sshKeyTypePrefix[key.GetKeyType()], b64.StdEncoding.EncodeToString(key.GetPublicKey()), key.Description)
		if i < len(credReq.GetSshCaPublicKeys())-1 {
			fmt.Fprintf(&b, `,`)
		}
	}
	fmt.Fprintf(&b, ` ] }`)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, err
	}
	if err := sc.SSHMgmtSet(b.String()); err != nil {
		return nil, err
	}
	if err := srv.writeSshHostCredentialsMetadataToDB(sshCaKeysVersionFld, credReq.GetVersion()); err != nil {
		return nil, err
	}
	if err := srv.writeSshHostCredentialsMetadataToDB(sshCaKeysCreatedOnFld, strconv.FormatUint(credReq.GetCreatedOn(), 10)); err != nil {
		return nil, err
	}
	resp := &credz.RotateHostParametersResponse{
		Response: &credz.RotateHostParametersResponse_SshCaPublicKey{},
	}
	return resp, nil
}

// processGlomeConfig processes the GLOME config from the request and
// sends it to the host service to be written to the file system.
func (srv *GNSICredentialzServer) processGlomeConfig(ctx context.Context, req *credz.RotateHostParametersRequest, lastUpdated int64) (*credz.RotateHostParametersResponse, error) {
	glomeReq := req.GetGlome()
	if glomeReq == nil {
		return nil, fmt.Errorf("no glome request found")
	}

	// Validate the GLOME request and build the DBUS message.
	if err := validateGlomeRequest(glomeReq); err != nil {
		return nil, err
	}

	// Marshal the GLOME request proto message to JSON. Emits default values such as enabled=false,
	// and uses proto field names instead of lowerCamelCase.
	jsonBytes, err := protojson.MarshalOptions{
		EmitDefaultValues: true,
		UseProtoNames:     true,
	}.Marshal(glomeReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Glome request: %v", err)
	}

	// Send the DBUS message to SONiC host service.
	dbusClient, err := ssc.NewDbusClient()
	if err != nil {
		return nil, err
	}
	if err := dbusClient.GLOMEConfigSet(ctx, string(jsonBytes)); err != nil {
		return nil, err
	}

	// Write the new GLOME config from the request to the STATE_DB.
	newGlomeConfigMetadata := &GlomeConfigMetadata{
		Enabled:     glomeReq.GetEnabled(),
		KeyVersion:  glomeReq.GetKeyVersion(),
		LastUpdated: lastUpdated,
	}

	// Push the new GLOME config to the STATE_DB and update the server data for rollback.
	if err := srv.updateGlomeState(ctx, newGlomeConfigMetadata); err != nil {
		return nil, err
	}

	return &credz.RotateHostParametersResponse{Response: &credz.RotateHostParametersResponse_Glome{}}, nil
}

// validateGlomeRequest checks if Glome request is valid.
// If GLOME is disabled, ensure other fields are not set.
func validateGlomeRequest(req *credz.GlomeRequest) error {
	// Validate GLOME configurations if GLOME is enabled.
	if req.GetEnabled() {
		if len(req.GetKey()) == 0 {
			return fmt.Errorf("GLOME key is empty")
		}
		if req.GetKeyVersion() <= 0 {
			return fmt.Errorf("GLOME key version is not valid")
		}
		if err := isValidURLPrefix(req.GetUrlPrefix()); err != nil {
			return fmt.Errorf("GLOME URL prefix is not valid: %v", err.Error())
		}
		return nil
	}
	// Ensure other fields are not set if GLOME is disabled.
	if len(req.GetKey()) > 0 || req.GetKeyVersion() != 0 || len(req.GetUrlPrefix()) != 0 {
		return fmt.Errorf("GLOME key, key_version, and url_prefix cannot be set if GLOME is disabled, but received key: %v, key_version: %v, url_prefix: %v", req.GetKey(), req.GetKeyVersion(), req.GetUrlPrefix())
	}
	return nil
}

// isValidURLPrefix checks if the URL prefix is not empty and is a valid URL.
func isValidURLPrefix(prefix string) error {
	if len(prefix) == 0 {
		return fmt.Errorf("GLOME URL prefix is empty")
	}
	_, err := url.Parse(prefix)
	return err
}

// SSH Helpers
// writeSshAccountCredentialsMetadataToDB writes the credentials freshness data to the DB.
func (srv *GNSICredentialzServer) writeSshAccountCredentialsMetadataToDB(account, fld, val string) error {
	err := writeCredentialsMetadataToDB(sshAccountTbl, account, fld, val)
	if err != nil {
		return err
	}
	meta, ok := srv.sshCredMetadata.Accounts[account]
	if !ok {
		meta = SshAccountVersion{KeysVersion: "unknown", KeysCreatedOn: "0", UsersVersion: "unknown", UsersCreatedOn: "0"}
	}
	switch fld {
	case sshKeysVersionFld:
		meta.KeysVersion = val
	case sshKeysCreatedOnFld:
		meta.KeysCreatedOn = val
	case sshPrincipalsVersionFld:
		meta.UsersVersion = val
	case sshPrincipalsCreatedOnFld:
		meta.UsersCreatedOn = val
	}
	srv.sshCredMetadata.Accounts[account] = meta
	return nil
}

// writeSshHostCredentialsMetadataToDB writes the credentials freshness data to the DB.
func (srv *GNSICredentialzServer) writeSshHostCredentialsMetadataToDB(fld, val string) error {
	err := writeCredentialsMetadataToDB(sshHostTbl, "", fld, val)
	if err != nil {
		return err
	}
	switch fld {
	case sshCaKeysVersionFld:
		srv.sshCredMetadata.Host.CaKeysVersion = val
	case sshCaKeysCreatedOnFld:
		srv.sshCredMetadata.Host.CaKeysCreatedOn = val
	}
	return nil
}

// updateGlomeState saves the current GLOME config metadata from STATE_DB to the server for rollback,
// and writes the new GLOME config metadata from GlomeRequest to the STATE_DB.
func (srv *GNSICredentialzServer) updateGlomeState(ctx context.Context, newGlomeConfigMetadata *GlomeConfigMetadata) error {
	// Read the current GLOME config from STATE_DB and save it to server data for GLOME rollback.
	if currentGlomeConfigMetadata, err := srv.readGlomeConfigMetadataFromStateDB(ctx); err != nil {
		log.V(0).Infof("failed to create STATE_DB checkpoint for GLOME: %v", err)
		return fmt.Errorf("failed to create STATE_DB checkpoint for GLOME: %v", err)
	} else {
		srv.glomeConfigMetadata = currentGlomeConfigMetadata
	}

	// Write the new GLOME config from the request to the STATE_DB.
	if err := srv.writeGlomeConfigMetadataToStateDB(ctx, newGlomeConfigMetadata); err != nil {
		return err
	}
	return nil
}

type SshAccountVersion struct {
	KeysVersion    string `json:"keys_version"`
	KeysCreatedOn  string `json:"keys_created_on"`
	UsersVersion   string `json:"users_version"`
	UsersCreatedOn string `json:"users_created_on"`
}

type SshHostVersion struct {
	CaKeysVersion   string `json:"ca_public_keys_version"`
	CaKeysCreatedOn string `json:"ca_public_keys_created_on"`
}
type SshCredMetadata struct {
	Accounts map[string]SshAccountVersion `json:"accounts"`
	Host     SshHostVersion               `json:"host"`
}

func NewSshCredMetadata() *SshCredMetadata {
	return &SshCredMetadata{
		Accounts: make(map[string]SshAccountVersion),
		Host:     SshHostVersion{CaKeysVersion: "unknown", CaKeysCreatedOn: "0"},
	}
}

func (srv *GNSICredentialzServer) checkpointSshCredentialFreshness() {
	srv.sshCredMetadataCopy = *srv.sshCredMetadata
}

func (srv *GNSICredentialzServer) revertSshCredentialFreshness() {
	srv.writeSshHostCredentialsMetadataToDB(sshCaKeysVersionFld, srv.sshCredMetadataCopy.Host.CaKeysVersion)
	srv.writeSshHostCredentialsMetadataToDB(sshCaKeysCreatedOnFld, srv.sshCredMetadataCopy.Host.CaKeysCreatedOn)
	for a, u := range srv.sshCredMetadataCopy.Accounts {
		srv.writeSshAccountCredentialsMetadataToDB(a, sshKeysVersionFld, u.KeysVersion)
		srv.writeSshAccountCredentialsMetadataToDB(a, sshKeysCreatedOnFld, u.KeysCreatedOn)
		srv.writeSshAccountCredentialsMetadataToDB(a, sshPrincipalsVersionFld, u.UsersVersion)
		srv.writeSshAccountCredentialsMetadataToDB(a, sshPrincipalsCreatedOnFld, u.UsersCreatedOn)
	}
}

func (srv *GNSICredentialzServer) saveCredentialsFreshness(path string) error {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(*srv.sshCredMetadata); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func (srv *GNSICredentialzServer) loadCredentialFreshness(path string) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, srv.sshCredMetadata)
}

// CONSOLE helpers
// writeConsoleAccountCredentialsMetadataToDB writes the credentials freshness data to the DB.
func (srv *GNSICredentialzServer) writeConsoleAccountCredentialsMetadataToDB(account, fld, val string) error {
	if err := writeCredentialsMetadataToDB(consoleAccountTbl, account, fld, val); err != nil {
		return err
	}
	meta, ok := srv.consoleCredMetadata.Accounts[account]
	if !ok {
		meta = ConsoleAccountVersion{PasswordVersion: "unknown", PasswordCreatedOn: "0"}
	}
	switch fld {
	case consolePasswordVersionFld:
		meta.PasswordVersion = val
	case consolePasswordCreatedOnFld:
		meta.PasswordCreatedOn = val
	}
	srv.consoleCredMetadata.Accounts[account] = meta
	return nil
}

type ConsoleAccountVersion struct {
	PasswordVersion   string `json:"password_version"`
	PasswordCreatedOn string `json:"password_created_on"`
}

type ConsoleCredMetadata struct {
	Accounts map[string]ConsoleAccountVersion `json:"accounts"`
}

func NewConsoleCredMetadata() *ConsoleCredMetadata {
	return &ConsoleCredMetadata{
		Accounts: make(map[string]ConsoleAccountVersion),
	}
}

func (srv *GNSICredentialzServer) checkpointConsoleFreshness() {
	srv.consoleCredMetadataCopy = *srv.consoleCredMetadata
}

func (srv *GNSICredentialzServer) revertConsoleCredentialFreshness() {
	for a, u := range srv.consoleCredMetadataCopy.Accounts {
		srv.writeConsoleAccountCredentialsMetadataToDB(a, consolePasswordVersionFld, u.PasswordVersion)
		srv.writeConsoleAccountCredentialsMetadataToDB(a, consolePasswordCreatedOnFld, u.PasswordCreatedOn)
	}
}

func (srv *GNSICredentialzServer) saveConsoleCredentialsFreshness(path string) error {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(*srv.consoleCredMetadata); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func (srv *GNSICredentialzServer) loadConsoleCredentialFreshness(path string) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, srv.consoleCredMetadata)
}

// GlomeConfigMetadata is used to store the GLOME config metadata in the STATE_DB and on the server for checkpoint management.
// The default values for the fields are:
//   - Enabled: false (bool)
//   - KeyVersion: 0 (int32)
//   - LastUpdated: 0 (int64)
type GlomeConfigMetadata struct {
	Enabled     bool  `json:"enabled"`
	KeyVersion  int32 `json:"key_version"`
	LastUpdated int64 `json:"last_updated"` // Time in Unix epoch nanoseconds in string format.
}

var defaultGlomeConfigMetadata = &GlomeConfigMetadata{Enabled: false, KeyVersion: 0, LastUpdated: 0}

// InitGlomeConfigMetadata sets the server's initial glomeConfigMetadata from the data received from STATE_DB.
// - If the GlomeConfigMetadata does not exist in the STATE_DB, it sets the initial value to the default GlomeConfigMetadata.
// - If any error occurs, it sets the initial value to nil.
func (srv *GNSICredentialzServer) InitGlomeConfigMetadata(ctx context.Context) {
	// Get the GlomeConfigMetadata from the STATE_DB.
	glomeConfigMetadata, err := srv.readGlomeConfigMetadataFromStateDB(ctx)
	if err != nil {
		log.V(0).Infof("failed to read GLOME config metadata from STATE_DB: %v", err)
		srv.glomeConfigMetadata = nil
		return
	}

	// If the Glome config metadata does not exist in the STATE_DB, write the default values
	// to the STATE_DB and return the default GlomeConfigMetadata.
	if glomeConfigMetadata == defaultGlomeConfigMetadata {
		if err := srv.writeGlomeConfigMetadataToStateDB(ctx, defaultGlomeConfigMetadata); err != nil {
			// Even with error, we still return the default values here because the STATE_DB empty is
			// the same as the STATE_DB with default values.
			log.V(0).Infof("failed to write default GLOME config metadata to STATE_DB: %v", err)
		}
		srv.glomeConfigMetadata = defaultGlomeConfigMetadata
		return
	}

	// If the glome config metadata exists in the STATE_DB, write it to the server.
	srv.glomeConfigMetadata = glomeConfigMetadata
}

func (srv *GNSICredentialzServer) readGlomeConfigMetadataFromStateDB(ctx context.Context) (*GlomeConfigMetadata, error) {
	// Read the GLOME config metadata from the STATE_DB.
	glomeKey := getKey([]string{credentialsTbl, glomeConfigRedisKey})
	result, err := srv.stateDbClient.HGetAll(context.Background(), glomeKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get GLOME config metadata from STATE_DB: %v", err)
	}

	// If the GLOME config metadata does not exist in the STATE_DB, return default values
	// for GlomeConfigMetadata.
	if len(result) == 0 {
		return defaultGlomeConfigMetadata, nil
	}

	enabled, err := strconv.ParseBool(result["enabled"])
	if err != nil {
		return nil, fmt.Errorf("failed to parse 'enabled' field in GLOME config metadata from STATE_DB: %v", err)
	}
	keyVersion, err := strconv.ParseInt(result["key_version"], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse 'key_version' field in GLOME config metadata from STATE_DB: %v", err)
	}
	lastUpdated, err := strconv.ParseInt(result["last_updated"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse 'last_updated' field in GLOME config metadata from STATE_DB: %v", err)
	}

	return &GlomeConfigMetadata{
		Enabled:     enabled,
		KeyVersion:  int32(keyVersion),
		LastUpdated: int64(lastUpdated),
	}, nil
}

func (srv *GNSICredentialzServer) writeGlomeConfigMetadataToStateDB(ctx context.Context, newGlomeConfigMetadata *GlomeConfigMetadata) error {
	if newGlomeConfigMetadata == nil {
		return fmt.Errorf("newGlomeConfigMetadata is nil")
	}

	// Write the GLOME config metadata to the STATE_DB.
	glomeKey := getKey([]string{credentialsTbl, glomeConfigRedisKey})
	glomeFields := map[string]interface{}{
		"enabled":      newGlomeConfigMetadata.Enabled,
		"key_version":  newGlomeConfigMetadata.KeyVersion,
		"last_updated": newGlomeConfigMetadata.LastUpdated,
	}
	err := srv.stateDbClient.HSet(ctx, glomeKey, glomeFields).Err()
	if err != nil {
		return fmt.Errorf("failed to write GLOME config metdata to STATE_DB: %v", err)
	}

	return nil
}
