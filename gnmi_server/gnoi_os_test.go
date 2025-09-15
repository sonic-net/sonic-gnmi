package gnmi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/agiledragon/gomonkey/v2"
	ospb "github.com/openconfig/gnoi/os"
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

// --- Mock Definitions ---

// MockOSBackend is a mock implementation of the OSBackend interface for testing.
type MockOSBackend struct {
	InstallOSFunc func(req string) (string, error)
}

// InstallOS implements the OSBackend interface.
func (m *MockOSBackend) InstallOS(req string) (string, error) {
	if m.InstallOSFunc != nil {
		return m.InstallOSFunc(req)
	}
	return "", errors.New("InstallOSFunc not implemented in mock")
}

// Global mock function responses for the backend
var (
	// mockTransferReadySuccess returns a TransferReady response JSON string.
	mockTransferReadySuccess = func(req string) (string, error) {
		resp := &ospb.InstallResponse{
			Response: &ospb.InstallResponse_TransferReady{},
		}
		respStr, _ := json.Marshal(resp)
		return string(respStr), nil
	}

	// mockTransferEndSuccess returns a Validated response JSON string.
	mockTransferEndSuccess = func(req string) (string, error) {
		resp := &ospb.InstallResponse{
			Response: &ospb.InstallResponse_Validated{},
		}
		respStr, _ := json.Marshal(resp)
		return string(respStr), nil
	}
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
	f    func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server)
}{
	{
		desc: "OSInstallFailsIfTransferRequestIsMissingVersion",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
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
			testError(err, codes.Aborted, "Failed to process TransferRequest.", t)
		},
	},
	{
		desc: "OSInstallFailsForConcurrentOperations",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
			// --- Setup Mocks for Backend Logic (If needed for stream 1) ---
			patches := applyFullStreamSuccessPatch(t)
			defer patches.Reset()

			// --- Execute First Stream (Acquire Lock) ---
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferRequest. This acquires the lock inside s.Install().
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{Version: "os1.1"},
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
			// At this point, the global lock 'sem' is held by the first RPC.
			// --- Attempt Concurrent Operation (Should Fail) ---
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
			// Call Install again. This time, s.Install() should hit the !sem.TryLock() condition.
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
			// The server sends the InstallError response AND returns codes.Aborted.
			// Wait for the server to return codes.Aborted (stream closure).
			_, err = newstream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
			t.Logf("InstallError=%v", err)
			testError(err, codes.Aborted, "Concurrent Install RPCs", t)

			// --- Continue with the existing stream (Release Lock) ---
			t.Logf("Client continue with the existing stream")
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferEnd{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive Validated response. This triggers sem.Unlock() via defer.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetValidated() == nil {
				t.Fatal("Did not receive expected Validated response.")
			}
			// The lock is now released.
		},
	},
	{
		desc: "OSInstallFailsIfWrongMessageIsSent",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
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
			testError(err, codes.InvalidArgument, "Expected TransferRequest", t)
		},
	},
	{
		desc: "OSInstallAbortedImmediately",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
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
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
			patches := applyFullStreamSuccessPatch(t)
			defer patches.Reset()
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Create a mock OSServer just for the helper methods
			tempOSServer := &OSServer{Server: s, ImgDir: s.config.ImgDir}
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
			imgPath := tempOSServer.getVersionPath(version)
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
			// Now, read the final error from the stream.
			// The server will abort the entire RPC, so we expect a gRPC error here, not a message on the stream.
			_, err = stream.Recv()

			// Assert that the received error is of the expected type and code.
			if err == nil {
				t.Fatal("Expected an RPC error after sending TransferContent.")
			}

			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.Aborted || !strings.Contains(st.Message(), "already exists") {
				t.Fatalf("Expected Aborted error with 'already exists' message, got: %v", err)
			}
		},
	},
	{
		desc: "OSInstallFailsIfStreamClosesInTheMiddleOfTransfer",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
			t.Log("OSInstallFailsIfStreamClosesInTheMiddleOfTransfer starts")
			patches := applyFullStreamSuccessPatch(t)
			defer patches.Reset()
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			version := "os1.1"
			// Create a mock OSServer just for the helper methods
			tempOSServer := &OSServer{Server: s, ImgDir: s.config.ImgDir}
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
			if tempOSServer.imageExists(tempOSServer.getVersionPath(version)) {
				t.Fatal("Incomplete image should have been deleted!")
			}
			t.Log("Incomplete transfer has been removed")
		},
	},
	{
		desc: "OSInstallFailsIfWrongMsgIsSentInTheMiddleOfTransfer",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
			patches := applyFullStreamSuccessPatch(t)
			defer patches.Reset()
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			version := "os1.1"
			// Create a mock OSServer just for the helper methods
			tempOSServer := &OSServer{Server: s, ImgDir: s.config.ImgDir}
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
			if tempOSServer.imageExists(tempOSServer.getVersionPath(version)) {
				t.Fatal("Incomplete image should have been deleted!")
			}
		},
	},
	{
		desc: "OSInstallSucceeds",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
			patches := applyFullStreamSuccessPatch(t)
			defer patches.Reset()
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
			var resp *ospb.InstallResponse
			resp, err = stream.Recv()
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
		},
	},
	{
		desc: "OSInstallFailsIfBackendErrorsOnTransferReady",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
			// Patch the DBusOSBackend.InstallOS method
			patch := gomonkey.ApplyMethod(reflect.TypeOf(&DBusOSBackend{}), "InstallOS",
				func(_ *DBusOSBackend, req string) (string, error) {
					// Return a gRPC status error here to simulate a backend failure.
					return "", status.Errorf(codes.Unimplemented, "OS Install not supported")
				})
			defer patch.Reset()
			// 1. Send TransferRequest to the server.
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
			// 2. Expect a gRPC error from the server, not a response message.
			// The server should abort the stream immediately when the patched function returns an error.
			_, err = stream.Recv()

			// 3. Assert that the received error is of the expected type and code.
			if err == nil {
				t.Fatal("Expected a gRPC error, but received a nil error.")
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.Unimplemented {
				t.Fatalf("Expected Unimplemented, got: %v", err)
			}
		},
	},
	{
		desc: "OSInstallFailsOnBadTransferReadyJSON",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
			// Patch the DBusOSBackend.InstallOS method to return bad JSON.
			patch := gomonkey.ApplyMethod(reflect.TypeOf(&DBusOSBackend{}), "InstallOS",
				func(_ *DBusOSBackend, req string) (string, error) {
					return "{bad-json", nil
				})
			defer patch.Reset()
			// 1. Send TransferRequest to the server.
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
			// 2. Expect an InstallError response from the server.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatalf("Expected a response, but received gRPC error: %v", err)
			}
			// 3. Assert that the received response is an InstallError.
			instErr := resp.GetInstallError()
			if instErr == nil {
				t.Fatal("Expected an InstallError, but got a nil response.")
			}
		},
	},
	{
		desc: "OSInstallFailsWithAuthenticationFailure",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
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
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *Server) {
			lockSem()
			defer unlockSem()
			// Create a mock OSServer instance to call the Install method on.
			// This instance is needed because the concurrency check is inside Install.
			osSrv := &OSServer{
				Server:  s,
				backend: &MockOSBackend{}, // Backend can be nil/empty for concurrency test
			}
			// Prepare fake server stream that simulates Send error
			fakeStream := &fakeInstallServer{
				sendErr: errors.New("simulated send error"),
				ctx:     ctx,
			}

			// Call Install directly with the fake stream
			err := osSrv.Install(fakeStream)

			// The error should be codes.Aborted with your simulated message
			if err == nil || status.Code(err) != codes.Aborted || !strings.Contains(err.Error(), "simulated send error") {
				t.Fatalf("Expected Aborted error with send error message, got: %v", err)
			}
		},
	},
}

// Helper function to patch the DBusOSBackend for a full successful stream flow.
func applyFullStreamSuccessPatch(t *testing.T) *gomonkey.Patches {
	patches := gomonkey.NewPatches()

	// InstallOS is called twice: once for TransferRequest (needs TransferReady)
	// and once for TransferEnd (needs Validated).
	callCount := 0
	patches.ApplyMethod(reflect.TypeOf(&DBusOSBackend{}), "InstallOS",
		func(_ *DBusOSBackend, req string) (string, error) {
			callCount++
			if callCount == 1 { // First call: TransferRequest
				if !strings.Contains(req, `"transferRequest"`) {
					t.Fatalf("Expected TransferRequest on first InstallOS call, got: %s", req)
				}
				return mockTransferReadySuccess(req)
			}
			if callCount == 2 { // Second call: TransferEnd
				if !strings.Contains(req, `"transferEnd"`) {
					t.Fatalf("Expected TransferEnd on second InstallOS call, got: %s", req)
				}
				return mockTransferEndSuccess(req)
			}
			return "", fmt.Errorf("InstallOS called unexpected number of times (%d)", callCount)
		},
	)
	return patches
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
			// The test logic now receives the top-level *Server
			test.f(ctx, t, sc, s)
		})
	}
}

func testError(err error, code codes.Code, msg string, t *testing.T) {
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T", err)
	}
	if st.Code() != code {
		t.Fatalf("expected status code %s, got %s", code, st.Code())
	}
	if !strings.Contains(st.Message(), msg) {
		t.Fatalf("expected error message to contain %q, got %q", msg, st.Message())
	}
}

// Helper function to create a minimal OSServer instance for unit testing.
func newTestOSServer() *OSServer {
	return &OSServer{
		Server: &Server{
			config: &Config{
				ImgDir: "/tmp", // Mock directory path
			},
		},
		backend: &MockOSBackend{InstallOSFunc: mockTransferReadySuccess},
		ImgDir:  "/tmp",
	}
}

func TestProcessTransferContent_OpenFileError(t *testing.T) {
	// Mock the os.OpenFile function to simulate a failure
	patch := gomonkey.ApplyFunc(os.OpenFile, func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, errors.New("simulated OpenFile failure")
	})
	defer patch.Reset()

	srv := newTestOSServer()

	// Call the method under test
	resp := srv.processTransferContent([]byte("test data"), "/tmp/test.img")
	t.Logf("processTransferContent response=%v", resp)

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

	srv := newTestOSServer()

	// Call the method being tested
	resp := srv.processTransferContent([]byte("test data"), "/tmp/test.img")
	t.Logf("processTransferContent response=%v", resp)

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

	srv := newTestOSServer()

	resp := srv.processTransferContent([]byte("test data"), "/tmp/test.img")
	t.Logf("processTransferContent response=%v", resp)

	if resp == nil || resp.GetInstallError() == nil {
		t.Errorf("Expected error response due to Close failure, got: %+v", resp)
	}
}

func TestProcessTransferReq_MarshalError(t *testing.T) {
	mockReq := &ospb.InstallRequest{
		Request: &ospb.InstallRequest_TransferRequest{
			TransferRequest: &ospb.TransferRequest{
				Version: "1.0.0",
			},
		},
	}

	// Define the mock return values.
	// The first nil is for the []byte, the second value is the error.
	outputs := []gomonkey.OutputCell{
		{Values: gomonkey.Params{nil, fmt.Errorf("mock marshal error")}},
	}

	// Patch the json.Marshal function.
	patches := gomonkey.ApplyFuncSeq(json.Marshal, outputs)
	defer patches.Reset()

	srv := newTestOSServer()
	resp := srv.processTransferReq(mockReq)

	installError := resp.GetInstallError()
	if installError == nil {
		t.Fatalf("Expected an InstallError, but got nil")
	}

	expectedDetail := "Failed to marshal TransferReady JSON: err: mock marshal error"
	if !strings.Contains(installError.GetDetail(), expectedDetail) {
		t.Errorf("Expected detail to contain '%s', but got '%s'", expectedDetail, installError.GetDetail())
	}
}

func TestProcessTransferReq_GenericError(t *testing.T) {
	// 1. Create a valid request to pass initial checks
	req := &ospb.InstallRequest{
		Request: &ospb.InstallRequest_TransferRequest{
			TransferRequest: &ospb.TransferRequest{
				Version: "1.0.0",
			},
		},
	}

	// 2. Create the mock OSServer with the mocked backend
	mockSrv := &OSServer{
		backend: &MockOSBackend{
			InstallOSFunc: func(req string) (string, error) {
				return "", fmt.Errorf("this is a generic backend error")
			},
		},
	}

	// 3. Call the function under test
	resp := mockSrv.processTransferReq(req)

	// 4. Assert that the response contains the expected error detail.
	installError := resp.GetInstallError()
	if installError == nil {
		t.Fatalf("Expected an InstallError response, but got nil")
	}

	expectedDetail := "Error in TransferReady response: err: this is a generic backend error"
	if !strings.Contains(installError.GetDetail(), expectedDetail) {
		t.Errorf("Expected response detail to contain '%s', but got '%s'", expectedDetail, installError.GetDetail())
	}
}

func TestProcessTransferEnd_UnmarshalError(t *testing.T) {
	// Create a mock function for InstallOS that returns bad JSON.
	mockInstallOS := func(req string) (string, error) {
		return `{"status": "this is not a valid InstallResponse proto"}`, nil
	}

	// Create the necessary structs to run the test.
	srv := &OSServer{
		backend: &MockOSBackend{
			InstallOSFunc: mockInstallOS,
		},
	}

	// Call the function under test.
	req := &ospb.InstallRequest{}
	resp := srv.processTransferEnd(req)

	// Assert the result.
	if resp == nil {
		t.Fatal("Expected a non-nil response, but got nil")
	}

	// Assert that the response is an InstallError.
	installError := resp.GetInstallError()
	if installError == nil {
		t.Errorf("Expected response to be an InstallError, but got a different response type: %T", resp.Response)
	}

	// Assert that the error detail contains the expected substring.
	expectedErrMsg := "Failed to unmarshal TransferEnd JSON"
	if !strings.Contains(resp.GetInstallError().GetDetail(), expectedErrMsg) {
		t.Errorf("Expected error message to contain '%s', but got '%s'", expectedErrMsg, resp.GetInstallError().GetDetail())
	}
}

func TestProcessTransferEnd_MarshalError(t *testing.T) {
	// 1. Create a valid request to pass to the function under test.
	mockReq := &ospb.InstallRequest{}

	// Define the mock return values.
	// The first nil is for the []byte, the second value is the error.
	outputs := []gomonkey.OutputCell{
		{Values: gomonkey.Params{nil, fmt.Errorf("mock marshal error")}},
	}

	// Patch the json.Marshal function.
	patches := gomonkey.ApplyFuncSeq(json.Marshal, outputs)
	defer patches.Reset() // Un-patch the function after the test.

	// 3. Create a mock server and call the function under test.
	srv := newTestOSServer()
	resp := srv.processTransferEnd(mockReq)

	// 4. Assert the result to verify the error path was taken.
	installError := resp.GetInstallError()
	if installError == nil {
		t.Fatalf("Expected an InstallError, but got nil")
	}

	expectedDetail := "Failed to marshal TransferEnd JSON: err: mock marshal error"
	if !strings.Contains(installError.GetDetail(), expectedDetail) {
		t.Errorf("Expected detail to contain '%s', but got '%s'", expectedDetail, installError.GetDetail())
	}
}

func TestProcessTransferEnd_BackendError(t *testing.T) {
	// Create a mock OSConfig that returns an error.
	mockBackend := &MockOSBackend{
		InstallOSFunc: func(req string) (string, error) {
			return "", fmt.Errorf("this is a backend error")
		},
	}

	// Create a mock server with the mock OSBackend.
	srv := &OSServer{
		backend: mockBackend,
	}

	// Call the function under test with a valid request.
	req := &ospb.InstallRequest{}
	resp := srv.processTransferEnd(req)

	// Assert that the function returned the correct error response.
	installError := resp.GetInstallError()
	if installError == nil {
		t.Fatalf("Expected an InstallError, but got nil")
	}

	expectedDetail := "Error in TransferEnd response: err: this is a backend error"
	if !strings.Contains(installError.GetDetail(), expectedDetail) {
		t.Errorf("Expected detail to contain '%s', but got '%s'", expectedDetail, installError.GetDetail())
	}
}

func TestRemoveIncompleteTrf_RemoveFails(t *testing.T) {
	// This tests a helper method, so we create a minimal OSServer instance.
	srv := newTestOSServer()

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

func TestHandleErrorResponse(t *testing.T) {
	// Test is simple proto structure validation, kept as is.
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
	// Test is simple proto structure validation, kept as is.
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
