package gnmi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type healthzArtifactTestStream struct {
	healthz.Healthz_ArtifactServer
	ctx       context.Context
	responses []*healthz.ArtifactResponse
	send      func(*healthz.ArtifactResponse) error
}

func (s *healthzArtifactTestStream) Context() context.Context {
	return s.ctx
}

func (s *healthzArtifactTestStream) Send(resp *healthz.ArtifactResponse) error {
	s.responses = append(s.responses, resp)
	if s.send != nil {
		return s.send(resp)
	}
	return nil
}

func newHealthzArtifactTestServer() *HealthzServer {
	return &HealthzServer{Server: &Server{config: &Config{}}}
}

var testHealthzCases = []struct {
	desc string
	f    func(ctx context.Context, t *testing.T, sc healthz.HealthzClient)
}{
	{
		desc: "HealthzGetForInvalidPaths",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			req := &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{
							Name: "invalid",
						},
						{
							Name: "component",
							Key: map[string]string{
								"name": "all",
							},
						},
						{
							Name: "healthz",
						},
						{
							Name: "alert-info",
						},
					},
				},
			}
			_, err := sc.Get(ctx, req)
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)

			req = &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{
							Name: "components",
						},
						{
							Name: "invalid",
							Key: map[string]string{
								"name": "all",
							},
						},
						{
							Name: "healthz",
						},
						{
							Name: "alert-info",
						},
					},
				},
			}
			_, err = sc.Get(ctx, req)
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)

			req = &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{
							Name: "components",
						},
						{
							Name: "component",
							Key: map[string]string{
								"invalid": "all",
							},
						},
						{
							Name: "healthz",
						},
						{
							Name: "alert-info",
						},
					},
				},
			}
			_, err = sc.Get(ctx, req)
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)

			req = &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{
							Name: "components",
						},
						{
							Name: "component",
							Key: map[string]string{
								"name": "all",
							},
						},
						{
							Name: "invalid",
						},
						{
							Name: "alert-info",
						},
					},
				},
			}
			_, err = sc.Get(ctx, req)
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)

			req = &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{
							Name: "components",
						},
						{
							Name: "component",
							Key: map[string]string{
								"name": "all",
							},
						},
						{
							Name: "healthz",
						},
						{
							Name: "invalid",
						},
					},
				},
			}
			_, err = sc.Get(ctx, req)
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)
		},
	},
	{
		desc: "GetDebugData_Marshal_Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			dummy_path := &types.Path{}

			patch := gomonkey.ApplyFunc(json.Marshal, func(v interface{}) ([]byte, error) {
				return nil, fmt.Errorf("marshal error")
			})
			defer patch.Reset()
			_, err := getDebugData(dummy_path)
			if err == nil || !strings.Contains(err.Error(), "marshal error") {
				t.Errorf("Expected marshal error, got: %v", err)
			}
		},
	},
	{
		desc: "GetDebugData_NewDbusClient_Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			dummy_path := &types.Path{}

			patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return nil, fmt.Errorf("dbus creation failed")
			})
			defer patch.Reset()

			_, err := getDebugData(dummy_path)
			if err == nil || !strings.Contains(err.Error(), "dbus creation failed") {
				t.Errorf("Expected dbus client creation error, got: %v", err)
			}
		},
	},
	{
		desc: "Get_Fail_Authentication error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			patch := gomonkey.ApplyFuncReturn(authenticate, nil, status.Error(codes.Unauthenticated, "unauthenticated"))
			defer patch.Reset()
			// Healthz Get
			req := &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{
							Name: "components",
						},
						{
							Name: "component",
							Key: map[string]string{
								"name": "all",
							},
						},
						{
							Name: "healthz",
						},
						{
							Name: "alert-info",
						},
					},
				},
			}
			resp, err := sc.Get(ctx, req)

			if err == nil {
				t.Errorf("Expected authentication error, got nil")
			}
			if status.Code(err) != codes.Unauthenticated {
				t.Errorf("Expected Unauthenticated error, got: %v", err)
			}
			if resp != nil {
				t.Errorf("Expected nil response, got: %+v", resp)
			}
		},
	},
	{
		desc: "GetDebugData_HealthzCollect_DBus_Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			fakeclient := &ssc.FakeClientWithError{}

			// Patch NewDbusClient to return fakeClient
			patches := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return fakeclient, nil
			})
			defer patches.Reset()

			// Call getDebugData
			path := &types.Path{} // dummy value
			resp, err := getDebugData(path)

			// Validate
			if err == nil {
				t.Errorf("Expected error, got nil")
			}
			if resp != nil {
				t.Errorf("Expected nil response, got: %+v", resp)
			}
			if !strings.Contains(err.Error(), "Host service error") {
				t.Errorf("Expected Host service error, got: %v", err)
			}
		},
	},

	{
		desc: "GetDebugData_WaitForArtifact_error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			dummy_path := &types.Path{}

			patch1 := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return &ssc.FakeClient{CollectResponse: "/tmp/fakefile"}, nil
			})
			patch2 := gomonkey.ApplyFunc(waitForArtifact, func(string) (string, error) {
				return "", fmt.Errorf("timeout")
			})
			defer patch1.Reset()
			defer patch2.Reset()

			_, err := getDebugData(dummy_path)
			if err == nil || !strings.Contains(err.Error(), "timeout") {
				t.Errorf("Expected wait timeout error, got: %v", err)
			}
		},
	},

	{
		desc: "WaitForArtifact_NewDbusClient_Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			// Patch NewDbusClient to return an error
			patch := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, nil, fmt.Errorf("dbus connection failed"))
			defer patch.Reset()

			_, err := waitForArtifact("any-file")
			if err == nil {
				t.Errorf("Expected error from NewDbusClient, got nil")
			}
			if err.Error() != "dbus connection failed" {
				t.Errorf("Unexpected error message: %v", err)
			}
		},
	},
	{

		desc: "GetDebugData_Success_Path",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			//defaultPath := &types.Path{}
			defaultPath := &types.Path{
				Elem: []*types.PathElem{
					{Name: "components"},
					{Name: "component", Key: map[string]string{"name": "chassis"}},
					{Name: "logging"},
					{Name: "log-level-alert"},
				},
			}
			// Create both host and container path versions
			dummy_hostfile := "/tmp/dump/fake-collect-success"
			dummy_containerfile := "/mnt/host/tmp/dump/fake-collect-success"
			dummyData := []byte("dummy log data")

			_ = os.MkdirAll(filepath.Dir(dummy_containerfile), 0755)
			if err := os.WriteFile(dummy_containerfile, dummyData, 0644); err != nil {
				t.Fatalf("failed to create test artifact file: %v", err)
			}
			defer os.Remove(dummy_containerfile)

			patch1 := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return &ssc.FakeClient{CollectResponse: dummy_hostfile}, nil
			})
			patch2 := gomonkey.ApplyFunc(waitForArtifact, func(string) (string, error) {
				return dummy_hostfile, nil
			})
			defer patch1.Reset()
			defer patch2.Reset()

			resp, err := getDebugData(defaultPath)
			if err != nil {
				t.Fatalf("Expected success, got error: %v", err)
			}
			if resp == nil || len(resp.Component.Artifacts) != 1 {
				t.Fatalf("Expected one artifact in response")
			}

			// Validate hash
			expectedHash := sha256.Sum256(dummyData)
			gotHash := resp.Component.Artifacts[0].GetFile().Hash.Hash
			if !reflect.DeepEqual(expectedHash[:], gotHash) {
				t.Errorf("SHA256 hash mismatch: got %x, want %x", gotHash, expectedHash)
			}
		},
	},
	{
		desc: "HealthzGetForValidPaths",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Patch DBus client creation
			patches.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)

			// Patch ReadFile
			patches.ApplyFunc(os.ReadFile, func(name string) ([]byte, error) {
				return []byte("fake content"), nil
			})

			// Patch waitForArtifact
			patches.ApplyFunc(waitForArtifact, func(path string) (string, error) {
				return "", nil
			})

			// Test 1: /components/component[name=healthz]/alert-info
			req := &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{Name: "components"},
						{
							Name: "component",
							Key:  map[string]string{"name": "healthz"},
						},
						{Name: "alert-info"},
					},
				},
			}
			_, err := sc.Get(ctx, req)
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)

			// Test 2: /components/component[name=healthz]/critical-info
			req = &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{Name: "components"},
						{
							Name: "component",
							Key:  map[string]string{"name": "healthz"},
						},
						{Name: "critical-info"},
					},
				},
			}
			_, err = sc.Get(ctx, req)
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)

			// Test 3: /components/component[name=healthz]/all-info
			req = &healthz.GetRequest{
				Path: &types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
						{Name: "components"},
						{
							Name: "component",
							Key:  map[string]string{"name": "healthz"},
						},
						{Name: "all-info"},
					},
				},
			}
			_, err = sc.Get(ctx, req)
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)
		},
	},
	{
		desc: "TestgetDebugData_emptyPath",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			if p := isDebugData(nil); p != false {
				t.Errorf("expected false for nil path, got %v", p)
			}
		},
	},
	{
		desc: "HealthzCheck_SuccessPath",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			artifactColTimeout = 40 * time.Millisecond
			artifactSleepTime = 5 * time.Millisecond

			fakeclient := &ssc.FakeClientWithError{}
			patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return fakeclient, nil
			})
			defer patch.Reset()

			result, err := waitForArtifact("/tmp/fakefile")
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if result != "fake-check-success" {
				t.Errorf("expected 'fake-check-success', got: %s", result)
			}
		},
	},

	{
		desc: "HealthzListFailsForInvalidComponent",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			_, err := sc.List(ctx, &healthz.ListRequest{})
			testErr(err, codes.Unimplemented, "gNOI Healthz List not implemented", t)
		},
	},
	{
		desc: "HealthzCheckFailsForInvalidComponent",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			_, err := sc.Check(ctx, &healthz.CheckRequest{})
			testErr(err, codes.Unimplemented, "gNOI Healthz Check not implemented", t)
		},
	},
	{
		desc: "Acknowledge fails with Authentication_Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			patch := gomonkey.ApplyFuncReturn(authenticate, nil, status.Error(codes.Unauthenticated, "unauthenticated"))
			defer patch.Reset()
			req := &healthz.AcknowledgeRequest{Id: "ack-event"}

			resp, err := sc.Acknowledge(ctx, req)

			if err == nil {
				t.Errorf("Expected authentication error, got nil")
			}
			if status.Code(err) != codes.Unauthenticated {
				t.Errorf("Expected Unauthenticated error, got: %v", err)
			}
			if resp != nil {
				t.Errorf("Expected nil response, got: %+v", resp)
			}
		},
	},
	{
		desc: "TestHealthzServer_Acknowledge",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			fakeClient := &ssc.FakeClient{}

			// Patch NewDbusClient to return the fake client
			patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return fakeClient, nil
			})
			defer patch.Reset()

			// Create a request with a valid ID
			req := &healthz.AcknowledgeRequest{
				Id: "ack-event",
			}

			// Call Acknowledge
			resp, err := sc.Acknowledge(ctx, req)

			if err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
			if resp == nil {
				t.Errorf("Expected non-nil response, got nil")
			}
		},
	},
	{
		desc: "TestHealthzServer_Acknowledge_DBUS_Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			fakeClient := &ssc.FakeClientWithError{}

			// Patch NewDbusClient to return the fake client
			patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return fakeClient, nil
			})
			defer patch.Reset()

			// Create a request with a valid ID
			req := &healthz.AcknowledgeRequest{
				Id: "ack-event",
			}

			// Call Acknowledge
			resp, err := sc.Acknowledge(ctx, req)
			if err == nil {
				t.Errorf("Expected error, got nil")
			}
			if resp != nil {
				t.Errorf("Expected nil response, got: %+v", resp)
			}
		},
	},
	{
		desc: "Acknowledge_NewDbusClient_Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			// Patch NewDbusClient to return an error
			patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return nil, fmt.Errorf("simulated dbus client creation error")
			})
			defer patch.Reset()
			req := &healthz.AcknowledgeRequest{Id: "ack-event"}
			resp, err := sc.Acknowledge(ctx, req)

			if err == nil {
				t.Errorf("Expected error due to client creation failure, got nil")
			}

			if resp != nil {
				t.Errorf("Expected nil response, got: %+v", resp)
			}
		},
	},
	{
		desc: "TestHealthzArtifact_FileNotFound",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			srv := newHealthzArtifactTestServer()
			req := &healthz.ArtifactRequest{Id: "/tmp/dump/nonexistent_file.txt"}
			mockStream := &healthzArtifactTestStream{ctx: ctx}

			err := srv.Artifact(req, mockStream)
			if err == nil {
				t.Fatalf("expected error for nonexistent file, got nil")
			}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error, got: %v", err)
			}
			if st.Code() != codes.NotFound && !strings.Contains(st.Message(), "no such file") {
				t.Errorf("expected NotFound, got %v (message: %s)", st.Code(), st.Message())
			}
		},
	},
	{
		desc: "TestHealthzArtifact_InvalidPath",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			srv := newHealthzArtifactTestServer()
			req := &healthz.ArtifactRequest{Id: "/invalid/path/file.txt"}
			mockStream := &healthzArtifactTestStream{ctx: ctx}

			err := srv.Artifact(req, mockStream)
			if err == nil {
				t.Fatalf("expected error for invalid path, got nil")
			}
			st, _ := status.FromError(err)
			if st.Code() != codes.InvalidArgument {
				t.Errorf("expected NotFound, got %v", st.Code())
			}
		},
	},
	{
		desc: "TestHealthzArtifact_ValidPath",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			srv := newHealthzArtifactTestServer()
			req := &healthz.ArtifactRequest{Id: "/tmp/dump/valid.txt"}
			content := []byte("this is valid test content")

			file, err := os.CreateTemp(t.TempDir(), "healthz-artifact-*")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			defer file.Close()
			if _, err := file.Write(content); err != nil {
				t.Fatalf("failed to write temp file: %v", err)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				t.Fatalf("failed to reset temp file: %v", err)
			}

			patch := gomonkey.ApplyFunc(os.Open, func(path string) (*os.File, error) {
				want := healthzArtifactPath(req.GetId())
				if path != want {
					t.Fatalf("os.Open(%q), want %q", path, want)
				}
				return file, nil
			})
			defer patch.Reset()

			mockStream := &healthzArtifactTestStream{ctx: ctx}

			if err := srv.Artifact(req, mockStream); err != nil {
				t.Fatalf("Artifact() returned error: %v", err)
			}
			if len(mockStream.responses) != 3 {
				t.Fatalf("Artifact() sent %d responses, want 3", len(mockStream.responses))
			}
			if _, ok := mockStream.responses[0].Contents.(*healthz.ArtifactResponse_Header); !ok {
				t.Fatalf("first response is %T, want header", mockStream.responses[0].Contents)
			}
			bytesResp, ok := mockStream.responses[1].Contents.(*healthz.ArtifactResponse_Bytes)
			if !ok {
				t.Fatalf("second response is %T, want bytes", mockStream.responses[1].Contents)
			}
			if string(bytesResp.Bytes) != string(content) {
				t.Fatalf("streamed content = %q, want %q", bytesResp.Bytes, content)
			}
			if _, ok := mockStream.responses[2].Contents.(*healthz.ArtifactResponse_Trailer); !ok {
				t.Fatalf("third response is %T, want trailer", mockStream.responses[2].Contents)
			}
		},
	},
	{
		desc: "TestHealthzArtifact_SeekFailure",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			srv := newHealthzArtifactTestServer()
			req := &healthz.ArtifactRequest{Id: "/tmp/dump/seek_fail.txt"}

			realPath := "/mnt/host/tmp/dump/seek_fail.txt"
			_ = os.MkdirAll(filepath.Dir(realPath), 0755)
			_ = os.WriteFile(realPath, []byte("dummy"), 0644)
			defer os.Remove(realPath)

			patch := gomonkey.ApplyMethod(reflect.TypeOf(&os.File{}), "Seek",
				func(_ *os.File, offset int64, whence int) (int64, error) {
					return 0, fmt.Errorf("seek fail simulated")
				},
			)
			defer patch.Reset()
			mockStream := &healthzArtifactTestStream{ctx: ctx}
			err := srv.Artifact(req, mockStream)
			if err == nil {
				t.Fatalf("expected seek failure, got nil")
			}
			st, _ := status.FromError(err)
			if st.Code() != codes.Internal {
				t.Errorf("expected Internal seek failure, got %v", st.Code())
			}
		},
	},
	{
		desc: "TestHealthzArtifact_HeaderSendFailure",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			srv := newHealthzArtifactTestServer()
			req := &healthz.ArtifactRequest{Id: "/tmp/dump/header_fail.txt"}

			// Create a valid file
			realPath := "/mnt/host/tmp/dump/header_fail.txt"
			_ = os.MkdirAll(filepath.Dir(realPath), 0755)
			_ = os.WriteFile(realPath, []byte("dummy data for header test"), 0644)
			defer os.Remove(realPath)

			mockStream := &healthzArtifactTestStream{
				ctx: ctx,
				send: func(resp *healthz.ArtifactResponse) error {
					if _, ok := resp.Contents.(*healthz.ArtifactResponse_Header); ok {
						return fmt.Errorf("simulated header send failure")
					}
					return nil
				},
			}

			err := srv.Artifact(req, mockStream)
			if err == nil {
				t.Fatalf("expected header send failure, got nil")
			}

			st, _ := status.FromError(err)
			if st.Code() != codes.Unknown {
				t.Errorf("expected Unknown error for header send failure, got %v", st.Code())
			}

			if len(mockStream.responses) != 1 {
				t.Errorf("expected Send to be called once (for header), got %d", len(mockStream.responses))
			}
		},
	},
	{
		desc: "TestHealthzArtifact_TrailerSendFailure",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			srv := newHealthzArtifactTestServer()
			req := &healthz.ArtifactRequest{Id: "/tmp/dump/trailer_fail.txt"}

			// Prepare a valid file
			realPath := "/mnt/host/tmp/dump/trailer_fail.txt"
			_ = os.MkdirAll(filepath.Dir(realPath), 0755)
			_ = os.WriteFile(realPath, []byte("dummy file"), 0644)
			defer os.Remove(realPath)

			mockStream := &healthzArtifactTestStream{
				ctx: ctx,
				send: func(resp *healthz.ArtifactResponse) error {
					if _, ok := resp.Contents.(*healthz.ArtifactResponse_Trailer); ok {
						return fmt.Errorf("simulated trailer send failure")
					}
					return nil
				},
			}

			err := srv.Artifact(req, mockStream)
			if err == nil {
				t.Fatalf("expected trailer send failure, got nil")
			}

			st, _ := status.FromError(err)
			if st.Code() != codes.Unknown {
				t.Errorf("expected Unknown (wrapped plain error), got %v", st.Code())
			}
		},
	},
	{
		desc: "TestHealthzArtifact_FileReadFailure",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			srv := newHealthzArtifactTestServer()
			req := &healthz.ArtifactRequest{Id: "/tmp/dump/read_fail.txt"}

			realPath := "/mnt/host/tmp/dump/read_fail.txt"
			_ = os.MkdirAll(filepath.Dir(realPath), 0755)
			_ = os.WriteFile(realPath, []byte("dummy"), 0644)
			defer os.Remove(realPath)

			f, err := os.Open(realPath)
			if err != nil {
				t.Fatalf("failed to open test file: %v", err)
			}
			defer f.Close()

			// Patch os.File.Read to simulate read error (not EOF)
			patch := gomonkey.ApplyMethod(
				reflect.TypeOf(f), "Read",
				func(_ *os.File, b []byte) (int, error) {
					return 0, fmt.Errorf("simulated read error")
				},
			)
			defer patch.Reset()

			mockStream := &healthzArtifactTestStream{ctx: ctx}

			err = srv.Artifact(req, mockStream)
			if err == nil {
				t.Fatalf("expected read failure, got nil")
			}

			st, _ := status.FromError(err)
			if st.Code() != codes.Internal {
				t.Errorf("expected Internal for read failure, got %v", st.Code())
			}
		},
	},
	{
		desc: "TestHealthzArtifact_ChunkSendFailure",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			srv := newHealthzArtifactTestServer()
			req := &healthz.ArtifactRequest{Id: "/tmp/dump/chunk_fail.txt"}

			realPath := "/mnt/host/tmp/dump/chunk_fail.txt"
			_ = os.MkdirAll(filepath.Dir(realPath), 0755)
			_ = os.WriteFile(realPath, make([]byte, 8192), 0644) // file > ddFileSegSize (4096)
			defer os.Remove(realPath)

			mockStream := &healthzArtifactTestStream{
				ctx: ctx,
				send: func(resp *healthz.ArtifactResponse) error {
					if _, ok := resp.Contents.(*healthz.ArtifactResponse_Bytes); ok {
						return fmt.Errorf("simulated chunk send failure")
					}
					return nil
				},
			}

			err := srv.Artifact(req, mockStream)
			if err == nil {
				t.Fatalf("expected chunk send failure, got nil")
			}

			st, _ := status.FromError(err)
			if st.Code() != codes.Unknown {
				t.Errorf("expected Unknown for chunk send failure, got %v", st.Code())
			}

			if len(mockStream.responses) < 2 {
				t.Errorf("expected Send called for header + chunk, got %d", len(mockStream.responses))
			}
		},
	},
}

func TestHealthzArtifactAuthorizationPrecedesRequestAndFileHandling(t *testing.T) {
	srv := newHealthzArtifactTestServer()
	streamCtx := context.WithValue(context.Background(), struct{}{}, "artifact-auth")
	stream := &healthzArtifactTestStream{ctx: streamCtx}
	authErr := status.Error(codes.PermissionDenied, "artifact read denied")

	authCalls := 0
	openCalls := 0
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(authenticate, func(config *Config, ctx context.Context, target string, writeAccess bool) (context.Context, error) {
		authCalls++
		if config != srv.config {
			t.Errorf("authenticate config = %p, want %p", config, srv.config)
		}
		if ctx != streamCtx {
			t.Errorf("authenticate context = %v, want stream context %v", ctx, streamCtx)
		}
		if target != "gnoi" {
			t.Errorf("authenticate target = %q, want gnoi", target)
		}
		if writeAccess {
			t.Error("authenticate requested write access, want read access")
		}
		return ctx, authErr
	})
	patches.ApplyFunc(os.Open, func(string) (*os.File, error) {
		openCalls++
		return nil, fmt.Errorf("os.Open must not be called after authorization denial")
	})

	for _, id := range []string{"/invalid/path/file.txt", "/tmp/dump/artifact.txt"} {
		err := srv.Artifact(&healthz.ArtifactRequest{Id: id}, stream)
		if err != authErr {
			t.Errorf("Artifact(%q) error = %v, want exact authentication error %v", id, err, authErr)
		}
	}

	if authCalls != 2 {
		t.Errorf("authenticate called %d times, want 2", authCalls)
	}
	if openCalls != 0 {
		t.Errorf("os.Open called %d times after authorization denial, want 0", openCalls)
	}
	if len(stream.responses) != 0 {
		t.Errorf("stream sent %d responses after authorization denial, want 0", len(stream.responses))
	}
}

// TestHealthzServer tests implementation of gnoi.Healthz server.
func TestHealthzServer(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	targetAddr := "127.0.0.1:8081"
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, test := range testHealthzCases {
		t.Run(test.desc, func(t *testing.T) {
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()
			sc := healthz.NewHealthzClient(conn)
			test.f(ctx, t, sc)
		})
	}
}
