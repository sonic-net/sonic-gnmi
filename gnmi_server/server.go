package gnmi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	operationalhandler "github.com/sonic-net/sonic-gnmi/pkg/server/operational-handler"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	spb_gnoi "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	spb_jwt_gnoi "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	_ "github.com/sonic-net/sonic-gnmi/show_client"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"

	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	gnmi_extpb "github.com/openconfig/gnmi/proto/gnmi_ext"
	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	"github.com/openconfig/gnoi/factory_reset"
	gnoi_system_pb "github.com/openconfig/gnoi/system"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	gnoi_healthz_pb "github.com/openconfig/gnoi/healthz"
	gnoi_os_pb "github.com/openconfig/gnoi/os"
	gnoi_debug "github.com/sonic-net/sonic-gnmi/pkg/gnoi/debug"
	gnoi_debug_pb "github.com/sonic-net/sonic-gnmi/proto/gnoi/debug"
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
	s   *grpc.Server
	lis net.Listener
	// udsServer is the gRPC server for Unix domain socket connections (no TLS).
	// This is nil if UnixSocket is not configured.
	udsServer *grpc.Server
	// udsListener is the listener for Unix domain socket connections.
	// This is nil if UnixSocket is not configured.
	udsListener net.Listener
	config      *Config
	cMu         sync.Mutex
	clients     map[string]*Client
	// SaveStartupConfig points to a function that is called to save changes of
	// configuration to a file. By default it points to an empty function -
	// the configuration is not saved to a file.
	SaveStartupConfig func() error
	// ReqFromMaster point to a function that is called to verify if the request
	// comes from a master controller.
	ReqFromMaster func(req *gnmipb.SetRequest, masterEID *uint128) error
	masterEID     uint128
	gnoi_system_pb.UnimplementedSystemServer
	factory_reset.UnimplementedFactoryResetServer
}

// handleOperationalGet handles OPERATIONAL target requests directly with standard gNMI types
func (s *Server) handleOperationalGet(ctx context.Context, req *gnmipb.GetRequest, paths []*gnmipb.Path, prefix *gnmipb.Path) (*gnmipb.GetResponse, error) {
	// Authentication - use gnoi auth even though this is a gNMI Get operation.
	// The OPERATIONAL target provides operational state queries (like disk space)
	// that supplement gNOI services when existing gNOI definitions don't provide
	// what we need. This allows reusing gnoi_readonly/gnoi_readwrite roles
	// for operational data access control.
	authTarget := "gnoi"
	ctx, err := authenticate(s.config, ctx, authTarget, false)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, err
	}

	// Create operational handler
	operationalHandler, err := operationalhandler.NewOperationalHandler(paths, prefix)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, status.Error(codes.NotFound, err.Error())
	}
	defer operationalHandler.Close()

	// Get data from operational handler
	values, err := operationalHandler.Get(nil)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		// Handler returns proper status errors, propagate them directly
		return nil, err
	}

	// Convert directly to gNMI notifications (no SONiC wrapper!)
	notifications := make([]*gnmipb.Notification, len(values))
	for index, value := range values {
		update := &gnmipb.Update{
			Path: value.Path,
			Val:  value.Value,
		}

		notifications[index] = &gnmipb.Notification{
			Timestamp: value.Timestamp,
			Prefix:    prefix,
			Update:    []*gnmipb.Update{update},
		}
	}

	return &gnmipb.GetResponse{Notification: notifications}, nil
}

// FileServer is the server API for File service.
// All implementations must embed UnimplementedFileServer
// for forward compatibility
type FileServer struct {
	*Server
	gnoi_file_pb.UnimplementedFileServer
}

// OSBackend defines the interface for the OS installation backend service.
type OSBackend interface {
	InstallOS(req string) (string, error)
}

// OSServer is the server API for System service.
// All implementations must embed UnimplementedSystemServer
// for forward compatibility
type OSServer struct {
	*Server
	backend OSBackend // Dependency interface
	ImgDir  string
	gnoi_os_pb.UnimplementedOSServer
}

// ContainerzServer is the server API for Containerz service.
type ContainerzServer struct {
	server *Server
	gnoi_containerz_pb.UnimplementedContainerzServer
}

// DebugServer is the server API for Debug service.
type DebugServer struct {
	*Server
	readWhitelist  []string
	writeWhitelist []string
	gnoi_debug_pb.UnimplementedDebugServer
}

// HealthzServer is the server API for System Health service.
// All implementations must embed UnimplementedSystemServer
// for forward compatibility
type HealthzServer struct {
	*Server
	gnoi_healthz_pb.UnimplementedHealthzServer
}

type AuthTypes map[string]bool

// Config is a collection of values for Server
type Config struct {
	// Port for the Server to listen on. If 0 or unset the Server will pick a port
	// for this Server. Port > 0 enables the TCP listener.
	Port int64
	// UnixSocket is the path to a Unix domain socket to listen on.
	// When set, an additional listener is created for local connections without TLS.
	UnixSocket          string
	LogLevel            int
	Threshold           int
	UserAuth            AuthTypes
	EnableTranslibWrite bool
	EnableNativeWrite   bool
	EnableTranslation   bool
	ZmqPort             string
	IdleConnDuration    int
	ConfigTableName     string
	Vrf                 string
	EnableCrl           bool
	// Path to the directory where image is stored.
	ImgDir string
}

// DBusOSBackend is a concrete implementation of OSBackend
type DBusOSBackend struct{}

// InstallOS implements the OSBackend interface.
func (d *DBusOSBackend) InstallOS(req string) (string, error) {
	log.Infof("DBusOSBackend.InstallOS: %v", req)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return "", err
	}
	defer sc.Close()
	return sc.InstallOS(req)
}

var AuthLock sync.Mutex
var maMu sync.Mutex

const WriteAccessMode = "readwrite"
const ReadOnlyMode = "readonly"
const NoAccessMode = "noaccess"

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

// registerAllServices registers all gNMI and gNOI services on the given gRPC server.
func registerAllServices(s *grpc.Server, srv *Server, fileSrv *FileServer,
	osSrv *OSServer, containerzSrv *ContainerzServer,
	debugSrv *DebugServer, healthzSrv *HealthzServer) {
	gnmipb.RegisterGNMIServer(s, srv)
	factory_reset.RegisterFactoryResetServer(s, srv)
	spb_jwt_gnoi.RegisterSonicJwtServiceServer(s, srv)
	if srv.config.EnableTranslibWrite || srv.config.EnableNativeWrite {
		gnoi_system_pb.RegisterSystemServer(s, srv)
		gnoi_file_pb.RegisterFileServer(s, fileSrv)
		gnoi_os_pb.RegisterOSServer(s, osSrv)
		gnoi_containerz_pb.RegisterContainerzServer(s, containerzSrv)
		gnoi_debug_pb.RegisterDebugServer(s, debugSrv)
		gnoi_healthz_pb.RegisterHealthzServer(s, healthzSrv)
	}
	if srv.config.EnableTranslibWrite {
		spb_gnoi.RegisterSonicServiceServer(s, srv)
	}
	spb_gnoi.RegisterDebugServer(s, srv)
}

// NewServer returns an initialized Server.
//
// tlsOpts contains TLS credentials and is used only for the TCP listener.
// commonOpts contains interceptors, keepalive params, etc. and is used for both listeners.
//
// When config.Port > 0, a TCP listener is created with TLS.
// When config.UnixSocket is set, an additional UDS listener is created without TLS.
func NewServer(config *Config, tlsOpts []grpc.ServerOption, commonOpts []grpc.ServerOption) (*Server, error) {
	if config == nil {
		return nil, errors.New("config not provided")
	}
	common_utils.InitCounters()

	srv := &Server{
		config:            config,
		clients:           map[string]*Client{},
		SaveStartupConfig: saveOnSetDisabled,
		ReqFromMaster:     ReqFromMasterDisabledMA,
		masterEID:         uint128{High: 0, Low: 0},
	}

	// Create service servers (shared between TCP and UDS)
	fileSrv := &FileServer{Server: srv}
	osBackend := &DBusOSBackend{}
	osSrv := &OSServer{
		Server:  srv,
		backend: osBackend,
		ImgDir:  srv.config.ImgDir,
	}
	containerzSrv := &ContainerzServer{server: srv}
	healthzSrv := &HealthzServer{Server: srv}
	readWhitelist, writeWhitelist := gnoi_debug.ConstructWhitelists()
	debugSrv := &DebugServer{
		Server:         srv,
		readWhitelist:  readWhitelist,
		writeWhitelist: writeWhitelist,
	}

	var err error

	// TCP Server (Port > 0)
	if config.Port > 0 {
		tcpOpts := append(tlsOpts, commonOpts...)
		srv.s = grpc.NewServer(tcpOpts...)
		reflection.Register(srv.s)

		srv.lis, err = net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
		if err != nil {
			return nil, fmt.Errorf("failed to open listener port %d: %v", config.Port, err)
		}

		registerAllServices(srv.s, srv, fileSrv, osSrv, containerzSrv, debugSrv, healthzSrv)
	}

	// UDS Server (UnixSocket set)
	if config.UnixSocket != "" {
		// UDS server uses only commonOpts (no TLS)
		srv.udsServer = grpc.NewServer(commonOpts...)
		reflection.Register(srv.udsServer)

		// Create socket directory if it doesn't exist (0750 to prevent unauthorized access
		// during the window between socket creation and permission setting)
		socketDir := filepath.Dir(config.UnixSocket)
		if err := os.MkdirAll(socketDir, 0750); err != nil {
			if srv.lis != nil {
				srv.lis.Close()
			}
			return nil, fmt.Errorf("failed to create socket directory %s: %v", socketDir, err)
		}

		os.Remove(config.UnixSocket) // Remove stale socket
		srv.udsListener, err = net.Listen("unix", config.UnixSocket)
		if err != nil {
			// Cleanup TCP listener if it was created
			if srv.lis != nil {
				srv.lis.Close()
			}
			return nil, fmt.Errorf("failed to listen on unix socket %s: %v", config.UnixSocket, err)
		}
		// Restrict socket access to container user (root) and group
		if err := os.Chmod(config.UnixSocket, 0660); err != nil {
			log.Warningf("Failed to set permissions on unix socket %s: %v; disabling UDS listener", config.UnixSocket, err)
			srv.udsListener.Close()
			os.Remove(config.UnixSocket)
			srv.udsListener = nil
			srv.udsServer = nil
		} else {
			registerAllServices(srv.udsServer, srv, fileSrv, osSrv, containerzSrv, debugSrv, healthzSrv)
		}
	}

	// Require at least one listener
	if srv.lis == nil && srv.udsListener == nil {
		return nil, errors.New("no listener configured: port must be > 0 or unix_socket must be set")
	}

	log.V(1).Infof("Created Server on %s, read-only: %t", srv.Address(), !srv.config.EnableTranslibWrite)
	return srv, nil
}

// Serve will start the Server serving and block until closed.
// If both TCP and UDS listeners are configured, both are served concurrently.
func (srv *Server) Serve() error {
	if srv.s == nil && srv.udsServer == nil {
		return fmt.Errorf("Serve() failed: not initialized")
	}

	errChan := make(chan error, 2)

	// Start TCP server if configured
	if srv.s != nil && srv.lis != nil {
		go func() {
			log.V(1).Infof("Starting TCP server on %s", srv.lis.Addr().String())
			err := srv.s.Serve(srv.lis)
			if err != nil {
				errChan <- fmt.Errorf("TCP server: %w", err)
			} else {
				errChan <- nil
			}
		}()
	}

	// Start UDS server if configured
	if srv.udsServer != nil && srv.udsListener != nil {
		go func() {
			log.V(1).Infof("Starting UDS server on %s", srv.udsListener.Addr().String())
			err := srv.udsServer.Serve(srv.udsListener)
			if err != nil {
				errChan <- fmt.Errorf("UDS server: %w", err)
			} else {
				errChan <- nil
			}
		}()
	}

	// Block until first error (or server stop)
	return <-errChan
}

// ForceStop stops the server immediately without waiting for connections to close.
func (srv *Server) ForceStop() {
	if srv.s != nil {
		srv.s.Stop()
	}
	if srv.udsServer != nil {
		srv.udsServer.Stop()
	}
	// Cleanup UDS socket file
	if srv.config != nil && srv.config.UnixSocket != "" {
		os.Remove(srv.config.UnixSocket)
	}
}

// Stop gracefully stops the server, waiting for active connections to close.
func (srv *Server) Stop() {
	if srv.s != nil {
		srv.s.GracefulStop()
	}
	if srv.udsServer != nil {
		srv.udsServer.GracefulStop()
	}
	// Cleanup UDS socket file
	if srv.config != nil && srv.config.UnixSocket != "" {
		os.Remove(srv.config.UnixSocket)
	}
}

// Address returns the addresses the Server is listening on.
func (srv *Server) Address() string {
	var addrs []string
	if srv.lis != nil {
		addr := srv.lis.Addr().String()
		addrs = append(addrs, strings.Replace(addr, "[::]", "localhost", 1))
	}
	if srv.udsListener != nil {
		addrs = append(addrs, srv.udsListener.Addr().String())
	}
	return strings.Join(addrs, ", ")
}

// Port returns the port the Server is listening to.
func (srv *Server) Port() int64 {
	return srv.config.Port
}

// Auth - Authenticate
func (srv *Server) Auth(ctx context.Context) (context.Context, error) {
	return authenticate(srv.config, ctx, "gnmi", false)
}

func authenticate(config *Config, ctx context.Context, target string, writeAccess bool) (context.Context, error) {
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
		// role must be readwrite to support write access
		if success && config.ConfigTableName != "" {
			match := false
			target = strings.ToLower(target)
			for _, role := range rc.Auth.Roles {
				role = strings.TrimSpace(role)
				if strings.HasPrefix(role, target) {
					// Extract the postfix from the role
					// e.g. role=gnmi_config_db_readwrite
					// e.g. role=gnoi_readonly
					postfix := strings.TrimPrefix(role, target)
					postfix = strings.TrimPrefix(postfix, "_")
					// Check if the role postfix indicates no access, and deny access if true.
					if postfix == NoAccessMode {
						return ctx, fmt.Errorf("%s does not have access, target %s, role %s", rc.Auth.User, target, role)
					} else if postfix == ReadOnlyMode {
						// ReadOnlyMode is allowed for read access
						if writeAccess {
							return ctx, fmt.Errorf("%s does not have access, target %s, role %s", rc.Auth.User, target, role)
						} else {
							match = true
							break
						}
					} else if postfix == WriteAccessMode {
						// WriteAccessMode is allowed for read/write access
						match = true
						break
					}
				}
			}
			if !match && writeAccess {
				return ctx, fmt.Errorf("%s does not have write access, target %s", rc.Auth.User, target)
			}
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

	err := c.Run(stream, s.config)
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

	if req.GetType() != gnmipb.GetRequest_ALL {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, status.Errorf(codes.Unimplemented, "unsupported request type: %s", gnmipb.GetRequest_DataType_name[int32(req.GetType())])
	}

	if err := s.checkEncodingAndModel(req.GetEncoding(), req.GetUseModels()); err != nil {
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
	var err error
	// Handle OPERATIONAL target directly without SONiC routing
	if target == "OPERATIONAL" {
		return s.handleOperationalGet(ctx, req, paths, prefix)
	}

	authTarget := "gnmi"
	if target == "OTHERS" {
		dc, err = sdc.NewNonDbClient(paths, prefix)
		authTarget = "gnmi_other"
	} else if target == "SHOW" {
		dc, err = sdc.NewShowClient(paths, prefix)
		authTarget = "gnmi_show"
	} else if targetDbName, ok, _, _ := sdc.IsTargetDb(target); ok {
		dc, err = sdc.NewDbClient(paths, prefix)
		authTarget = "gnmi_" + targetDbName
	} else {
		if origin == "" {
			origin, err = ParseOrigin(paths)
			if err != nil {
				return nil, err
			}
		}
		if check := IsNativeOrigin(origin); check {
			var targetDbName string
			dc, err = sdc.NewMixedDbClient(paths, prefix, origin, encoding, s.config.ZmqPort, s.config.Vrf, &targetDbName)
			authTarget = "gnmi_" + targetDbName
		} else {
			dc, err = sdc.NewTranslClient(prefix, paths, ctx, extensions)
		}
	}

	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, status.Error(codes.NotFound, err.Error())
	}
	defer dc.Close()

	ctx, err = authenticate(s.config, ctx, authTarget, false)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		return nil, err
	}
	notifications := make([]*gnmipb.Notification, len(paths))
	spbValues, err := dc.Get(nil)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_GET_FAIL)
		if st, ok := status.FromError(err); ok {
			return nil, st.Err()
		}
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
	var err error
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
	authTarget := "gnmi"
	if check := IsNativeOrigin(origin); check {
		if s.config.EnableNativeWrite == false {
			common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
			return nil, grpc.Errorf(codes.Unimplemented, "GNMI native write is disabled")
		}

		// Fast path: bypass validation for allowed tables/SKUs
		allUpdates := append(req.GetReplace(), req.GetUpdate()...)
		if resp, used, err := bypass.TrySet(ctx, prefix, req.GetDelete(), allUpdates); used {
			if err != nil {
				common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
				return nil, status.Error(codes.Internal, err.Error())
			}
			common_utils.IncCounter(common_utils.GNMI_SET_BYPASS)
			return resp, nil
		}

		var targetDbName string
		dc, err = sdc.NewMixedDbClient(paths, prefix, origin, encoding, s.config.ZmqPort, s.config.Vrf, &targetDbName)
		authTarget = "gnmi_" + targetDbName
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

	ctx, err = authenticate(s.config, ctx, authTarget, true)
	if err != nil {
		common_utils.IncCounter(common_utils.GNMI_SET_FAIL)
		return nil, err
	}
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
	ctx, err := authenticate(s.config, ctx, "gnmi", false)
	if err != nil {
		return nil, err
	}
	extensions := req.GetExtension()

	/* Fetch the client capabitlities. */
	var supportedModels []gnmipb.ModelData
	dc, _ := sdc.NewTranslClient(nil, nil, ctx, extensions)
	supportedModels = append(supportedModels, dc.Capabilities()...)
	var targetDbName string
	dc, _ = sdc.NewMixedDbClient(nil, nil, "", gnmipb.Encoding_JSON_IETF, s.config.ZmqPort, s.config.Vrf, &targetDbName)
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
