package gnmi

import (
	//"bytes"
	"context"
	//"crypto/rand"
	"crypto/tls"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	//"io"
	"os"
	"testing"
	//"time"
	"encoding/json"
	"fmt"
	"strings"
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
			defaultPath := &types.Path{
				Elem: []*types.PathElem{
					{Name: "components"},
					{Name: "component", Key: map[string]string{"name": "chassis"}},
					{Name: "logging"},
					{Name: "log-level-alert"},
				},
			}
			patch := gomonkey.ApplyFunc(json.Marshal, func(v interface{}) ([]byte, error) {
				return nil, fmt.Errorf("marshal error")
			})
			defer patch.Reset()
			_, err := getDebugData(defaultPath)
			if err == nil || !strings.Contains(err.Error(), "marshal error") {
				t.Errorf("Expected marshal error, got: %v", err)
			}
		},
	},
	{
		desc: "TestGetDebugData-NewDbusClient Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			defaultPath := &types.Path{
				Elem: []*types.PathElem{
					{Name: "components"},
					{Name: "component", Key: map[string]string{"name": "chassis"}},
					{Name: "logging"},
					{Name: "log-level-alert"},
				},
			}
			patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return nil, fmt.Errorf("dbus creation failed")
			})
			defer patch.Reset()

			_, err := getDebugData(defaultPath)
			if err == nil || !strings.Contains(err.Error(), "dbus creation failed") {
				t.Errorf("Expected dbus client creation error, got: %v", err)
			}
		},
	},
	{
		desc: "TestGetDebugData-HealthzCollect Error",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			defaultPath := &types.Path{
				Elem: []*types.PathElem{
					{Name: "components"},
					{Name: "component", Key: map[string]string{"name": "chassis"}},
					{Name: "logging"},
					{Name: "log-level-alert"},
				},
			}
			patch1 := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
				return &ssc.FakeClient{CollectErr: fmt.Errorf("collect failed")}, nil
			})
			defer patch1.Reset()

			_, err := getDebugData(defaultPath)
			//testErr(err, codes.Unimplemented, "Expected Host Service error", t)
			if err == nil || !strings.Contains(err.Error(), "Host service error") {
				t.Errorf("Expected Host service error, got: %v", err)
			}
		},
	},
	{
		desc: "HealthzGetForValidPaths",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			//fakeClient := &ssc.FakeDbusClient{}

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
			/*
				if err != nil {
					t.Errorf("Healthz.Get failed for alert-info path: %v", err)
				} else if resp.GetComponent() == nil {
					t.Errorf("Healthz.Get response for alert-info is nil")
				} else {
					t.Logf("Got response for alert-info: Status=%v, ID=%v", resp.Component.Status, resp.Component.Id)
				}*/

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
			/*
				if err != nil {
					t.Errorf("Healthz.Get failed for critical-info path: %v", err)
				} else if resp.GetComponent() == nil {
					t.Errorf("Healthz.Get response for critical-info is nil")
				} else {
					t.Logf("Got response for critical-info: Status=%v, ID=%v", resp.Component.Status, resp.Component.Id)
				}*/

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
			/*
				if err != nil {
					t.Errorf("Healthz.Get failed for all-info path: %v", err)
				} else if resp.GetComponent() == nil {
					t.Errorf("Healthz.Get response for all-info is nil")
				} else {
					t.Logf("Got response for all-info: Status=%v, ID=%v", resp.Component.Status, resp.Component.Id)
				}*/
		},
	},
	/*{
		desc: "HealthzGetForDebugDataFailsForHostServiceError",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {

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
			_, err := sc.Get(ctx, req)
			testErr(err, codes.Internal, "Error", t)
		},
	},
	{
		desc: "HealthzGetForDebugDataFailsForHostServiceErrorCode",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()
			patches.ApplyFuncReturn(authenticate, nil, nil)
			patches.gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)

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
			_, err := sc.Get(ctx, req)
			testErr(err, codes.Internal, "Host service error", t)
		},
	},
	{
		desc: "HealthzGetForDebugDataFailsForNotExistingFile",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {

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
			_, err := sc.Get(ctx, req)
			testErr(err, codes.Internal, "Error", t)
		},
	},
	{
		desc: "HealthzGetForDebugDataFailsForCheckError",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			artifactColTimeout = 30 * time.Second

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
			_, err := sc.Get(ctx, req)
			testErr(err, codes.Internal, "Error", t)
		},
	},
	{
		desc: "HealthzAcknowledgeForDebugDataForHostServiceError",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			req := &healthz.AcknowledgeRequest{
				Id: "file",
			}
			_, err := sc.Acknowledge(ctx, req)
			testErr(err, codes.Internal, "Error", t)
		},
	},
	{
		desc: "HealthzAcknowledgeForDebugDataForHostServiceErrorCode",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			req := &healthz.AcknowledgeRequest{
				Id: "file",
			}
			_, err := sc.Acknowledge(ctx, req)
			testErr(err, codes.Internal, "Host service error", t)
		},
	},
	{
		desc: "HealthzGetForDebugDataSucceeds",
		f: func(ctx context.Context, t *testing.T, sc healthz.HealthzClient) {
			testFile := "test_file"

			// Create test file
			file := make([]byte, 1048576)
			if _, err := rand.Read(file); err != nil {
				t.Fatal("Fail to generate random file")
			}
			if err := os.WriteFile(testFile, file, 0777); err != nil {
				t.Fatal("Fail to generate random file")
			}
			defer func() {
				os.Remove(testFile)
			}()

			// Healthz Get
			getReq := &healthz.GetRequest{
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
			getResp, err := sc.Get(ctx, getReq)
			if err != nil {
				t.Fatal("Expected success, got ", err.Error())
			}
			if len(getResp.GetComponent().GetArtifacts()) != 1 {
				t.Fatal("Expected 1 artifact, got ", len(getResp.GetComponent().GetArtifacts()))
			}

			// Healthz Artifact
			artifactReq := &healthz.ArtifactRequest{
				Id: getResp.GetComponent().GetArtifacts()[0].GetId(),
			}
			stream, err := sc.Artifact(ctx, artifactReq)
			if err != nil {
				t.Fatal("Expected success, got ", err.Error())
			}
			var artifact []byte
			for {
				artifactResp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal("Expected success, got ", err.Error())
				}
				if b, ok := artifactResp.GetContents().(*healthz.ArtifactResponse_Bytes); ok {
					artifact = append(artifact, b.Bytes...)
				}
			}
			if bytes.Compare(artifact, file) != 0 {
				t.Fatal("Artifcat files corrupted")
			}

			// Healthz Acknowledge
			acknowledgeReq := &healthz.AcknowledgeRequest{
				Id: getResp.GetComponent().GetArtifacts()[0].GetId(),
			}
			if _, err := sc.Acknowledge(ctx, acknowledgeReq); err != nil {
				t.Fatal("Expected success, got ", err.Error())
			}
		},
	},*/
}

// TestHealthzServer tests implementation of gnoi.Healthz server.
func TestHealthzServer(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()
	targetAddr := "127.0.0.1:8081"
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	//conn, err := grpc.Dial(targetAddr, opts...)
	//if err != nil {
	//	t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	//}
	//defer conn.Close()
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
