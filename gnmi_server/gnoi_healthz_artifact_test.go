package gnmi

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type artifactTestStream struct {
	healthz.Healthz_ArtifactServer
	ctx       context.Context
	responses []*healthz.ArtifactResponse
	send      func(*healthz.ArtifactResponse) error
}

func (stream *artifactTestStream) Context() context.Context {
	if stream.ctx == nil {
		return context.Background()
	}
	return stream.ctx
}

func (stream *artifactTestStream) Send(response *healthz.ArtifactResponse) error {
	stream.responses = append(stream.responses, response)
	if stream.send != nil {
		return stream.send(response)
	}
	return nil
}

func newHealthzArtifactTestServer(t *testing.T) *HealthzServer {
	t.Helper()
	return &HealthzServer{
		Server:           &Server{config: &Config{}},
		artifactResolver: newArtifactTestResolver(t),
	}
}

func TestHealthzArtifactAuthenticatesBeforeResolving(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	stream := &artifactTestStream{ctx: context.Background()}
	previousAuthenticate := healthzArtifactAuthenticate
	healthzArtifactAuthenticate = func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
		return ctx, status.Error(codes.Unauthenticated, "unauthenticated")
	}
	t.Cleanup(func() { healthzArtifactAuthenticate = previousAuthenticate })

	err := server.Artifact(&healthz.ArtifactRequest{Id: "../invalid"}, stream)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Artifact() code = %v, want %v; err=%v", status.Code(err), codes.Unauthenticated, err)
	}
}

func TestHealthzArtifactStreamsOpaqueDLDDArtifact(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "dldd-0123456789abcdef0123456789abcdef.tar.gz"
	content := []byte("DLDD artifact content")
	writeArtifactTestFile(t, server.artifactResolver, filepath.Join(server.artifactResolver.dlddDirectory, artifactID), content)
	stream := &artifactTestStream{ctx: context.Background()}

	if err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream); err != nil {
		t.Fatalf("Artifact() failed: %v", err)
	}
	if len(stream.responses) != 3 {
		t.Fatalf("Artifact() sent %d responses, want header, data, trailer", len(stream.responses))
	}

	header := stream.responses[0].GetHeader()
	if header == nil || header.GetId() != artifactID {
		t.Fatalf("unexpected artifact header: %+v", header)
	}
	fileHeader := header.GetFile()
	if fileHeader == nil || fileHeader.GetName() != artifactID || fileHeader.GetSize() != int64(len(content)) {
		t.Fatalf("unexpected file header: %+v", fileHeader)
	}
	wantHash := sha256.Sum256(content)
	if fileHeader.GetHash() == nil || string(fileHeader.GetHash().GetHash()) != string(wantHash[:]) {
		t.Fatalf("unexpected artifact hash: %+v", fileHeader.GetHash())
	}
	if got := stream.responses[1].GetBytes(); string(got) != string(content) {
		t.Fatalf("artifact bytes = %q, want %q", got, content)
	}
	if stream.responses[2].GetTrailer() == nil {
		t.Fatal("Artifact() did not send a trailer")
	}
}

func TestHealthzArtifactStreamsEmptyContentMessage(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "dldd-11111111111111111111111111111111.tar.gz"
	writeArtifactTestFile(t, server.artifactResolver,
		filepath.Join(server.artifactResolver.dlddDirectory, artifactID), nil)
	stream := &artifactTestStream{ctx: context.Background()}

	if err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream); err != nil {
		t.Fatalf("Artifact() failed: %v", err)
	}
	if len(stream.responses) != 3 {
		t.Fatalf("Artifact() sent %d responses, want header, empty bytes, trailer", len(stream.responses))
	}
	if _, ok := stream.responses[1].Contents.(*healthz.ArtifactResponse_Bytes); !ok {
		t.Fatalf("middle response = %T, want ArtifactResponse_Bytes", stream.responses[1].Contents)
	}
}

func TestHealthzReadOnlyServerRejectsMutatingOperations(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	if healthzMutationEnabled(server.config) {
		t.Fatal("test server unexpectedly has Healthz mutations enabled")
	}

	if _, err := server.Acknowledge(context.Background(), &healthz.AcknowledgeRequest{Id: "event-id"}); status.Code(err) != codes.Unimplemented || !strings.Contains(err.Error(), "read-only mode") {
		t.Fatalf("Acknowledge() error = %v, want read-only Unimplemented", err)
	}
	if _, err := server.Check(context.Background(), &healthz.CheckRequest{}); status.Code(err) != codes.Unimplemented || !strings.Contains(err.Error(), "read-only mode") {
		t.Fatalf("Check() error = %v, want read-only Unimplemented", err)
	}

	path := &types.Path{Elem: []*types.PathElem{
		{Name: "components"},
		{Name: "component", Key: map[string]string{"name": "all"}},
		{Name: "healthz"},
		{Name: "alert-info"},
	}}
	if _, err := server.Get(context.Background(), &healthz.GetRequest{Path: path}); status.Code(err) != codes.Unimplemented || !strings.Contains(err.Error(), "read-only mode") {
		t.Fatalf("legacy Healthz.Get() error = %v, want read-only Unimplemented", err)
	}
}

func TestHealthzAcknowledgeValidatesLegacyArtifactBeforeDBus(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	server.config.EnableNativeWrite = true

	outside := filepath.Join(server.artifactResolver.hostMount, "outside.tar.gz")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	directory := server.artifactResolver.containerPath("/tmp/dump/directory")
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatal(err)
	}
	symlink := server.artifactResolver.containerPath("/tmp/dump/symlink.tar.gz")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		id   string
		code codes.Code
	}{
		{name: "empty", id: "", code: codes.InvalidArgument},
		{name: "relative", id: "artifact.tar.gz", code: codes.InvalidArgument},
		{name: "traversal", id: "/tmp/dump/../outside.tar.gz", code: codes.InvalidArgument},
		{name: "contained traversal", id: "/tmp/dump/nested/../artifact.tar.gz", code: codes.InvalidArgument},
		{name: "NUL", id: "/tmp/dump/bad\x00name", code: codes.InvalidArgument},
		{name: "directory", id: "/tmp/dump/directory", code: codes.InvalidArgument},
		{name: "symlink", id: "/tmp/dump/symlink.tar.gz", code: codes.PermissionDenied},
		{name: "missing", id: "/tmp/dump/missing.tar.gz", code: codes.NotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server.Acknowledge(context.Background(), &healthz.AcknowledgeRequest{Id: test.id})
			if status.Code(err) != test.code {
				t.Fatalf("Acknowledge(%q) code = %v, want %v; err=%v", test.id, status.Code(err), test.code, err)
			}
		})
	}
}

func TestHealthzAcknowledgeAcceptsContainedAbsoluteArtifact(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	server.config.EnableNativeWrite = true
	artifactID := "/tmp/dump/nested/legacy.tar.gz"
	artifactPath := writeArtifactTestFile(t, server.artifactResolver, artifactID, []byte("legacy"))

	patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		return &ssc.FakeClient{}, nil
	})
	defer patch.Reset()

	if _, err := server.Acknowledge(context.Background(), &healthz.AcknowledgeRequest{Id: artifactID}); err != nil {
		t.Fatalf("Acknowledge(%q) failed: %v", artifactID, err)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("fake D-Bus acknowledgement unexpectedly changed test artifact: %v", err)
	}
}

func TestHealthzMutationEnabledByEitherWriteBackend(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		want   bool
	}{
		{name: "nil", config: nil, want: false},
		{name: "read only", config: &Config{}, want: false},
		{name: "translib", config: &Config{EnableTranslibWrite: true}, want: true},
		{name: "native", config: &Config{EnableNativeWrite: true}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := healthzMutationEnabled(test.config); got != test.want {
				t.Fatalf("healthzMutationEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}
