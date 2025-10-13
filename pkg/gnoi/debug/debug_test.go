package debug

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sonic-net/sonic-gnmi/internal/debug"
	debug_pb "github.com/sonic-net/sonic-gnmi/proto/gnoi/debug"
)

var (
	originalRunCommand = debug.RunCommand
	testWhitelist      = []string{
		"ls",
		"show",
		"invalid-command",
		"sleep",
		"any",
	}
)

type mockDebugServerStream struct {
	ctx       context.Context
	responses []*debug_pb.DebugResponse
	sendErr   error
	mu        sync.Mutex
	grpc.ServerStream
}

func (s *mockDebugServerStream) Context() context.Context {
	return s.ctx
}

func (s *mockDebugServerStream) Send(resp *debug_pb.DebugResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendErr != nil {
		return s.sendErr
	}
	s.responses = append(s.responses, resp)
	return nil
}

func (s *mockDebugServerStream) getResponses() []*debug_pb.DebugResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Return a copy to avoid race conditions
	resps := make([]*debug_pb.DebugResponse, len(s.responses))
	copy(resps, s.responses)
	return resps
}

func TestHandleCommandRequest(t *testing.T) {
	testCases := []struct {
		name         string
		req          *debug_pb.DebugRequest
		runCmdFn     func(ctx context.Context, outCh chan<- string, errCh chan<- string, roleAccount string, byteLimit int64, cmd string, args ...string) (int, error)
		expectedData []string
		expectedCode int32
		expectErr    bool
		errType      codes.Code
		errMsg       string
	}{
		{
			name:      "Error on nil request",
			req:       nil,
			expectErr: true,
			errType:   codes.InvalidArgument,
			errMsg:    "request cannot be nil",
		},
		{
			name:      "Error on nil command",
			req:       &debug_pb.DebugRequest{},
			expectErr: true,
			errType:   codes.InvalidArgument,
		},
		{
			name: "Error on unimplemented SHELL mode",
			req: &debug_pb.DebugRequest{
				Command: []byte("ls"),
				Mode:    debug_pb.DebugRequest_MODE_SHELL,
			},
			expectErr: true,
			errType:   codes.Unimplemented,
		},
		{
			name: "Error on UNSPECIFIED mode",
			req: &debug_pb.DebugRequest{
				Command: []byte("ls"),
				Mode:    debug_pb.DebugRequest_MODE_UNSPECIFIED,
			},
			expectErr: true,
			errType:   codes.InvalidArgument,
		},
		{
			name: "Successful execution",
			req: &debug_pb.DebugRequest{
				Command:     []byte("show version"),
				Mode:        debug_pb.DebugRequest_MODE_CLI,
				RoleAccount: "test-admin",
				ByteLimit:   1024,
			},
			runCmdFn: func(ctx context.Context, outCh chan<- string, errCh chan<- string, roleAccount string, byteLimit int64, cmd string, args ...string) (int, error) {
				defer func() {
					close(outCh)
					close(errCh)
				}()

				outCh <- "SONiC Version: 202305"
				errCh <- "Some warning on stderr"
				return 0, nil
			},
			expectedCode: 0,
			expectedData: []string{
				"SONiC Version: 202305",
				"Some warning on stderr",
			},
		},
		{
			name: "Execution fails with non-zero exit code",
			req: &debug_pb.DebugRequest{
				Command: []byte("invalid-command"),
				Mode:    debug_pb.DebugRequest_MODE_CLI,
			},
			runCmdFn: func(ctx context.Context, outCh chan<- string, errCh chan<- string, roleAccount string, byteLimit int64, cmd string, args ...string) (int, error) {
				defer func() {
					close(outCh)
					close(errCh)
				}()
				errCh <- "command not found"
				return 127, nil
			},
			expectedCode: 127,
			expectedData: []string{
				"command not found",
			},
		},
		{
			name: "RunCommand returns an infrastructure error",
			req: &debug_pb.DebugRequest{
				Command: []byte("any"),
				Mode:    debug_pb.DebugRequest_MODE_CLI,
			},
			runCmdFn: func(ctx context.Context, outCh chan<- string, errCh chan<- string, roleAccount string, byteLimit int64, cmd string, args ...string) (int, error) {
				close(outCh)
				close(errCh)
				return -1, errors.New("failed to start process")
			},
			expectErr: true,
			errType:   codes.FailedPrecondition,
			errMsg:    "failed to start process",
		},
	}

	// Annoying to capture in table tests, just keep separate
	t.Run("Timeout is respected", func(t *testing.T) {
		req := &debug_pb.DebugRequest{
			Command: []byte("sleep 10"),
			Mode:    debug_pb.DebugRequest_MODE_CLI,
			Timeout: (50 * time.Millisecond).Nanoseconds(),
		}
		stream := &mockDebugServerStream{ctx: context.Background()}
		var capturedCtx context.Context

		runCommand = func(ctx context.Context, outCh chan<- string, errCh chan<- string, roleAccount string, byteLimit int64, cmd string, args ...string) (int, error) {
			close(outCh)
			close(errCh)
			capturedCtx = ctx
			// Simulate a long-running command that respects cancellation
			<-ctx.Done()
			return -1, ctx.Err() // Return context error
		}

		err := HandleCommandRequest(req, stream, testWhitelist)
		if err == nil {
			t.Fatal("expected an error but got nil")
		}
		if capturedCtx == nil {
			t.Fatal("RunCommand was not called with a context")
		}
		if !errors.Is(capturedCtx.Err(), context.DeadlineExceeded) {
			t.Errorf("expected context error %v, got %v", context.DeadlineExceeded, capturedCtx.Err())
		}
	})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.runCmdFn != nil {
				runCommand = tc.runCmdFn
			}
			defer func() { runCommand = originalRunCommand }()

			stream := &mockDebugServerStream{ctx: context.Background()}
			err := HandleCommandRequest(tc.req, stream, testWhitelist)

			if tc.expectErr {
				if err == nil {
					t.Fatal("expected an error, but got nil")
				}

				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("expected error to be a gRPC error, got: %v", err)
				}

				if st.Code() != tc.errType {
					t.Errorf("expected code %v, got: %v", tc.errType, st.Code())
				}

				if !strings.Contains(st.Message(), tc.errMsg) {
					t.Errorf("expected message to contain %q, got: %q", tc.errMsg, st.Message())
				}
			} else {
				if err != nil {
					t.Fatalf("did not expect an error but got: %v", err)
				}

				responses := stream.getResponses()
				expectedNumRes := len(tc.expectedData) + 2
				if len(responses) != expectedNumRes {
					t.Fatalf("expected %d responses, got %d", expectedNumRes, len(responses))
				}

				// 1. Check first res is the request
				if !reflect.DeepEqual(tc.req, responses[0].GetRequest()) {
					t.Errorf("unexpected request response, got %+v", responses[0].GetRequest())
				}

				// 2. Check the in-between res
				if tc.expectedData != nil {
					for _, res := range responses[1 : len(responses)-1] {
						data := string(res.GetData())

						if !sliceContains(tc.expectedData, data) {
							t.Errorf("responses did not contain expected data: %s", data)
						}
					}
				}

				// Check the in-between res
				// 3. Check last is the exit code
				if code := responses[len(responses)-1].GetStatus().GetCode(); code != tc.expectedCode {
					t.Errorf("expected exit code %d, got %d", tc.expectedCode, code)
				}
			}
		})
	}
}

// --- Test Helper Functions ---

func TestSendHelpers(t *testing.T) {
	t.Run("sendReqInResponse", func(t *testing.T) {
		stream := &mockDebugServerStream{ctx: context.Background()}
		req := &debug_pb.DebugRequest{Command: []byte("test")}
		err := sendReqInResponse(stream, req)
		if err != nil {
			t.Fatalf("did not expect an error but got: %v", err)
		}
		responses := stream.getResponses()
		if len(responses) != 1 {
			t.Fatalf("expected 1 response, got %d", len(responses))
		}
		if !reflect.DeepEqual(req, responses[0].GetRequest()) {
			t.Errorf("unexpected request response, got %+v", responses[0].GetRequest())
		}
	})

	t.Run("sendDataInResponse", func(t *testing.T) {
		stream := &mockDebugServerStream{ctx: context.Background()}
		data := "hello world"
		err := sendDataInResponse(stream, data)
		if err != nil {
			t.Fatalf("did not expect an error but got: %v", err)
		}
		responses := stream.getResponses()
		if len(responses) != 1 {
			t.Fatalf("expected 1 response, got %d", len(responses))
		}
		if !bytes.Equal([]byte(data), responses[0].GetData()) {
			t.Errorf("unexpected data response, got %q", string(responses[0].GetData()))
		}
	})

	t.Run("sendStatusInResponse", func(t *testing.T) {
		stream := &mockDebugServerStream{ctx: context.Background()}
		exitCode := 42
		err := sendStatusInResponse(stream, exitCode)
		if err != nil {
			t.Fatalf("did not expect an error but got: %v", err)
		}
		responses := stream.getResponses()
		if len(responses) != 1 {
			t.Fatalf("expected 1 response, got %d", len(responses))
		}
		if code := responses[0].GetStatus().GetCode(); code != int32(exitCode) {
			t.Errorf("expected exit code %d, got %d", exitCode, code)
		}
	})
}
