package gnmi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

var (
	artifactColTimeout time.Duration = 5 * time.Minute
	artifactSleepTime  time.Duration = 5 * time.Second
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

func waitForArtifact(file string) (string, error) {
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return "", err
	}
	defer sc.Close()
	ctx, cancel := context.WithTimeout(context.Background(),
		artifactColTimeout)
	defer cancel()
	for {
		if result, err := sc.HealthzCheck(file); err == nil {
			log.V(2).Infof("HealthzCheck status=%q artifact=%q", result, file)
			return result, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("artifact collection timeout on file %s: %w", file, ctx.Err())
		case <-time.After(artifactSleepTime):
			// Continue loop
		}
	}
}

func getDebugData(p *types.Path) (*healthz.GetResponse, error) {
	log.Infof("getDebugData() request path: %+v\n", p)
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
	result, err := waitForArtifact(s)
	if err != nil {
		log.Errorf("waitForArtifact failed: %v", err)
		//return nil, status.Errorf(codes.Internal, "Error: %v", err)
		return nil, err
	}
	log.V(2).Infof("HealthzCheck completed with status %q", result)

	//Set Component HealthStatus based on HealthzCheck result
	var healthStatus healthz.Status
	switch result {
	case "Artifact ready":
		healthStatus = healthz.Status_STATUS_HEALTHY
	case "Artifact not ready":
		healthStatus = healthz.Status_STATUS_UNHEALTHY
	default:
		healthStatus = healthz.Status_STATUS_UNSPECIFIED
	}
	log.V(2).Infof("Healthz host artifact path: %q", s)

	// Reuse the Artifact RPC resolver for legacy debug artifacts. This keeps
	// path containment and symlink handling identical across both Healthz APIs.
	f, filePath, err := defaultArtifactResolver.open(s)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	log.V(2).Infof("Healthz container artifact path: %q", filePath)

	artifactHeader, err := buildHealthzArtifactHeader(s, f)
	if err != nil {
		return nil, err
	}

	log.Infof("Construct Get Response structure\n")
	resp := &healthz.GetResponse{}
	resp.Component = &healthz.ComponentStatus{
		Path:      p,
		Id:        s,
		Status:    healthStatus,
		Artifacts: []*healthz.ArtifactHeader{artifactHeader},
	}
	return resp, nil
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
		// The legacy Get implementation starts a new collection over D-Bus. It is
		// not a read of an existing DLDD artifact and must respect the server's
		// global write/mutation policy.
		if !writeEnabled(srv.config) {
			return nil, healthzReadOnlyError()
		}
		return getDebugData(path)
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

	// Acknowledge is the destructive half of the legacy debug-artifact API.
	// Resolve and pin the artifact through os.Root before forwarding its ID to
	// the host service. The host service repeats this validation at deletion
	// time, which closes the container/host validation gap.
	artifact, _, err := srv.getArtifactResolver().openLegacy(req.GetId())
	if err != nil {
		return nil, err
	}
	defer artifact.Close()

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
	if _, err := authenticate(srv.config, ctx, "gnoi", true); err != nil {
		return nil, err
	}
	if !writeEnabled(srv.config) {
		return nil, healthzReadOnlyError()
	}
	return nil, status.Errorf(codes.Unimplemented, "gNOI Healthz Check not implemented")
}
