// Package oras implements the SONiC gNOI Oras service.
//
// This is the PoC implementation tracked by ADO Feature #37984064 and the
// design doc at doc/oras-pull-design.md. It supports a single Pull RPC that
// streams an OCI/ORAS artifact from a registry to a local file on the target,
// reporting progress along the way.
package oras

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
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
	ctx := stream.Context()
	started := time.Now()

	if err := validatePullRequest(req); err != nil {
		return err
	}

	repo, err := newRepository(req)
	if err != nil {
		return err
	}

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

	if err := stream.Send(&oraspb.PullResponse{
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
	go progressLoop(ctx, stream, &transferred, uint64(layer.Size), progressDone)
	defer func() { close(progressDone) }()

	if err := fetchLayerWithProgress(ctx, repo, annotated, fs, &transferred); err != nil {
		return mapRegistryError(err, "fetch layer")
	}

	// Move the layer file into place. file.Store wrote it as stagingName
	// inside stagingDir.
	srcPath := filepath.Join(stagingDir, stagingName)
	if err := os.Rename(srcPath, req.GetLocalPath()); err != nil {
		// Rename across filesystems falls back to copy-and-delete.
		if err := copyAndRemove(srcPath, req.GetLocalPath()); err != nil {
			return status.Errorf(codes.Internal, "stage to local_path: %v", err)
		}
	}

	return stream.Send(&oraspb.PullResponse{
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
	if proxy := req.GetHttpProxy(); proxy != "" {
		if _, err := url.Parse(proxy); err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid http_proxy: %v", err)
		}
	}
	return nil
}

func validateLocalPath(p string) error {
	cleaned := filepath.Clean(p)
	if !filepath.IsAbs(cleaned) {
		return fmt.Errorf("path must be absolute, got: %s", p)
	}
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("path traversal not allowed: %s", p)
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

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy := req.GetHttpProxy(); proxy != "" {
		u, _ := url.Parse(proxy)
		transport.Proxy = http.ProxyURL(u)
	}

	client := &auth.Client{
		Client: &http.Client{
			Transport: retry.NewTransport(transport),
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
	if err := jsonUnmarshalStrict(manifest, &m); err != nil {
		return ocispec.Descriptor{}, status.Errorf(codes.Internal, "parse manifest: %v", err)
	}
	if len(m.Layers) != 1 {
		return ocispec.Descriptor{}, status.Errorf(codes.InvalidArgument,
			"PoC supports single-layer artifacts only; manifest has %d layers", len(m.Layers))
	}
	return m.Layers[0], nil
}

// fetchLayerWithProgress copies a single layer descriptor into the file store
// while updating the transferred counter. We use oras.Copy to leverage the
// graph machinery, but with an artificial intermediate that lets us tee the
// blob stream through a counter.
func fetchLayerWithProgress(ctx context.Context, src *remote.Repository, layer ocispec.Descriptor, dst *file.Store, transferred *atomic.Uint64) error {
	rc, err := src.Fetch(ctx, layer)
	if err != nil {
		return err
	}
	defer rc.Close()

	tr := &countingReader{r: rc, n: transferred}
	if err := dst.Push(ctx, layer, tr); err != nil {
		return err
	}
	_ = oras.Copy // keep import in case we switch to oras.Copy later
	return nil
}

func progressLoop(ctx context.Context, stream oraspb.Oras_PullServer, transferred *atomic.Uint64, total uint64, done <-chan struct{}) {
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
// that match the design doc.
func mapRegistryError(err error, op string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "401") || strings.Contains(msg, "Unauthorized") || strings.Contains(msg, "authentication required"):
		return status.Errorf(codes.Unauthenticated, "%s: %v", op, err)
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "timeout"):
		return status.Errorf(codes.Unavailable, "%s: %v", op, err)
	case strings.Contains(msg, "404") || strings.Contains(msg, "not found"):
		return status.Errorf(codes.NotFound, "%s: %v", op, err)
	case strings.Contains(msg, "no space left on device"):
		return status.Errorf(codes.ResourceExhausted, "%s: %v", op, err)
	}
	return status.Errorf(codes.Internal, "%s: %v", op, err)
}
