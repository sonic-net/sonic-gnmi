package gnmi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	//"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

var testHealthzCases = []struct {
	desc string
	f    func(ctx context.Context, t *testing.T, sc healthz.HealthzClient)
}{
	{
		desc: "HealthzGetFailsForInvalidComponent",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			_, err := sc.Get(ctx, &healthz.GetRequest{})
			testErr(err, codes.Unimplemented, "Healthz.Get is unimplemented", t)
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
		desc: "TestGetDebugData-Marshal error",
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
		desc: "TestGetDebugData-NewDbusClient Error",
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
		desc: "Get fail with Authentication error",
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
		desc: "TestGetDebugData_HealthzCollect_DBus_Error",
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
		desc: "TestGetDebugData-WaitForArtifact error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			dummy_path := &types.Path{}

			patch1 := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return &ssc.FakeClient{CollectResponse: "/tmp/fakefile"}, nil
			})
			patch2 := gomonkey.ApplyFunc(waitForArtifact, func(string) error {
				return fmt.Errorf("timeout")
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
		desc: "TestWaitForArtifact_NewDbusClientError",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			// Patch NewDbusClient to return an error
			patch := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, nil, fmt.Errorf("dbus connection failed"))
			defer patch.Reset()

			err := waitForArtifact("any-file")
			if err == nil {
				t.Errorf("Expected error from NewDbusClient, got nil")
			}
			if err.Error() != "dbus connection failed" {
				t.Errorf("Unexpected error message: %v", err)
			}
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
		desc: "Test getDebugData fails with os.ReadFile error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			dummy_path := &types.Path{}
			path := "/tmp/nonexistent_artifact"
			patch1 := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return &ssc.FakeClient{CollectResponse: path}, nil
			})
			patch2 := gomonkey.ApplyFunc(waitForArtifact, func(string) error {
				return nil
			})
			defer patch1.Reset()
			defer patch2.Reset()

			_ = os.Remove(path) // Ensure the file doesn't exist
			_, err := getDebugData(dummy_path)
			if err == nil || !strings.Contains(err.Error(), "no such file") {
				t.Errorf("Expected file read error, got: %v", err)
			}
		},
	},
	{

		desc: "Test getDebugData for Success Path",
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
			// Setup dummy file for artifact
			dummyFile := "fake-collect-success"
			dummyData := []byte("dummy log data")
			_ = os.WriteFile(dummyFile, dummyData, 0644)
			defer os.Remove(dummyFile)

			patch1 := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return &ssc.FakeClient{CollectResponse: dummyFile}, nil
			})
			patch2 := gomonkey.ApplyFunc(waitForArtifact, func(string) error {
				return nil
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
			patches.ApplyFunc(waitForArtifact, func(path string) error {
				return nil
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
