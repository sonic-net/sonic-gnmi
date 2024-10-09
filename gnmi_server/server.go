package gnmi

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	spb_gnoi "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	spb_jwt_gnoi "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"

	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	gnmi_extpb "github.com/openconfig/gnmi/proto/gnmi_ext"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

var (
	supportedEncodings = []gnmipb.Encoding{gnmipb.Encoding_JSON, gnmipb.Encoding_JSON_IETF, gnmipb.Encoding_PROTO}
)

// Server manages a single gNMI Server implementation. Each client that connects
// via Subscribe or Get will receive a stream of updates based on the requested
// path. Set request is processed by server too.
type Server struct {
	s       *grpc.Server
	lis     net.Listener
	config  *Config
	cMu     sync.Mutex
	clients map[string]*Client
	// SaveStartupConfig points to a function that is called to save changes of
	// configuration to a file. By default it points to an empty function -
	// the configuration is not saved to a file.
	SaveStartupConfig func() error
	// ReqFromMaster point to a function that is called to verify if the request
	// comes from a master controller.
	ReqFromMaster func(req *gnmipb.SetRequest, masterEID *uint128) error
	masterEID     uint128
}


// FileServer is the server API for File service.
// All implementations must embed UnimplementedFileServer
// for forward compatibility
type FileServer struct {
	*Server
	gnoi_file_pb.UnimplementedFileServer
}

// SystemServer is the server API for System service.
// All implementations must embed UnimplementedSystemServer
// for forward compatibility
type SystemServer struct {
	*Server
	gnoi_system_pb.UnimplementedSystemServer
}

type AuthTypes map[string]bool

// Config is a collection of values for Server
type Config struct {
	// Port for the Server to listen on. If 0 or unset the Server will pick a port
	// for this Server.
	Port                int64
	LogLevel            int
	Threshold           int
	UserAuth            AuthTypes
	EnableTranslibWrite bool
	EnableNativeWrite   bool
	ZmqPort             string
	IdleConnDuration    int
	ConfigTableName     string
	EnableCrl           bool
}

var AuthLock sync.Mutex
var maMu sync.Mutex

func (i AuthTypes) String() string {
	if i["none"] {
		return ""
	}
	b := new(bytes.Buffer)
	for key, value := range i {
		if value {
			fmt.Fprintf(b, "%s ", key)
		}
	}
	return b.String()
}

func (i AuthTypes) Any() bool {
	if i["none"] {
		return false
	}
	for _, value := range i {
		if value {
			return true
		}
	}
	return false
}

func (i AuthTypes) Enabled(mode string) bool {
	if i["none"] {
		return false
	}
	if value, exist := i[mode]; exist && value {
		return true
	}
	return false
}

func (i AuthTypes) Set(mode string) error {
	modes := strings.Split(mode, ",")
	for _, m := range modes {
		m = strings.Trim(m, " ")
		if m == "none" || m == "" {
			i["none"] = true
			return nil
		}

		if _, exist := i[m]; !exist {
			return fmt.Errorf("Expecting one or more of 'cert', 'password' or 'jwt'")
		}
		i[m] = true
	}
	return nil
}

func (i AuthTypes) Unset(mode string) error {
	modes := strings.Split(mode, ",")
	for _, m := range modes {
		m = strings.Trim(m, " ")
		if _, exist := i[m]; !exist {
			return fmt.Errorf("Expecting one or more of 'cert', 'password' or 'jwt'")
		}
		i[m] = false
	}
	return nil
}

// New returns an initialized Server.
func NewServer(config *Config, opts []grpc.ServerOption) (*Server, error) {
	if config == nil {
		return nil, errors.New("config not provided")
	}
	common_utils.InitCounters()

	s := grpc.NewServer(opts...)
	reflection.Register(s)

	srv := &Server{
		s:                 s,
		config:            config,
		clients:           map[string]*Client{},
		SaveStartupConfig: saveOnSetDisabled,
		// ReqFromMaster point to a function that is called to verify if
		// the request comes from a master controller.
		ReqFromMaster: ReqFromMasterDisabledMA,
		masterEID:     uint128{High: 0, Low: 0},
	}

	fileSrv := &FileServer{Server: srv}
	systemSrv := &SystemServer{Server: srv}

	var err error
	if srv.config.Port < 0 {
		srv.config.Port = 0
	}
	srv.lis, err = net.Listen("tcp", fmt.Sprintf(":%d", srv.config.Port))
	if err != nil {
		return nil, fmt.Errorf("failed to open listener port %d: %v", srv.config.Port, err)
	}
	gnmipb.RegisterGNMIServer(srv.s, srv)
	spb_jwt_gnoi.RegisterSonicJwtServiceServer(srv.s, srv)
	if srv.config.EnableTranslibWrite || srv.config.EnableNativeWrite {
		gnoi_system_pb.RegisterSystemServer(srv.s, systemSrv)
		gnoi_file_pb.RegisterFileServer(srv.s, fileSrv)
	}
	if srv.config.EnableTranslibWrite {
		spb_gnoi.RegisterSonicServiceServer(srv.s, srv)
	}
	spb_gnoi.RegisterDebugServer(srv.s, srv)
	log.V(1).Infof("Created Server on %s, read-only: %t", srv.Address(), !srv.config.EnableTranslibWrite)
	return srv, nil
}

// Serve will start the Server serving and block until closed.
func (srv *Server) Serve() error {
	s := srv.s
	if s == nil {
		return fmt.Errorf("Serve() failed: not initialized")
	}
	return srv.s.Serve(srv.lis)
}

func (srv *Server) ForceStop() {
	s := srv.s
	if s == nil {
		log.Errorf("ForceStop() failed: not initialized")
		return
	}
	s.Stop()
}

func (srv *Server) Stop() {
	s := srv.s
	if s == nil {
		log.Errorf("Stop() failed: not initialized")
		return
	}
	s.GracefulStop()
}

// Address returns the port the Server is listening to.
func (srv *Server) Address() string {
	addr := srv.lis.Addr().String()
	return strings.Replace(addr, "[::]", "localhost", 1)
}

// Port returns the port the Server is listening to.
func (srv *Server) Port() int64 {
	return srv.config.Port
}

func authenticate(config *Config, ctx context.Context) (context.Context, error) {
	var err error
	success := false
	rc, ctx := common_utils.GetContext(ctx)
	if !config.UserAuth.Any() {
		//No Auth enabled
		rc.Auth.AuthEnabled = false
		return ctx, nil
	}

	rc.Auth.AuthEnabled = true
	if config.UserAuth.Enabled("password") {
		ctx, err = BasicAuthenAndAuthor(ctx)
		if err == nil {
			success = true
		}
	}
	if !success && config.UserAuth.Enabled("jwt") {
		_, ctx, err = JwtAuthenAndAuthor(ctx)
		if err == nil {
			success = true
		}
	}
	if !success && config.UserAuth.Enabled("cert") {
		ctx, err = ClientCertAuthenAndAuthor(ctx, config.ConfigTableName, config.EnableCrl)
		if err == nil {
			success = true
		}
	}

	//Allow for future authentication mechanisms here...

	if !success {
		return ctx, status.Error(codes.Unauthenticated, "Unauthenticated")
	}
	log.V(5).Infof("authenticate user %v, roles %v", rc.Auth.User, rc.Auth.Roles)

	return ctx, nil
}

// Subscribe implements the gNMI Subscribe RPC.
func (s *Server) Subscribe(stream gnmipb.GNMI_SubscribeServer) error {
	ctx := stream.Context()
	ctx, err := authenticate(s.config, ctx)
	if err != nil {
		return err
	}

	pr, ok := peer.FromContext(ctx)
	if !ok {
		return grpc.Errorf(codes.InvalidArgument, "failed to get peer from ctx")
		//return fmt.Errorf("failed to get peer from ctx")
	}
	if pr.Addr == net.Addr(nil) {
		return grpc.Errorf(codes.InvalidArgument, "failed to get peer address")
	}

	/* TODO: authorize the user
	msg, ok := credentials.AuthorizeUser(ctx)
	if !ok {
		log.Infof("denied a Set request: %v", msg)
		return nil, status.Error(codes.PermissionDenied, msg)
	}
	*/

	c := NewClient(pr.Addr)

	c.setLogLevel(s.config.LogLevel)
	c.setConnectionManager(s.config.Threshold)

	s.cMu.Lock()
	if oc, ok := s.clients[c.String()]; ok {
		log.V(2).Infof("Delete duplicate client %s", oc)
		oc.Close()
		delete(s.clients, c.String())
	}
	s.clients[c.String()] = c
	s.cMu.Unlock()

	err = c.Run(stream)
	s.cMu.Lock()
	delete(s.clients, c.String())
	s.cMu.Unlock()

	log.Flush()
	return err
}

// checkEncodingAndModel checks whether encoding and models are supported by the server. Return error if anything is unsupported.
func (s *Server) checkEncodingAndModel(encoding gnmipb.Encoding, models []*gnmipb.ModelData) error {
	hasSupportedEncoding := false
	for _, supportedEncoding := range supportedEncodings {
		if encoding == supportedEncoding {
			hasSupportedEncoding = true
			break
		}
	}
	if !hasSupportedEncoding {
		return fmt.Errorf("unsupported encoding: %s", gnmipb.Encoding_name[int32(encoding)])
	}

	return nil
}

func ParseOrigin(paths []*gnmipb.Path) (string, error) {
	origin := ""
	if len(paths) == 0 {
		return origin, nil
	}
	for i, path := range paths {
		if i == 0 {
			origin = path.Origin
		} else {
			if origin != path.Origin {
				return "", status.Error(codes.Unimplemented, "Origin conflict in path")
			}
		}
	}
	return origin, nil
}

func IsNativeOrigin(origin string) bool {
	return origin == "sonic-db"
}

// Get implements the Get RPC in gNMI spec.
func (s *Server) Get(ctx context.Context, req *gnmipb.GetRequest) (*gnmipb.GetResponse, error) {
	common_utils.IncCounter(common_utils.GNMI_GET)
	ctx, err := authenticate(s.config, ctx)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, err
	}

	if req.GetType() != gnmipb.GetRequest_ALL {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, status.Errorf(codes.Unimplemented, "unsupported request type: %s", gnmipb.GetRequest_DataType_name[int32(req.GetType())])
	}

	if err = s.checkEncodingAndModel(req.GetEncoding(), req.GetUseModels()); err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, status.Error(codes.Unimplemented, err.Error())
	}

	target := ""
	origin := ""
	prefix := req.GetPrefix()
	if prefix != nil {
		target = prefix.GetTarget()
		origin = prefix.Origin
	}

	paths := req.GetPath()
	extensions := req.GetExtension()
	encoding := req.GetEncoding()
	log.V(2).Infof("GetRequest paths: %v", paths)

	var dc sdc.Client

	if target == "OTHERS" {
		dc, err = sdc.NewNonDbClient(paths, prefix)
	} else if _, ok, _, _ := sdc.IsTargetDb(target); ok {
		dc, err = sdc.NewDbClient(paths, prefix)
	} else {
		if origin == "" {
			origin, err = ParseOrigin(paths)
			if err != nil {
				return nil, err
			}
		}
		if check := IsNativeOrigin(origin); check {
			dc, err = sdc.NewMixedDbClient(paths, prefix, origin, encoding, s.config.ZmqPort)
		} else {
			dc, err = sdc.NewTranslClient(prefix, paths, ctx, extensions)
		}
	}

	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, status.Error(codes.NotFound, err.Error())
	}
	defer dc.Close()
	notifications := make([]*gnmipb.Notification, len(paths))
	spbValues, err := dc.Get(nil)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, status.Error(codes.NotFound, err.Error())
	}

	for index, spbValue := range spbValues {
		update := &gnmipb.Update{
			Path: spbValue.GetPath(),
			Val:  spbValue.GetVal(),
		}

		notifications[index] = &gnmipb.Notification{
			Timestamp: spbValue.GetTimestamp(),
			Prefix:    prefix,
			Update:    []*gnmipb.Update{update},
		}
	}
	return &gnmipb.GetResponse{Notification: notifications}, nil
}

// saveOnSetEnabled saves configuration to a file
func SaveOnSetEnabled() error {
	sc, err := ssc.NewDbusClient()
	if err != nil {
		log.V(0).Infof("Saving startup config failed to create dbus client: %v", err)
		return err
	}
	if err := sc.ConfigSave("/etc/sonic/config_db.json"); err != nil {
		log.V(0).Infof("Saving startup config failed: %v", err)
		return err
	} else {
		log.V(1).Infof("Success! Startup config has been saved!")
	}
	return nil
}

// SaveOnSetDisabeld does nothing.
func saveOnSetDisabled() error { return nil }

func (s *Server) Set(ctx context.Context, req *gnmipb.SetRequest) (*gnmipb.SetResponse, error) {
	e := s.ReqFromMaster(req, &s.masterEID)
	if e != nil {
		return nil, e
	}

	common_utils.IncCounter(common_utils.GNMI_SET)
	if s.config.EnableTranslibWrite == false && s.config.EnableNativeWrite == false {
		common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
		return nil, grpc.Errorf(codes.Unimplemented, "GNMI is in read-only mode")
	}
	ctx, err := authenticate(s.config, ctx)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
		return nil, err
	}
	var results []*gnmipb.UpdateResult

	/* Fetch the prefix. */
	prefix := req.GetPrefix()
	origin := ""
	if prefix != nil {
		origin = prefix.Origin
	}
	extensions := req.GetExtension()
	encoding := gnmipb.Encoding_JSON_IETF

	var dc sdc.Client
	paths := req.GetDelete()
	for _, path := range req.GetReplace() {
		paths = append(paths, path.GetPath())
	}
	for _, path := range req.GetUpdate() {
		paths = append(paths, path.GetPath())
	}
	if origin == "" {
		origin, err = ParseOrigin(paths)
		if err != nil {
			return nil, err
		}
	}
	if check := IsNativeOrigin(origin); check {
		if s.config.EnableNativeWrite == false {
			common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
			return nil, grpc.Errorf(codes.Unimplemented, "GNMI native write is disabled")
		}
		dc, err = sdc.NewMixedDbClient(paths, prefix, origin, encoding, s.config.ZmqPort)
	} else {
		if s.config.EnableTranslibWrite == false {
			common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
			return nil, grpc.Errorf(codes.Unimplemented, "Translib write is disabled")
		}
		/* Create Transl client. */
		dc, err = sdc.NewTranslClient(prefix, nil, ctx, extensions)
	}

	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
		return nil, status.Error(codes.NotFound, err.Error())
	}
	defer dc.Close()

	/* DELETE */
	for _, path := range req.GetDelete() {
		log.V(2).Infof("Delete path: %v", path)

		res := gnmipb.UpdateResult{
			Path: path,
			Op:   gnmipb.UpdateResult_DELETE,
		}

		/* Add to Set response results. */
		results = append(results, &res)
	}

	/* REPLACE */
	for _, path := range req.GetReplace() {
		log.V(2).Infof("Replace path: %v ", path)

		res := gnmipb.UpdateResult{
			Path: path.GetPath(),
			Op:   gnmipb.UpdateResult_REPLACE,
		}
		/* Add to Set response results. */
		results = append(results, &res)
	}

	/* UPDATE */
	for _, path := range req.GetUpdate() {
		log.V(2).Infof("Update path: %v ", path)

		res := gnmipb.UpdateResult{
			Path: path.GetPath(),
			Op:   gnmipb.UpdateResult_UPDATE,
		}
		/* Add to Set response results. */
		results = append(results, &res)
	}
	err = dc.Set(req.GetDelete(), req.GetReplace(), req.GetUpdate())
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
	} else {
		s.SaveStartupConfig()
	}

	return &gnmipb.SetResponse{
		Prefix:   req.GetPrefix(),
		Response: results,
	}, err

}

func (s *Server) Capabilities(ctx context.Context, req *gnmipb.CapabilityRequest) (*gnmipb.CapabilityResponse, error) {
	ctx, err := authenticate(s.config, ctx)
	if err != nil {
		return nil, err
	}
	extensions := req.GetExtension()

	/* Fetch the client capabitlities. */
	var supportedModels []gnmipb.ModelData
	dc, _ := sdc.NewTranslClient(nil, nil, ctx, extensions)
	supportedModels = append(supportedModels, dc.Capabilities()...)
	dc, _ = sdc.NewMixedDbClient(nil, nil, "", gnmipb.Encoding_JSON_IETF, s.config.ZmqPort)
	supportedModels = append(supportedModels, dc.Capabilities()...)

	suppModels := make([]*gnmipb.ModelData, len(supportedModels))

	for index, model := range supportedModels {
		suppModels[index] = &gnmipb.ModelData{
			Name:         model.Name,
			Organization: model.Organization,
			Version:      model.Version,
		}
	}

	sup_bver := spb.SupportedBundleVersions{
		BundleVersion: translib.GetYangBundleVersion().String(),
		BaseVersion:   translib.GetYangBaseVersion().String(),
	}
	sup_msg, _ := proto.Marshal(&sup_bver)
	ext := gnmi_extpb.Extension{}
	ext.Ext = &gnmi_extpb.Extension_RegisteredExt{
		RegisteredExt: &gnmi_extpb.RegisteredExtension{
			Id:  spb.SUPPORTED_VERSIONS_EXT,
			Msg: sup_msg}}
	exts := []*gnmi_extpb.Extension{&ext}

	return &gnmipb.CapabilityResponse{SupportedModels: suppModels,
		SupportedEncodings: supportedEncodings,
		GNMIVersion:        "0.7.0",
		Extension:          exts}, nil
}

type uint128 struct {
	High uint64
	Low  uint64
}

func (lh *uint128) Compare(rh *uint128) int {
	if rh == nil {
		// For MA disabled case, EID supposed to be 0.
		rh = &uint128{High: 0, Low: 0}
	}
	if lh.High > rh.High {
		return 1
	}
	if lh.High < rh.High {
		return -1
	}
	if lh.Low > rh.Low {
		return 1
	}
	if lh.Low < rh.Low {
		return -1
	}
	return 0
}

// ReqFromMasterEnabledMA returns true if the request is sent by the master
// controller.
func ReqFromMasterEnabledMA(req *gnmipb.SetRequest, masterEID *uint128) error {
	// Read the election_id.
	reqEID := uint128{High: 0, Low: 0}
	hasMaExt := false
	// It can be one of many extensions, so iterate through them to find it.
	for _, e := range req.GetExtension() {
		ma := e.GetMasterArbitration()
		if ma == nil {
			continue
		}

		hasMaExt = true
		// The Master Arbitration descriptor has been found.
		if ma.ElectionId == nil {
			return status.Errorf(codes.InvalidArgument, "MA: ElectionId missing")
		}

		if ma.Role != nil {
			// Role will be implemented later.
			return status.Errorf(codes.Unimplemented, "MA: Role is not implemented")
		}

		reqEID = uint128{High: ma.ElectionId.High, Low: ma.ElectionId.Low}
		// Use the election ID that is in the last extension, so, no 'break' here.
	}

	if !hasMaExt {
		log.V(0).Infof("MA: No Master Arbitration in setRequest extension, masterEID %v is not updated", masterEID)
		return nil
	}

	maMu.Lock()
	defer maMu.Unlock()
	switch masterEID.Compare(&reqEID) {
	case 1: // This Election ID is smaller than the known Master Election ID.
		return status.Errorf(codes.PermissionDenied, "Election ID is smaller than the current master. Rejected. Master EID: %v. Current EID: %v.", masterEID, reqEID)
	case -1: // New Master Election ID received!
		log.V(0).Infof("New master has been elected with %v\n", reqEID)
		*masterEID = reqEID
	}
	return nil
}

// ReqFromMasterDisabledMA always returns true. It is used when Master Arbitration
// is disabled.
func ReqFromMasterDisabledMA(req *gnmipb.SetRequest, masterEID *uint128) error {
	return nil
}
