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

var originalRunCommand = debug.RunCommand

type mockDebugServerStream struct {
	ctx context.Context
	responses []*debug_pb.DebugResponse
	sendErr error
	mu sync.Mutex
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
	defer func() { runCommand = originalRunCommand }()

	t.Run("Error on nil request", func(t *testing.T) {
		stream := &mockDebugServerStream{ctx: context.Background()}
		err := HandleCommandRequest(nil, stream)
		if err == nil {
			t.Fatal("expected an error but got nil")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected error to be a gRPC status error")
		}
		if st.Code() != codes.InvalidArgument {
			t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
		}
		expectedMsg := "request cannot be nil"
		if !strings.Contains(st.Message(), expectedMsg) {
			t.Errorf("expected message to contain %q, but it was %q", expectedMsg, st.Message())
		}
	})

	t.Run("Error on nil command", func(t *testing.T) {
		req := &debug_pb.DebugRequest{}
		stream := &mockDebugServerStream{ctx: context.Background()}
		err := HandleCommandRequest(req, stream)
		if err == nil {
			t.Fatal("expected an error but got nil")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected error to be a gRPC status error")
		}
		if st.Code() != codes.InvalidArgument {
			t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
		}
	})

	t.Run("Error on unimplemented SHELL mode", func(t *testing.T) {
		req := &debug_pb.DebugRequest{
			Command: []byte("ls"),
			Mode:    debug_pb.DebugRequest_MODE_SHELL,
		}
		stream := &mockDebugServerStream{ctx: context.Background()}
		err := HandleCommandRequest(req, stream)
		if err == nil {
			t.Fatal("expected an error but got nil")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected error to be a gRPC status error")
		}
		if st.Code() != codes.Unimplemented {
			t.Errorf("expected code %v, got %v", codes.Unimplemented, st.Code())
		}
	})

	t.Run("Error on UNSPECIFIED mode", func(t *testing.T) {
		req := &debug_pb.DebugRequest{
			Command: []byte("ls"),
			Mode:    debug_pb.DebugRequest_MODE_UNSPECIFIED,
		}
		stream := &mockDebugServerStream{ctx: context.Background()}
		err := HandleCommandRequest(req, stream)
		if err == nil {
			t.Fatal("expected an error but got nil")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected error to be a gRPC status error")
		}
		if st.Code() != codes.InvalidArgument {
			t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
		}
	})

	t.Run("Successful execution", func(t *testing.T) {
		req := &debug_pb.DebugRequest{
			Command:     []byte("show version"),
			Mode:        debug_pb.DebugRequest_MODE_CLI,
			RoleAccount: "test-admin",
			ByteLimit:   1024,
		}
		stream := &mockDebugServerStream{ctx: context.Background()}

		// Mock RunCommand to simulate a successful execution with output
		runCommand = func(ctx context.Context, outCh chan<- string, errCh chan<- string, cmdStr string, roleAccount string, byteLimit int64) (int, error) {
			defer func() {
				close(outCh)
				close(errCh)
			}()
			if cmdStr != "show version" {
				t.Errorf("expected command %q, got %q", "show version", cmdStr)
			}
			if roleAccount != "test-admin" {
				t.Errorf("expected role %q, got %q", "test-admin", roleAccount)
			}
			if byteLimit != 1024 {
				t.Errorf("expected byte limit %d, got %d", 1024, byteLimit)
			}
			outCh <- "SONiC Version: 202305"
			errCh <- "Some warning on stderr"
			return 0, nil // Exit code 0, no error
		}

		err := HandleCommandRequest(req, stream)
		if err != nil {
			t.Fatalf("did not expect an error but got: %v", err)
		}

		responses := stream.getResponses()
		if len(responses) != 4 {
			t.Logf("res: %+v", responses)
			t.Fatalf("expected 4 responses, got %d", len(responses))
		}

		// 1. Check Request response
		if !reflect.DeepEqual(req, responses[0].GetRequest()) {
			t.Errorf("unexpected request response, got %+v", responses[0].GetRequest())
		}

		// 2. Check Data responses (order is not guaranteed between stdout/stderr)
		stdout := "SONiC Version: 202305"
		stderr := "Some warning on stderr"
		data1 := string(responses[1].GetData())
		data2 := string(responses[2].GetData())

		if !((data1 == stdout && data2 == stderr) || (data1 == stderr && data2 == stdout)) {
			t.Errorf("unexpected data responses, got %q and %q", data1, data2)
		}

		// 3. Check Status response
		if code := responses[3].GetStatus().GetCode(); code != 0 {
			t.Errorf("expected exit code 0, got %d", code)
		}
	})

	t.Run("Execution fails with non-zero exit code", func(t *testing.T) {
		req := &debug_pb.DebugRequest{
			Command: []byte("invalid-command"),
			Mode:    debug_pb.DebugRequest_MODE_CLI,
		}
		stream := &mockDebugServerStream{ctx: context.Background()}

		runCommand = func(ctx context.Context, outCh chan<- string, errCh chan<- string, cmdStr string, roleAccount string, byteLimit int64) (int, error) {
			defer func() {
				close(outCh)
				close(errCh)
			}()
			errCh <- "command not found"
			return 127, nil // Exit code 127
		}

		err := HandleCommandRequest(req, stream)
		if err != nil {
			t.Fatalf("did not expect an error but got: %v", err)
		}

		responses := stream.getResponses()
		if len(responses) != 3 { // Request, Data, Status
			t.Fatalf("expected 3 responses, got %d", len(responses))
		}
		if code := responses[2].GetStatus().GetCode(); code != 127 {
			t.Errorf("expected exit code 127, got %d", code)
		}
	})

	t.Run("RunCommand returns an infrastructure error", func(t *testing.T) {
		req := &debug_pb.DebugRequest{
			Command: []byte("any"),
			Mode:    debug_pb.DebugRequest_MODE_CLI,
		}
		stream := &mockDebugServerStream{ctx: context.Background()}
		expectedErr := errors.New("failed to start process")

		runCommand = func(ctx context.Context, outCh chan<- string, errCh chan<- string, cmdStr string, roleAccount string, byteLimit int64) (int, error) {
			close(outCh)
			close(errCh)
			return -1, expectedErr
		}

		err := HandleCommandRequest(req, stream)
		if err == nil {
			t.Fatal("expected an error but got nil")
		}

		st, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected error to be a gRPC status error")
		}
		if st.Code() != codes.FailedPrecondition {
			t.Errorf("expected code %v, got %v", codes.FailedPrecondition, st.Code())
		}
		if !strings.Contains(st.Message(), expectedErr.Error()) {
			t.Errorf("expected message to contain %q, but it was %q", expectedErr.Error(), st.Message())
		}
	})

	t.Run("Timeout is respected", func(t *testing.T) {
		req := &debug_pb.DebugRequest{
			Command: []byte("sleep 10"),
			Mode:    debug_pb.DebugRequest_MODE_CLI,
			Timeout: (50 * time.Millisecond).Nanoseconds(),
		}
		stream := &mockDebugServerStream{ctx: context.Background()}
		var capturedCtx context.Context

		runCommand = func(ctx context.Context, outCh chan<- string, errCh chan<- string, cmdStr string, roleAccount string, byteLimit int64) (int, error) {
			close(outCh)
			close(errCh)
			capturedCtx = ctx
			// Simulate a long-running command that respects cancellation
			<-ctx.Done()
			return -1, ctx.Err() // Return context error
		}

		err := HandleCommandRequest(req, stream)
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
