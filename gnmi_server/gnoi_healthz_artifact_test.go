package gnmi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
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

type artifactReaderHook struct {
	io.ReadSeeker
	readHook func()
}

func (reader *artifactReaderHook) Read(buf []byte) (int, error) {
	n, err := reader.ReadSeeker.Read(buf)
	if reader.readHook != nil {
		reader.readHook()
	}
	return n, err
}

type legacyCollectionService struct {
	ssc.Service
	artifactID string
}

type cancelAfterOpenContext struct {
	context.Context
	errCalls int
}

func (ctx *cancelAfterOpenContext) Err() error {
	ctx.errCalls++
	if ctx.errCalls >= 2 {
		return context.Canceled
	}
	return nil
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
	// Real gRPC serializes each frame before Send returns. Keep test frames
	// independent of the buffer that Artifact reuses for its next read.
	stored := response
	if data, ok := response.Contents.(*healthz.ArtifactResponse_Bytes); ok {
		stored = &healthz.ArtifactResponse{
			Contents: &healthz.ArtifactResponse_Bytes{Bytes: bytes.Clone(data.Bytes)},
		}
	}
	stream.responses = append(stream.responses, stored)
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

func TestHealthzArtifactHeaderUsesGenericMimeForLegacyFile(t *testing.T) {
	header, err := buildHealthzArtifactHeader(context.Background(), "/tmp/dump/diagnostic", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	if header.GetFile().GetName() != "diagnostic" || header.GetFile().GetMimetype() != "application/octet-stream" {
		t.Fatalf("unexpected legacy file metadata: %+v", header.GetFile())
	}
}

func TestHealthzArtifactHeaderCancelsDuringHashing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	reader := &artifactReaderHook{ReadSeeker: bytes.NewReader(make([]byte, 64*1024)), readHook: func() {
		reads++
		cancel()
	}}
	header, err := buildHealthzArtifactHeader(ctx, "diagnostic", reader)
	if header != nil || status.Code(err) != codes.Canceled || reads != 1 {
		t.Fatalf("header hashing = (%v, %v) after %d reads, want cancellation after first read", header, err, reads)
	}
}

func TestHealthzArtifactHeaderBoundsGrowthAndRejectsTruncation(t *testing.T) {
	content := bytes.Repeat([]byte("original"), 8192)
	for _, grow := range []bool{false, true} {
		name := "truncation"
		if grow {
			name = "growth"
		}
		t.Run(name, func(t *testing.T) {
			underlying := bytes.NewReader(content)
			reader := &artifactReaderHook{ReadSeeker: underlying}
			reader.readHook = func() {
				reader.readHook = nil
				offset, err := underlying.Seek(0, io.SeekCurrent)
				if err != nil {
					t.Fatal(err)
				}
				if grow {
					underlying.Reset(append(bytes.Clone(content), bytes.Repeat([]byte("extra"), 8192)...))
				} else {
					underlying.Reset(content[:offset])
				}
				if _, err := underlying.Seek(offset, io.SeekStart); err != nil {
					t.Fatal(err)
				}
			}
			header, err := buildHealthzArtifactHeader(context.Background(), "diagnostic", reader)
			if !grow {
				if header != nil || status.Code(err) != codes.Internal {
					t.Fatalf("truncated header hashing = (%v, %v), want Internal", header, err)
				}
				return
			}
			wantHash := sha256.Sum256(content)
			if err != nil || header.GetFile().GetSize() != int64(len(content)) ||
				!bytes.Equal(header.GetFile().GetHash().GetHash(), wantHash[:]) {
				t.Fatalf("growing header hashing = (%v, %v), want initial size and hash", header, err)
			}
		})
	}
}

func TestHealthzArtifactHeaderPreservesLargeLegacyArchives(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const size = 51 * 1024 * 1024
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	header, err := buildHealthzArtifactHeader(context.Background(), "/tmp/dump/large", file)
	if err != nil || header.GetFile().GetSize() != size {
		t.Fatalf("large legacy header = (%v, %v), want size %d", header, err, size)
	}
}

func TestHealthzArtifactHandlesFileChangesAfterHeader(t *testing.T) {
	for _, grow := range []bool{false, true} {
		name := "truncation"
		if grow {
			name = "growth"
		}
		t.Run(name, func(t *testing.T) {
			server := newHealthzArtifactTestServer(t)
			artifactID := "dldd-0123456789abcdef0123456789abcdef.tar.gz"
			content := []byte("initial archive")
			path := writeArtifactTestFile(t, server.artifactResolver,
				filepath.Join(server.artifactResolver.dlddDirectory, artifactID), content)
			stream := &artifactTestStream{send: func(response *healthz.ArtifactResponse) error {
				if response.GetHeader() == nil {
					return nil
				}
				if grow {
					return os.WriteFile(path, append(bytes.Clone(content), []byte("extra")...), 0644)
				}
				return os.Truncate(path, 0)
			}}
			err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream)
			if !grow {
				if status.Code(err) != codes.Internal || len(stream.responses) != 1 {
					t.Fatalf("Artifact(truncated) = %v after %d responses, want Internal without trailer", err, len(stream.responses))
				}
				return
			}
			if err != nil || len(stream.responses) != 3 || !bytes.Equal(stream.responses[1].GetBytes(), content) ||
				stream.responses[0].GetHeader().GetFile().GetSize() != int64(len(content)) {
				t.Fatalf("Artifact(growing) = %v with %d responses, want only initial bytes", err, len(stream.responses))
			}
		})
	}
}

func TestHealthzArtifactCancelsDuringStreaming(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "healthz-0123456789abcdef0123456789abcdef.tar.gz"
	writeArtifactTestFile(t, server.artifactResolver,
		filepath.Join(server.artifactResolver.healthzDirectory, artifactID), make([]byte, 2*ddFileSegSize))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &artifactTestStream{ctx: ctx, send: func(response *healthz.ArtifactResponse) error {
		if _, ok := response.Contents.(*healthz.ArtifactResponse_Bytes); ok {
			cancel()
		}
		return nil
	}}
	err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream)
	if status.Code(err) != codes.Canceled || len(stream.responses) != 2 {
		t.Fatalf("Artifact(canceled during stream) = %v after %d responses, want Canceled without trailer", err, len(stream.responses))
	}
}

func TestHealthzArtifactStreamsCompletedArchive(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "healthz-0123456789abcdef0123456789abcdef.tar.gz"
	content := make([]byte, 2*ddFileSegSize+1)
	for index := range content {
		content[index] = byte(index*31 + index/ddFileSegSize)
	}
	writeArtifactTestFile(t, server.artifactResolver,
		filepath.Join(server.artifactResolver.healthzDirectory, artifactID), content)
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
	if header.GetId() != artifactID || file.GetName() != artifactID || file.GetMimetype() != "application/gzip" || file.GetSize() != int64(len(content)) ||
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

func TestHealthzArtifactStreamsExistingDLDDArchive(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "dldd-0123456789abcdef0123456789abcdef.tar.gz"
	writeArtifactTestFile(t, server.artifactResolver,
		filepath.Join(server.artifactResolver.dlddDirectory, artifactID), []byte("existing archive"))
	stream := &artifactTestStream{}
	if err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream); err != nil {
		t.Fatalf("Artifact() failed for an existing DLDD archive: %v", err)
	}
	if len(stream.responses) != 3 || stream.responses[0].GetHeader().GetId() != artifactID ||
		string(stream.responses[1].GetBytes()) != "existing archive" || stream.responses[2].GetTrailer() == nil {
		t.Fatalf("Artifact() returned an invalid legacy archive stream: %+v", stream.responses)
	}
}

func TestWaitForHealthzArtifactAllowsAsynchronousCollection(t *testing.T) {
	for _, prefix := range []string{"healthz-", "dldd-"} {
		t.Run(prefix, func(t *testing.T) {
			resolver := newArtifactTestResolver(t)
			artifactID := prefix + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.tar.gz"
			written := make(chan error, 1)
			go func() {
				time.Sleep(20 * time.Millisecond)
				path := resolver.containerPath(filepath.Join(resolver.artifactDirectory(artifactID), artifactID))
				written <- os.WriteFile(path, []byte("ready"), 0644)
			}()

			file, err := waitForHealthzArtifact(
				context.Background(), resolver, artifactID, time.Second, 5*time.Millisecond,
				func(string) (string, error) { return "PENDING", nil },
			)
			if err != nil {
				t.Fatalf("waitForHealthzArtifact() failed: %v", err)
			}
			file.Close()
			if err := <-written; err != nil {
				t.Fatalf("failed to create asynchronous artifact: %v", err)
			}
		})
	}
}

func TestWaitForHealthzArtifactRetriesStatusTimeout(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	artifactID := "healthz-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.tar.gz"
	path := resolver.containerPath(filepath.Join(resolver.healthzDirectory, artifactID))
	checked := make(chan struct{}, 1)
	written := make(chan error, 1)
	go func() {
		<-checked
		time.Sleep(20 * time.Millisecond)
		written <- os.WriteFile(path, []byte("ready"), 0644)
	}()

	file, err := waitForHealthzArtifact(context.Background(), resolver, artifactID,
		time.Second, 5*time.Millisecond, func(string) (string, error) {
			select {
			case checked <- struct{}{}:
			default:
			}
			return "", status.Error(codes.DeadlineExceeded, "host status call timed out")
		})
	if err != nil {
		t.Fatalf("waitForHealthzArtifact() failed after a transient status timeout: %v", err)
	}
	file.Close()
	if err := <-written; err != nil {
		t.Fatalf("failed to create asynchronous artifact: %v", err)
	}
}

func TestWaitForHealthzArtifactHonorsCancellation(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForHealthzArtifact(
		ctx, resolver, "healthz-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.tar.gz",
		time.Second, 5*time.Millisecond, nil,
	)
	if status.Code(err) != codes.Canceled {
		t.Fatalf("waitForHealthzArtifact() code = %v, want %v; err=%v",
			status.Code(err), codes.Canceled, err)
	}
}

func TestWaitForHealthzArtifactChecksCancellationAfterOpen(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	artifactID := "healthz-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.tar.gz"
	writeArtifactTestFile(t, resolver, filepath.Join(resolver.healthzDirectory, artifactID), []byte("ready"))
	ctx := &cancelAfterOpenContext{Context: context.Background()}
	file, err := waitForHealthzArtifact(ctx, resolver, artifactID, time.Second, time.Millisecond, nil)
	if file != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("waitForHealthzArtifact() = (%v, %v), want canceled after opening", file, err)
	}
}

func TestWaitForHealthzArtifactCancelsDuringStatusCheck(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	go func() {
		_, err := waitForHealthzArtifact(ctx, resolver,
			"healthz-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.tar.gz", 5*time.Second, time.Millisecond,
			func(string) (string, error) {
				close(entered)
				<-release
				return "PENDING", nil
			})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("artifact status check did not start")
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("canceled status check code = %v, want %v; err=%v", status.Code(err), codes.Canceled, err)
		}
	case <-time.After(time.Second):
		t.Fatal("Artifact wait stayed blocked in the status check after cancellation")
	}
}

func TestHealthzArtifactChecksCancellationBeforeHashing(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	artifactID := "healthz-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.tar.gz"
	path := writeArtifactTestFile(t, server.artifactResolver,
		filepath.Join(server.artifactResolver.healthzDirectory, artifactID), []byte("ready"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(waitForHealthzArtifact,
		func(context.Context, artifactPathResolver, string, time.Duration, time.Duration,
			func(string) (string, error)) (*os.File, error) {
			cancel()
			return os.Open(path)
		})
	stream := &artifactTestStream{ctx: ctx}
	err := server.Artifact(&healthz.ArtifactRequest{Id: artifactID}, stream)
	if status.Code(err) != codes.Canceled || len(stream.responses) != 0 {
		t.Fatalf("Artifact(canceled before hash) = %v after %d responses, want Canceled", err, len(stream.responses))
	}
}

func TestWaitForHealthzArtifactReopensAfterCompletion(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	artifactID := "healthz-dddddddddddddddddddddddddddddddd.tar.gz"
	file, err := waitForHealthzArtifact(context.Background(), resolver, artifactID,
		time.Second, time.Millisecond, func(string) (string, error) {
			path := resolver.containerPath(filepath.Join(resolver.healthzDirectory, artifactID))
			return "COMPLETED", os.WriteFile(path, []byte("ready"), 0644)
		})
	if err != nil {
		t.Fatalf("waitForHealthzArtifact() failed after completion: %v", err)
	}
	file.Close()
}

func TestHealthzArtifactReturnsPromptNotFoundForMissingReservation(t *testing.T) {
	for _, prefix := range []string{"healthz-", "dldd-"} {
		t.Run(prefix, func(t *testing.T) {
			server := newHealthzArtifactTestServer(t)
			useCatalogTestService(t, &ssc.FakeClient{
				HealthzArtifactStatusResponse: `{"state":"MISSING"}`,
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stream := &artifactTestStream{ctx: ctx}

			err := server.Artifact(&healthz.ArtifactRequest{
				Id: prefix + "cccccccccccccccccccccccccccccccc.tar.gz",
			}, stream)
			if status.Code(err) != codes.NotFound || len(stream.responses) != 0 {
				t.Fatalf("Artifact(missing) = %v after %d responses, want prompt NotFound", err, len(stream.responses))
			}
		})
	}
}

func TestHealthzArtifactFailsWhenCompletedArchiveIsNotMounted(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	useCatalogTestService(t, &ssc.FakeClient{
		HealthzArtifactStatusResponse: `{"state":"COMPLETED"}`,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream := &artifactTestStream{ctx: ctx}
	err := server.Artifact(&healthz.ArtifactRequest{
		Id: "healthz-cccccccccccccccccccccccccccccccc.tar.gz",
	}, stream)
	if status.Code(err) != codes.Internal || len(stream.responses) != 0 {
		t.Fatalf("Artifact(unmounted completed archive) = %v after %d responses, want Internal", err, len(stream.responses))
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

	if _, err := server.collectDebugData(context.Background(), &types.Path{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("collectDebugData() code = %v, want InvalidArgument; err=%v", status.Code(err), err)
	}
}

func TestHealthzReadOnlyServerRejectsMutations(t *testing.T) {
	server := newHealthzArtifactTestServer(t)
	tests := []struct {
		name string
		call func() error
	}{
		{name: "legacy collection", call: func() error {
			_, err := server.Check(context.Background(), &healthz.CheckRequest{Path: healthzDebugPath()})
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
