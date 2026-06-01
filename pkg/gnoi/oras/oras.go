// Package oras implements the SONiC gNOI Oras service.
//
// This is the PoC implementation tracked by ADO Feature #37984064 and the
// design doc at doc/oras-pull-design.md. It supports a single Pull RPC that
// streams an OCI/ORAS artifact from a registry to a local file on the target,
// reporting progress along the way.
package oras

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	log "github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"
	"oras.land/oras-go/v2/registry/remote/retry"

	oraspb "github.com/sonic-net/sonic-gnmi/proto/gnoi/oras"
)

const (
	// progressInterval bounds how often PullProgress messages are emitted.
	progressInterval = 1 * time.Second
)

// allowedPathPrefixes mirrors the file-server allowlist in
// pkg/gnoi/file/file.go to keep the on-disk write surface consistent.
var allowedPathPrefixes = []string{"/tmp/", "/var/tmp/", "/host/"}

// HandlePull implements the Pull RPC. The implementation is server-streaming:
// it emits a single PullStarted once the manifest is resolved, zero or more
// PullProgress messages while bytes are being transferred, and a final
// PullResult on success. Returning any error (including from stream.Send)
// terminates the stream.
func HandlePull(req *oraspb.PullRequest, stream oraspb.Oras_PullServer) error {
	if err := validatePullRequest(req); err != nil {
		return err
	}
	repo, err := newRepository(req)
	if err != nil {
		return err
	}
	return handlePullWithRepo(req, stream, repo)
}

// safeStream wraps an Oras_PullServer with a mutex so that progressLoop and
// the main goroutine can both Send without racing on the gRPC stream.
// gRPC's ServerStream.Send is not safe for concurrent use.
type safeStream struct {
	mu     sync.Mutex
	stream oraspb.Oras_PullServer
}

func (s *safeStream) Send(r *oraspb.PullResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(r)
}

func (s *safeStream) Context() context.Context { return s.stream.Context() }

// handlePullWithRepo is the testable seam: HandlePull does request validation
// and constructs the repository, then delegates here. Tests construct the
// repository themselves (e.g. with PlainHTTP=true against an httptest server).
func handlePullWithRepo(req *oraspb.PullRequest, stream oraspb.Oras_PullServer, repo *remote.Repository) error {
	ss := &safeStream{stream: stream}
	ctx := ss.Context()
	started := time.Now()

	ref := pullReference(req)
	log.Infof("[Oras.Pull] resolving %s/%s@%s", req.GetRegistry(), req.GetRepository(), ref)

	manifestDesc, err := repo.Resolve(ctx, ref)
	if err != nil {
		return mapRegistryError(err, "resolve manifest")
	}

	// Fetch manifest bytes so we can pick the single layer.
	mfRC, err := repo.Fetch(ctx, manifestDesc)
	if err != nil {
		return mapRegistryError(err, "fetch manifest")
	}
	mfBytes, err := io.ReadAll(mfRC)
	mfRC.Close()
	if err != nil {
		return status.Errorf(codes.Internal, "read manifest: %v", err)
	}

	layer, err := pickSingleLayer(mfBytes)
	if err != nil {
		return err
	}

	if err := ss.Send(&oraspb.PullResponse{
		Event: &oraspb.PullResponse_Started{
			Started: &oraspb.PullStarted{
				ManifestDigest: manifestDesc.Digest.String(),
				TotalBytes:     uint64(layer.Size),
			},
		},
	}); err != nil {
		return err
	}

	// Stage the layer into a temporary directory next to local_path, then
	// rename into place on success. oras-go's file.Store writes by layer
	// digest into the staging directory; we then move that file to local_path.
	dir := filepath.Dir(req.GetLocalPath())
	stagingDir, err := os.MkdirTemp(dir, ".oras-pull-")
	if err != nil {
		return status.Errorf(codes.Internal, "create staging dir: %v", err)
	}
	defer os.RemoveAll(stagingDir)

	fs, err := file.New(stagingDir)
	if err != nil {
		return status.Errorf(codes.Internal, "init file store: %v", err)
	}
	defer fs.Close()

	// Subscribe to the layer descriptor so the file store writes a file we
	// can find afterwards by name. We use the layer digest as the filename.
	stagingName := layer.Digest.Encoded()
	annotated := layer
	if annotated.Annotations == nil {
		annotated.Annotations = map[string]string{}
	}
	annotated.Annotations[ocispec.AnnotationTitle] = stagingName

	// Progress tracker. oras-go does not surface progress callbacks for
	// non-Copy fetches, so we tee the fetch through a tracked reader.
	var transferred atomic.Uint64
	progressDone := make(chan struct{})
	progressExited := make(chan struct{})
	go func() {
		progressLoop(ctx, ss, &transferred, uint64(layer.Size), progressDone)
		close(progressExited)
	}()

	fetchErr := fetchLayerWithProgress(ctx, repo, annotated, fs, &transferred)

	// Stop the progress goroutine and wait for it to drain before any
	// further Send on the stream, so the final PullResult can never race
	// with a PullProgress (even though safeStream would serialize them).
	close(progressDone)
	<-progressExited

	if fetchErr != nil {
		return mapRegistryError(fetchErr, "fetch layer")
	}

	// Move the layer file into place. file.Store wrote it as stagingName
	// inside stagingDir.
	srcPath := filepath.Join(stagingDir, stagingName)
	if err := os.Rename(srcPath, req.GetLocalPath()); err != nil {
		// Only fall back to copy-and-delete for cross-filesystem renames;
		// every other os.Rename failure (perm, target-is-dir, etc.) is
		// surfaced as-is so it shows up in logs and the gRPC error.
		if !isCrossDeviceError(err) {
			return status.Errorf(codes.Internal, "rename to local_path: %v", err)
		}
		log.V(1).Infof("[Oras.Pull] rename %s -> %s: %v; falling back to copy", srcPath, req.GetLocalPath(), err)
		if err := copyAndRemove(srcPath, req.GetLocalPath()); err != nil {
			return status.Errorf(codes.Internal, "copy to local_path: %v", err)
		}
	}

	return ss.Send(&oraspb.PullResponse{
		Event: &oraspb.PullResponse_Result{
			Result: &oraspb.PullResult{
				ManifestDigest: manifestDesc.Digest.String(),
				LayerDigest:    layer.Digest.String(),
				BytesWritten:   uint64(layer.Size),
				LocalPath:      req.GetLocalPath(),
				Elapsed:        durationpb.New(time.Since(started)),
			},
		},
	})
}

// isCrossDeviceError reports whether err is an os.Rename failure caused by
// src and dst being on different filesystems (EXDEV).
func isCrossDeviceError(err error) bool {
	var le *os.LinkError
	if errors.As(err, &le) {
		return errors.Is(le.Err, syscall.EXDEV)
	}
	return errors.Is(err, syscall.EXDEV)
}

func validatePullRequest(req *oraspb.PullRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if req.GetRegistry() == "" {
		return status.Error(codes.InvalidArgument, "registry is required")
	}
	if req.GetRepository() == "" {
		return status.Error(codes.InvalidArgument, "repository is required")
	}
	if req.GetTag() == "" && req.GetDigest() == "" {
		return status.Error(codes.InvalidArgument, "either tag or digest is required")
	}
	if req.GetLocalPath() == "" {
		return status.Error(codes.InvalidArgument, "local_path is required")
	}
	if err := validateLocalPath(req.GetLocalPath()); err != nil {
		return status.Errorf(codes.FailedPrecondition, "invalid local_path: %v", err)
	}
	return nil
}

func validateLocalPath(p string) error {
	cleaned := filepath.Clean(p)
	if !filepath.IsAbs(cleaned) {
		return fmt.Errorf("path must be absolute, got: %s", p)
	}
	// filepath.Clean has already collapsed any real `..` traversal segments
	// against the absolute root. Any `..` left can only be a literal path
	// component, so reject only segments that equal "..", not substrings.
	for _, seg := range strings.Split(cleaned, string(filepath.Separator)) {
		if seg == ".." {
			return fmt.Errorf("path traversal not allowed: %s", p)
		}
	}
	for _, prefix := range allowedPathPrefixes {
		if strings.HasPrefix(cleaned, prefix) {
			return nil
		}
	}
	return fmt.Errorf("path must be under %v, got: %s", allowedPathPrefixes, cleaned)
}

func pullReference(req *oraspb.PullRequest) string {
	if d := req.GetDigest(); d != "" {
		return d
	}
	return req.GetTag()
}

func newRepository(req *oraspb.PullRequest) (*remote.Repository, error) {
	repoRef := fmt.Sprintf("%s/%s", req.GetRegistry(), req.GetRepository())
	repo, err := remote.NewRepository(repoRef)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid repository reference %q: %v", repoRef, err)
	}

	// Use http.DefaultTransport so the standard HTTP_PROXY/HTTPS_PROXY/
	// NO_PROXY env vars (honored by http.ProxyFromEnvironment) take effect.
	// On lab testbeds where the target needs a proxy to reach the registry,
	// set those env vars on the gnmi process; production switches with a
	// route to the registry need no configuration.
	client := &auth.Client{
		Client: &http.Client{
			Transport: retry.NewTransport(http.DefaultTransport),
		},
		Cache: auth.NewCache(),
	}
	if basic := req.GetAuth().GetBasic(); basic != nil {
		username, password := basic.GetUsername(), basic.GetPassword()
		client.Credential = func(_ context.Context, _ string) (auth.Credential, error) {
			return auth.Credential{Username: username, Password: password}, nil
		}
	}
	repo.Client = client
	return repo, nil
}

func pickSingleLayer(manifest []byte) (ocispec.Descriptor, error) {
	var m ocispec.Manifest
	if err := parseManifest(manifest, &m); err != nil {
		return ocispec.Descriptor{}, status.Errorf(codes.Internal, "parse manifest: %v", err)
	}
	if len(m.Layers) != 1 {
		return ocispec.Descriptor{}, status.Errorf(codes.InvalidArgument,
			"PoC supports single-layer artifacts only; manifest has %d layers", len(m.Layers))
	}
	return m.Layers[0], nil
}

// fetchLayerWithProgress copies a single layer descriptor into the file store
// while updating the transferred counter.
func fetchLayerWithProgress(ctx context.Context, src *remote.Repository, layer ocispec.Descriptor, dst *file.Store, transferred *atomic.Uint64) error {
	rc, err := src.Fetch(ctx, layer)
	if err != nil {
		return err
	}
	defer rc.Close()

	tr := &countingReader{r: rc, n: transferred}
	return dst.Push(ctx, layer, tr)
}

func progressLoop(ctx context.Context, stream *safeStream, transferred *atomic.Uint64, total uint64, done <-chan struct{}) {
	tick := time.NewTicker(progressInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-tick.C:
			cur := transferred.Load()
			if err := stream.Send(&oraspb.PullResponse{
				Event: &oraspb.PullResponse_Progress{
					Progress: &oraspb.PullProgress{
						BytesTransferred: cur,
						TotalBytes:       total,
					},
				},
			}); err != nil {
				log.V(1).Infof("[Oras.Pull] progress send failed: %v", err)
				return
			}
		}
	}
}

type countingReader struct {
	r io.Reader
	n *atomic.Uint64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n.Add(uint64(n))
	}
	return n, err
}

// copyAndRemove is used when os.Rename fails because src and dst are on
// different filesystems (the staging tmpdir is created next to dst, so this
// path is unlikely in practice but kept for safety).
func copyAndRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return os.Remove(src)
}

// mapRegistryError translates oras-go / network errors into gRPC status codes
// that match the design doc. Prefers typed-error inspection over substring
// matching so that unrelated error text containing words like "404" or
// "not found" does not get misclassified.
func mapRegistryError(err error, op string) error {
	if err == nil {
		return nil
	}

	// oras-go typed errors.
	var ec *errcode.ErrorResponse
	if errors.As(err, &ec) {
		switch ec.StatusCode {
		case http.StatusUnauthorized:
			return status.Errorf(codes.Unauthenticated, "%s: %v", op, err)
		case http.StatusForbidden:
			return status.Errorf(codes.PermissionDenied, "%s: %v", op, err)
		case http.StatusNotFound:
			return status.Errorf(codes.NotFound, "%s: %v", op, err)
		}
	}
	if errors.Is(err, errdef.ErrNotFound) {
		return status.Errorf(codes.NotFound, "%s: %v", op, err)
	}

	// Network-level classification.
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return status.Errorf(codes.Unavailable, "%s: %v", op, err)
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return status.Errorf(codes.Unavailable, "%s: %v", op, err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return status.Errorf(codes.Unavailable, "%s: %v", op, err)
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return status.Errorf(codes.Unavailable, "%s: %v", op, err)
	}

	// Disk-full is a syscall errno on Linux.
	if errors.Is(err, syscall.ENOSPC) {
		return status.Errorf(codes.ResourceExhausted, "%s: %v", op, err)
	}

	return status.Errorf(codes.Internal, "%s: %v", op, err)
}
