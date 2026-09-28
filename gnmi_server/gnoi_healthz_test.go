package gnmi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func healthzTestComponentPath() *types.Path {
	return healthzComponentPath("chassis")
}

type catalogTestService struct {
	*ssc.FakeClient
	events []healthzCatalogEvent
	acks   int
}

func (service *catalogTestService) HealthzGet(raw string) (string, error) {
	var request struct {
		Component string `json:"component"`
	}
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return "", err
	}
	for index := len(service.events) - 1; index >= 0; index-- {
		if service.events[index].Component == request.Component {
			encoded, _ := json.Marshal(service.events[index])
			return string(encoded), nil
		}
	}
	return "", &ssc.DbusStatusError{Code: int32(syscall.ENOENT), Message: "event not found"}
}

func (service *catalogTestService) HealthzList(raw string) (string, error) {
	var request struct {
		Component           string `json:"component"`
		IncludeAcknowledged bool   `json:"include_acknowledged"`
	}
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return "", err
	}
	events := make([]healthzCatalogEvent, 0, len(service.events))
	for _, event := range service.events {
		if event.Component == request.Component && (request.IncludeAcknowledged || !event.Acknowledged) {
			events = append(events, event)
		}
	}
	encoded, _ := json.Marshal(events)
	return string(encoded), nil
}

func (service *catalogTestService) HealthzAcknowledge(raw string) (string, error) {
	var request struct {
		Component string `json:"component"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return "", err
	}
	service.acks++
	for index := range service.events {
		if service.events[index].Component == request.Component && service.events[index].ID == request.ID {
			service.events[index].Acknowledged = true
			encoded, _ := json.Marshal(service.events[index])
			return string(encoded), nil
		}
	}
	return "", &ssc.DbusStatusError{Code: int32(syscall.ENOENT), Message: "event not found"}
}

func useCatalogTestService(t *testing.T, service ssc.Service) {
	t.Helper()
	previous := ssc.NewDbusClientProvider
	ssc.NewDbusClientProvider = func() (ssc.Service, error) { return service, nil }
	t.Cleanup(func() { ssc.NewDbusClientProvider = previous })
}

func healthzDebugPath() *types.Path {
	return &types.Path{Elem: []*types.PathElem{
		{Name: "components"},
		{Name: "component", Key: map[string]string{"name": "chassis"}},
		{Name: "healthz"},
		{Name: "alert-info"},
	}}
}

func TestHealthzCheckCollectsDebugData(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	server.config.EnableNativeWrite = true
	artifactID := "/tmp/dump/healthz.tar.gz"
	content := []byte("healthz diagnostics")
	writeArtifactTestFile(t, server.artifactResolver, artifactID, content)
	fakeClient := &ssc.FakeClient{CollectResponse: artifactID}

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		return fakeClient, nil
	})
	patches.ApplyFunc(waitForArtifact, func(_ context.Context, checker healthzArtifactChecker, artifact string) (string, error) {
		if checker != fakeClient || artifact != artifactID {
			t.Fatalf("waitForArtifact() received checker %T and artifact %q", checker, artifact)
		}
		return healthzArtifactReady, nil
	})

	path := healthzDebugPath()
	response, err := server.Check(context.Background(), &healthz.CheckRequest{Path: path})
	if err != nil {
		t.Fatalf("Check() failed: %v", err)
	}
	if response.GetStatus().GetId() != artifactID || response.GetStatus().GetPath() != path || len(response.GetStatus().GetArtifacts()) != 1 {
		t.Fatalf("unexpected Check() response: %+v", response)
	}
	if response.GetStatus().GetStatus() != healthz.Status_STATUS_UNSPECIFIED || response.GetStatus().GetArtifacts()[0].GetFile().GetName() != filepath.Base(artifactID) {
		t.Fatalf("legacy diagnostic collection asserted an assessment or wrong filename: %+v", response.GetStatus())
	}
	file := response.GetStatus().GetArtifacts()[0].GetFile()
	wantHash := sha256.Sum256(content)
	if file.GetSize() != int64(len(content)) || !bytes.Equal(file.GetHash().GetHash(), wantHash[:]) {
		t.Fatalf("unexpected artifact metadata: %+v", file)
	}
}

func TestHealthzPublicRPCsRequireAuthentication(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	patch := gomonkey.ApplyFuncReturn(authenticate, nil, status.Error(codes.Unauthenticated, "unauthenticated"))
	defer patch.Reset()

	for name, call := range map[string]func() error{
		"Get": func() error {
			_, err := server.Get(context.Background(), &healthz.GetRequest{Path: healthzDebugPath()})
			return err
		},
		"List": func() error {
			_, err := server.List(context.Background(), &healthz.ListRequest{Path: healthzTestComponentPath()})
			return err
		},
		"Check": func() error {
			_, err := server.Check(context.Background(), &healthz.CheckRequest{Path: healthzDebugPath()})
			return err
		},
		"Acknowledge": func() error {
			_, err := server.Acknowledge(context.Background(), &healthz.AcknowledgeRequest{Id: "/tmp/dump/artifact"})
			return err
		},
	} {
		if err := call(); status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s() code = %v, want %v; err=%v", name, status.Code(err), codes.Unauthenticated, err)
		}
	}
}

func TestHealthzCollectDebugDataReportsCollectionFailure(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		return &ssc.FakeClientWithError{}, nil
	})
	defer patch.Reset()

	response, err := server.collectDebugData(context.Background(), healthzDebugPath())
	if response != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "Host service error") {
		t.Fatalf("collectDebugData() = (%+v, %v), want an immediate collection failure", response, err)
	}
}

func TestHealthzLegacyGetDoesNotStartCollection(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	dbusCalls := 0
	patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		dbusCalls++
		return &ssc.FakeClient{}, nil
	})
	defer patch.Reset()

	_, err := server.Get(
		context.Background(), &healthz.GetRequest{Path: healthzDebugPath()},
	)
	if status.Code(err) != codes.NotFound || dbusCalls != 0 {
		t.Fatalf("Get() = %v after %d D-Bus calls, want NotFound without collection", err, dbusCalls)
	}
}

func TestHealthzCheckRejectsUnsupportedEventID(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	server.config.EnableNativeWrite = true

	_, err := server.Check(context.Background(), &healthz.CheckRequest{
		Path: healthzDebugPath(), EventId: "event-123",
	})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("Check(event_id) code = %v, want Unimplemented", status.Code(err))
	}
}

func TestHealthzCatalogLifecycle(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	server.config.EnableNativeWrite = true
	artifactID := "healthz-0123456789abcdef0123456789abcdef.tar.gz"
	filePath := server.artifactResolver.containerPath(filepath.Join(server.artifactResolver.healthzDirectory, artifactID))
	writeArtifactTestFile(t, server.artifactResolver, filepath.Join(server.artifactResolver.healthzDirectory, artifactID), []byte("diagnostics"))
	service := &catalogTestService{FakeClient: &ssc.FakeClient{}, events: []healthzCatalogEvent{{
		ID: artifactID, Component: "chassis", Status: "UNHEALTHY", ObservedAt: 1789056817,
		ArtifactID: artifactID,
		Children:   []healthzCatalogEvent{{ID: "child-event", Component: "PSU0", Status: "HEALTHY", ObservedAt: 1789056816}},
	}}}
	useCatalogTestService(t, service)
	path := healthzTestComponentPath()
	ctx := context.Background()

	got, err := server.Get(ctx, &healthz.GetRequest{Path: path})
	if err != nil || got.GetComponent() == nil || len(got.GetComponent().GetArtifacts()) != 1 ||
		len(got.GetComponent().GetSubcomponents()) != 1 {
		t.Fatalf("Get() = (%+v, %v), want one event with archive and child", got, err)
	}
	if got.GetComponent().GetId() != artifactID ||
		got.GetComponent().GetStatus() != healthz.Status_STATUS_UNHEALTHY ||
		got.GetComponent().GetArtifacts()[0].GetId() != artifactID ||
		got.GetComponent().GetArtifacts()[0].GetFile().GetMimetype() != "application/gzip" ||
		got.GetComponent().GetPath().GetOrigin() != "openconfig" ||
		got.GetComponent().GetSubcomponents()[0].GetPath().GetElem()[1].GetKey()["name"] != "PSU0" ||
		got.GetComponent().GetCreated().GetSeconds() != 1789056817 {
		t.Fatalf("Get() = (%+v, %v), want stored unhealthy event", got, err)
	}
	listed, err := server.List(ctx, &healthz.ListRequest{Path: path})
	if err != nil || len(listed.GetStatuses()) != 1 {
		t.Fatalf("List() = (%+v, %v), want one event", listed, err)
	}
	stream := &artifactTestStream{}
	if err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream); err != nil ||
		len(stream.responses) == 0 || stream.responses[0].GetHeader().GetId() != artifactID {
		t.Fatalf("Artifact() failed for the event's archive: %v", err)
	}

	for repeat := 0; repeat < 2; repeat++ {
		ack, err := server.Acknowledge(ctx, &healthz.AcknowledgeRequest{Path: path, Id: artifactID})
		if err != nil || !ack.GetStatus().GetAcknowledged() || ack.GetStatus().GetId() != artifactID {
			t.Fatalf("Acknowledge() repeat %d = (%+v, %v)", repeat, ack, err)
		}
	}
	if service.acks != 2 {
		t.Fatalf("host acknowledgement calls = %d, want two idempotent calls", service.acks)
	}
	listed, err = server.List(ctx, &healthz.ListRequest{Path: path})
	if err != nil || len(listed.GetStatuses()) != 0 {
		t.Fatalf("default List() = (%+v, %v), want acknowledged event hidden", listed, err)
	}
	listed, err = server.List(ctx, &healthz.ListRequest{Path: path, IncludeAcknowledged: true})
	if err != nil || len(listed.GetStatuses()) != 1 || !listed.GetStatuses()[0].GetAcknowledged() {
		t.Fatalf("include acknowledged List() = (%+v, %v)", listed, err)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("Acknowledge deleted artifact: %v", err)
	}

	service.events = append(service.events, healthzCatalogEvent{
		ID: "event-recovery", Component: "chassis", Status: "HEALTHY", ObservedAt: 1789056818,
	})
	got, err = server.Get(ctx, &healthz.GetRequest{Path: path})
	if err != nil || got.GetComponent().GetId() != "event-recovery" ||
		got.GetComponent().GetStatus() != healthz.Status_STATUS_HEALTHY ||
		len(got.GetComponent().GetArtifacts()) != 0 {
		t.Fatalf("Get(recovery) = (%+v, %v), want distinct event without repeated artifact", got, err)
	}
	server.config.EnableNativeWrite = false
	if _, err := server.Get(ctx, &healthz.GetRequest{Path: path}); err != nil {
		t.Fatalf("read-only Get() failed: %v", err)
	}
	if _, err := server.List(ctx, &healthz.ListRequest{Path: path}); err != nil {
		t.Fatalf("read-only List() failed: %v", err)
	}
}

func TestHealthzParentGetContainsKnownChildWithoutParentAssessment(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	service := &catalogTestService{FakeClient: &ssc.FakeClient{}, events: []healthzCatalogEvent{{
		Component: "chassis", Status: "UNSPECIFIED",
		Children: []healthzCatalogEvent{{
			ID: "child-fault", Component: "PSU0", Status: "UNHEALTHY", ObservedAt: 1789056817,
		}},
	}}}
	useCatalogTestService(t, service)
	response, err := server.Get(context.Background(), &healthz.GetRequest{Path: healthzTestComponentPath()})
	if err != nil {
		t.Fatal(err)
	}
	parent := response.GetComponent()
	if parent.GetStatus() != healthz.Status_STATUS_UNSPECIFIED || parent.GetId() != "" ||
		parent.GetCreated() != nil || len(parent.GetArtifacts()) != 0 ||
		len(parent.GetSubcomponents()) != 1 ||
		parent.GetSubcomponents()[0].GetId() != "child-fault" ||
		parent.GetSubcomponents()[0].GetStatus() != healthz.Status_STATUS_UNHEALTHY {
		t.Fatalf("parent Get fabricated an assessment or dropped child: %+v", parent)
	}
}

func TestHealthzComponentPathAndMissingEvent(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	service := &catalogTestService{FakeClient: &ssc.FakeClient{}}
	useCatalogTestService(t, service)
	for _, path := range []*types.Path{
		nil,
		{Elem: []*types.PathElem{{Name: "components"}, {Name: "component", Key: map[string]string{"id": "chassis"}}}},
		{Origin: "vendor", Elem: healthzTestComponentPath().GetElem()},
	} {
		if _, err := server.Get(context.Background(), &healthz.GetRequest{Path: path}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("Get(%v) = %v, want InvalidArgument", path, err)
		}
	}
	if _, err := server.Get(context.Background(), &healthz.GetRequest{Path: healthzTestComponentPath()}); status.Code(err) != codes.NotFound {
		t.Fatalf("Get(missing) = %v, want NotFound", err)
	}
	server.config.EnableNativeWrite = true
	if _, err := server.Acknowledge(context.Background(), &healthz.AcknowledgeRequest{Path: healthzTestComponentPath(), Id: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("Acknowledge(missing) = %v, want NotFound", err)
	}
	for _, eventID := range []string{"", "existing"} {
		_, err := server.Check(context.Background(), &healthz.CheckRequest{Path: healthzTestComponentPath(), EventId: eventID})
		if status.Code(err) != codes.Unimplemented {
			t.Errorf("Check(event_id=%q) = %v, want Unimplemented", eventID, err)
		}
	}
	if _, err := server.Check(context.Background(), &healthz.CheckRequest{Path: &types.Path{}, EventId: "existing"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Check(malformed path, event_id) = %v, want InvalidArgument", err)
	}
	if _, err := server.List(context.Background(), &healthz.ListRequest{Path: healthzTestComponentPath()}); err != nil {
		t.Fatalf("List(empty) failed: %v", err)
	}
}
