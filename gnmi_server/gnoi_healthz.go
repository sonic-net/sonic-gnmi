package gnmi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	log "github.com/golang/glog"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const healthzDefaultHostRoot = "/mnt/host"

var healthzHostRoot = healthzDefaultHostRoot

func healthzArtifactPath(path string) string {
	return filepath.Join(healthzHostRoot, path)
}

const (
	compKey          string = "name"
	ddComponentKey   string = "component"
	ddComponentAll   string = "all"
	ddLogLvlKey      string = "level"
	ddLogLvlAlert    string = "alert"
	ddLogLvlCritical string = "critical"
	ddLogLvlAll      string = "all"
	ddLogLvlSuf      string = "-info"
)

func healthzReadOnlyError() error {
	return status.Error(codes.Unimplemented, "gNOI Healthz mutation is disabled in read-only mode")
}

func isDebugData(p *types.Path) bool {
	if p == nil {
		return false
	}
	elems := p.GetElem()
	log.V(5).Infof("Healthz path elements: %+v", elems)
	if len(elems) != 4 {
		return false
	}
	if elems[0].GetName() != "components" || len(elems[0].GetKey()) > 0 {
		return false
	}
	if elems[1].GetName() != "component" || len(elems[1].GetKey()) != 1 {
		return false
	}
	if _, ok := elems[1].GetKey()["name"]; !ok {
		return false
	}
	if elems[2].GetName() != "healthz" || len(elems[2].GetKey()) > 0 {
		return false
	}
	if (elems[3].GetName() != ddLogLvlAlert+ddLogLvlSuf && elems[3].GetName() != ddLogLvlCritical+ddLogLvlSuf && elems[3].GetName() != ddLogLvlAll+ddLogLvlSuf) || len(elems[3].GetKey()) > 0 {
		return false
	}
	return true
}

func (srv *HealthzServer) collectDebugData(ctx context.Context, p *types.Path) (*healthz.ComponentStatus, error) {
	log.Infof("collectDebugData() request path: %+v\n", p)
	c := ddComponentAll
	ll := ddLogLvlAlert
	elems := p.GetElem()
	if len(elems) == 4 {
		c, _ = elems[1].GetKey()["name"]
		ll = strings.TrimSuffix(elems[3].GetName(), ddLogLvlSuf)
	}
	req := map[string]string{
		ddComponentKey: c,
		ddLogLvlKey:    ll,
	}
	b, err := json.Marshal(req)
	if err != nil {
		log.Errorf("getDebugData(): JSON marshal failed: %v", err)
		return nil, err
	}
	sc, err := ssc.NewDbusClient()
	if err != nil {
		log.Errorf("NewDbusClient error: %v\n", err)
		return nil, err
	}
	defer sc.Close()
	s, err := sc.HealthzCollect(string(b))
	if err != nil {
		log.Errorf("HealthzCollect() Dbus failed: %v", err)
		return nil, status.Errorf(codes.Internal, "Host service error: %v", err)
	}
	// Wait for artifact file to be ready.
	result, err := waitForArtifact(ctx, sc, s)
	if err != nil {
		log.Errorf("waitForArtifact failed: %v", err)
		return nil, err
	}
	log.V(2).Infof("HealthzCheck completed with status %q", result)

	log.V(2).Infof("Healthz host artifact path: %q", s)

	// Apply the shared artifact containment checks.
	f, filePath, err := srv.getArtifactResolver().openLegacy(s)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	log.V(2).Infof("Healthz container artifact path: %q", filePath)

	artifactHeader, err := buildHealthzArtifactHeader(s, f)
	if err != nil {
		return nil, err
	}

	return &healthz.ComponentStatus{
		Path:      p,
		Id:        s,
		Status:    healthz.Status_STATUS_HEALTHY,
		Artifacts: []*healthz.ArtifactHeader{artifactHeader},
	}, nil
}

// Get implements the corresponding RPC.
func (srv *HealthzServer) Get(ctx context.Context, req *healthz.GetRequest) (*healthz.GetResponse, error) {
	log.V(1).Infof("Get RPC request Path: %v\n", req.GetPath())
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		log.Errorf("Healthz.Get authentication failed: %v", err)
		return nil, err
	}
	if req == nil || req.GetPath() == nil {
		log.Warning("Healthz.Get received request with nil path")
		return nil, status.Errorf(codes.InvalidArgument, "Healthz.Get received nil request or path")
	}
	path := req.GetPath()
	log.V(1).Infof("Healthz.Get request path: %+v", path.GetElem())
	if isDebugData(path) {
		return nil, status.Error(codes.NotFound, "no collected Healthz status is available for this path")
	}
	log.Warning("Healthz.Get received unsupported component path")
	return nil, status.Errorf(codes.Unimplemented, "Healthz.Get is unimplemented for component: [%s].", path.GetElem())
}

// Acknowledge implements the corresponding RPC.
func (srv *HealthzServer) Acknowledge(ctx context.Context, req *healthz.AcknowledgeRequest) (*healthz.AcknowledgeResponse, error) {
	log.V(1).Infof("Acknowledge RPC Get request ID: %+v", req.GetId())
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		log.Errorf("Healthz.Acknowledge authentication failed: %v", err)
		return nil, err
	}
	if req == nil || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Healthz.Acknowledge requires an event ID")
	}
	if !writeEnabled(srv.config) {
		return nil, healthzReadOnlyError()
	}

	sc, err := ssc.NewDbusClient()
	if err != nil {
		log.Errorf("NewDbusClient error: %v\n", err)
		return nil, err
	}
	defer sc.Close()
	_, err = sc.HealthzAck(req.GetId())
	if err != nil {
		log.Errorf("HealthzAck() Dbus failed: %v", err)
		return nil, status.Errorf(codes.Internal, "Host service error: %v", err)
	}

	return &healthz.AcknowledgeResponse{}, nil
}

func (srv *HealthzServer) List(ctx context.Context, req *healthz.ListRequest) (*healthz.ListResponse, error) {
	if _, err := authenticate(srv.config, ctx, "gnoi", false); err != nil {
		return nil, err
	}
	return nil, status.Errorf(codes.Unimplemented, "gNOI Healthz List not implemented")
}

func (srv *HealthzServer) Check(ctx context.Context, req *healthz.CheckRequest) (*healthz.CheckResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetPath() == nil {
		return nil, status.Error(codes.InvalidArgument, "Healthz.Check requires a component path")
	}
	if !writeEnabled(srv.config) {
		return nil, healthzReadOnlyError()
	}
	if req.GetEventId() != "" {
		return nil, status.Error(codes.Unimplemented, "event-specific Healthz checks are not implemented")
	}
	if !isDebugData(req.GetPath()) {
		return nil, status.Errorf(
			codes.Unimplemented,
			"Healthz.Check is unimplemented for component: [%s].",
			req.GetPath().GetElem(),
		)
	}
	component, err := srv.collectDebugData(ctx, req.GetPath())
	if err != nil {
		return nil, err
	}
	return &healthz.CheckResponse{Status: component}, nil
}
