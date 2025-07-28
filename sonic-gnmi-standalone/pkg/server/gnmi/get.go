package gnmi

import (
	"context"
	"time"

	"github.com/golang/glog"
	"github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Get retrieves the requested paths from the target device.
// This implements the gNMI Get RPC for read-only access to system information.
func (s *Server) Get(ctx context.Context, req *gnmi.GetRequest) (*gnmi.GetResponse, error) {
	glog.V(2).Infof("Received gNMI Get request for %d paths", len(req.Path))

	// Validate the request
	if len(req.Path) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no paths specified in Get request")
	}

	// Create response with current timestamp
	resp := &gnmi.GetResponse{
		Notification: []*gnmi.Notification{
			{
				Timestamp: time.Now().UnixNano(),
				Update:    []*gnmi.Update{},
			},
		},
	}

	// Process each requested path
	for i, path := range req.Path {
		glog.V(3).Infof("Processing path %d: %s", i+1, pathToString(path))

		update, err := s.processPath(path)
		if err != nil {
			glog.Errorf("Failed to process path %s: %v", pathToString(path), err)
			return nil, err
		}

		if update != nil {
			resp.Notification[0].Update = append(resp.Notification[0].Update, update)
			glog.V(3).Infof("Successfully processed path: %s", pathToString(path))
		}
	}

	glog.V(2).Infof("Get request completed successfully, returning %d updates",
		len(resp.Notification[0].Update))

	return resp, nil
}

// processPath handles individual path requests and routes them to appropriate handlers.
func (s *Server) processPath(path *gnmi.Path) (*gnmi.Update, error) {
	if path == nil {
		return nil, status.Error(codes.InvalidArgument, "nil path in request")
	}

	pathStr := pathToString(path)

	// Route to appropriate handler based on path structure
	switch {
	case isFilesystemPath(path):
		return s.handleFilesystemPath(path)
	default:
		return nil, status.Errorf(codes.NotFound, "path not found: %s", pathStr)
	}
}
