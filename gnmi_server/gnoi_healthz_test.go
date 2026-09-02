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

func TestHealthzGetDebugData(t *testing.T) {
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
	response, err := server.Get(context.Background(), &healthz.GetRequest{Path: path})
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if response.GetComponent().GetId() != artifactID || response.GetComponent().GetPath() != path || len(response.GetComponent().GetArtifacts()) != 1 {
		t.Fatalf("unexpected getDebugData() response: %+v", response)
	}
	file := response.GetComponent().GetArtifacts()[0].GetFile()
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

func TestHealthzGetDebugDataReportsCollectionFailure(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		return &ssc.FakeClientWithError{}, nil
	})
	defer patch.Reset()

	response, err := server.getDebugData(context.Background(), healthzDebugPath())
	if response != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "Host service error") {
		t.Fatalf("getDebugData() = (%+v, %v), want an immediate collection failure", response, err)
	}
}
