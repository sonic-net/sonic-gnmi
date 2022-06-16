package gnmi

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	//spb "github.com/sonic-net/sonic-gnmi/proto"
	spb_jwt_gnoi "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	log "github.com/golang/glog"
	//"github.com/golang/protobuf/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	//gnmi_extpb "github.com/openconfig/gnmi/proto/gnmi_ext"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	//"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"net"
	"strings"
	"sync"
)

var (
	supportedEncodings = []gnmipb.Encoding{gnmipb.Encoding_JSON, gnmipb.Encoding_JSON_IETF}
)

var (
	supportedModels = []*gnmipb.ModelData{
		{
			Name:         "sonic-yang",
			Organization: "SONiC",
			Version:      "0.1.0",
		},
		{
			Name:         "sonic-db",
			Organization: "SONiC",
			Version:      "0.1.0",
		},
	}
)

// Server manages a single gNMI Server implementation. Each client that connects
// via Subscribe or Get will receive a stream of updates based on the requested
// path. Set request is processed by server too.
type Server struct {
	s       *grpc.Server
	lis     net.Listener
	config  *Config
	cMu     sync.Mutex
}
type AuthTypes map[string]bool

// Config is a collection of values for Server
type Config struct {
	// Port for the Server to listen on. If 0 or unset the Server will pick a port
	// for this Server.
	Port     int64
	UserAuth AuthTypes
	TestMode bool
}

var AuthLock sync.Mutex

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

	workPath := "/etc/sonic/gnmi"
	os.RemoveAll(workPath)
	os.MkdirAll(workPath, 0777)
	common_utils.InitCounters()

	s := grpc.NewServer(opts...)
	reflection.Register(s)

	srv := &Server{
		s:       s,
		config:  config,
	}
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
	gnoi_system_pb.RegisterSystemServer(srv.s, srv)

	log.V(1).Infof("Created Server on %s", srv.Address())
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

// Address returns the port the Server is listening to.
func (srv *Server) Address() string {
	addr := srv.lis.Addr().String()
	return strings.Replace(addr, "[::]", "localhost", 1)
}

// Port returns the port the Server is listening to.
func (srv *Server) Port() int64 {
	return srv.config.Port
}

func authenticate(UserAuth AuthTypes, ctx context.Context) (context.Context, error) {
	var err error
	success := false
	rc, ctx := common_utils.GetContext(ctx)
	if !UserAuth.Any() {
		//No Auth enabled
		rc.Auth.AuthEnabled = false
		return ctx, nil
	}
	rc.Auth.AuthEnabled = true
	if UserAuth.Enabled("password") {
		ctx, err = BasicAuthenAndAuthor(ctx)
		if err == nil {
			success = true
		}
	}
	if !success && UserAuth.Enabled("jwt") {
		_, ctx, err = JwtAuthenAndAuthor(ctx)
		if err == nil {
			success = true
		}
	}
	if !success && UserAuth.Enabled("cert") {
		ctx, err = ClientCertAuthenAndAuthor(ctx)
		if err == nil {
			success = true
		}
	}

	//Allow for future authentication mechanisms here...

	if !success {
		return ctx, status.Error(codes.Unauthenticated, "Unauthenticated")
	}

	return ctx, nil
}

// Subscribe implements the gNMI Subscribe RPC.
func (s *Server) Subscribe(stream gnmipb.GNMI_SubscribeServer) error {
	ctx := stream.Context()
	_, err := authenticate(s.config.UserAuth, ctx)
	if err != nil {
		return err
	}
	return grpc.Errorf(codes.Unimplemented, "Capabilities is not supported")
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

func ParseTarget(target string, paths []*gnmipb.Path) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	for i, path := range paths {
		elems := path.GetElem()
		if elems == nil {
			return "", status.Error(codes.Unimplemented, "No target specified in path")
		}
		if target == "" {
			if i == 0 {
				target = elems[0].GetName()
			}
		} else if target != elems[0].GetName() {
			return "", status.Error(codes.Unimplemented, "Target conflict in path")
		}
	}
	if target == "" {
		return "", status.Error(codes.Unimplemented, "No target specified in path")
	}
	return target, nil
}

func ParseOrigin(origin string, paths []*gnmipb.Path) (string, error) {
	if len(paths) == 0 {
		return origin, nil
	}
	for i, path := range paths {
		if origin == "" {
			if i == 0 {
				origin = path.Origin
			}
		} else if origin != path.Origin {
			return "", status.Error(codes.Unimplemented, "Origin conflict in path")
		}
	}
	if origin == "" {
		return origin, status.Error(codes.Unimplemented, "No origin specified in path")
	}
	return origin, nil
}

func IsSupportedOrigin(origin string) bool {
	for _, model := range supportedModels {
		if model.Name == origin {
			return true
		}
	}
	return false
}

// Get implements the Get RPC in gNMI spec.
func (s *Server) Get(ctx context.Context, req *gnmipb.GetRequest) (*gnmipb.GetResponse, error) {
	_, err := authenticate(s.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	common_utils.IncCounter("GNMI get")

	if req.GetType() != gnmipb.GetRequest_ALL {
		common_utils.IncCounter("GNMI get fail")
		return nil, status.Errorf(codes.Unimplemented, "unsupported request type: %s", gnmipb.GetRequest_DataType_name[int32(req.GetType())])
	}

	if err = s.checkEncodingAndModel(req.GetEncoding(), req.GetUseModels()); err != nil {
		common_utils.IncCounter("GNMI get fail")
		return nil, status.Error(codes.Unimplemented, err.Error())
	}

	target := ""
	origin := ""
	prefix := req.GetPrefix()
	if prefix != nil {
		elems := prefix.GetElem()
		if elems != nil {
			target = elems[0].GetName()
		}
		origin = prefix.Origin
	}

	paths := req.GetPath()
	if target == "" {
		target, err = ParseTarget(target, paths)
		if err != nil {
			common_utils.IncCounter("GNMI get fail")
			return nil, err
		}
	}
	if origin == "" {
		origin, err = ParseOrigin(origin, paths)
		if err != nil {
			common_utils.IncCounter("GNMI get fail")
			return nil, err
		}
	}
	if check := IsSupportedOrigin(origin); !check {
		common_utils.IncCounter("GNMI get fail")
		return nil, status.Errorf(codes.Unimplemented, "Invalid origin: %s", origin)
	}
	if origin == "sonic-yang" {
		common_utils.IncCounter("GNMI get fail")
		return nil, status.Errorf(codes.Unimplemented, "SONiC Yang Schema is not implemented yet")
	}
	log.V(5).Infof("GetRequest paths: %v", paths)

	var dc sdc.Client

	if _, ok, _, _ := sdc.IsTargetDb(target); ok {
		dc, err = sdc.NewDbClient(paths, prefix, target, origin, s.config.TestMode)
	} else {
		common_utils.IncCounter("GNMI get fail")
		return nil, status.Errorf(codes.Unimplemented, "Invalid target: %s", target)
	}

	if err != nil {
		common_utils.IncCounter("GNMI get fail")
		return nil, status.Error(codes.NotFound, err.Error())
	}
	notifications := make([]*gnmipb.Notification, len(paths))
	spbValues, err := dc.Get(nil)
	dc.Close()
	if err != nil {
		common_utils.IncCounter("GNMI get fail")
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

func (s *Server) Set(ctx context.Context, req *gnmipb.SetRequest) (*gnmipb.SetResponse, error) {
	_, err := authenticate(s.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	var results []*gnmipb.UpdateResult

	common_utils.IncCounter("GNMI set")
	target := ""
	origin := ""
	prefix := req.GetPrefix()
	if prefix != nil {
		elems := prefix.GetElem()
		if elems != nil {
			target = elems[0].GetName()
		}
		origin = prefix.Origin
	}

	paths := req.GetDelete()
	for _, path := range req.GetReplace() {
		paths = append(paths, path.GetPath())
	}
	for _, path := range req.GetUpdate() {
		paths = append(paths, path.GetPath())
	}
	if target == "" {
		target, err = ParseTarget(target, paths)
		if err != nil {
			common_utils.IncCounter("GNMI set fail")
			return nil, err
		}
	}
	if origin == "" {
		origin, err = ParseOrigin(origin, paths)
		if err != nil {
			common_utils.IncCounter("GNMI set fail")
			return nil, err
		}
	}
	if check := IsSupportedOrigin(origin); !check {
		common_utils.IncCounter("GNMI set fail")
		return nil, status.Errorf(codes.Unimplemented, "Invalid origin: %s", origin)
	}
	if origin == "sonic-yang" {
		common_utils.IncCounter("GNMI set fail")
		return nil, status.Errorf(codes.Unimplemented, "SONiC Yang Schema is not implemented yet")
	}

	var dc sdc.Client

	if _, ok, _, _ := sdc.IsTargetDb(target); ok {
		dc, err = sdc.NewDbClient(nil, prefix, target, origin, s.config.TestMode)
	} else {
		common_utils.IncCounter("GNMI set fail")
		return nil, status.Errorf(codes.Unimplemented, "Invalid target: %s", target)
	}

	if err != nil {
		common_utils.IncCounter("GNMI set fail")
		return nil, status.Error(codes.NotFound, err.Error())
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
	dc.Close()
	if err != nil {
		common_utils.IncCounter("GNMI set fail")
	}

	return &gnmipb.SetResponse{
		Prefix:   req.GetPrefix(),
		Response: results,
	}, err

}

func (s *Server) Capabilities(ctx context.Context, req *gnmipb.CapabilityRequest) (*gnmipb.CapabilityResponse, error) {
	_, err := authenticate(s.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	return &gnmipb.CapabilityResponse{SupportedModels: supportedModels,
		SupportedEncodings: supportedEncodings}, nil
}
