package gnmi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

func TestHealthzGetDoesNotStartCollection(t *testing.T) {
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
