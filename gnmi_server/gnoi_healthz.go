package gnmi

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func healthzComponentName(path *types.Path) (string, error) {
	if path == nil {
		return "", status.Error(codes.InvalidArgument, "Healthz requires a component path")
	}
	if origin := path.GetOrigin(); origin != "" && origin != "openconfig" {
		return "", status.Error(codes.InvalidArgument, "Healthz requires an OpenConfig component path")
	}
	elems := path.GetElem()
	if len(elems) != 2 || elems[0] == nil || elems[1] == nil ||
		elems[0].GetName() != "components" || len(elems[0].GetKey()) != 0 ||
		elems[1].GetName() != "component" || len(elems[1].GetKey()) != 1 ||
		elems[1].GetKey()[compKey] == "" {
		return "", status.Error(codes.InvalidArgument, "Healthz requires /components/component[name=X]")
	}
	return elems[1].GetKey()[compKey], nil
}

func healthzComponentPath(component string) *types.Path {
	return &types.Path{Origin: "openconfig", Elem: []*types.PathElem{
		{Name: "components"},
		{Name: "component", Key: map[string]string{compKey: component}},
	}}
}

// healthzCatalogEvent is the deliberately small D-Bus metadata contract. The
// host catalog owns IDs, acknowledgement, retention, and artifact availability.
type healthzCatalogEvent struct {
	ID           string                `json:"id"`
	Component    string                `json:"component"`
	Status       string                `json:"status"`
	Acknowledged bool                  `json:"acknowledged"`
	ObservedAt   int64                 `json:"observed_at"`
	ArtifactID   string                `json:"artifact_id,omitempty"`
	Children     []healthzCatalogEvent `json:"children,omitempty"`
}

func healthzCatalogError(err error) error {
	var dbusErr *ssc.DbusStatusError
	if errors.As(err, &dbusErr) {
		switch syscall.Errno(dbusErr.Code) {
		case syscall.ENOENT:
			return status.Error(codes.NotFound, dbusErr.Message)
		case syscall.EINVAL:
			return status.Error(codes.InvalidArgument, dbusErr.Message)
		}
	}
	return status.Errorf(codes.Internal, "Healthz host service error: %v", err)
}

func healthzEventStatus(event healthzCatalogEvent, allowSummary bool) (*healthz.ComponentStatus, error) {
	// A parent with child events but no event of its own is an unassessed
	// container, not an inferred healthy event.
	if allowSummary && event.ID == "" && event.Status == "UNSPECIFIED" && event.ObservedAt == 0 &&
		!event.Acknowledged && event.ArtifactID == "" && event.Component != "" && len(event.Children) > 0 {
		result := &healthz.ComponentStatus{Path: healthzComponentPath(event.Component), Status: healthz.Status_STATUS_UNSPECIFIED}
		for _, child := range event.Children {
			childStatus, err := healthzEventStatus(child, true)
			if err != nil {
				return nil, err
			}
			result.Subcomponents = append(result.Subcomponents, childStatus)
		}
		return result, nil
	}
	if event.ID == "" || event.Component == "" || event.ObservedAt <= 0 {
		return nil, status.Error(codes.Internal, "Healthz host service returned an incomplete event")
	}
	var assessment healthz.Status
	switch event.Status {
	case "HEALTHY":
		assessment = healthz.Status_STATUS_HEALTHY
	case "UNHEALTHY":
		assessment = healthz.Status_STATUS_UNHEALTHY
	default:
		return nil, status.Errorf(codes.Internal, "Healthz host service returned unknown status %q", event.Status)
	}
	result := &healthz.ComponentStatus{
		Path:         healthzComponentPath(event.Component),
		Id:           event.ID,
		Status:       assessment,
		Acknowledged: event.Acknowledged,
		Created:      timestamppb.New(time.Unix(event.ObservedAt, 0)),
	}
	if event.ArtifactID != "" {
		result.Artifacts = []*healthz.ArtifactHeader{{
			Id: event.ArtifactID,
			ArtifactType: &healthz.ArtifactHeader_File{File: &healthz.FileArtifactType{
				Name:     filepath.Base(event.ArtifactID),
				Mimetype: "application/gzip",
			}},
		}}
	}
	for _, child := range event.Children {
		status, err := healthzEventStatus(child, true)
		if err != nil {
			return nil, err
		}
		result.Subcomponents = append(result.Subcomponents, status)
	}
	return result, nil
}

func healthzCatalogRequest(request interface{}) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to encode Healthz request: %v", err)
	}
	return string(encoded), nil
}

func healthzCatalogClient() (ssc.Service, error) {
	client, err := ssc.NewDbusClientProvider()
	if err != nil {
		return nil, healthzCatalogError(err)
	}
	return client, nil
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
		Status:    healthz.Status_STATUS_UNSPECIFIED,
		Artifacts: []*healthz.ArtifactHeader{artifactHeader},
	}, nil
}

// Get implements the corresponding RPC.
func (srv *HealthzServer) Get(ctx context.Context, req *healthz.GetRequest) (*healthz.GetResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return nil, err
	}
	if isDebugData(req.GetPath()) {
		return nil, status.Error(codes.NotFound, "no collected Healthz status is available for this legacy path")
	}
	component, err := healthzComponentName(req.GetPath())
	if err != nil {
		return nil, err
	}
	request, err := healthzCatalogRequest(map[string]string{"component": component})
	if err != nil {
		return nil, err
	}
	client, err := healthzCatalogClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	response, err := client.HealthzGet(request)
	if err != nil {
		return nil, healthzCatalogError(err)
	}
	var event healthzCatalogEvent
	if err := json.Unmarshal([]byte(response), &event); err != nil {
		return nil, status.Errorf(codes.Internal, "invalid Healthz Get response: %v", err)
	}
	if event.Component != component {
		return nil, status.Error(codes.Internal, "Healthz host service returned a different component")
	}
	result, err := healthzEventStatus(event, true)
	if err != nil {
		return nil, err
	}
	return &healthz.GetResponse{Component: result}, nil
}

// Acknowledge implements the corresponding RPC.
func (srv *HealthzServer) Acknowledge(ctx context.Context, req *healthz.AcknowledgeRequest) (*healthz.AcknowledgeResponse, error) {
	_, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	if !writeEnabled(srv.config) {
		return nil, healthzReadOnlyError()
	}
	if req == nil || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Healthz.Acknowledge requires an event ID")
	}
	component, err := healthzComponentName(req.GetPath())
	if err != nil {
		return nil, err
	}
	request, err := healthzCatalogRequest(map[string]string{"component": component, "id": req.GetId()})
	if err != nil {
		return nil, err
	}
	client, err := healthzCatalogClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	response, err := client.HealthzAcknowledge(request)
	if err != nil {
		return nil, healthzCatalogError(err)
	}
	var event healthzCatalogEvent
	if err := json.Unmarshal([]byte(response), &event); err != nil {
		return nil, status.Errorf(codes.Internal, "invalid Healthz Acknowledge response: %v", err)
	}
	if event.Component != component || event.ID != req.GetId() || !event.Acknowledged {
		return nil, status.Error(codes.Internal, "Healthz host service returned an inconsistent acknowledgement")
	}
	result, err := healthzEventStatus(event, false)
	if err != nil {
		return nil, err
	}
	return &healthz.AcknowledgeResponse{Status: result}, nil
}

func (srv *HealthzServer) List(ctx context.Context, req *healthz.ListRequest) (*healthz.ListResponse, error) {
	if _, err := authenticate(srv.config, ctx, "gnoi", false); err != nil {
		return nil, err
	}
	component, err := healthzComponentName(req.GetPath())
	if err != nil {
		return nil, err
	}
	request, err := healthzCatalogRequest(struct {
		Component           string `json:"component"`
		IncludeAcknowledged bool   `json:"include_acknowledged"`
	}{component, req.GetIncludeAcknowledged()})
	if err != nil {
		return nil, err
	}
	client, err := healthzCatalogClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	response, err := client.HealthzList(request)
	if err != nil {
		return nil, healthzCatalogError(err)
	}
	var events []healthzCatalogEvent
	if err := json.Unmarshal([]byte(response), &events); err != nil {
		return nil, status.Errorf(codes.Internal, "invalid Healthz List response: %v", err)
	}
	result := &healthz.ListResponse{}
	for _, event := range events {
		if event.Component != component {
			return nil, status.Error(codes.Internal, "Healthz host service returned a different component")
		}
		if event.Acknowledged && !req.GetIncludeAcknowledged() {
			continue
		}
		status, err := healthzEventStatus(event, false)
		if err != nil {
			return nil, err
		}
		result.Statuses = append(result.Statuses, status)
	}
	return result, nil
}

func (srv *HealthzServer) Check(ctx context.Context, req *healthz.CheckRequest) (*healthz.CheckResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	if !writeEnabled(srv.config) {
		return nil, healthzReadOnlyError()
	}
	if req == nil || req.GetPath() == nil {
		return nil, status.Error(codes.InvalidArgument, "Healthz.Check requires a component path")
	}
	legacyDiagnostic := isDebugData(req.GetPath())
	if !legacyDiagnostic {
		if _, err := healthzComponentName(req.GetPath()); err != nil {
			return nil, err
		}
	}
	if req.GetEventId() != "" {
		return nil, status.Error(codes.Unimplemented, "event-specific Healthz checks are not implemented")
	}
	if legacyDiagnostic {
		component, err := srv.collectDebugData(ctx, req.GetPath())
		if err != nil {
			return nil, err
		}
		return &healthz.CheckResponse{Status: component}, nil
	}
	return nil, status.Error(codes.Unimplemented, "Healthz.Check has no procedure for this component")
}
