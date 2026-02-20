package gnmi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/golang/glog"
	certz "github.com/openconfig/gnsi/certz"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultProfile string = "gnxi"
	// Prefixes for DB entries
	certTbl string = "CERT"
	certId  string = "certificate"
	tbId    string = "ca_trust_bundle"
	crlId   string = "certificate_revocation_list_bundle"
	authId  string = "authentication_policy"

	// Suffixes for DB entries
	versionFld string = "_version"
	createdFld string = "_created_on"

	// CRL organization
	crlDefault string = "crl" // translate between v0 and v1 organization
	crlFlush   string = "_flush"
	crlTmpDir  string = "tmp"

	backupExt string = ".bak"
)

var (
	certzMu               sync.Mutex
	csrPrefix             []byte = []byte("CSR1_")
	integrityManifestFile string = "/mbm/boot_manifest.cbor"
)

type GNSICertzServer struct {
	*Server
	profiles map[string]*profile

	certz.UnimplementedCertzServer
}

type profile struct {
	ID             string      `json:"profile_id"`
	ActiveEntities entityGroup `json:"active"`
	LastEntities   entityGroup `json:"last_active"`
	generatedKey   []byte      `json:"-"`
}

type entityGroup struct {
	Cert        *genericEntity `json:"certificate"`
	TrustBundle *genericEntity `json:"trust_bundle"`
	CrlBundle   *genericEntity `json:"crl_bundle"`
	AuthPolicy  *genericEntity `json:"auth_policy"`
}

type genericEntity struct {
	EType     CertzType `json:type,omitempty`
	CreatedOn uint64    `json:created,omitempty`
	Version   string    `json:version,omitempty`
	CertPath  string    `json:cert_path,omitempty`
	KeyPath   string    `json:key_path,omitempty`
	Final     bool      `json:finalized,omitempty`
}

type CertzType int

const (
	certType CertzType = iota
	tbType
	crlType
	apType
)

func (ct CertzType) String() string {
	switch ct {
	case certType:
		return "Certificate"
	case tbType:
		return "TrustBundle"
	case crlType:
		return "CRLBundle"
	case apType:
		return "AuthPolicy"
	default:
		return "unknown"
	}

}

func NewGNSICertzServer(srv *Server) *GNSICertzServer {
	log.V(2).Info("Starting gNSI Certz Server")
	s := &GNSICertzServer{
		Server:   srv,
		profiles: make(map[string]*profile),
	}

	if srv.config.IntManFile != "" {
		integrityManifestFile = srv.config.IntManFile
	}
	log.V(2).Infof("Integrity Manifest File: %v", integrityManifestFile)

	if err := loadCertzMetadata(srv.config.CertzMetaFile, s.profiles); err != nil {
		log.V(0).Info(err)
	}
	if _, ok := s.profiles[defaultProfile]; !ok {
		// didn't find the default profile, so make it
		s.profiles[defaultProfile] = s.bootstrapDefaultProfile()
	}

	for _, p := range s.profiles {
		writeEntityFreshness(p.ID, p.ActiveEntities.Cert)
		writeEntityFreshness(p.ID, p.ActiveEntities.TrustBundle)
		writeEntityFreshness(p.ID, p.ActiveEntities.CrlBundle)
		writeEntityFreshness(p.ID, p.ActiveEntities.AuthPolicy)
	}

	saveCertzMetadata(srv.config.CertzMetaFile, s.profiles)

	// Prepare CRL default dir if not present
	crlPath := filepath.Join(srv.config.CertCRLConfig, crlDefault)
	if _, err := os.Stat(crlPath); err != nil && os.IsNotExist(err) {
		log.V(2).Infof("Creating CRL Default path: %v", crlPath)
		if err := os.MkdirAll(crlPath, 0777); err != nil {
			log.V(1).Infof("Failed Creating CRL Default dir: %v", crlPath)
		}
	}
	flushPath := filepath.Join(srv.config.CertCRLConfig, crlDefault+crlFlush)
	if _, err := os.Stat(flushPath); err != nil && os.IsNotExist(err) {
		log.V(2).Infof("Creating CRL Flush path: %v", flushPath)
		if err := os.MkdirAll(flushPath, 0777); err != nil {
			log.V(1).Infof("Failed Creating CRL Flush dir: %v", flushPath)
		}
	}
	log.V(2).Info("Certz Server Ready")
	return s
}

func (srv *GNSICertzServer) AddProfile(context.Context, *certz.AddProfileRequest) (*certz.AddProfileResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AddProfile not implemented")
}
func (srv *GNSICertzServer) DeleteProfile(context.Context, *certz.DeleteProfileRequest) (*certz.DeleteProfileResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DeleteProfile not implemented")
}
func (srv *GNSICertzServer) GetProfileList(context.Context, *certz.GetProfileListRequest) (*certz.GetProfileListResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetProfileList not implemented")
}
func (srv *GNSICertzServer) CanGenerateCSR(ctx context.Context, req *certz.CanGenerateCSRRequest) (*certz.CanGenerateCSRResponse, error) {
	if req.GetParams().GetCommonName() == "" {
		return &certz.CanGenerateCSRResponse{CanGenerate: false}, nil
	}
	return &certz.CanGenerateCSRResponse{CanGenerate: true}, nil
}

func (srv *GNSICertzServer) bootstrapDefaultProfile() *profile {
	log.V(2).Info("Bootstrapping default profile")
	time := uint64(time.Now().UnixNano())

	return &profile{
		ID: defaultProfile,
		ActiveEntities: entityGroup{
			Cert: &genericEntity{
				EType:     certType,
				CreatedOn: time,
				Version:   "V1",
				CertPath:  restoreFromFile(srv.config.SrvCertLnk, srv.config.SrvCertFile),
				KeyPath:   restoreFromFile(srv.config.SrvKeyLnk, srv.config.SrvKeyFile),
				Final:     true,
			},
			TrustBundle: &genericEntity{
				EType:     tbType,
				CreatedOn: time,
				Version:   "V1",
				CertPath:  restoreFromFile(srv.config.CaCertLnk, srv.config.CaCertFile),
				Final:     true,
			},
			CrlBundle: &genericEntity{
				EType:     crlType,
				CreatedOn: time,
				Version:   "V1",
				CertPath:  srv.config.CertCRLConfig,
				Final:     true,
			},
			AuthPolicy: &genericEntity{
				EType:     apType,
				CreatedOn: time,
				Version:   "V1",
				CertPath:  srv.config.FedPolicyFile,
				Final:     true,
			},
		},
		LastEntities: entityGroup{
			Cert:        &genericEntity{},
			TrustBundle: &genericEntity{},
			CrlBundle:   &genericEntity{},
			AuthPolicy:  &genericEntity{},
		},
	}
}

// Rotate implements corresponding RPC.
func (srv *GNSICertzServer) Rotate(stream certz.Certz_RotateServer) error {
	ctx := stream.Context()
	_, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return err
	}

	session := time.Now().Nanosecond()
	if !certzMu.TryLock() {
		return status.Error(codes.Aborted, "concurrent certz.Rotate RPCs are not allowed")
	}
	defer certzMu.Unlock()

	log.V(2).Infof("[%v]gNSI: Begin certz.Rotate", session)
	defer log.V(2).Infof("[%v]gNSI: End certz.Rotate", session)

	var profileId string
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			log.V(0).Infof("[%v]gNSI: Received unexpected EOF", session)
			// Connection closed without Finalize message. Revert all changes made until now.
			srv.revertProfile(profileId)
			return status.Error(codes.Aborted, "No Finalize message")
		}
		if err != nil {
			log.V(0).Infof("[%v]gNSI: Received error: %v", session, err)
			// Connection closed without Finalize message. Revert all changes made until now.
			srv.revertProfile(profileId)
			return status.Errorf(codes.Aborted, "Stream recv err: %v", err)
		}
		if endReq := req.GetFinalizeRotation(); endReq != nil {
			// This is the last message. All changes are final.
			log.V(0).Infof("[%v]gNSI: Received Finalize: %v", session, endReq)
			// The new credentials have been confirmed to be correct. Remove the old ones.
			if err := srv.finalizeProfile(profileId); err != nil {
				return status.Errorf(codes.Unknown, "Failed to remove the old credentials: %v", err)
			}
			log.V(2).Infof("[%v]gNSI: Rotation Finalized", session)
			return nil
		}
		if profileId == "" {
			profileId = defaultProfile
			if reqID := req.GetSslProfileId(); reqID != "" {
				profileId = reqID
			}
		}
		log.V(2).Infof("[%v]gNSI: Rotating profile: %s", session, profileId)
		resp, err := srv.processRotateRequest(profileId, req)
		if err != nil {
			srv.revertProfile(profileId)
			return status.Errorf(codes.Aborted, "Process err: %v", err)
		}
		// Send confirmation that the UploadRequest was processed.
		if err := stream.Send(resp); err != nil {
			srv.revertProfile(profileId)
			return status.Errorf(codes.Aborted, "Stream send err: %v", err)
		}
	}
}

func (srv *GNSICertzServer) processRotateRequest(profileID string, req *certz.RotateCertificateRequest) (*certz.RotateCertificateResponse, error) {
	if _, ok := srv.profiles[profileID]; !ok {
		return &certz.RotateCertificateResponse{}, status.Errorf(codes.InvalidArgument, "Rotate requested with invalid ssl_profile_id: %s", profileID)
	}
	rotateResp := certz.RotateCertificateResponse{}
	switch req.RotateRequest.(type) {
	case *certz.RotateCertificateRequest_GenerateCsr:
		resp, err := srv.doGenerateCsr(profileID, req.GetGenerateCsr())
		if err != nil {
			return nil, err
		}
		rotateResp.RotateResponse = &certz.RotateCertificateResponse_GeneratedCsr{GeneratedCsr: resp}
	case *certz.RotateCertificateRequest_Certificates:
		resp, err := srv.doUpload(req.GetCertificates(), req.GetForceOverwrite(), profileID)
		if err != nil {
			return nil, err
		}
		rotateResp.RotateResponse = &certz.RotateCertificateResponse_Certificates{Certificates: resp}
	default:
		return nil, status.Errorf(codes.Aborted, "Invalid RotateRequest: %T", req.RotateRequest)
	}
	return &rotateResp, nil
}

func (srv *GNSICertzServer) doGenerateCsr(profileID string, req *certz.GenerateCSRRequest) (*certz.GenerateCSRResponse, error) {
	log.V(2).Info("Generating Csr")

	keySize, sigAlgo := parseCSRSuite(req.GetParams().GetCsrSuite())
	csrTemplate := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:         req.GetParams().CommonName,
			Country:            []string{req.GetParams().Country},
			Province:           []string{req.GetParams().State},
			Locality:           []string{req.GetParams().City},
			Organization:       []string{req.GetParams().Organization},
			OrganizationalUnit: []string{req.GetParams().OrganizationalUnit},
		},
		SignatureAlgorithm: sigAlgo,
	}

	var privKey any
	var err error
	switch sigAlgo {
	case x509.SHA256WithRSA, x509.SHA384WithRSA, x509.SHA512WithRSA:
		log.V(2).Infof("Generating keys for RSA: %v", req.GetParams().GetCsrSuite().String())
		privKey, err = rsa.GenerateKey(rand.Reader, keySize)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "GenerateKey failed: %v", err)
		}
	case x509.ECDSAWithSHA256, x509.ECDSAWithSHA384, x509.ECDSAWithSHA512:
		log.V(2).Infof("Generating keys for ECDSA: %v", req.GetParams().GetCsrSuite().String())
		var curve elliptic.Curve
		switch keySize {
		case 256:
			curve = elliptic.P256()
		case 384:
			curve = elliptic.P384()
		case 521:
			curve = elliptic.P521()
		default:
			return nil, status.Errorf(codes.InvalidArgument, "Unsupported key size for ECDSA: %v", keySize)
		}
		privKey, err = ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "GenerateKey failed: %v", err)
		}
	case x509.PureEd25519:
		fallthrough
	case x509.UnknownSignatureAlgorithm:
		fallthrough
	default:
		return nil, status.Errorf(codes.InvalidArgument, "Unsupported Algorithm: %v", sigAlgo.String())

	}

	csrCert, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, privKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CreateCertificateRequest failed: %v", err)
	}
	csr := pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE REQUEST", Bytes: csrCert,
	})
	// Save the CSR Private Key
	key, err := x509.MarshalPKCS8PrivateKey(privKey)
	srv.profiles[profileID].generatedKey = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})

	return &certz.GenerateCSRResponse{
		CertificateSigningRequest: &certz.CertificateSigningRequest{
			Type:                      certz.CertificateType_CERTIFICATE_TYPE_X509,
			Encoding:                  certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
			CertificateSigningRequest: csr,
		},
	}, nil
}

func (srv *GNSICertzServer) doUpload(req *certz.UploadRequest, overwrite bool, profileID string) (*certz.UploadResponse, error) {
	muPath.Lock()
	defer muPath.Unlock()

	if len(req.GetEntities()) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "entity cannot be empty")
	}
	for _, entityMsg := range req.GetEntities() {
		if entityMsg.GetCreatedOn() == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "created_on cannot be empty")
		}
		if entityMsg.GetVersion() == "" {
			return nil, status.Errorf(codes.InvalidArgument, "version cannot be empty")
		}

		var expEntity *genericEntity
		// Entity is OneOf the following
		if c := entityMsg.GetCertificateChain(); c != nil {
			expEntity = srv.newGenericEntity(certType, profileID, entityMsg)
		}
		if tb := entityMsg.GetTrustBundle(); tb != nil {
			expEntity = srv.newGenericEntity(tbType, profileID, entityMsg)
		}
		if crl := entityMsg.GetCertificateRevocationListBundle(); crl != nil {
			if srv.Server.config.CertCRLConfig == "" {
				return nil, status.Error(codes.Aborted, "CRL not configured")
			}
			expEntity = srv.newGenericEntity(crlType, profileID, entityMsg)
		}
		if ap := entityMsg.GetAuthenticationPolicy(); ap != nil {
			expEntity = srv.newGenericEntity(apType, profileID, entityMsg)
		}
		if expEntity == nil {
			return nil, status.Errorf(codes.Internal, "failed to find entity type: %+v", entityMsg)
		}

		if !overwrite {
			if err := srv.entityAlreadyActive(profileID, expEntity); err != nil {
				return nil, err
			}
		}
		if err := srv.saveEntities(profileID, entityMsg, expEntity); err != nil {
			return nil, status.Errorf(codes.Aborted, "Entity save err: %v", err)
		}
		if err := srv.activateEntity(profileID, expEntity); err != nil {
			return nil, status.Errorf(codes.Aborted, "Entity activate err: %v", err)
		}
	}
	return &certz.UploadResponse{}, nil
}

func (srv *GNSICertzServer) newGenericEntity(eType CertzType, profileID string, entityMsg *certz.Entity) *genericEntity {
	ver := strings.ReplaceAll(entityMsg.GetVersion(), " ", "-")
	keyPath := ""
	certPath := ""
	t := time.Now().UnixNano()
	switch eType {
	case certType:
		// This filepath Dir is based on default profile
		certPath = fmt.Sprintf("%v/%v_%v_%d_cert.pem", filepath.Dir(srv.config.SrvCertLnk), profileID, ver, t)
		keyPath = fmt.Sprintf("%v/%v_%v_%d_key.pem", filepath.Dir(srv.config.SrvKeyLnk), profileID, ver, t)
	case tbType:
		certPath = fmt.Sprintf("%v/ca_%v_%v_%d_bundle.pem", filepath.Dir(srv.config.CaCertLnk), profileID, ver, t)
	case crlType:
		// translate from v0 to v1 defaults
		crlTranslate := profileID
		if crlTranslate == defaultProfile {
			crlTranslate = crlDefault
		}
		certPath = filepath.Join(srv.config.CertCRLConfig, crlTranslate)
	case apType:
		certPath = srv.config.FedPolicyFile
	}
	log.V(2).Infof("creating new Entity: %+v", entityMsg)
	return &genericEntity{
		EType:     eType,
		CreatedOn: entityMsg.GetCreatedOn(),
		Version:   entityMsg.GetVersion(),
		CertPath:  certPath,
		KeyPath:   keyPath,
	}
}

func (srv *GNSICertzServer) activateEntity(profileID string, entity *genericEntity) error {
	profile, ok := srv.profiles[profileID]
	if !ok || profile == nil {
		return status.Errorf(codes.InvalidArgument, "Rotate requested with invalid ssl_profile_id: %s", profileID)
	}
	log.V(2).Infof("Activating: %+v", entity)
	switch entity.EType {
	case certType:
		if err := atomicSetSrvCertKeyPair(srv.config, entity.CertPath, entity.KeyPath); err != nil {
			return err
		}
		if profile.ActiveEntities.Cert != nil && profile.ActiveEntities.Cert.Final {
			profile.LastEntities.Cert = profile.ActiveEntities.Cert
		}
		profile.ActiveEntities.Cert = entity

	case tbType:
		if err := atomicSetCACert(srv.config, entity.CertPath); err != nil {
			return err
		}
		if profile.ActiveEntities.TrustBundle != nil && profile.ActiveEntities.TrustBundle.Final {
			profile.LastEntities.TrustBundle = profile.ActiveEntities.TrustBundle
		}
		profile.ActiveEntities.TrustBundle = entity

	case crlType:
		if profile.ActiveEntities.CrlBundle != nil && profile.ActiveEntities.CrlBundle.Final {
			profile.LastEntities.CrlBundle = profile.ActiveEntities.CrlBundle
		}
		profile.ActiveEntities.CrlBundle = entity

	case apType:
		if profile.ActiveEntities.AuthPolicy != nil && profile.ActiveEntities.AuthPolicy.Final {
			profile.LastEntities.AuthPolicy = profile.ActiveEntities.AuthPolicy
		}
		profile.ActiveEntities.AuthPolicy = entity
	}

	writeEntityFreshness(profileID, entity)
	return nil
}

func (srv *GNSICertzServer) saveEntities(profileID string, entityMsg *certz.Entity, expEntity *genericEntity) error {
	log.V(2).Infof("Saving entity: %+v", expEntity)
	switch expEntity.EType {
	case certType:
		cert, key, err := srv.readCertChain(profileID, entityMsg.GetCertificateChain())
		if err != nil {
			return err
		}
		if e := attemptWrite(expEntity.KeyPath, key, 0600); e != nil {
			return e
		}
		return attemptWrite(expEntity.CertPath, cert, 0600)
	case tbType:
		tb := entityMsg.GetTrustBundle().GetCertificate()
		if tb.Type != certz.CertificateType_CERTIFICATE_TYPE_X509 {
			return status.Errorf(codes.InvalidArgument, "trustBundle type has to be X.509")
		}
		if tb.Encoding != certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM {
			return status.Errorf(codes.InvalidArgument, "trustBundle encoding has to be PEM")
		}
		bundle := tb.Certificate
		if len(bundle) == 0 {
			return status.Errorf(codes.InvalidArgument, "trustBundle cannot be empty")
		}
		return attemptWrite(expEntity.CertPath, bundle, 0600)
	case crlType:
		if err := rotateCRLBundle(entityMsg.GetCertificateRevocationListBundle(), expEntity.CertPath); err != nil {
			return status.Errorf(codes.Aborted, err.Error())
		}
		return nil
	case apType:
		if _, state := os.Lstat(expEntity.CertPath + backupExt); os.IsNotExist(state) {
			log.V(2).Info("Backing up Auth Policy")
			if err := os.Rename(expEntity.CertPath, expEntity.CertPath+backupExt); err != nil {
				return err
			}
		}
		if err := attemptWrite(expEntity.CertPath, entityMsg.GetAuthenticationPolicy().GetSerialized().GetValue(), 0600); err != nil {
			log.V(1).Infof("Failed to save Auth Policy: %v", err)
			if e := os.Rename(expEntity.CertPath+backupExt, expEntity.CertPath); e != nil {
				log.V(1).Infof("Failed to restore Auto Policy Backup: %v", e)
			}
			return err
		}
		return nil
	}
	return status.Errorf(codes.Internal, "failed to find entity type: %+v", entityMsg)
}

func (srv *GNSICertzServer) readCertChain(profileID string, certChain *certz.CertificateChain) ( /*cert*/ []byte /*key*/, []byte, error) {
	// step through the certificate chain and append each parent
	if certChain.GetCertificate() == nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "Missing Certificate")
	}
	if certChain.GetCertificate().Type != certz.CertificateType_CERTIFICATE_TYPE_X509 {
		return nil, nil, status.Errorf(codes.InvalidArgument, "certificate type has to be X.509")
	}
	if certChain.GetCertificate().Encoding != certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM {
		return nil, nil, status.Errorf(codes.InvalidArgument, "certificate encoding has to be PEM")
	}
	if certChain.GetCertificate().Certificate == nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "Missing Cert data")
	}
	cert := append(certChain.GetCertificate().Certificate, byte('\n'))
	key := certChain.GetCertificate().PrivateKey
	if certChain.GetCertificate().PrivateKey == nil {
		if srv.profiles[profileID].generatedKey == nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "Missing Key")
		}
		log.V(2).Info("No key provided; Using generated key.")
		key = srv.profiles[profileID].generatedKey
	}

	certChain = certChain.GetParent()
	for certChain != nil {
		if certChain.GetCertificate().Type != certz.CertificateType_CERTIFICATE_TYPE_X509 {
			return nil, nil, status.Errorf(codes.InvalidArgument, "certificate type has to be X.509")
		}
		if certChain.GetCertificate().Encoding != certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM {
			return nil, nil, status.Errorf(codes.InvalidArgument, "certificate encoding has to be PEM")
		}
		//replace with slices.Concat once go1.22 is available
		cert = append(cert, append(certChain.GetCertificate().Certificate, byte('\n'))...)

		certChain = certChain.GetParent()
	}
	return cert, key, nil
}

func (srv *GNSICertzServer) revertProfile(profileID string) {
	log.V(2).Infof("Reverting to last known good gRPC credentials for profile=%v", profileID)
	if profileID == "" {
		profileID = defaultProfile
		log.V(2).Infof("Reverting default profile: %v", defaultProfile)
	}
	profile, ok := srv.profiles[profileID]
	if !ok || profile == nil {
		log.V(2).Infof("No profile to revert: %v", profileID)
		return
	}
	if profile.ActiveEntities.Cert.Final == false {
		log.V(2).Info("Rollback Cert")
		if err := atomicSetSrvCertKeyPair(srv.config, profile.LastEntities.Cert.CertPath, profile.LastEntities.Cert.KeyPath); err != nil {
			log.V(0).Infof("Failed to revert certificate files: %e", err)
		}
		writeEntityFreshness(profileID, profile.LastEntities.Cert)
		removeEntityFiles(profile.ActiveEntities.Cert)
		profile.ActiveEntities.Cert = profile.LastEntities.Cert
	}
	if profile.ActiveEntities.TrustBundle.Final == false {
		log.V(2).Info("Rollback TB")
		if err := atomicSetCACert(srv.config, profile.LastEntities.TrustBundle.CertPath); err != nil {
			log.V(0).Infof("Failed to revert trust bundle file: %e", err)
		}
		writeEntityFreshness(profileID, profile.LastEntities.TrustBundle)
		removeEntityFiles(profile.ActiveEntities.TrustBundle)
		profile.ActiveEntities.TrustBundle = profile.LastEntities.TrustBundle
	}
	if profile.ActiveEntities.CrlBundle.Final == false {
		log.V(2).Info("Rollback CRL")
		// translate from v0 to v1 defaults
		crlTranslate := profileID
		if crlTranslate == defaultProfile {
			crlTranslate = crlDefault
		}
		path := filepath.Join(srv.config.CertCRLConfig, crlTranslate)
		copyCRLBundle(path+crlFlush, path)
		writeEntityFreshness(profileID, profile.LastEntities.CrlBundle)
		profile.ActiveEntities.CrlBundle = profile.LastEntities.CrlBundle
	}
	if profile.ActiveEntities.AuthPolicy.Final == false {
		log.V(2).Info("Rollback AP")
		if err := os.Rename(profile.ActiveEntities.AuthPolicy.CertPath+backupExt, profile.ActiveEntities.AuthPolicy.CertPath); err != nil {
			log.V(0).Infof("Failed to revert Auth Policy: %v", err)
		}
		writeEntityFreshness(profileID, profile.LastEntities.AuthPolicy)
		profile.ActiveEntities.AuthPolicy = profile.LastEntities.AuthPolicy
	}
}

func (srv *GNSICertzServer) finalizeProfile(profileID string) error {
	log.V(2).Infof("Finalizing gRPC credentials for profile=%v", profileID)
	profile, ok := srv.profiles[profileID]
	if !ok || profile == nil {
		return status.Errorf(codes.InvalidArgument, "Finalize requested with invalid ssl_profile_id: %s", profileID)
	}
	if profile.ActiveEntities.Cert.Final != true {
		removeEntityFiles(profile.LastEntities.Cert)
		profile.ActiveEntities.Cert.Final = true
	}
	profile.LastEntities.Cert = profile.ActiveEntities.Cert

	if profile.ActiveEntities.TrustBundle.Final != true {
		removeEntityFiles(profile.LastEntities.TrustBundle)
		profile.ActiveEntities.TrustBundle.Final = true
	}
	profile.LastEntities.TrustBundle = profile.ActiveEntities.TrustBundle

	if profile.ActiveEntities.CrlBundle.Final == false {
		// translate from v0 to v1 defaults
		crlTranslate := profileID
		if crlTranslate == defaultProfile {
			crlTranslate = crlDefault
		}
		path := filepath.Join(srv.config.CertCRLConfig, crlTranslate)
		copyCRLBundle(path, path+crlFlush)
		profile.ActiveEntities.CrlBundle.Final = true
		profile.LastEntities.CrlBundle = profile.ActiveEntities.CrlBundle
	}

	if profile.ActiveEntities.AuthPolicy.Final != true {
		log.V(2).Info("Finalize AP")
		if err := os.Remove(profile.ActiveEntities.AuthPolicy.CertPath + backupExt); err != nil {
			log.V(1).Infof("Auth Policy backup was lost: %v", err)
		}
		profile.ActiveEntities.AuthPolicy.Final = true
	}
	profile.LastEntities.AuthPolicy = profile.ActiveEntities.AuthPolicy

	return saveCertzMetadata(srv.config.CertzMetaFile, srv.profiles)
}

func writeEntityFreshness(profileID string, entity *genericEntity) {
	log.V(2).Infof("Writing %s profile to DB: %+v", profileID, entity)
	var vFld string
	var cFld string

	//	CreatedOn is received as seconds but should be stored as NanoSeconds
	created := strconv.FormatUint(entity.CreatedOn, 10) + "000000000"

	switch entity.EType {
	case certType:
		vFld = certId + versionFld
		cFld = certId + createdFld
	case tbType:
		vFld = tbId + versionFld
		cFld = tbId + createdFld
	case crlType:
		vFld = crlId + versionFld
		cFld = crlId + createdFld
	case apType:
		vFld = authId + versionFld
		cFld = authId + createdFld
	default:
		log.V(0).Infof("Unknown entity in profile: %s : %+v", profileID, entity)
	}

	writeCredentialsMetadataToDB(certTbl, profileID, cFld, created)
	writeCredentialsMetadataToDB(certTbl, profileID, vFld, entity.Version)
}

func saveCertzMetadata(path string, profiles map[string]*profile) error {
	log.V(2).Info("Saving gRPC credentials metadata.")
	data, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	log.V(2).Infof("Certz Metadata:\n%v", string(data))
	return attemptWrite(path, data, 0644)
}

func loadCertzMetadata(path string, profiles map[string]*profile) error {
	log.V(2).Infof("Loading metadata from `%v`...\n", path)
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(bytes, &profiles); err != nil {
		return err
	}
	for k, p := range profiles {
		if err := validateProfileExists(*p); err != nil {
			log.V(1).Infof("%v profile error: %v", k, err)
			delete(profiles, k)
		}
		log.V(2).Infof("Added %s profile: %+v", k, *p)
	}
	return nil
}

func validateProfileExists(profile profile) error {
	if profile.ID == "" {
		return status.Errorf(codes.NotFound, "cannot validate empty profile")
	}
	// Cert Paths
	if _, err := os.Lstat(profile.ActiveEntities.Cert.CertPath); os.IsNotExist(err) {
		return status.Errorf(codes.NotFound, "Cert '%v': '%v' does not exist", profile.ID, profile.ActiveEntities.Cert.CertPath)
	}
	if _, err := os.Lstat(profile.ActiveEntities.Cert.KeyPath); os.IsNotExist(err) {
		return status.Errorf(codes.NotFound, "Key '%v': '%v' does not exist", profile.ID, profile.ActiveEntities.Cert.KeyPath)
	}
	// Trust Bundle Path
	if _, err := os.Lstat(profile.ActiveEntities.TrustBundle.CertPath); os.IsNotExist(err) {
		return status.Errorf(codes.NotFound, "TB '%v': '%v' does not exist", profile.ID, profile.ActiveEntities.TrustBundle.CertPath)
	}
	// CRL Path - nothing needed
	// Auth Policy Path - nothing needed
	return nil
}

func (srv *GNSICertzServer) entityAlreadyActive(profileID string, entity *genericEntity) error {
	profile, ok := srv.profiles[profileID]
	if !ok {
		return nil
	}
	switch entity.EType {
	case certType:
		if entity.Version == profile.ActiveEntities.Cert.Version {
			return status.Errorf(codes.AlreadyExists, "%s with profile=`%s` and version=`%v` already exists", entity.EType, profileID, entity.Version)
		}
	case tbType:
		if entity.Version == profile.ActiveEntities.TrustBundle.Version {
			return status.Errorf(codes.AlreadyExists, "%s with profile=`%s` and version=`%v` already exists", entity.EType, profileID, entity.Version)
		}
	case crlType:
		if entity.Version == profile.ActiveEntities.CrlBundle.Version {
			return status.Errorf(codes.AlreadyExists, "%s with profile=`%s` and version=`%v` already exists", entity.EType, profileID, entity.Version)
		}
	case apType:
		if entity.Version == profile.ActiveEntities.AuthPolicy.Version {
			return status.Errorf(codes.AlreadyExists, "%s with profile=`%s` and version=`%v` already exists", entity.EType, profileID, entity.Version)
		}
	}
	return nil
}

func rotateCRLBundle(crl *certz.CertificateRevocationListBundle, path string) error {
	log.V(2).Infof("Saving CRL to: %s", path)
	certs := crl.GetCertificateRevocationLists()
	if certs == nil || len(certs) == 0 {
		return status.Errorf(codes.InvalidArgument, "CRL bundle cannot be empty")
	}
	for i, c := range certs {
		if t := c.GetType(); t != certz.CertificateType_CERTIFICATE_TYPE_X509 {
			return status.Errorf(codes.InvalidArgument, "CRL type has to be X.509")
		}
		if c.GetEncoding() != certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM {
			return status.Errorf(codes.InvalidArgument, "CRL encoding has to be PEM")
		}
		if len(c.GetCertificateRevocationList()) == 0 {
			return status.Errorf(codes.InvalidArgument, "CRL #%v is empty", i)
		}
		der, _ := pem.Decode(c.GetCertificateRevocationList())
		if _, err := x509.ParseRevocationList(der.Bytes); err != nil {
			return status.Errorf(codes.InvalidArgument, "CRL failed to parse: %v", err)
		}
		if err := attemptWrite(filepath.Join(path, c.GetId()), c.CertificateRevocationList, 0644); err != nil {
			return status.Errorf(codes.FailedPrecondition, "CRL %v write failed: %v", c.GetId(), err)
		}
	}

	dstFiles, err := os.ReadDir(path)
	if err != nil {
		log.V(1).Info(err)
	}
	for _, file := range dstFiles {
		if err := os.Remove(filepath.Join(path, file.Name())); err != nil {
			log.V(1).Info(err)
		}
	}
	for _, c := range certs {
		if err := attemptWrite(filepath.Join(path, c.GetId()), c.CertificateRevocationList, 0644); err != nil {
			return status.Errorf(codes.FailedPrecondition, "CRL %v write failed: %v", c.GetId(), err)
		}
	}
	return nil
}

func copyCRLBundle(srcPath string, dstPath string) {
	log.V(2).Infof("Copying CRL Bundle from: %s to: %s", srcPath, dstPath)

	dstFiles, err := os.ReadDir(dstPath)
	if err != nil {
		log.V(1).Info(err)
	}
	for _, file := range dstFiles {
		if err := os.Remove(filepath.Join(dstPath, file.Name())); err != nil {
			log.V(1).Info(err)
		}
	}
	srcFiles, err := os.ReadDir(srcPath)
	if err != nil {
		log.V(1).Info(err)
	}
	for _, file := range srcFiles {
		input, err := os.ReadFile(filepath.Join(srcPath, file.Name()))
		if err != nil {
			log.V(1).Info(err)
			continue
		}
		if err = attemptWrite(filepath.Join(dstPath, file.Name()), input, 0644); err != nil {
			log.V(1).Info(err)
			continue
		}
	}
}

func rmSymlink(path string) (string, error) {
	// NOTE: muPath has to be writer-locked when entering this function.
	log.V(2).Infof("Removing sym link: %s", path)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return "", nil
	}
	// The symbolic link exists. Backup it so it can be restored in case of an emergency.
	old, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	// Now remove the symbolic link.
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return old, nil
}

func restoreSymlink(oldTarget, link string) error {
	// NOTE: muPath has to be writer-locked when entering this function.
	log.V(2).Infof("Restoring sym link: %s  old: %s", link, oldTarget)
	if oldTarget == "" {
		return nil
	}
	// Check if oldTarget exists before creating the symlink.
	if _, err := os.Stat(oldTarget); os.IsNotExist(err) {
		return fmt.Errorf("oldTarget %q does not exist: %w", oldTarget, err)
	} else if err != nil {
		// Handle other potential errors during os.Stat
		return fmt.Errorf("failed to stat oldTarget %q: %w", oldTarget, err)
	}
	if _, err := rmSymlink(link); err != nil {
		return err
	}
	return os.Symlink(oldTarget, link)
}

func rmFileIfNotPointedToBySymlink(file, link string) error {
	// NOTE: muPath has to be writer-locked when entering this function.
	// Check if link points to a file.
	log.V(2).Infof("Removing file: %s link: %s", file, link)
	if _, err := os.Lstat(link); os.IsNotExist(err) {
		return nil
	}
	// The symbolic link exists. Read the path to the linked file.
	if linked, err := os.Readlink(link); err != nil || file == linked {
		return err
	}
	// Now remove the file.
	return os.Remove(file)
}

func isSymlinkValid(path string) bool {
	// NOTE: muPath has to be writer-locked when entering this function.
	log.V(2).Infof("Validating sym link: %s", path)
	if fi, err := os.Stat(path); os.IsNotExist(err) || fi == nil || !fi.Mode().IsRegular() {
		return false
	}
	return true
}

// atomicSetSrvCertKeyPair atomically replaces server's private key and certificate.
func atomicSetSrvCertKeyPair(cfg *Config, sCert, sKey string) error {
	// NOTE: muPath has to be writer-locked when entering this function.
	// This assumes default profile and needs to be replaced when multi-profile is supported
	log.V(2).Infof("Attempting to set Cert: %s", sCert)
	cert, err := filepath.Abs(sCert)
	if err != nil {
		return err
	}
	key, err := filepath.Abs(sKey)
	if err != nil {
		return err
	}
	// Remove the old symlink to server's certificate.
	oldCert, err := rmSymlink(cfg.SrvCertLnk)
	if err != nil {
		return err
	}
	// Remove the old symlink to server's private key .
	oldKey, err := rmSymlink(cfg.SrvKeyLnk)
	if err != nil {
		_ = restoreSymlink(oldCert, cfg.SrvCertLnk)
		return err
	}
	// Create new symbolic link to new certificate.
	if err := os.Symlink(cert, cfg.SrvCertLnk); err != nil {
		// Ignore the following errors as they are secondary and report the problem with creating the new symlink.
		_ = restoreSymlink(oldCert, cfg.SrvCertLnk)
		_ = restoreSymlink(oldKey, cfg.SrvKeyLnk)
		return err
	}
	// Create new symbolic link to new private key.
	if err := os.Symlink(key, cfg.SrvKeyLnk); err != nil {
		// Ignore the following errors as they are secondary and report the problem with creating the new symlink.
		_ = restoreSymlink(oldCert, cfg.SrvCertLnk)
		_ = restoreSymlink(oldKey, cfg.SrvKeyLnk)
		return err
	}
	log.V(2).Infof("Succesful Set Cert: %s", sCert)
	return nil
}

// atomicSetCACert atomically replaces CA's certificate.
// trustBundle is the CA
func atomicSetCACert(cfg *Config, caCert string) error {
	// NOTE: muPath has to be writer-locked when entering this function.
	// This assumes default profile and needs to be replaced when multi-profile is supported
	log.V(2).Infof("Attempt Set CA: %s", caCert)
	cert, err := filepath.Abs(caCert)
	if err != nil {
		return err
	}
	// Remove the old symlink to CA's certificate.
	oldCert, err := rmSymlink(cfg.CaCertLnk)
	if err != nil {
		return err
	}
	// Create new symbolic link to new certificate.
	if err := os.Symlink(cert, cfg.CaCertLnk); err != nil {
		// Ignore the following error as it is secondary and report the problem with creating the new symlink.
		_ = restoreSymlink(oldCert, cfg.CaCertLnk)
		return err
	}
	log.V(2).Infof("Succesful Set CA: %s", caCert)
	return nil
}

func restoreFromFile(link string, file string) string {
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		if path, readErr := os.Readlink(link); readErr == nil {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				return path
			}
		}
	}

	if _, err := os.Stat(file); os.IsNotExist(err) {
		log.V(0).Infof("cannot find file: %s link: %s", file, link)
		return "unknown"
	}
	return file
}

func attemptWrite(name string, data []byte, perm os.FileMode) error {
	err := os.WriteFile(name, data, perm)
	if err != nil {
		if e := os.Remove(name); e != nil {
			err = fmt.Errorf("Write %s failed: %w; Cleanup failed", name, err)
		}
	}
	return err
}

func removeEntityFiles(e *genericEntity) {
	switch e.EType {
	case certType:
		log.V(2).Infof("Removing Cert: %s", e.CertPath)
		if err := os.Remove(e.CertPath); err != nil {
			log.V(1).Infof("Removing Cert failed: %s", err)
		}
		log.V(2).Infof("Removing Key: %s", e.KeyPath)
		if err := os.Remove(e.KeyPath); err != nil {
			log.V(1).Infof("Removing Key failed: %s", err)
		}
	case tbType:
		log.V(2).Infof("Removing TrustBundle: %s", e.CertPath)
		if err := os.Remove(e.CertPath); err != nil {
			log.V(1).Infof("Removing TrustBundle failed: %s", err)
		}
	}
}

func parseCSRSuite(suite certz.CSRSuite) (int, x509.SignatureAlgorithm) {
	switch suite {
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_2048_SIGNATURE_ALGORITHM_SHA_2_256:
		return 2048, x509.SHA256WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_2048_SIGNATURE_ALGORITHM_SHA_2_384:
		return 2048, x509.SHA384WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_2048_SIGNATURE_ALGORITHM_SHA_2_512:
		return 2048, x509.SHA512WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_3072_SIGNATURE_ALGORITHM_SHA_2_256:
		return 3072, x509.SHA512WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_3072_SIGNATURE_ALGORITHM_SHA_2_384:
		return 3072, x509.SHA384WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_3072_SIGNATURE_ALGORITHM_SHA_2_512:
		return 3072, x509.SHA512WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_4096_SIGNATURE_ALGORITHM_SHA_2_256:
		return 4096, x509.SHA512WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_4096_SIGNATURE_ALGORITHM_SHA_2_384:
		return 4096, x509.SHA384WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_4096_SIGNATURE_ALGORITHM_SHA_2_512:
		return 4096, x509.SHA512WithRSA
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_256:
		return 256, x509.ECDSAWithSHA256
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_384:
		return 256, x509.ECDSAWithSHA384
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_512:
		return 256, x509.ECDSAWithSHA512
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP384R1_SIGNATURE_ALGORITHM_SHA_2_256:
		return 384, x509.ECDSAWithSHA256
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP384R1_SIGNATURE_ALGORITHM_SHA_2_384:
		return 384, x509.ECDSAWithSHA384
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP384R1_SIGNATURE_ALGORITHM_SHA_2_512:
		return 384, x509.ECDSAWithSHA512
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP521R1_SIGNATURE_ALGORITHM_SHA_2_256:
		return 521, x509.ECDSAWithSHA256
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP521R1_SIGNATURE_ALGORITHM_SHA_2_384:
		return 521, x509.ECDSAWithSHA384
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP521R1_SIGNATURE_ALGORITHM_SHA_2_512:
		return 521, x509.ECDSAWithSHA512
	case certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_EDDSA_ED25519:
		return 256, x509.PureEd25519
	case certz.CSRSuite_CSRSUITE_CIPHER_UNSPECIFIED:
		fallthrough
	default:
		return 0, x509.UnknownSignatureAlgorithm
	}
}
