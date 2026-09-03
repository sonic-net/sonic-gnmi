package gnmi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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

type legacyCollectionService struct {
	ssc.Service
	artifactID string
}

func (service *legacyCollectionService) HealthzCollect(string) (string, error) {
	return service.artifactID, nil
}

func (*legacyCollectionService) Close() error { return nil }

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
	server.config.UserAuth = AuthTypes{"jwt": true}

	err := server.Artifact(&healthz.ArtifactRequest{Id: "../invalid"}, &artifactTestStream{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Artifact() code = %v, want %v; err=%v", status.Code(err), codes.Unauthenticated, err)
	}
}

func TestHealthzArtifactRejectsNilRequest(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	if err := server.Artifact(nil, &artifactTestStream{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Artifact(nil) code = %v, want %v; err=%v", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestHealthzArtifactStreamsCompletedArchive(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "dldd-0123456789abcdef0123456789abcdef.tar.gz"
	content := bytes.Repeat([]byte("x"), 2*ddFileSegSize+1)
	writeArtifactTestFile(t, server.artifactResolver,
		filepath.Join(server.artifactResolver.dlddDirectory, artifactID), content)
	stream := &artifactTestStream{}

	if err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream); err != nil {
		t.Fatalf("Artifact() failed: %v", err)
	}
	if len(stream.responses) != 5 {
		t.Fatalf("Artifact() sent %d responses, want header, three data frames, trailer", len(stream.responses))
	}
	header := stream.responses[0].GetHeader()
	file := header.GetFile()
	wantHash := sha256.Sum256(content)
	if header.GetId() != artifactID || file.GetName() != artifactID || file.GetSize() != int64(len(content)) ||
		file.GetHash().GetMethod() != types.HashType_SHA256 || !bytes.Equal(file.GetHash().GetHash(), wantHash[:]) {
		t.Fatalf("unexpected artifact header: %+v", header)
	}

	streamed := make([]byte, 0, len(content))
	for _, response := range stream.responses[1 : len(stream.responses)-1] {
		if _, ok := response.Contents.(*healthz.ArtifactResponse_Bytes); !ok || len(response.GetBytes()) > ddFileSegSize {
			t.Fatalf("invalid artifact data frame: %+v", response)
		}
		streamed = append(streamed, response.GetBytes()...)
	}
	if !bytes.Equal(streamed, content) {
		t.Fatal("Artifact() did not reconstruct the original archive")
	}
	if stream.responses[len(stream.responses)-1].GetTrailer() == nil {
		t.Fatal("Artifact() did not terminate with a trailer")
	}
}

func TestWaitForDLDDArtifactAllowsAsynchronousCollection(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	artifactID := "dldd-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.tar.gz"
	written := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		path := resolver.containerPath(filepath.Join(resolver.dlddDirectory, artifactID))
		written <- os.WriteFile(path, []byte("ready"), 0644)
	}()

	file, err := waitForDLDDArtifact(
		context.Background(), resolver, artifactID, time.Second, 5*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("waitForDLDDArtifact() failed: %v", err)
	}
	file.Close()
	if err := <-written; err != nil {
		t.Fatalf("failed to create asynchronous artifact: %v", err)
	}
}

func TestWaitForDLDDArtifactHonorsCancellation(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForDLDDArtifact(
		ctx, resolver, "dldd-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.tar.gz",
		time.Second, 5*time.Millisecond,
	)
	if status.Code(err) != codes.Canceled {
		t.Fatalf("waitForDLDDArtifact() code = %v, want %v; err=%v",
			status.Code(err), codes.Canceled, err)
	}
}

func TestHealthzArtifactPropagatesStreamFailure(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "dldd-33333333333333333333333333333333.tar.gz"
	writeArtifactTestFile(t, server.artifactResolver,
		filepath.Join(server.artifactResolver.dlddDirectory, artifactID), []byte("artifact"))
	sendErr := errors.New("send failed")
	stream := &artifactTestStream{send: func(response *healthz.ArtifactResponse) error {
		if response.GetBytes() != nil {
			return sendErr
		}
		return nil
	}}

	err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream)
	if !errors.Is(err, sendErr) || len(stream.responses) != 2 {
		t.Fatalf("Artifact() error = %v after %d sends, want data send failure", err, len(stream.responses))
	}
}

func TestLegacyHealthzCollectionRejectsOpaqueDLDDArtifactID(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "dldd-22222222222222222222222222222222.tar.gz"
	writeArtifactTestFile(t, server.artifactResolver,
		filepath.Join(server.artifactResolver.dlddDirectory, artifactID), []byte("DLDD artifact"))

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		return &legacyCollectionService{artifactID: artifactID}, nil
	})
	patches.ApplyFunc(waitForArtifact, func(context.Context, healthzArtifactChecker, string) (string, error) {
		return healthzArtifactReady, nil
	})

	if _, err := server.getDebugData(context.Background(), &types.Path{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("getDebugData() code = %v, want InvalidArgument; err=%v", status.Code(err), err)
	}
}

func TestHealthzReadOnlyServerRejectsMutations(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	tests := []struct {
		name string
		call func() error
	}{
		{name: "legacy collection", call: func() error {
			_, err := server.Get(context.Background(), &healthz.GetRequest{Path: healthzDebugPath()})
			return err
		}},
		{name: "acknowledge", call: func() error {
			_, err := server.Acknowledge(context.Background(), &healthz.AcknowledgeRequest{Id: "/tmp/dump/artifact"})
			return err
		}},
		{name: "check", call: func() error {
			_, err := server.Check(context.Background(), &healthz.CheckRequest{})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); status.Code(err) != codes.Unimplemented {
				t.Fatalf("read-only call code = %v, want %v; err=%v", status.Code(err), codes.Unimplemented, err)
			}
		})
	}
}

func TestHealthzAcknowledgeValidatesBeforeDBus(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	server.config.EnableNativeWrite = true
	outside := filepath.Join(server.artifactResolver.hostMount, "outside.tar.gz")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, server.artifactResolver.containerPath("/tmp/dump/symlink.tar.gz")); err != nil {
		t.Fatal(err)
	}
	dbusCalls := 0
	patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		dbusCalls++
		return &ssc.FakeClient{}, nil
	})
	defer patch.Reset()

	for _, test := range []struct {
		id   string
		code codes.Code
	}{
		{id: "/tmp/dump/bad\x00name", code: codes.InvalidArgument},
		{id: "/tmp/dump/symlink.tar.gz", code: codes.PermissionDenied},
	} {
		if _, err := server.Acknowledge(context.Background(), &healthz.AcknowledgeRequest{Id: test.id}); status.Code(err) != test.code {
			t.Fatalf("Acknowledge(%q) code = %v, want %v; err=%v", test.id, status.Code(err), test.code, err)
		}
	}
	if dbusCalls != 0 {
		t.Fatalf("invalid artifacts reached D-Bus %d times", dbusCalls)
	}
}

func TestHealthzAcknowledgeContainedArtifact(t *testing.T) {
	for _, test := range []struct {
		name   string
		client ssc.Service
		code   codes.Code
	}{
		{name: "success", client: &ssc.FakeClient{}},
		{name: "D-Bus failure", client: &ssc.FakeClientWithError{}, code: codes.Internal},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newHealthzArtifactTestServer(t)
			server.config.EnableNativeWrite = true
			artifactID := "/tmp/dump/nested/legacy.tar.gz"
			writeArtifactTestFile(t, server.artifactResolver, artifactID, []byte("legacy"))
			patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return test.client, nil
			})
			defer patch.Reset()

			response, err := server.Acknowledge(context.Background(), &healthz.AcknowledgeRequest{Id: artifactID})
			if status.Code(err) != test.code || (test.code == codes.OK && response == nil) {
				t.Fatalf("Acknowledge(%q) = (%+v, %v), want code %v", artifactID, response, err, test.code)
			}
		})
	}
}
