package gnmi

import (
	"context"

	"github.com/golang/glog"
	"github.com/openconfig/gnmi/proto/gnmi"
)

// Capabilities returns the capabilities of this gNMI server.
// This includes supported data models, encodings, and gNMI version.
func (s *Server) Capabilities(ctx context.Context, req *gnmi.CapabilityRequest) (*gnmi.CapabilityResponse, error) {
	glog.V(2).Info("Received gNMI Capabilities request")

	return &gnmi.CapabilityResponse{
		// No YANG models are registered as the server provides custom paths
		// without formal schema definitions. Future work should add proper
		// YANG models for filesystem monitoring capabilities.
		SupportedModels: []*gnmi.ModelData{},
		SupportedEncodings: []gnmi.Encoding{
			gnmi.Encoding_JSON,
			gnmi.Encoding_JSON_IETF,
		},
		GNMIVersion: "0.7.0",
	}, nil
}

// getSupportedPaths returns a list of all supported gNMI paths.
func getSupportedPaths() []string {
	return []string{
		"/sonic/system/filesystem[path=*]/disk-space",
	}
}
