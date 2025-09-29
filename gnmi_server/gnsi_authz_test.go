package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/openconfig/gnsi/authz"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

const (
	// Authz is a location of the Authz Policy
	authzTestPolicyFile = "../testdata/gnsi/authz_policy.json"
	authzTestMetaFile   = "../testdata/gnsi/authz_meta.json"
)

func createAuthzServer(t *testing.T) *Server {
	cfg := testServerConfig(testSrvType)
	cfg.AuthzPolicy = true
	cfg.AuthzPolicyFile = authzTestPolicyFile
	cfg.AuthzMetaFile = authzTestMetaFile
	resetAuthzPolicyFile(cfg)
	return createCustomServer(t, cfg)
}

var authzRotationTestCases = []struct {
	desc string
	f    func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server)
}{
	{
		desc: "RotateOpenClose",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 1) Close connection without sending any message.
			stream.CloseSend()
			// 2) Receive error reporting premature closure of the stream.
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
		},
	},
	{
		desc: "RotatePolicyEmptyRequest",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 1) Generate a rotation request and send it to the switch.
			err = stream.Send(&authz.RotateAuthzRequest{})
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
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV1)
		},
	},
	{
		desc: "RotatePolicyEmptyUploadRequest",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 1) Generate a rotation request and send it to the switch.
			err = stream.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{},
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
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV1)
		},
	},
	{
		desc: "RotatePolicyWrongJSON",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 1) Generate a rotation request and send it to the switch.
			err = stream.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						Version:   generateVersion(),
						CreatedOn: generateCreatedOn(),
						Policy:    string(`{"key":}`),
					},
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
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV1)
		},
	},
	{
		desc: "RotatePolicyNoVersion",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 1) Generate a rotation request and send it to the switch.
			err = stream.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						CreatedOn: generateCreatedOn(),
						Policy:    string(`{}`),
					},
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
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV1)
		},
	},
	{
		desc: "RotatePolicySuccess",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 1) Generate a authz policy and send it to the switch.
			err = stream.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						Version:   generateVersion(),
						CreatedOn: generateCreatedOn(),
						Policy:    authzTestPolicyFileV2,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 2) Receive confirmation that the certificate was accepted.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUploadResponse(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			// 3) Check if the credentials are pointed to by the links.
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV2)
			// 4) Finalize the operation by sending the Finalize message.
			if err = stream.Send(&authz.RotateAuthzRequest{RotateRequest: &authz.RotateAuthzRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
		},
	},
	{
		desc: "RotatePolicyNoFinalize",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 1) Generate a authz policy and send it to the switch.
			err = stream.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						Version:   generateVersion(),
						CreatedOn: generateCreatedOn(),
						Policy:    authzTestPolicyFileV2,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 2) Receive confirmation that the certificate was accepted.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUploadResponse(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			// 3) Check if the credentials are pointed to by the links.
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV2)
			// 4) Close connection without sending any message.
			stream.CloseSend()
			// 5) Receive error reporting premature closure of the stream.
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
		},
	},
	{
		desc: "RotateTheSamePolicyTwice",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			ver := generateVersion()
			createdOn := generateCreatedOn()
			// 1) Generate a authz policy and send it to the switch.
			err = stream.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						Version:   ver,
						CreatedOn: createdOn,
						Policy:    authzTestPolicyFileV2,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 2) Receive confirmation that the certificate was accepted.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUploadResponse(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			// 3) Check if the credentials are pointed to by the links.
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV2)
			// 4) Finalize the operation by sending the Finalize message.
			if err = stream.Send(&authz.RotateAuthzRequest{RotateRequest: &authz.RotateAuthzRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
			// 5) Send the same authz policy to the switch.
			stream, err = sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			err = stream.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						Version:   ver,
						CreatedOn: createdOn,
						Policy:    authzTestPolicyFileV2,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 6) Receive confirmation that the certificate was rejected.
			if _, err := stream.Recv(); status.Code(err) != codes.AlreadyExists {
				t.Fatalf("Unexpected error: %v", err)
			}
			// 7) Check if the credentials are pointed to by the links.
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV2)
		},
	},
	{
		desc: "RotateTheSamePolicyTwiceWithForceOverwrite",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			ver := generateVersion()
			createdOn := generateCreatedOn()
			// 1) Generate a authz policy and send it to the switch.
			req := &authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						Version:   ver,
						CreatedOn: createdOn,
						Policy:    authzTestPolicyFileV2,
					},
				},
				ForceOverwrite: true,
			}
			err = stream.Send(req)
			if err != nil {
				t.Fatal(err.Error())
			}
			// 2) Receive confirmation that the certificate was accepted.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUploadResponse(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			// 3) Check if the credentials are pointed to by the links.
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV2)
			// 4) Finalize the operation by sending the Finalize message.
			if err = stream.Send(&authz.RotateAuthzRequest{RotateRequest: &authz.RotateAuthzRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
			// 5) Send the same authz policy to the switch.
			stream, err = sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			err = stream.Send(req)
			if err != nil {
				t.Fatal(err.Error())
			}
			// 6) Receive confirmation that the certificate was accepted.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUploadResponse(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			// 7) Check if the credentials are pointed to by the links.
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV2)
			// 8) Finalize the operation by sending the Finalize message.
			if err = stream.Send(&authz.RotateAuthzRequest{RotateRequest: &authz.RotateAuthzRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
		},
	},
	{
		desc: "ParallelRotationCalls",
		f: func(ctx context.Context, t *testing.T, sc authz.AuthzClient, s *Server) {
			// 0) Open the streaming RPC.
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			ver := generateVersion()
			createdOn := generateCreatedOn()
			// 1) Generate a authz policy and send it to the switch.
			err = stream.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						Version:   ver,
						CreatedOn: createdOn,
						Policy:    authzTestPolicyFileV2,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 2) Receive confirmation that the certificate was accepted.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if cfm := resp.GetUploadResponse(); cfm == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}
			// 3) Check if the credentials are pointed to by the links.
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV2)
			// 4) Attempt to send the same authz policy to the switch.
			stream2, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			err = stream2.Send(&authz.RotateAuthzRequest{
				RotateRequest: &authz.RotateAuthzRequest_UploadRequest{
					UploadRequest: &authz.UploadRequest{
						Version:   ver,
						CreatedOn: createdOn,
						Policy:    authzTestPolicyFileV2,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// 5) Receive information that the certificate was rejected.
			if _, err = stream2.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			// 6) Finalize the operation by sending the Finalize message.
			if err = stream.Send(&authz.RotateAuthzRequest{RotateRequest: &authz.RotateAuthzRequest_FinalizeRotation{}}); err != nil {
				t.Fatal(err.Error())
			}
			if _, err = stream.Recv(); err == nil {
				t.Fatal("Expected an error")
			}
			if err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}
			// 7) Check if the credentials are pointed to by the links.
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV2)
		},
	},
}

// TestGnsiAuthzRotation tests implementation of gnsi.authz rotate server.
func TestGnsiAuthzRotation(t *testing.T) {
	s := createAuthzServer(t)
	defer os.Remove(authzTestPolicyFile)
	go runServer(t, s)
	defer s.Stop()
	// Create a gNSI.authz client and connect it to the gNSI.authz server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	sc := authz.NewAuthzClient(conn)
	var mu sync.Mutex
	for _, tc := range authzRotationTestCases {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		t.Run(tc.desc, func(t *testing.T) {
			mu.Lock()
			defer mu.Unlock()
			tc.f(ctx, t, sc, s)
			if err := resetAuthzPolicyFile(s.config); err != nil {
				t.Errorf("Error when reverting to V1: %v", err)
			}
			// And sanity check
			expectPolicyMatch(t, authzTestPolicyFile, authzTestPolicyFileV1)
		})
		cancel()
	}
	// TODO(b/344081417) Enable code once NSF Freeze is implemented
	// Test RPCs during NSF freeze mode.
	// s.WarmRestartHelper.SetFreezeStatus(true)
	// t.Run("RotateUnavailableDuringFreeze", func(t *testing.T) {
	// 	stream, err := sc.Rotate(context.Background(), grpc.EmptyCallOption{})
	// 	if err != nil {
	// 		t.Fatal(err.Error())
	// 	}
	// 	if err = stream.Send(&authz.RotateAuthzRequest{}); err != nil {
	// 		t.Fatal(err.Error())
	// 	}
	// 	_, err = stream.Recv()
	// 	testErr(err, codes.Unavailable, "RPC disabled since NSF is ongoing!", t)
	// })
	// s.WarmRestartHelper.SetFreezeStatus(false)
	s.gnsiAuthz.saveAuthzFileFreshess(s.config.AuthzMetaFile)
}
func expectPolicyMatch(t *testing.T, path, src string) {
	t.Helper()
	dst, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if src != string(dst) {
		t.Fatalf("want golden:\n%v\ngot %s:\n%v", src, path, string(dst))
	}
}
func generateCreatedOn() uint64 {
	return uint64(time.Now().UnixNano())
}
func resetAuthzPolicyFile(config *Config) error {
	return attemptWrite(config.AuthzPolicyFile, []byte(authzTestPolicyFileV1), 0600)
}

const authzTestPolicyFileV1 = `{
  "name": "policy_file_1",
  "allow_rules": [
    {
      "name": "allow_all"
    }
  ],
  "audit_logging_options": {
    "audit_condition": "ON_DENY_AND_ALLOW",
    "audit_loggers": [
      {
        "name": "authz_logger",
        "is_optional": false
      }
    ]
  }
}`
const authzTestPolicyFileV2 = `{
  "name": "policy_file_2",
  "allow_rules": [
    {
      "name": "allow_all"
    }
  ],
  "audit_logging_options": {
    "audit_condition": "ON_DENY_AND_ALLOW",
    "audit_loggers": [
      {
        "name": "authz_logger",
        "is_optional": false
      }
    ]
  }
}`
