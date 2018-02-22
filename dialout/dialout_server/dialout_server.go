package dialout_server

import (
	"errors"
	"fmt"
	log "github.com/golang/glog"
	"github.com/google/gnxi/utils"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"io"
	"net"
	"strings"
	"sync"
)

var (
	supportedEncodings = []gpb.Encoding{gpb.Encoding_JSON, gpb.Encoding_JSON_IETF}
)

// Server manages a single GNMIDialOut_PublishServer implementation. Each client that connects
// via PublistRequest sends subscribeResponse to the server.
type Server struct {
	s         *grpc.Server
	lis       net.Listener
	config    *Config
	cMu       sync.Mutex
	clients   map[string]*Client
	sRWMu     sync.RWMutex //for protection of appending data to data store
	dataStore interface{}  //For storing the data received
}

// Config is a collection of values for Server
type Config struct {
	// Port for the Server to listen on. If 0 or unset the Server will pick a port
	// for this Server.
	Port int64
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
	spb.RegisterGNMIDialOutServer(srv.s, srv)
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

func (srv *Server) Stop() error {
	s := srv.s
	if s == nil {
		return fmt.Errorf("Serve() failed: not initialized")
	}
	srv.s.Stop()
	log.V(1).Infof("Server stopped on %s", srv.Address())
	return nil
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

// Port returns the port the Server is listening to.
func (srv *Server) SetDataStore(dataStore interface{}) {
	srv.dataStore = dataStore
}

// Publish implements the GNMI DialOut Publish RPC.
func (srv *Server) Publish(stream spb.GNMIDialOut_PublishServer) error {
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

	srv.cMu.Lock()
	if oc, ok := srv.clients[c.String()]; ok {
		log.V(2).Infof("Delete duplicate client %s", oc)
		oc.Close()
		delete(srv.clients, c.String())
	}
	srv.clients[c.String()] = c
	srv.cMu.Unlock()

	err := c.Run(srv, stream)
	c.Close()

	srv.cMu.Lock()
	delete(srv.clients, c.String())
	srv.cMu.Unlock()

	log.Flush()
	return err
}

// Client contains information about a subscribe client that has connected to the server.
type Client struct {
	addr    net.Addr
	sendMsg int64
	recvMsg int64
	errors  int64
	polled  chan struct{}
	stop    chan struct{}
	mu      sync.RWMutex
}

// NewClient returns a new initialized client.
func NewClient(addr net.Addr) *Client {
	return &Client{
		addr: addr,
	}
}

// String returns the target the client is querying.
func (c *Client) String() string {
	return c.addr.String()
}

// Run process streaming from publish client. The first message received must be a
// SubscriptionList. Once the client is started, it will run until the stream
// is closed or the schedule completes. For Poll queries the Run will block
// internally after sync until a Poll request is made to the server.
func (c *Client) Run(srv *Server, stream spb.GNMIDialOut_PublishServer) (err error) {
	defer log.V(1).Infof("Client %s shutdown", c)

	if stream == nil {
		return grpc.Errorf(codes.FailedPrecondition, "cannot start client: stream is nil")
	}

	defer func() {
		if err != nil {
			c.errors++
		}
	}()

	for {
		subscribeResponse, err := stream.Recv()
		c.recvMsg++
		if err != nil {
			if err == io.EOF {
				return grpc.Errorf(codes.Aborted, "stream EOF received")
			}
			return grpc.Errorf(grpc.Code(err), "received error from client")
		}

		srv.sRWMu.Lock()
		if srv.dataStore != nil {
			switch ds := srv.dataStore.(type) {
			default:
				log.V(1).Infof("unexpected type %T\n", srv.dataStore)
			case *[]*gpb.SubscribeResponse:
				*ds = append(*ds, subscribeResponse)
			}
		}
		srv.sRWMu.Unlock()

		if srv.dataStore == nil {
			fmt.Println("== subscribeResponse:")
			utils.PrintProto(subscribeResponse)
		}

		// TODO: send back (m *PublishResponse))
	}
	return grpc.Errorf(codes.InvalidArgument, "Exiting")
}

// Closing of client queue is triggered upon end of stream receive or stream error
// or fatal error of any client go routine .
// it will cause cancle of client context and exit of the send goroutines.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	log.V(1).Infof("Client %s Close, sendMsg %v recvMsg %v errors %v", c, c.sendMsg, c.recvMsg, c.errors)

	if c.stop != nil {
		close(c.stop)
	}
	if c.polled != nil {
		close(c.polled)
	}
}
