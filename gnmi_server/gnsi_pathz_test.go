package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"     // Added for net.TCPAddr and net.ParseIP
	"net/url" // Added for url.Parse
	"os"
	"sync"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnsi/pathz"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	// Pathz is a location of the Pathz Policy
	pathzTestPolicyFile = "../testdata/gnsi/pathz_policy.pb.txt"
	pathzTestMetaFile   = "../testdata/gnsi/pathz-version.json"
	port                = 8081
)

var (
	TestPathzPolicyFile string // Global variable to hold policy path
	TestPathzMetaFile   string // Global variable to hold meta path
)

func createPathzServer(t *testing.T, portPathz int64) *Server {
	t.Helper()
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{
		Port:                portPathz,
		EnableTranslibWrite: true,
		UserAuth: AuthTypes{
			"password": true,
			"cert":     true,
			"jwt":      true,
		},
		ImgDir:          "/tmp",
		PathzMetaFile:   TestPathzMetaFile,
		PathzPolicyFile: TestPathzPolicyFile,
		PathzPolicy:     true,
	}
	s, err := NewServer(cfg, opts, nil)
	if err != nil {
		t.Fatalf("Failed to create Pathz server: %v", err)
	}
	return s
}

var pathzRotationTestCases = []struct {
	desc string
	f    func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server)
}{
	{
		desc: "RotateOpenClose",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			stream.CloseSend()
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "RotateStreamRecvError",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			// 0) Open the streaming RPC.
			// We use a context with a short timeout, and then cancel it later to trigger the error.
			shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			stream, err := sc.Rotate(shortCtx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}

			// 1) Send a valid policy upload request to move the server past the auth check
			// and into the main `for` loop. This also creates a backup file.
			err = stream.Send(&pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						Version:   generatePathzVersion(),
						CreatedOn: generatePathzCreatedOn(),
						Policy: &pathz.AuthorizationPolicy{
							Rules: []*pathz.AuthorizationRule{
								&pathz.AuthorizationRule{
									Id:        "Rule1",
									Principal: &pathz.AuthorizationRule_User{User: "User1"},
									Path: &gnmipb.Path{
										Elem: []*gnmipb.PathElem{
											&gnmipb.PathElem{
												Name: "a",
											},
											&gnmipb.PathElem{
												Name: "b",
												Key: map[string]string{
													"k1": "v1",
													"k2": "v2",
												},
											},
										},
									},
									Action: pathz.Action_ACTION_PERMIT,
									Mode:   pathz.Mode_MODE_READ,
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 2) Receive the confirmation response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUpload(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}

			// 3) Cancel the client-side context.
			cancel()

			// 4) Attempt to receive the next message. This should fail with a non-EOF error.
			_, err = stream.Recv()

			// We expect a non-nil error.
			if err == nil {
				t.Fatal("Expected an error (e.g., context canceled) from stream.Recv()")
			}

			// The server wraps the `stream.Recv()` error and returns it with codes.Aborted.
			// The original error inside the server will be a gRPC/context error (e.g., codes.Canceled or DeadlineExceeded).
			if status.Code(err) != codes.Canceled {
				t.Fatalf("Expected codes.Canceled error from client due to context cancellation, got: %v", status.Code(err))
			}
		},
	},
	{
		desc: "RotateStreamSendError",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			// 0) Create a temporary, separate connection just for this test, as we must close it prematurely.
			tlsConfig := &tls.Config{InsecureSkipVerify: true}
			cred := &loginCreds{Username: testUsername, Password: testPassword}
			opts := []grpc.DialOption{
				grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
				grpc.WithPerRPCCredentials(cred),
			}
			targetAddr := fmt.Sprintf("127.0.0.1:%d", port)

			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			// NOTE: Defer conn.Close() is NOT used as we close it manually.

			tempClient := pathz.NewPathzClient(conn)
			// Open the streaming RPC.
			stream, err := tempClient.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				conn.Close()
				t.Fatal(err.Error())
			}

			// 1) Send a valid policy upload request. This will cause the server to process it
			// and attempt to send the response (`stream.Send(resp)`).
			err = stream.Send(&pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						Version:   generatePathzVersion(),
						CreatedOn: generatePathzCreatedOn(),
						Policy: &pathz.AuthorizationPolicy{
							Rules: []*pathz.AuthorizationRule{
								&pathz.AuthorizationRule{
									Id:        "Rule1",
									Principal: &pathz.AuthorizationRule_User{User: "User1"},
									Path: &gnmipb.Path{
										Elem: []*gnmipb.PathElem{
											&gnmipb.PathElem{
												Name: "a",
											},
											&gnmipb.PathElem{
												Name: "b",
												Key: map[string]string{
													"k1": "v1",
													"k2": "v2",
												},
											},
										},
									},
									Action: pathz.Action_ACTION_PERMIT,
									Mode:   pathz.Mode_MODE_READ,
								},
							},
						},
					},
				},
			})
			if err != nil {
				conn.Close()
				t.Fatal(err.Error())
			}

			// 2) Immediately close the underlying client connection.
			// This guarantees the server's subsequent stream.Send(resp) will fail with a transport error,
			// triggering the desired coverage block.
			if err := conn.Close(); err != nil {
				t.Fatalf("Failed to close connection: %v", err)
			}

			// 3) Attempt to receive confirmation. This call will fail due to the closed connection.
			// The failure of the server's Send operation (unseen directly by the client)
			// covers the target lines in the server's Rotate RPC.
			if _, err := stream.Recv(); err == nil {
				t.Fatal("Expected an error (connection closed) but received a successful response.")
			}
		},
	},
	{
		desc: "RotatePolicyEmptyUploadRequest",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 1) Generate a rotation request and send it to the switch.
			err = stream.Send(&pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 2) Receive error reporting premature closure of the stream.
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			// And sanity check
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "RotatePolicyEmptyRequest",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			if err = stream.Send(&pathz.RotateRequest{}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "RotatePolicyWrongPolicyProto",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			req := &pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						Version:   generatePathzVersion(),
						CreatedOn: generatePathzCreatedOn(),
						Policy: &pathz.AuthorizationPolicy{
							Rules: []*pathz.AuthorizationRule{
								&pathz.AuthorizationRule{
									Id:        "Rule1",
									Principal: &pathz.AuthorizationRule_User{User: "User1"},
									Path: &gnmipb.Path{
										Elem: []*gnmipb.PathElem{
											&gnmipb.PathElem{
												Name: "a",
											},
											&gnmipb.PathElem{
												Name: "b",
												Key: map[string]string{
													"k1": "v1",
													"k2": "v2",
												},
											},
										},
									},
									Action: pathz.Action_ACTION_PERMIT,
									Mode:   pathz.Mode_MODE_READ,
								},
								&pathz.AuthorizationRule{
									Id:        "Rule2",
									Principal: &pathz.AuthorizationRule_User{User: "User1"},
									Path: &gnmipb.Path{
										Elem: []*gnmipb.PathElem{
											&gnmipb.PathElem{
												Name: "a",
											},
											&gnmipb.PathElem{
												Name: "b",
												Key: map[string]string{
													"k1": "v1",
													"k3": "v3",
												},
											},
										},
									},
									Action: pathz.Action_ACTION_PERMIT,
									Mode:   pathz.Mode_MODE_READ,
								},
							},
						},
					},
				},
			}
			if err = stream.Send(req); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "RotatePolicyNoVersion",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			req := &pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						CreatedOn: generatePathzCreatedOn(),
						Policy: &pathz.AuthorizationPolicy{
							Rules: []*pathz.AuthorizationRule{
								&pathz.AuthorizationRule{
									Id:        "Rule1",
									Principal: &pathz.AuthorizationRule_User{User: "User1"},
									Path: &gnmipb.Path{
										Elem: []*gnmipb.PathElem{
											&gnmipb.PathElem{
												Name: "a",
											},
											&gnmipb.PathElem{
												Name: "b",
												Key: map[string]string{
													"k1": "v1",
													"k2": "v2",
												},
											},
										},
									},
									Action: pathz.Action_ACTION_PERMIT,
									Mode:   pathz.Mode_MODE_READ,
								},
							},
						},
					},
				},
			}
			if err = stream.Send(req); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "RotatePolicySuccess",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			policy := &pathz.AuthorizationPolicy{}
			if err = proto.UnmarshalText(string(pathzTestPolicyDeny), policy); err != nil {
				t.Fatal(err.Error())
			}
			if err = stream.Send(&pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						Version:   generatePathzVersion(),
						CreatedOn: generatePathzCreatedOn(),
						Policy:    policy,
					},
				},
			}); err != nil {
				t.Fatal(err.Error())
			}
			if resp, err := stream.Recv(); err != nil || resp.GetUpload() == nil {
				t.Fatalf("Did not receive expected UploadResponse response; err: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyDeny)
			if err = stream.Send(&pathz.RotateRequest{RotateRequest: &pathz.RotateRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
			if err := resetPathzPolicyFile(s.config.PathzPolicyFile); err != nil {
				t.Errorf("Error when reverting to V1: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "RotatePolicyNoFinalize",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			policy := &pathz.AuthorizationPolicy{}
			if err = proto.UnmarshalText(string(pathzTestPolicyDeny), policy); err != nil {
				t.Fatal(err.Error())
			}
			req := &pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						Version:   generatePathzVersion(),
						CreatedOn: generatePathzCreatedOn(),
						Policy:    policy,
					},
				},
			}
			if err = stream.Send(req); err != nil {
				t.Fatal(err.Error())
			}
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUpload(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyDeny)
			stream.CloseSend()
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			if err := resetPathzPolicyFile(s.config.PathzPolicyFile); err != nil {
				t.Errorf("Error when reverting to V1: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "FinalizeNoRotate",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			if err := stream.Send(&pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_FinalizeRotation{},
			}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err := stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatalf("unexpected error; want Arborted, got: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "RotateTheSamePolicyTwice",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			policy := &pathz.AuthorizationPolicy{}
			if err = proto.UnmarshalText(string(pathzTestPolicyDeny), policy); err != nil {
				t.Fatal(err.Error())
			}
			req := &pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						Version:   generatePathzVersion(),
						CreatedOn: generatePathzCreatedOn(),
						Policy:    policy,
					},
				},
			}
			if err = stream.Send(req); err != nil {
				t.Fatal(err.Error())
			}
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUpload(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyDeny)
			if err = stream.Send(&pathz.RotateRequest{RotateRequest: &pathz.RotateRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
			// Send the same pathz policy to the switch.
			stream, err = sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			if err = stream.Send(req); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if status.Code(err) != codes.AlreadyExists {
				t.Fatalf("Unexpected error: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyDeny)
			if err := resetPathzPolicyFile(s.config.PathzPolicyFile); err != nil {
				t.Errorf("Error when reverting to V1: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "RotateTheSamePolicyTwiceWithForceOverwrite",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			policy := &pathz.AuthorizationPolicy{}
			if err = proto.UnmarshalText(string(pathzTestPolicyDeny), policy); err != nil {
				t.Fatal(err.Error())
			}
			req := &pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						Version:   generatePathzVersion(),
						CreatedOn: generatePathzCreatedOn(),
						Policy:    policy,
					},
				},
				ForceOverwrite: true,
			}
			if err = stream.Send(req); err != nil {
				t.Fatal(err.Error())
			}
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUpload(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyDeny)
			if err = stream.Send(&pathz.RotateRequest{RotateRequest: &pathz.RotateRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
			// Send the same pathz policy to the switch with force overwrite.
			stream, err = sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			if err = stream.Send(req); err != nil {
				t.Fatal(err.Error())
			}
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUpload(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyDeny)
			if err = stream.Send(&pathz.RotateRequest{RotateRequest: &pathz.RotateRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
			if err := resetPathzPolicyFile(s.config.PathzPolicyFile); err != nil {
				t.Errorf("Error when reverting to V1: %v", err)
			}
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)
		},
	},
	{
		desc: "ParallelRotationCalls",
		f: func(ctx context.Context, t *testing.T, sc pathz.PathzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			policy := &pathz.AuthorizationPolicy{}
			if err = proto.UnmarshalText(string(pathzTestPolicyDeny), policy); err != nil {
				t.Fatal(err.Error())
			}
			req := &pathz.RotateRequest{
				RotateRequest: &pathz.RotateRequest_UploadRequest{
					UploadRequest: &pathz.UploadRequest{
						Version:   generatePathzVersion(),
						CreatedOn: generatePathzCreatedOn(),
						Policy:    policy,
					},
				},
			}
			if err = stream.Send(req); err != nil {
				t.Fatal(err.Error())
			}
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUpload(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			expectPolicyMatch(t, s.config.PathzPolicyFile, pathzTestPolicyDeny)
			// Attempt to send the same pathz policy to the switch.
			stream2, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			stream2.Send(req)
			if _, err = stream2.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			// Finalize the operation.
			if err = stream.Send(&pathz.RotateRequest{RotateRequest: &pathz.RotateRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
			expectPolicyMatch(t, s.config.PathzPolicyFile, pathzTestPolicyDeny)
			if err := resetPathzPolicyFile(s.config.PathzPolicyFile); err != nil {
				t.Errorf("Error when reverting to V1: %v", err)
			}
			expectPolicyMatch(t, s.config.PathzPolicyFile, pathzTestPolicyPermit)
		},
	},
}

// TestPathzRotation tests implementation of pathz rotate service.
func TestGnsiPathzRotation(t *testing.T) {
	// Set the configuration paths globally.
	TestPathzPolicyFile = pathzTestPolicyFile
	TestPathzMetaFile = pathzTestMetaFile

	const testPort = 8081
	s := createPathzServer(t, testPort)

	defer os.Remove(pathzTestPolicyFile)
	go runServer(t, s)
	defer s.Stop()

	// Create a gNSI.pathz client and connect it to the gNSI.pathz server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	// Use dummy credentials for the client
	cred := &loginCreds{Username: testUsername, Password: testPassword}

	// Attach both TLS transport and the PerRPC BasicAuth credentials
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	}

	targetAddr := fmt.Sprintf("127.0.0.1:%d", port)
	orig := authenticateFunc
	defer func() { authenticateFunc = orig }()
	authenticateFunc = func(config *Config, ctx context.Context, target string, writeAccess bool) (context.Context, error) {
		return ctx, nil
	}
	var mu sync.Mutex
	for _, tc := range pathzRotationTestCases {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		t.Run(tc.desc, func(t *testing.T) {
			mu.Lock()
			defer mu.Unlock()
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				cancel()
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()
			sc := pathz.NewPathzClient(conn)
			tc.f(ctx, t, sc, s)
			if err := resetPathzPolicyFile(s.config.PathzPolicyFile); err != nil {
				t.Errorf("Error when reverting to V1: %v", err)
			}
			// And sanity check
			expectPolicyMatch(t, pathzTestPolicyFile, pathzTestPolicyPermit)

		})
		cancel()
	}
	s.gnsiPathz.savePathzFileFreshess(s.config.PathzMetaFile)
}

// TestGnsiPathzRotateUnauthenticated tests implementation of gnsi.pathz Rotate Unsuthenticated error.
func TestGnsiPathzRotateUnauthenticated(t *testing.T) {
	const testPort = 8083 // Use a different port to avoid conflict
	s := createPathzServer(t, testPort)
	go runServer(t, s)
	defer s.Stop()

	// Create gNSI.pathz client
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	cred := &loginCreds{Username: testUsername, Password: testPassword}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", testPort)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	noCredsClient := pathz.NewPathzClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// 1) Open the streaming RPC using the client *without* credentials.
	stream, err := noCredsClient.Rotate(ctx, grpc.EmptyCallOption{})

	// Check if the server immediately rejected the connection due to missing credentials.
	// If the error is not nil, we check the status code.
	if err != nil {
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("Expected Unauthenticated error on stream creation, got: %v (code: %v)", err, status.Code(err))
		}
		return // Authentication failed as expected.
	}

	// 2) If the stream successfully opened, the server's authentication
	// will fail upon the first `Recv()`. We close the send stream to get the final error.
	stream.CloseSend()

	// 3) Receive error reporting authentication failure.
	if _, err = stream.Recv(); err == nil {
		t.Fatal("Expected an error due to authentication failure.")
	}

	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Expected Unauthenticated error, got: %v (code: %v)", err, status.Code(err))
	}
	s.gnsiPathz.savePathzFileFreshess(s.config.PathzMetaFile)
}

func TestGnsiPathzRunGet(t *testing.T) {
	const testPort = 8084 // Use a different port to avoid conflict
	s := createPathzServer(t, testPort)
	go runServer(t, s)
	defer s.Stop()
	baseCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel() // Always good practice to defer the cancel
	spiffeURL, _ := url.Parse("spiffe://example.org/ns/default/sa/test-user")
	p := &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: testPort},
		AuthInfo: credentials.TLSInfo{
			SPIFFEID: spiffeURL,
		},
	}
	ctx := peer.NewContext(baseCtx, p)

	// Create gNSI.pathz client
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	cred := &loginCreds{Username: testUsername, Password: testPassword}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", testPort)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	//gClient := gnmipb.NewGNMIClient(conn)

	// Prepare Get request, the specific path doesn't matter, they should all
	// fail due to the pathz policy
	pathTgt := "OC_YANG"

	textPbPath := pathToPb("/openconfig-system:system/state/boot-time")
	var pbPath gnmipb.Path
	reqDataType := gnmipb.GetRequest_ALL
	if err = proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatal(err.Error())
	}

	prefix := gnmipb.Path{Target: pathTgt}
	req := &gnmipb.GetRequest{
		Type:     reqDataType,
		Prefix:   &prefix,
		Path:     []*gnmipb.Path{&pbPath},
		Encoding: gnmipb.Encoding_JSON_IETF,
	}

	//_, errN := gClient.Get(ctx, req)
	_, errN := s.Get(ctx, req)
	if errN == nil {
		t.Fatalf("Expected error, but passed")
	}
}

// TestGnsiPathzRunGet tests implementation of RunGet
func TestGnsiPathzRunGetErrSPIFFEID(t *testing.T) {
	const testPort = 8084 // Use a different port to avoid conflict
	s := createPathzServer(t, testPort)
	go runServer(t, s)
	defer s.Stop()

	// Create gNSI.pathz client
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	cred := &loginCreds{Username: testUsername, Password: testPassword}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", testPort)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	gClient := gnmipb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	// Prepare Get request, the specific path doesn't matter, they should all
	// fail due to the pathz policy
	pathTgt := "OC_YANG"

	textPbPath := pathToPb("/openconfig-system:system/state/boot-time")
	var pbPath gnmipb.Path
	reqDataType := gnmipb.GetRequest_ALL
	if err = proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatal(err.Error())
	}

	prefix := gnmipb.Path{Target: pathTgt}
	req := &gnmipb.GetRequest{
		Type:     reqDataType,
		Prefix:   &prefix,
		Path:     []*gnmipb.Path{&pbPath},
		Encoding: gnmipb.Encoding_JSON_IETF,
	}

	_, errN := gClient.Get(ctx, req)
	if errN == nil {
		t.Fatalf("Expected error, but passed")
	}
}

func TestGnsiPathzRunSet(t *testing.T) {
	const testPort = 8084 // Use a different port to avoid conflict
	s := createPathzServer(t, testPort)
	go runServer(t, s)
	defer s.Stop()
	baseCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel() // Always good practice to defer the cancel
	spiffeURL, _ := url.Parse("spiffe://example.org/ns/default/sa/test-user")
	p := &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: testPort},
		AuthInfo: credentials.TLSInfo{
			SPIFFEID: spiffeURL,
		},
	}
	ctx := peer.NewContext(baseCtx, p)

	// Create gNSI.pathz client
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	cred := &loginCreds{Username: testUsername, Password: testPassword}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", testPort)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	//gClient := gnmipb.NewGNMIClient(conn)

	// Prepare Get request, the specific path doesn't matter, they should all
	// fail due to the pathz policy
	pathTgt := "OC_YANG"

	textPbPath := pathToPb("/openconfig-system:system/state/boot-time")
	var pbPath gnmipb.Path
	if err = proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatal(err.Error())
	}

	prefix := gnmipb.Path{Target: pathTgt}
	req := &gnmipb.SetRequest{
		Prefix: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
		Update: []*gnmipb.Update{
			newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			newPbUpdate("interface[name=Ethernet4]/config/mtu", `{"mtu": 9105}`),
		}}
	req = &gnmipb.SetRequest{
		Prefix: &prefix,
	}
	_, errN := s.Set(ctx, req)

	if errN == nil {
		t.Fatalf("Expected error, but passed")
	}
}
func TestGnsiPathzRunSetErrSPIFFEID(t *testing.T) {
	const testPort = 8084 // Use a different port to avoid conflict
	s := createPathzServer(t, testPort)
	go runServer(t, s)
	defer s.Stop()
	baseCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel() // Always good practice to defer the cancel
	spiffeURL, _ := url.Parse("spiffe://example.org/ns/default/sa/test-user")
	p := &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: testPort},
		AuthInfo: credentials.TLSInfo{
			SPIFFEID: spiffeURL,
		},
	}
	ctx := peer.NewContext(baseCtx, p)

	// Create gNSI.pathz client
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	cred := &loginCreds{Username: testUsername, Password: testPassword}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", testPort)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	gClient := gnmipb.NewGNMIClient(conn)

	// Prepare Get request, the specific path doesn't matter, they should all
	// fail due to the pathz policy
	pathTgt := "OC_YANG"

	textPbPath := pathToPb("/openconfig-system:system/state/boot-time")
	var pbPath gnmipb.Path
	if err = proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatal(err.Error())
	}

	prefix := gnmipb.Path{Target: pathTgt}
	req := &gnmipb.SetRequest{
		Prefix: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
		Update: []*gnmipb.Update{
			newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			newPbUpdate("interface[name=Ethernet4]/config/mtu", `{"mtu": 9105}`),
		}}
	req = &gnmipb.SetRequest{
		Prefix: &prefix,
	}
	_, errN := gClient.Set(ctx, req)

	if errN == nil {
		t.Fatalf("Expected error, but passed")
	}
}
func resetPathzPolicyFile(path string) error {
	return attemptWrite(path, []byte(pathzTestPolicyPermit), 0600)
}

// TestGnsiPathzUnimplemented tests implementation of gnsi.pathz Probe and Get server.
func TestGnsiPathzUnimplemented(t *testing.T) {
	// Setup is similar to TestGnsiPathzRotation, but we don't need the full rotation logic.

	const testPort = 8082 // Use a different port to avoid conflict
	s := createPathzServer(t, testPort)
	go runServer(t, s)
	defer s.Stop()

	// Create gNSI.pathz client
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	cred := &loginCreds{Username: testUsername, Password: testPassword}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", testPort)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	sc := pathz.NewPathzClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// --- Test Probe RPC ---
	t.Run("ProbeUnimplemented", func(t *testing.T) {
		_, err := sc.Probe(ctx, &pathz.ProbeRequest{})
		if status.Code(err) != codes.Unimplemented {
			t.Fatalf("Probe() returned unexpected error code: got %v, want %v", status.Code(err), codes.Unimplemented)
		}
	})

	// --- Test Get RPC ---
	t.Run("GetUnimplemented", func(t *testing.T) {
		_, err := sc.Get(ctx, &pathz.GetRequest{})
		if status.Code(err) != codes.Unimplemented {
			t.Fatalf("Get() returned unexpected error code: got %v, want %v", status.Code(err), codes.Unimplemented)
		}
	})
}

// TestGnsiPathzMisc tests implementation of gnsi.pathz other functions used.

func TestGnsiPathzMisc(t *testing.T) {
	// --- Test copyFile Error scenarios ---
	t.Run("PathzCopyFile", func(t *testing.T) {
		if err := copyFile("test", ""); err == nil {
			t.Error("expected: error, got: nil")
		}
	})

	t.Run("PathzCopyNonRegularFile", func(t *testing.T) {
		// 1. Create a temporary directory to use as the invalid input
		tempDir, err := os.MkdirTemp("", "testdir")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		// Schedule cleanup to remove the temp directory after the test finishes
		defer os.RemoveAll(tempDir)
		if err := copyFile(tempDir, ""); err == nil {
			t.Error("expected: error, got: nil")
		}
	})
	t.Run("PathzCopyFileDstErr", func(t *testing.T) {
		// 1. Create a temporary directory to use as the invalid input
		_, err := os.Create("tempFile")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		// Schedule cleanup to remove the temp directory after the test finishes
		defer os.Remove("tempFile")
		if err := copyFile("", "tempFile"); err == nil {
			t.Error("expected: error, got: nil")
		}
	})
	t.Run("PathzCopyFileSrcErr", func(t *testing.T) {
		// 1. Create a temporary directory to use as the invalid input
		_, err := os.Create("tempFile")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		// Schedule cleanup to remove the temp directory after the test finishes
		defer os.Remove("tempFile")
		if err := copyFile("tempFile", ""); err == nil {
			t.Error("expected: error, got: nil")
		}
	})
	// --- Test fileCheck Error scenarios ---
	t.Run("PathzFileCheckNonRegularFile", func(t *testing.T) {
		// 1. Create a temporary directory to use as the invalid input
		tempDir, err := os.MkdirTemp("", "testdir")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		// Schedule cleanup to remove the temp directory after the test finishes
		defer os.RemoveAll(tempDir)
		if err := fileCheck(tempDir); err == nil {
			t.Error("expected: error, got: nil")
		}
	})
}
func generatePathzCreatedOn() uint64 {
	return uint64(time.Now().UnixNano())
}

func generatePathzVersion() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

const pathzTestPolicyPermit = `rules: <
  id: "Rule1"
  user: "User1"
  path: <
  >
  action: ACTION_PERMIT
  mode: MODE_READ
>
groups: <
  name: "Group1"
  users: <
    name: "User1"
  >
  users: <
    name: "User2"
  >
>
`
const pathzTestPolicyDeny = `rules: <
  id: "Rule1"
  user: "User1"
  path: <
  >
  action: ACTION_DENY
  mode: MODE_READ
>
groups: <
  name: "Group1"
  users: <
    name: "User1"
  >
  users: <
    name: "User2"
  >
>
`
