package oras

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	oraspb "github.com/sonic-net/sonic-gnmi/proto/gnoi/oras"
)

// ---------- helpers ----------

func mustStatus(t *testing.T, err error) *status.Status {
	t.Helper()
	s, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a gRPC status", err)
	}
	return s
}

// ---------- validatePullRequest ----------

func TestValidatePullRequest(t *testing.T) {
	good := &oraspb.PullRequest{
		Registry:   "r.example.com",
		Repository: "ns/repo",
		Reference:  &oraspb.PullRequest_Tag{Tag: "v1"},
		LocalPath:  "/tmp/out.bin",
	}

	cases := []struct {
		name string
		mut  func(r *oraspb.PullRequest)
		code codes.Code
	}{
		{"happy", func(r *oraspb.PullRequest) {}, codes.OK},
		{"happy-with-proxy", func(r *oraspb.PullRequest) { r.HttpProxy = "http://10.0.0.1:8888" }, codes.OK},
		{"happy-digest", func(r *oraspb.PullRequest) {
			r.Reference = &oraspb.PullRequest_Digest{Digest: "sha256:" + strings.Repeat("a", 64)}
		}, codes.OK},
		{"missing-registry", func(r *oraspb.PullRequest) { r.Registry = "" }, codes.InvalidArgument},
		{"missing-repo", func(r *oraspb.PullRequest) { r.Repository = "" }, codes.InvalidArgument},
		{"missing-ref", func(r *oraspb.PullRequest) { r.Reference = nil }, codes.InvalidArgument},
		{"missing-localpath", func(r *oraspb.PullRequest) { r.LocalPath = "" }, codes.InvalidArgument},
		{"bad-localpath-relative", func(r *oraspb.PullRequest) { r.LocalPath = "out.bin" }, codes.FailedPrecondition},
		{"bad-localpath-outside-allowlist", func(r *oraspb.PullRequest) { r.LocalPath = "/etc/passwd" }, codes.FailedPrecondition},
		{"bad-localpath-traversal", func(r *oraspb.PullRequest) { r.LocalPath = "/tmp/../etc/passwd" }, codes.FailedPrecondition},
		{"bad-proxy", func(r *oraspb.PullRequest) { r.HttpProxy = "http://[::1" }, codes.InvalidArgument},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := proto_clone(good)
			tc.mut(req)
			err := validatePullRequest(req)
			if tc.code == codes.OK {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error code %v, got nil", tc.code)
			}
			if got := mustStatus(t, err).Code(); got != tc.code {
				t.Fatalf("expected code %v, got %v (%v)", tc.code, got, err)
			}
		})
	}

	t.Run("nil-request", func(t *testing.T) {
		if err := validatePullRequest(nil); err == nil || mustStatus(t, err).Code() != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument for nil request, got %v", err)
		}
	})
}

func proto_clone(r *oraspb.PullRequest) *oraspb.PullRequest {
	return proto.Clone(r).(*oraspb.PullRequest)
}

// ---------- validateLocalPath ----------

func TestValidateLocalPath(t *testing.T) {
	for _, p := range []string{"/tmp/foo", "/var/tmp/x.bin", "/host/a/b"} {
		if err := validateLocalPath(p); err != nil {
			t.Errorf("expected %s ok, got %v", p, err)
		}
	}
	for _, p := range []string{"foo", "../foo", "/tmp/../etc/x", "/etc/x", "/home/me/x"} {
		if err := validateLocalPath(p); err == nil {
			t.Errorf("expected %s rejected", p)
		}
	}
}

// ---------- pullReference ----------

func TestPullReference(t *testing.T) {
	if got := pullReference(&oraspb.PullRequest{Reference: &oraspb.PullRequest_Tag{Tag: "v1"}}); got != "v1" {
		t.Errorf("tag: got %q", got)
	}
	if got := pullReference(&oraspb.PullRequest{Reference: &oraspb.PullRequest_Digest{Digest: "sha256:xx"}}); got != "sha256:xx" {
		t.Errorf("digest: got %q", got)
	}
}

// ---------- pickSingleLayer ----------

func TestPickSingleLayer(t *testing.T) {
	mk := func(n int) []byte {
		m := ocispec.Manifest{}
		for i := 0; i < n; i++ {
			m.Layers = append(m.Layers, ocispec.Descriptor{MediaType: "application/octet-stream", Digest: "sha256:abc", Size: 1})
		}
		b, _ := json.Marshal(m)
		return b
	}
	if _, err := pickSingleLayer(mk(0)); err == nil || mustStatus(t, err).Code() != codes.InvalidArgument {
		t.Errorf("0 layers: %v", err)
	}
	d, err := pickSingleLayer(mk(1))
	if err != nil || d.Digest != "sha256:abc" {
		t.Errorf("1 layer: d=%+v err=%v", d, err)
	}
	if _, err := pickSingleLayer(mk(2)); err == nil || mustStatus(t, err).Code() != codes.InvalidArgument {
		t.Errorf("2 layers: %v", err)
	}
	if _, err := pickSingleLayer([]byte("not-json")); err == nil || mustStatus(t, err).Code() != codes.Internal {
		t.Errorf("bad json: %v", err)
	}
}

// ---------- mapRegistryError ----------

func TestMapRegistryError(t *testing.T) {
	if mapRegistryError(nil, "op") != nil {
		t.Errorf("nil should pass through")
	}
	cases := []struct {
		msg  string
		code codes.Code
	}{
		{"401 Unauthorized", codes.Unauthenticated},
		{"authentication required", codes.Unauthenticated},
		{"dial tcp: lookup x: no such host", codes.Unavailable},
		{"connection refused", codes.Unavailable},
		{"i/o timeout", codes.Unavailable},
		{"404 not found", codes.NotFound},
		{"write /tmp/x: no space left on device", codes.ResourceExhausted},
		{"boom", codes.Internal},
	}
	for _, c := range cases {
		got := mustStatus(t, mapRegistryError(errors.New(c.msg), "op")).Code()
		if got != c.code {
			t.Errorf("%q: want %v got %v", c.msg, c.code, got)
		}
	}
}

// ---------- countingReader ----------

func TestCountingReader(t *testing.T) {
	var n atomic.Uint64
	cr := &countingReader{r: strings.NewReader("hello world"), n: &n}
	buf := make([]byte, 5)
	if got, _ := cr.Read(buf); got != 5 || n.Load() != 5 {
		t.Errorf("first read: got=%d n=%d", got, n.Load())
	}
	got, err := io.ReadAll(cr)
	if err != nil || string(got) != " world" || n.Load() != 11 {
		t.Errorf("rest: got=%q n=%d err=%v", got, n.Load(), err)
	}
}

// ---------- copyAndRemove ----------

func TestCopyAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyAndRemove(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be removed, stat err=%v", err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "payload" {
		t.Errorf("dst contents: %q err=%v", b, err)
	}

	if err := copyAndRemove(filepath.Join(dir, "nope"), filepath.Join(dir, "dst2")); err == nil {
		t.Errorf("expected error on missing src")
	}
}

// ---------- jsonUnmarshalStrict ----------

func TestJsonUnmarshalStrict(t *testing.T) {
	var v map[string]any
	if err := jsonUnmarshalStrict([]byte(`{"a":1}`), &v); err != nil || v["a"].(float64) != 1 {
		t.Errorf("ok case: v=%v err=%v", v, err)
	}
	if err := jsonUnmarshalStrict([]byte(`not json`), &v); err == nil {
		t.Errorf("expected error on bad json")
	}
}

// ---------- newRepository ----------

func TestNewRepositoryBadRef(t *testing.T) {
	req := &oraspb.PullRequest{Registry: "BAD HOST WITH SPACES", Repository: "x"}
	if _, err := newRepository(req); err == nil || mustStatus(t, err).Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestNewRepositoryWiresCredentialAndProxy(t *testing.T) {
	req := &oraspb.PullRequest{
		Registry:   "r.example.com",
		Repository: "ns/repo",
		HttpProxy:  "http://proxy.local:8888",
		Auth: &oraspb.AuthConfig{Mode: &oraspb.AuthConfig_Basic{
			Basic: &oraspb.BasicAuth{Username: "u", Password: "p"},
		}},
	}
	repo, err := newRepository(req)
	if err != nil {
		t.Fatal(err)
	}
	cli, ok := repo.Client.(*auth.Client)
	if !ok {
		t.Fatalf("expected *auth.Client, got %T", repo.Client)
	}
	got, err := cli.Credential(context.Background(), "r.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "u" || got.Password != "p" {
		t.Errorf("creds: got %+v", got)
	}
	// Anonymous case: no Credential callback at all.
	req2 := &oraspb.PullRequest{Registry: "r.example.com", Repository: "ns/repo"}
	repo2, err := newRepository(req2)
	if err != nil {
		t.Fatal(err)
	}
	if c := repo2.Client.(*auth.Client).Credential; c != nil {
		t.Errorf("expected nil Credential for anonymous, got non-nil")
	}
}

// TestHandlePullValidationShortCircuit covers the thin HandlePull wrapper that
// runs validation + repo construction before delegating to the seam.
func TestHandlePullValidationShortCircuit(t *testing.T) {
	stream := &fakeStream{ctx: context.Background()}
	err := HandlePull(&oraspb.PullRequest{}, stream)
	if err == nil || mustStatus(t, err).Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument on empty request, got %v", err)
	}
}

// TestHandlePullEndToEnd exercises the HandlePull wrapper itself (not just the
// seam) by pointing at an unroutable registry. Validation and newRepository
// must succeed; the actual resolve fails. This covers HandlePull lines that
// delegate into handlePullWithRepo after a successful construction.
func TestHandlePullEndToEnd(t *testing.T) {
	dst := filepath.Join("/tmp", fmt.Sprintf("oras-test-%d.bin", os.Getpid()))
	defer os.Remove(dst)
	req := &oraspb.PullRequest{
		Registry:   "127.0.0.1:1", // guaranteed connection refused
		Repository: "ns/repo",
		Reference:  &oraspb.PullRequest_Tag{Tag: "v1"},
		LocalPath:  dst,
	}
	stream := &fakeStream{ctx: context.Background()}
	err := HandlePull(req, stream)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := mustStatus(t, err).Code(); got != codes.Unavailable {
		t.Errorf("expected Unavailable, got %v (%v)", got, err)
	}
}

// TestHandlePullBadRegistryRef covers the HandlePull → newRepository error path.
func TestHandlePullBadRegistryRef(t *testing.T) {
	req := &oraspb.PullRequest{
		Registry:   "BAD HOST", // space is invalid in registry ref
		Repository: "ns/repo",
		Reference:  &oraspb.PullRequest_Tag{Tag: "v1"},
		LocalPath:  "/tmp/x.bin",
	}
	err := HandlePull(req, &fakeStream{ctx: context.Background()})
	if err == nil || mustStatus(t, err).Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------- HandlePull, via a fake OCI registry ----------

// fakeStream collects PullResponse messages.
type fakeStream struct {
	oraspb.Oras_PullServer
	ctx  context.Context
	sent []*oraspb.PullResponse
}

func (s *fakeStream) Context() context.Context          { return s.ctx }
func (s *fakeStream) Send(r *oraspb.PullResponse) error { s.sent = append(s.sent, r); return nil }

// newFakeRegistry serves a single-layer OCI artifact at ns/repo:v1.
// require auth: when require401 is true, every request returns 401 unless
// Authorization: Basic <b64(u:p)> is present.
func newFakeRegistry(t *testing.T, payload []byte, requireAuth bool, expectUser, expectPass string) *httptest.Server {
	t.Helper()
	layerSum := sha256.Sum256(payload)
	layerDigest := digest.Digest("sha256:" + hex.EncodeToString(layerSum[:]))

	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: "application/vnd.oci.empty.v1+json",
			Digest:    digest.Digest("sha256:" + strings.Repeat("0", 64)),
			Size:      0,
		},
		Layers: []ocispec.Descriptor{{
			MediaType: "application/octet-stream",
			Digest:    layerDigest,
			Size:      int64(len(payload)),
		}},
	}
	manifest.SchemaVersion = 2
	mfBytes, _ := json.Marshal(manifest)
	mfSum := sha256.Sum256(mfBytes)
	mfDigest := digest.Digest("sha256:" + hex.EncodeToString(mfSum[:]))

	mux := http.NewServeMux()
	authOk := func(r *http.Request) bool {
		if !requireAuth {
			return true
		}
		u, p, ok := r.BasicAuth()
		return ok && u == expectUser && p == expectPass
	}
	send401 := func(w http.ResponseWriter) {
		w.Header().Set("WWW-Authenticate", `Basic realm="r"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		// API probe
		if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
			if !authOk(r) {
				send401(w)
				return
			}
			w.WriteHeader(200)
			return
		}
		if !authOk(r) {
			send401(w)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/v1") || strings.HasSuffix(r.URL.Path, "/manifests/"+string(mfDigest)):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", string(mfDigest))
			w.Header().Set("Content-Length", fmt.Sprint(len(mfBytes)))
			if r.Method == http.MethodHead {
				w.WriteHeader(200)
				return
			}
			w.Write(mfBytes)
		case strings.HasSuffix(r.URL.Path, "/blobs/"+string(layerDigest)):
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", string(layerDigest))
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			if r.Method == http.MethodHead {
				w.WriteHeader(200)
				return
			}
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func registryHost(t *testing.T, srv *httptest.Server) string {
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestHandlePullHappyPath(t *testing.T) {
	payload := []byte("hello-image-bytes")
	srv := newFakeRegistry(t, payload, false, "", "")
	dst := filepath.Join("/tmp", fmt.Sprintf("oras-test-%d.bin", os.Getpid()))
	defer os.Remove(dst)

	req := &oraspb.PullRequest{
		Registry:   registryHost(t, srv),
		Repository: "ns/repo",
		Reference:  &oraspb.PullRequest_Tag{Tag: "v1"},
		LocalPath:  dst,
	}
	// Force plain HTTP: oras-go uses HTTPS by default. We need the test
	// registry on http, which we get by setting PlainHTTP after construction.
	// Easiest path: skip via newRepository and inline the call here.
	repo, err := newRepository(req)
	if err != nil {
		t.Fatal(err)
	}
	repo.PlainHTTP = true
	_ = repo // keep for parity

	stream := &fakeStream{ctx: context.Background()}
	if err := handlePullWithRepo(req, stream, repo); err != nil {
		t.Fatalf("HandlePull: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q want %q", got, payload)
	}
	// At least one Started + one Result.
	var sawStart, sawResult bool
	for _, m := range stream.sent {
		switch m.Event.(type) {
		case *oraspb.PullResponse_Started:
			sawStart = true
		case *oraspb.PullResponse_Result:
			sawResult = true
		}
	}
	if !sawStart || !sawResult {
		t.Errorf("missing events: started=%v result=%v sent=%d", sawStart, sawResult, len(stream.sent))
	}
}

func TestHandlePullAuthFailure(t *testing.T) {
	srv := newFakeRegistry(t, []byte("x"), true, "right", "right")
	dst := filepath.Join("/tmp", fmt.Sprintf("oras-test-%d.bin", os.Getpid()))
	defer os.Remove(dst)

	req := &oraspb.PullRequest{
		Registry:   registryHost(t, srv),
		Repository: "ns/repo",
		Reference:  &oraspb.PullRequest_Tag{Tag: "v1"},
		LocalPath:  dst,
		Auth: &oraspb.AuthConfig{Mode: &oraspb.AuthConfig_Basic{
			Basic: &oraspb.BasicAuth{Username: "wrong", Password: "wrong"},
		}},
	}
	repo, err := newRepository(req)
	if err != nil {
		t.Fatal(err)
	}
	repo.PlainHTTP = true
	stream := &fakeStream{ctx: context.Background()}
	err = handlePullWithRepo(req, stream, repo)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := mustStatus(t, err).Code(); got != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v (%v)", got, err)
	}
}

// errStream returns an error on Send; used to exercise the "stream.Send
// failed" branches in handlePullWithRepo.
type errStream struct {
	fakeStream
	sendErr error
}

func (s *errStream) Send(r *oraspb.PullResponse) error { return s.sendErr }

// newCustomRegistry serves a manifest+blob the test can fully control: the
// supplied manifest body and blob body are returned verbatim. Useful for
// injecting multi-layer manifests or a 500 on the blob endpoint.
func newCustomRegistry(t *testing.T, mfBytes []byte, mfDigest, layerDigest string, layerHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/manifests/v1") || strings.HasSuffix(r.URL.Path, "/manifests/"+mfDigest):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", mfDigest)
			w.Header().Set("Content-Length", fmt.Sprint(len(mfBytes)))
			if r.Method == http.MethodHead {
				w.WriteHeader(200)
				return
			}
			w.Write(mfBytes)
		case layerDigest != "" && strings.HasSuffix(r.URL.Path, "/blobs/"+layerDigest):
			layerHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// makeManifest returns serialized manifest bytes + its sha256 digest.
func makeManifest(t *testing.T, layers []ocispec.Descriptor) ([]byte, string) {
	t.Helper()
	m := ocispec.Manifest{MediaType: ocispec.MediaTypeImageManifest, Layers: layers}
	m.SchemaVersion = 2
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return b, "sha256:" + hex.EncodeToString(sum[:])
}

func mkReq(host, dst string) *oraspb.PullRequest {
	return &oraspb.PullRequest{
		Registry:   host,
		Repository: "ns/repo",
		Reference:  &oraspb.PullRequest_Tag{Tag: "v1"},
		LocalPath:  dst,
	}
}

func mkRepo(t *testing.T, req *oraspb.PullRequest) *remote.Repository {
	t.Helper()
	repo, err := newRepository(req)
	if err != nil {
		t.Fatal(err)
	}
	repo.PlainHTTP = true
	return repo
}

// TestHandlePullMultiLayerManifest: manifest with 2 layers must be rejected
// by pickSingleLayer.
func TestHandlePullMultiLayerManifest(t *testing.T) {
	layers := []ocispec.Descriptor{
		{MediaType: "application/octet-stream", Digest: digest.Digest("sha256:" + strings.Repeat("a", 64)), Size: 1},
		{MediaType: "application/octet-stream", Digest: digest.Digest("sha256:" + strings.Repeat("b", 64)), Size: 1},
	}
	mfBytes, mfDigest := makeManifest(t, layers)
	srv := newCustomRegistry(t, mfBytes, mfDigest, "", nil)
	dst := filepath.Join("/tmp", fmt.Sprintf("oras-test-%d.bin", os.Getpid()))
	defer os.Remove(dst)
	req := mkReq(registryHost(t, srv), dst)
	err := handlePullWithRepo(req, &fakeStream{ctx: context.Background()}, mkRepo(t, req))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := mustStatus(t, err).Code(); got != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v (%v)", got, err)
	}
}

// TestHandlePullStartedSendError: stream.Send for PullStarted fails. Covers
// the early Send-error branch.
func TestHandlePullStartedSendError(t *testing.T) {
	payload := []byte("x")
	srv := newFakeRegistry(t, payload, false, "", "")
	dst := filepath.Join("/tmp", fmt.Sprintf("oras-test-%d.bin", os.Getpid()))
	defer os.Remove(dst)
	req := mkReq(registryHost(t, srv), dst)
	stream := &errStream{
		fakeStream: fakeStream{ctx: context.Background()},
		sendErr:    fmt.Errorf("stream broken"),
	}
	err := handlePullWithRepo(req, stream, mkRepo(t, req))
	if err == nil || !strings.Contains(err.Error(), "stream broken") {
		t.Errorf("expected stream broken error, got %v", err)
	}
}

// TestHandlePullMkdirTempError: local_path under /tmp but the parent dir does
// not exist, so MkdirTemp fails.
func TestHandlePullMkdirTempError(t *testing.T) {
	payload := []byte("x")
	srv := newFakeRegistry(t, payload, false, "", "")
	dst := fmt.Sprintf("/tmp/no-such-dir-%d/out.bin", os.Getpid())
	req := mkReq(registryHost(t, srv), dst)
	err := handlePullWithRepo(req, &fakeStream{ctx: context.Background()}, mkRepo(t, req))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := mustStatus(t, err).Code(); got != codes.Internal {
		t.Errorf("expected Internal, got %v (%v)", got, err)
	}
}

// TestHandlePullBlobFetchError: server returns 500 on the blob endpoint, so
// fetchLayerWithProgress fails.
func TestHandlePullBlobFetchError(t *testing.T) {
	payload := []byte("hello")
	layerSum := sha256.Sum256(payload)
	layerDigest := "sha256:" + hex.EncodeToString(layerSum[:])
	layers := []ocispec.Descriptor{{
		MediaType: "application/octet-stream",
		Digest:    digest.Digest(layerDigest),
		Size:      int64(len(payload)),
	}}
	mfBytes, mfDigest := makeManifest(t, layers)
	srv := newCustomRegistry(t, mfBytes, mfDigest, layerDigest, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	dst := filepath.Join("/tmp", fmt.Sprintf("oras-test-%d.bin", os.Getpid()))
	defer os.Remove(dst)
	req := mkReq(registryHost(t, srv), dst)
	err := handlePullWithRepo(req, &fakeStream{ctx: context.Background()}, mkRepo(t, req))
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestCopyAndRemoveDstError covers the dst-open failure branch of copyAndRemove.
func TestCopyAndRemoveDstError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Make a read-only subdir and aim dst into it.
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0700) })
	if err := copyAndRemove(src, filepath.Join(ro, "dst")); err == nil {
		t.Errorf("expected dst-open error")
	}
}
