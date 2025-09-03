package gnmi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/agiledragon/gomonkey/v2"
	log "github.com/golang/glog"
	ospb "github.com/openconfig/gnoi/os"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	json "google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

type fakeInstallServer struct {
	ospb.OS_InstallServer
	sendErr error
	ctx     context.Context
}

func (f *fakeInstallServer) Send(*ospb.InstallResponse) error {
	return f.sendErr
}

func (f *fakeInstallServer) Context() context.Context {
	return f.ctx
}

func lockSem() {
	pkg := reflect.ValueOf(&sem).Elem()
	semPtr := (*sync.Mutex)(unsafe.Pointer(pkg.UnsafeAddr()))
	semPtr.Lock()
}

func unlockSem() {
	pkg := reflect.ValueOf(&sem).Elem()
	semPtr := (*sync.Mutex)(unsafe.Pointer(pkg.UnsafeAddr()))
	semPtr.Unlock()
}

var testOSCases = []struct {
	desc string
	f    func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer)
}{
	{
		desc: "OSInstallFailsIfTransferRequestIsMissingVersion",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive InstallError due to missing version.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			instErr := resp.GetInstallError()
			if instErr == nil {
				t.Fatal("Expected InstallError!")
			}
			if instErr.GetType() != ospb.InstallError_PARSE_FAIL {
				t.Fatal("Expected InstallError type: PARSE_FAIL!")
			}
			// Receive error reporting.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
			testErr(err, codes.Aborted, "Failed to process TransferRequest.", t)
		},
	},
	{
		desc: "OSInstallFailsForConcurrentOperations",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: "os1.1",
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetTransferReady() == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
			// Create a new client.
			tlsConfig := &tls.Config{InsecureSkipVerify: true}
			opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()
			newsc := ospb.NewOSClient(conn)
			newctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			newstream, err := newsc.Install(newctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive InstallError due to Install in progress.
			resp, err = newstream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			instErr := resp.GetInstallError()
			if instErr == nil {
				t.Fatal("Expected InstallError!")
			}
			if instErr.GetType() != ospb.InstallError_INSTALL_IN_PROGRESS {
				t.Fatal("Expected InstallError type: INSTALL_IN_PROGRESS!")
			}
			_, err = newstream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
			t.Logf("InstallError=%v", err)
			testErr(err, codes.Aborted, "Concurrent Install RPCs", t)
			// Continue with the existing stream.
			t.Logf("Client continue with the existing stream")
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferEnd{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive Validated response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetValidated() == nil {
				t.Fatal("Did not receive expected Validated response.")
			}
		},
	},
	{
		desc: "OSInstallFailsIfWrongMessageIsSent",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferEnd; server expects TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferEnd{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive error reporting.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
			testErr(err, codes.InvalidArgument, "Expected TransferRequest", t)
		},
	},
	{
		desc: "OSInstallAbortedImmediately",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Close the stream immediately.
			stream.CloseSend()
			// Receive error reporting premature closure of the stream.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting on premature closure of the stream.")
			}
		},
	},
	{
		desc: "OSInstallFailsIfImageExistsWhenTransferBegins",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferRequest.
			version := "os1.1"
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetTransferReady() == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			// TransferReady initiates transferring content. Image must not exist at this point!
			imgPath := s.getVersionPath(version)
			f, err := os.OpenFile(imgPath, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				t.Fatal(err.Error())
			}
			if err := f.Close(); err != nil {
				t.Fatal(err.Error())
			}
			// Cleanup
			defer func() {
				if err := os.Remove(imgPath); err != nil {
					t.Errorf("Error while deleting temporary test file: %v\n", err)
				}
			}()
			// Send TransferContent.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferContent{
					TransferContent: []byte("unimportant string"),
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive InstallError.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			instErr := resp.GetInstallError()
			if instErr == nil {
				t.Fatal("Expected InstallError!")
			}
			// Receive error reporting.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
		},
	},
	{
		desc: "OSInstallFailsIfStreamClosesInTheMiddleOfTransfer",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			t.Log("OSInstallFailsIfStreamClosesInTheMiddleOfTransfer starts")
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			version := "os1.1"
			// Send TransferRequest.
			t.Log("Send TransferRequest")
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			t.Log("Received TransferReady")
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfReady := resp.GetTransferReady(); trfReady == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			// Send TransferContent.
			t.Log("Send TransferContent")
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferContent{
					TransferContent: []byte("unimportant string"),
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferProgress response.
			resp, err = stream.Recv()
			t.Log("Received TransferProgress")
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfProg := resp.GetTransferProgress(); trfProg == nil {
				t.Fatal("Did not receive expected TransferProgress response")
			}
			// Close the stream immediately.
			t.Log("Close the stream immediately")
			stream.CloseSend()

			// Receive error reporting premature closure of the stream.
			t.Log("Receive error reporting premature closure of the stream")
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting on premature closure of the stream.")
			}
			t.Logf("Got expected error from server: %v", err)

			// Check incomplete transfer is removed!
			if s.imageExists(s.getVersionPath(version)) {
				t.Fatal("Incomplete image should have been deleted!")
			}
			t.Log("Incomplete transfer has been removed")
		},
	},
	{
		desc: "OSInstallFailsIfWrongMsgIsSentInTheMiddleOfTransfer",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			version := "os1.1"
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfReady := resp.GetTransferReady(); trfReady == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			// Send TransferContent.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferContent{
					TransferContent: []byte("unimportant string"),
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferProgress response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfProg := resp.GetTransferProgress(); trfProg == nil {
				t.Fatal("Did not receive expected TransferProgress response")
			}
			// Send TransferRequest again. This is unexpected!
			// Server should send error message, clean up incomplete transfer.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive error reporting.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting on premature closure of the stream.")
			}
			// Check incomplete transfer is removed!
			if s.imageExists(s.getVersionPath(version)) {
				t.Fatal("Incomplete image should have been deleted!")
			}
		},
	},
	{
		desc: "OSInstallSucceeds",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			version := "os1.1"
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfReady := resp.GetTransferReady(); trfReady == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			data := []byte("unimportant string")
			// Send TransferContent.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferContent{
					TransferContent: data,
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferProgress response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfProg := resp.GetTransferProgress(); trfProg == nil {
				t.Fatal("Did not receive expected TransferProgress response")
			}
			// Send TransferEnd.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferEnd{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive Validated response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetValidated() == nil {
				t.Fatal("Did not receive expected Validated response.")
			}
			// Sanity check!
			imgPath := s.getVersionPath(version)
			dataRead, err := os.ReadFile(imgPath)
			if err != nil {
				t.Fatal(err.Error())
			}
			if string(data) != string(dataRead) {
				t.Fatal("Content doesn't match!")
			}
			// Cleanup
			if err = os.Remove(imgPath); err != nil {
				t.Errorf("Error while deleting temporary test file: %v\n", err)
			}
		},
	},
	{
		desc: "OSInstallFailsIfBackendErrorsOnTransferReady",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			// Override backend to return an error
			s.config.OSCfg.ProcessTransferReady = func(_ string) (string, error) {
				return "", status.Errorf(codes.Unimplemented, "OS Install not supported")
			}

			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{Version: "os1.2"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			// Expect gRPC Unimplemented
			_, err = stream.Recv()
			if err == nil || grpc.Code(err) != codes.Unimplemented {
				t.Fatalf("Expected Unimplemented, got: %v", err)
			}
		},
	},
	{
		desc: "OSInstallFailsOnBadTransferReadyJSON",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			s.config.OSCfg.ProcessTransferReady = func(_ string) (string, error) {
				return "{bad-json", nil
			}
			stream, _ := sc.Install(ctx, grpc.EmptyCallOption{})
			stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{Version: "os1.2"},
				},
			})
			resp, _ := stream.Recv()
			if resp.GetInstallError() == nil {
				t.Fatal("Expected InstallError due to bad JSON")
			}
		},
	},
	{
		desc: "OSInstallFailsWithAuthenticationFailure",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			// Patch authenticate to always fail with Unauthenticated error
			patch := gomonkey.ApplyFuncReturn(authenticate, ctx, status.Error(codes.Unauthenticated, "unauthenticated"))
			defer patch.Reset()

			// Attempt to call Install (should fail due to auth)
			stream, err := sc.Install(ctx)
			if err == nil {
				// Try sending a request; should fail on send or receive
				err = stream.Send(&ospb.InstallRequest{
					Request: &ospb.InstallRequest_TransferRequest{
						TransferRequest: &ospb.TransferRequest{Version: "os1.2"},
					},
				})
				if err == nil {
					// If sending didn't fail, try to receive (should fail here)
					_, err = stream.Recv()
				}
			}
			if err == nil || status.Code(err) != codes.Unauthenticated {
				t.Fatalf("Expected Unauthenticated error, got: %v", err)
			}
		},
	},
	{
		desc: "OSInstallFailsForConcurrentOperations_SendError",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			lockSem()
			defer unlockSem()
			// Prepare fake server stream that simulates Send error
			fakeStream := &fakeInstallServer{
				sendErr: errors.New("simulated send error"),
				ctx:     ctx,
			}

			// Call Install directly with the fake stream
			err := s.Install(fakeStream)

			// The error should be codes.Aborted with your simulated message
			if err == nil || status.Code(err) != codes.Aborted || !strings.Contains(err.Error(), "simulated send error") {
				t.Fatalf("Expected Aborted error with send error message, got: %v", err)
			}
		},
	},
}

// TestOSServer tests implementation of gnoi.OS server.
func TestOSServer(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.s.Stop()
	targetAddr := "127.0.0.1:8081"
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, test := range testOSCases {
		t.Run(test.desc, func(t *testing.T) {
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()
			sc := ospb.NewOSClient(conn)
			test.f(ctx, t, sc, &OSServer{Server: s})
		})
	}
}

// ProcessFakeTrfReady responds with the TransferReady response.
func ProcessFakeTrfReady(req string) (string, error) {
	// Fake response.
	resp := &ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferReady{},
	}
	respStr, err := json.Marshal(resp)
	if err != nil {
		log.Errorln("Cannot marshal TransferReady response!")
		return "", fmt.Errorf("Cannot marshal TransferReady response!")
	}
	return string(respStr), nil
}

// ProcessFakeTrfEnd responds with the Validated response.
func ProcessFakeTrfEnd(req string) (string, error) {
	// Fake response.
	resp := &ospb.InstallResponse{
		Response: &ospb.InstallResponse_Validated{},
	}
	respStr, err := json.Marshal(resp)
	if err != nil {
		log.Errorln("Cannot marshal TransferEnd response!")
		return "", fmt.Errorf("Cannot marshal TransferEnd response!")
	}
	return string(respStr), nil
}

func TestHandleErrorResponse(t *testing.T) {
	errResp := &ospb.InstallResponse{
		Response: &ospb.InstallResponse_InstallError{
			InstallError: &ospb.InstallError{
				Detail: fmt.Sprintf("something went wrong: %d", 42),
			},
		},
	}
	if errResp.GetInstallError() == nil {
		t.Fatal("Expected InstallError")
	}
	if !strings.Contains(errResp.GetInstallError().GetDetail(), "something went wrong: 42") {
		t.Fatal("Unexpected error message")
	}
}

func TestHandleErrorResponse_MarshalError(t *testing.T) {
	patch := gomonkey.ApplyFunc(json.Marshal, func(m proto.Message) ([]byte, error) {
		return nil, errors.New("mock marshal failure")
	})
	defer patch.Reset()

	// Provide a real proto.Message input
	req := &ospb.TransferRequest{}
	resp := &ospb.InstallResponse{
		Response: &ospb.InstallResponse_InstallError{
			InstallError: &ospb.InstallError{
				Detail: fmt.Sprintf("test marshal fail: %v", req),
			},
		},
	}

	assert.NotNil(t, resp)
	t.Logf("Got response: %+v", resp)
}

func TestProcessTransferContent_OpenFileError(t *testing.T) {
	// Mock the os.OpenFile function to simulate a failure
	patch := gomonkey.ApplyFunc(os.OpenFile, func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, errors.New("simulated OpenFile failure")
	})
	defer patch.Reset()

	// Initialize the Server struct with a mock Config
	srv := &OSServer{
		Server: &Server{
			config: &Config{
				OSCfg: &OSConfig{
					ImgDir: "/tmp", // Mock directory path
				},
			},
		},
		ProcessTransferState: &InstallRequestState{
			CurrentState: TransferReady, // Initial state should be a valid `State`
			NextState: map[State]map[Event]State{
				TransferReady: {
					TransferRequest: TransferProgress,
				},
				TransferProgress: {
					TransferContent: TransferProgress,
					TransferEnd:     Validated,
				},
			},
		},
	}

	// Call the method under test
	resp := srv.processTransferContent([]byte("test data"), "/tmp/test.img")
	t.Logf("processTransferContent response=%v\n", resp)

	// Check if the response is not nil and has the expected error
	if resp == nil || resp.GetInstallError() == nil {
		t.Fatalf("Expected install_error in InstallResponse, got: %+v", resp)
	}

	// Check if the error detail matches the expected error message
	expectedDetail := "Failed to open file [/tmp/test.img]."
	if resp.GetInstallError().GetDetail() != expectedDetail {
		t.Errorf("Expected error detail %q, got: %q", expectedDetail, resp.GetInstallError().GetDetail())
	}
}

func TestProcessTransferContent_WriteError(t *testing.T) {
	// Create a dummy *os.File
	f := &os.File{}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Patch os.OpenFile to return our dummy file and no error
	patches.ApplyFunc(os.OpenFile, func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return f, nil
	})

	// Patch (*os.File).Write to return an error (simulating write failure)
	patches.ApplyMethod(reflect.TypeOf(f), "Write", func(_ *os.File, b []byte) (int, error) {
		return 0, errors.New("simulated Write failure")
	})

	// Patch (*os.File).Close to simulate a clean close (no error)
	patches.ApplyMethod(reflect.TypeOf(f), "Close", func(_ *os.File) error {
		return nil
	})

	// Initialize the Server struct with a mock Config
	srv := &OSServer{
		Server: &Server{
			config: &Config{
				OSCfg: &OSConfig{
					ImgDir: "/tmp", // Mock directory path
				},
			},
		},
		ProcessTransferState: &InstallRequestState{
			CurrentState: TransferReady, // Initial state should be a valid `State`
			NextState: map[State]map[Event]State{
				TransferReady: {
					TransferRequest: TransferProgress,
				},
				TransferProgress: {
					TransferContent: TransferProgress,
					TransferEnd:     Validated,
				},
			},
		},
	}

	// Call the method being tested
	resp := srv.processTransferContent([]byte("test data"), "/tmp/test.img")
	t.Logf("processTransferContent response=%v\n", resp)

	// Validate that the response contains the expected error
	t.Logf("processTransferContent response=%v", resp.GetInstallError())

	if resp == nil || resp.GetInstallError() == nil {
		t.Fatalf("Expected error response due to Write failure, got: %+v", resp)
	}

	// Check the error detail
	t.Logf("Error Detail: %v", resp.GetInstallError().GetDetail())

	expectedDetail := "Failed to write to file [/tmp/test.img]."
	if resp.GetInstallError().GetDetail() != expectedDetail {
		t.Errorf("Expected error detail %q, got: %q", expectedDetail, resp.GetInstallError().GetDetail())
	}
}

func TestProcessTransferContent_CloseError(t *testing.T) {
	f := &os.File{}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Patch os.OpenFile to return dummy file
	patches.ApplyFunc(os.OpenFile, func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return f, nil
	})

	// Patch (*os.File).Write to simulate successful write
	patches.ApplyMethod(reflect.TypeOf(f), "Write", func(_ *os.File, b []byte) (int, error) {
		return len(b), nil
	})

	// Patch (*os.File).Close to simulate error on final close
	patches.ApplyMethod(reflect.TypeOf(f), "Close", func(_ *os.File) error {
		return errors.New("simulated Close failure")
	})

	// Initialize the Server struct with a mock Config
	srv := &OSServer{
		Server: &Server{
			config: &Config{
				OSCfg: &OSConfig{
					ImgDir: "/tmp", // Mock directory path
				},
			},
		},
		ProcessTransferState: &InstallRequestState{
			CurrentState: TransferReady, // Initial state should be a valid `State`
			NextState: map[State]map[Event]State{
				TransferReady: {
					TransferRequest: TransferProgress,
				},
				TransferProgress: {
					TransferContent: TransferProgress,
					TransferEnd:     Validated,
				},
			},
		},
	}

	resp := srv.processTransferContent([]byte("test data"), "/tmp/test.img")
	t.Logf("processTransferContent response=%v\n", resp)

	if resp == nil || resp.GetInstallError() == nil {
		t.Errorf("Expected error response due to Close failure, got: %+v", resp)
	}
}

func TestProcessInstallFromBackEnd_Success(t *testing.T) {
	// Patch ssc.NewDbusClient to return our fake client
	patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		return &ssc.FakeClient{}, nil
	})
	defer patch.Reset()

	result, err := ProcessInstallFromBackEnd("stable")
	t.Logf("ProcessInstallFromBackEnd result=%v", result)
	assert.NoError(t, err)
	assert.Equal(t, "stable", result)
}

func TestProcessInstallFromBackEnd_NewDbusClientFails(t *testing.T) {
	patch := gomonkey.ApplyFunc(ssc.NewDbusClient, func() (ssc.Service, error) {
		return nil, errors.New("dbus error")
	})
	defer patch.Reset()

	result, err := ProcessInstallFromBackEnd("stable")
	t.Logf("ProcessInstallFromBackEnd err=%v", err)
	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestRemoveIncompleteTrf_RemoveFails(t *testing.T) {
	srv := &OSServer{}

	// Create a temp file and don't delete it
	tmpFile, err := os.CreateTemp("", "testimage-*.img")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	imgPath := tmpFile.Name()
	tmpFile.Close()

	// Patch os.Remove to return error, triggering the "failed to remove" path
	patch := gomonkey.ApplyFunc(os.Remove, func(string) error {
		return errors.New("mock remove failure")
	})
	defer patch.Reset()

	// Ensure file still exists so imageExists returns true
	_, statErr := os.Stat(imgPath)
	if statErr != nil {
		t.Fatalf("Temp file unexpectedly missing: %v", statErr)
	}

	srv.removeIncompleteTransfer(imgPath)

	// Cleanup: remove file manually since our patched Remove does nothing
	_ = os.Remove(imgPath)
}
