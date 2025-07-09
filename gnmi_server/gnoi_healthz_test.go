package gnmi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"io"
	"os"
	"testing"
	"time"
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
	},*/
	{
		desc: "HealthzGetForDebugDataFailsForHostServiceErrorCode",
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
