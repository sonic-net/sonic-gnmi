package gnmi

import (
	"context"
	"fmt"

	pb "github.com/openconfig/gnmi/proto/gnmi"
)

// GetService exposes the production Get handler without constructing unrelated
// services, listeners or clients. Other gNMI methods remain Unimplemented.
type GetService struct {
	pb.UnimplementedGNMIServer
	server *Server
}

func NewGetService(config Config) (*GetService, error) {
	if config.PathzPolicy {
		return nil, fmt.Errorf("standalone Get service does not configure a pathz service")
	}
	return &GetService{server: &Server{config: &config}}, nil
}

func (s *GetService) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
	return s.server.Get(ctx, req)
}
