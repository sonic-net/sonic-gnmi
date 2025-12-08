package gnmi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	compKey           string        = "name"
	ddComponentKey    string        = "component"
	ddComponentAll    string        = "all"
	ddLogLvlKey       string        = "level"
	ddLogLvlAlert     string        = "alert"
	ddLogLvlCritical  string        = "critical"
	ddLogLvlAll       string        = "all"
	ddLogLvlSuf       string        = "-info"
	ddFileSegSize     int           = 4096
	artifactSleepTime time.Duration = 5 * time.Second
)

var (
	artifactColTimeout time.Duration = 5 * time.Minute
)

func isDebugData(p *types.Path) bool {
	if p == nil {
		return false
	}
	elems := p.GetElem()
	for i, e := range elems {
		fmt.Printf("elem[%d]: name=%s, keys=%v\n", i, e.GetName(), e.GetKey())
	}
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
			fmt.Printf("HealthzCheck Status:%s and Artifact file=%s\n", result, file)
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
	fmt.Printf("waitForArtifact result from HealthzCheck: %s\n", result)

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
	fmt.Printf("Artifact file path received on Host: %s\n", s)

	// Validate path is within allowed directory
	allowedDir := "/tmp/dump"
	cleanPath := filepath.Clean(s)
	if !strings.HasPrefix(cleanPath, allowedDir) {
		return nil, status.Errorf(codes.InvalidArgument, "Invalid artifact path")
	}
	file_path := filepath.Join("/mnt/host", cleanPath)
	fmt.Printf("Artifact filepath inside gnmi container: %s\n", file_path)

	// Stream-hash instead of loading entire file
	f, err := os.Open(file_path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Error: [%v]", err)
	}
	defer f.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, f) // Streams through hasher, constant memory
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Error hashing: [%v]", err)
	}
	hashSum := hasher.Sum(nil)

	log.Infof("Construct Get Response structure\n")
	resp := &healthz.GetResponse{}
	resp.Component = &healthz.ComponentStatus{
		Path:   p,
		Id:     s,
		Status: healthStatus,
		Artifacts: []*healthz.ArtifactHeader{
			&healthz.ArtifactHeader{
				Id: s,
				ArtifactType: &healthz.ArtifactHeader_File{
					File: &healthz.FileArtifactType{
						Name: s,
						Size: size,
						Hash: &types.HashType{
							Method: types.HashType_SHA256,
							Hash:   hashSum[:],
						},
					},
				},
			},
		},
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
		return getDebugData(path)
	}
	log.Warning("Healthz.Get received unsupported component path")
	return nil, status.Errorf(codes.Unimplemented, "Healthz.Get is unimplemented for component: [%s].", path.GetElem())
}

func (srv *HealthzServer) List(ctx context.Context, req *healthz.ListRequest) (*healthz.ListResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "gNOI Healthz List not implemented")
}

func (srv *HealthzServer) Check(ctx context.Context, req *healthz.CheckRequest) (*healthz.CheckResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "gNOI Healthz Check not implemented")
}
