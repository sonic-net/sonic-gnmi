package gnmi

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	log "github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"

	spb "github.com/jipanyang/sonic-telemetry/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// Server manages a single gNMI Server implementation. Each client that connects
// via Subscribe or Get will receive a stream of updates based on the requested
// path. Set request is processed by server too.
type Server struct {
	s      *grpc.Server
	lis    net.Listener
	config *Config
	// cMu protects clients.
	cMu     sync.Mutex
	clients map[string]*Client
}

// Config is a collection of values for Server
type Config struct {
	// Port for the Server to listen on. If 0 or unset the Server will pick a port
	// for this Server.
	Port int64
	// Per RPC credentials for the Server. If not set no per RPC auth will be used.
	Credentials *spb.Credentials
	// TLS cert for use on the Server. If not set the transport will not be TLS.
	Cert []byte
}

// New returns an initialized Server.
func NewServer(config *Config, opts []grpc.ServerOption) (*Server, error) {
	if config == nil {
		return nil, errors.New("config not provided")
	}
	s := grpc.NewServer(opts...)
	reflection.Register(s)

	srv := &Server{
		s:       s,
		config:  config,
		clients: map[string]*Client{},
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

// Subscribe implements the gNMI Subscribe RPC.
func (srv *Server) Subscribe(stream gnmipb.GNMI_SubscribeServer) error {
	ctx := stream.Context()

	pr, ok := peer.FromContext(ctx)
	if !ok {
		return fmt.Errorf("failed to get peer from ctx")
	}
	if pr.Addr == net.Addr(nil) {
		return fmt.Errorf("failed to get peer address")
	}

	/* TODO: authorize the user
	msg, ok := credentials.AuthorizeUser(ctx)
	if !ok {
		log.Infof("denied a Set request: %v", msg)
		return nil, status.Error(codes.PermissionDenied, msg)
	}
	*/

	c := NewClient(pr.Addr)

	srv.cMu.Lock()
	srv.clients[c.String()] = c
	srv.cMu.Unlock()

	err := c.Run(stream)
	srv.cMu.Lock()
	delete(srv.clients, c.String())
	srv.cMu.Unlock()

	return err
}

// Get method is not implemented. Refer to gnxi for examples with openconfig integration
func (srv *Server) Get(context.Context, *gnmipb.GetRequest) (*gnmipb.GetResponse, error) {
	return nil, grpc.Errorf(codes.Unimplemented, "Get() is not implemented")
}

// Set method is not implemented. Refer to gnxi for examples with openconfig integration
func (srv *Server) Set(context.Context, *gnmipb.SetRequest) (*gnmipb.SetResponse, error) {
	return nil, grpc.Errorf(codes.Unimplemented, "Set() is not implemented")
}

// Capabilities method is not implemented. Refer to gnxi for examples with openconfig integration
func (srv *Server) Capabilities(context.Context, *gnmipb.CapabilityRequest) (*gnmipb.CapabilityResponse, error) {
	return nil, grpc.Errorf(codes.Unimplemented, "Capabilities() is not implemented")
}
