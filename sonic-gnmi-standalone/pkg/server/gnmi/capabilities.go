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
		SupportedModels: []*gnmi.ModelData{
			{
				Name:         "sonic-system",
				Organization: "SONiC",
				Version:      "1.0.0",
			},
		},
		SupportedEncodings: []gnmi.Encoding{
			gnmi.Encoding_JSON,
			gnmi.Encoding_JSON_IETF,
		},
		GNMIVersion: "0.7.0",
	}, nil
}

// getSupportedPaths returns a list of all supported gNMI paths.
// This is used for documentation and validation purposes.
func getSupportedPaths() []string {
	return []string{
		"/sonic/system/filesystem[path=*]/disk-space",
		"/sonic/system/filesystem[path=*]/disk-space/total-mb",
		"/sonic/system/filesystem[path=*]/disk-space/available-mb",
	}
}
