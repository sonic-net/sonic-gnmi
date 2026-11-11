package gnmi

import (
	"context"
	"fmt"
	"sync"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
)

// GetReader is the data dependency of a database-target Get request.
// The service owns closing it after the request, including authorization failure.
type GetReader interface {
	Get(*sync.WaitGroup) ([]*spb.Value, error)
	Close() error
}

// DatabaseGetFactory constructs a reader, not an RPC response or an identity.
// Authorization targets are resolved by the production Get handler, independently
// of this factory. Nil preserves the production database implementation.
type DatabaseGetFactory func(paths []*pb.Path, prefix *pb.Path) (GetReader, error)

// GetService exposes the production Get handler without constructing unrelated
// services, listeners or clients. Other gNMI methods remain Unimplemented.
type GetService struct {
	pb.UnimplementedGNMIServer
	server *Server
}

func NewGetService(config Config, databaseGet DatabaseGetFactory) (*GetService, error) {
	if config.PathzPolicy {
		return nil, fmt.Errorf("standalone Get service does not configure a pathz service")
	}
	return &GetService{server: &Server{config: &config, databaseGet: databaseGet}}, nil
}

func (s *GetService) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
	return s.server.Get(ctx, req)
}
