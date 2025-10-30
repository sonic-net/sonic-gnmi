package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	gomonkey "github.com/agiledragon/gomonkey/v2"
	log "github.com/golang/glog"
	"github.com/openconfig/gnsi/authz"
	lvl "github.com/sonic-net/sonic-gnmi/gnmi_server/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"io"
	"os"
	"os/user"
	"sync"
	"testing"
	"time"
)

var (
	TestAuthzPolicyFile string // Global variable to hold policy path
	TestAuthzMetaFile   string // Global variable to hold meta path
)

const (
	// Authz is a location of the Authz Policy
	authzTestPolicyFile = "../testdata/gnsi/authz_policy.json"
	authzTestMetaFile   = "../testdata/gnsi/authz_meta.json"
)

// Dummy credentials for the test client
const testUsername = "username"
const testPassword = "password"

// Mock user structure and roles
var mockUser = &user.User{
	Uid:      "1000",
	Gid:      "1000",
	Username: testUsername,
	Name:     "Test User",
	HomeDir:  "/home/testuser",
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
	// Mock user.Lookup to bypass OS check
	mock1 := gomonkey.ApplyFunc(user.Lookup, func(username string) (*user.User, error) {
		if username == testUsername {
			return mockUser, nil // Success: Return a dummy user struct
		}
		// Fail for any other user lookup
		return nil, fmt.Errorf("unknown user %s", username)
	})
	defer mock1.Reset()

	// Mock UserPwAuth to bypass SSH/PAM check
	// Note: The original UserPwAuth is defined in the same package (gnmi).
	mock2 := gomonkey.ApplyFunc(UserPwAuth, func(username string, passwd string) (bool, error) {
		// Mock success for the test user
		return true, nil
	})
	defer mock2.Reset()

	// Set the configuration paths globally.
	TestAuthzPolicyFile = authzTestPolicyFile
	TestAuthzMetaFile = authzTestMetaFile

	const testPort = 8081
	s := createAuthServer(t, testPort)

	defer os.Remove(authzTestPolicyFile)
	go runServer(t, s)
	defer s.Stop()

	// Create a gNSI.authz client and connect it to the gNSI.authz server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	// Use dummy credentials for the client
	cred := &loginCreds{Username: testUsername, Password: testPassword}

	// Attach both TLS transport and the PerRPC BasicAuth credentials
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

func generateVersion() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func attemptWrite(name string, data []byte, perm os.FileMode) error {
	log.V(lvl.INFO).Infof("Writing: %s", name)
	err := os.WriteFile(name, data, perm)
	if err != nil {
		if e := os.Remove(name); e != nil {
			err = fmt.Errorf("Write %s failed: %w; Cleanup failed", name, err)
		}
	}
	return err
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
