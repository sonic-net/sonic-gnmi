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

	debugpb "github.com/openconfig/gnoi/debug"
	exec "github.com/sonic-net/sonic-gnmi/internal/exec"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type mockDebugServerStream struct {
	ctx       context.Context
	responses []*debugpb.DebugResponse
	sendErr   error
	failSend  int
	sendCalls int
	mu        sync.Mutex
	grpc.ServerStream
}

func (s *mockDebugServerStream) Context() context.Context {
	return s.ctx
}

func (s *mockDebugServerStream) Send(resp *debugpb.DebugResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendCalls++
	if s.sendErr != nil && (s.failSend == 0 || s.sendCalls == s.failSend) {
		return s.sendErr
	}
	s.responses = append(s.responses, resp)
	return nil
}

func (s *mockDebugServerStream) getResponses() []*debugpb.DebugResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	responses := make([]*debugpb.DebugResponse, len(s.responses))
	copy(responses, s.responses)
	return responses
}

func testPolicy(t *testing.T) *Policy {
	t.Helper()
	policy, err := LoadPolicy(writePolicyFile(t, `
version: 1
enabled_actions:
  - uptime
`))
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}
	return policy
}

func TestHandleCommandRequest(t *testing.T) {
	original := runCommand
	t.Cleanup(func() { runCommand = original })

	tests := []struct {
		name       string
		req        *debugpb.DebugRequest
		policy     *Policy
		access     AccessLevel
		run        func(context.Context, chan<- string, chan<- string, exec.ExecutionPlan) (int, error)
		wantCode   codes.Code
		wantData   []string
		wantStatus int32
		wantCalls  int
	}{
		{
			name:     "nil request",
			policy:   testPolicy(t),
			access:   AccessReadOnly,
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "nil command",
			req:      &debugpb.DebugRequest{Mode: debugpb.DebugRequest_MODE_CLI},
			policy:   testPolicy(t),
			access:   AccessReadOnly,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "shell mode remains unavailable",
			req: &debugpb.DebugRequest{
				Command: []byte("uptime"),
				Mode:    debugpb.DebugRequest_MODE_SHELL,
			},
			policy:   testPolicy(t),
			access:   AccessReadOnly,
			wantCode: codes.Unimplemented,
		},
		{
			name: "unspecified mode",
			req: &debugpb.DebugRequest{
				Command: []byte("uptime"),
			},
			policy:   testPolicy(t),
			access:   AccessReadOnly,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "caller selected account",
			req: &debugpb.DebugRequest{
				Command:     []byte("uptime"),
				Mode:        debugpb.DebugRequest_MODE_CLI,
				RoleAccount: "root",
			},
			policy:   testPolicy(t),
			access:   AccessReadOnly,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "negative timeout",
			req: &debugpb.DebugRequest{
				Command: []byte("uptime"),
				Mode:    debugpb.DebugRequest_MODE_CLI,
				Timeout: -1,
			},
			policy:   testPolicy(t),
			access:   AccessReadOnly,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "oversized output limit",
			req: &debugpb.DebugRequest{
				Command:   []byte("uptime"),
				Mode:      debugpb.DebugRequest_MODE_CLI,
				ByteLimit: 64*1024 + 1,
			},
			policy:   testPolicy(t),
			access:   AccessReadOnly,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "policy unavailable",
			req: &debugpb.DebugRequest{
				Command: []byte("uptime"),
				Mode:    debugpb.DebugRequest_MODE_CLI,
			},
			policy:   NewUnavailablePolicy(errors.New("load failed")),
			access:   AccessReadOnly,
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "action rejected",
			req: &debugpb.DebugRequest{
				Command: []byte("uptime --help"),
				Mode:    debugpb.DebugRequest_MODE_CLI,
			},
			policy:   testPolicy(t),
			access:   AccessReadOnly,
			wantCode: codes.PermissionDenied,
		},
		{
			name: "successful execution",
			req: &debugpb.DebugRequest{
				Command:   []byte("uptime"),
				Mode:      debugpb.DebugRequest_MODE_CLI,
				ByteLimit: 1024,
				Timeout:   int64(time.Second),
			},
			policy: testPolicy(t),
			access: AccessReadOnly,
			run: func(ctx context.Context, outCh chan<- string, errCh chan<- string, plan exec.ExecutionPlan) (int, error) {
				defer close(outCh)
				defer close(errCh)
				if plan.Executable != "/usr/bin/uptime" || plan.User != "admin" {
					return -1, errors.New("unexpected execution plan")
				}
				if plan.Timeout != time.Second || plan.OutputLimit != 1024 {
					return -1, errors.New("request limits were not applied")
				}
				outCh <- "up 10 days"
				errCh <- "warning"
				return 0, nil
			},
			wantData:   []string{"up 10 days", "warning"},
			wantStatus: 0,
			wantCalls:  1,
		},
		{
			name: "executor failure",
			req: &debugpb.DebugRequest{
				Command: []byte("uptime"),
				Mode:    debugpb.DebugRequest_MODE_CLI,
			},
			policy: testPolicy(t),
			access: AccessReadOnly,
			run: func(ctx context.Context, outCh chan<- string, errCh chan<- string, plan exec.ExecutionPlan) (int, error) {
				close(outCh)
				close(errCh)
				return -1, errors.New("failed to start")
			},
			wantCode:  codes.FailedPrecondition,
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			runCommand = func(ctx context.Context, outCh chan<- string, errCh chan<- string, plan exec.ExecutionPlan) (int, error) {
				calls++
				if tt.run != nil {
					return tt.run(ctx, outCh, errCh, plan)
				}
				close(outCh)
				close(errCh)
				return 0, nil
			}

			stream := &mockDebugServerStream{ctx: context.Background()}
			err := HandleCommandRequest(tt.req, stream, tt.policy, tt.access)
			if tt.wantCode != codes.OK {
				if status.Code(err) != tt.wantCode {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err != nil {
				t.Fatalf("HandleCommandRequest() error: %v", err)
			}
			if calls != tt.wantCalls {
				t.Fatalf("unexpected executor calls: got %d, want %d", calls, tt.wantCalls)
			}
			if tt.wantCode != codes.OK {
				return
			}

			responses := stream.getResponses()
			if len(responses) != len(tt.wantData)+2 {
				t.Fatalf("unexpected response count: %d", len(responses))
			}
			if !reflect.DeepEqual(tt.req, responses[0].GetRequest()) {
				t.Fatalf("unexpected request response: %+v", responses[0].GetRequest())
			}
			var data []string
			for _, response := range responses[1 : len(responses)-1] {
				data = append(data, string(response.GetData()))
			}
			for _, want := range tt.wantData {
				if !containsString(data, want) {
					t.Fatalf("missing response data %q in %q", want, data)
				}
			}
			if got := responses[len(responses)-1].GetStatus().GetCode(); got != tt.wantStatus {
				t.Fatalf("unexpected status: %d", got)
			}
		})
	}
}

func TestHandleCommandRequestCancelsOnDataSendFailure(t *testing.T) {
	original := runCommand
	t.Cleanup(func() { runCommand = original })

	canceled := make(chan struct{})
	runCommand = func(ctx context.Context, outCh chan<- string, errCh chan<- string, plan exec.ExecutionPlan) (int, error) {
		defer close(outCh)
		defer close(errCh)
		outCh <- "output"
		<-ctx.Done()
		close(canceled)
		return -1, ctx.Err()
	}

	sendErr := errors.New("send failed")
	stream := &mockDebugServerStream{
		ctx:      context.Background(),
		sendErr:  sendErr,
		failSend: 2,
	}
	req := &debugpb.DebugRequest{
		Command: []byte("uptime"),
		Mode:    debugpb.DebugRequest_MODE_CLI,
	}
	err := HandleCommandRequest(req, stream, testPolicy(t), AccessReadOnly)
	if !errors.Is(err, sendErr) {
		t.Fatalf("HandleCommandRequest() error = %v, want send error", err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("executor context was not canceled")
	}
	responses := stream.getResponses()
	if len(responses) != 1 || !reflect.DeepEqual(req, responses[0].GetRequest()) {
		t.Fatalf("unexpected responses before send failure: %+v", responses)
	}
}

func TestDebugWireCompatibilityGolden(t *testing.T) {
	// mode=CLI, command="uptime", byte_limit=1024, role_account="legacy"
	wire := []byte{
		0x08, 0x02,
		0x12, 0x06, 'u', 'p', 't', 'i', 'm', 'e',
		0x18, 0x80, 0x08,
		0x2a, 0x06, 'l', 'e', 'g', 'a', 'c', 'y',
	}
	var req debugpb.DebugRequest
	if err := proto.Unmarshal(wire, &req); err != nil {
		t.Fatalf("failed to decode legacy request: %v", err)
	}
	if req.GetMode() != debugpb.DebugRequest_MODE_CLI ||
		string(req.GetCommand()) != "uptime" ||
		req.GetByteLimit() != 1024 ||
		req.GetRoleAccount() != "legacy" {
		t.Fatalf(
			"unexpected decoded request: mode=%v command=%q byte_limit=%d role_account=%q",
			req.GetMode(), req.GetCommand(), req.GetByteLimit(), req.GetRoleAccount(),
		)
	}
}

func TestSendHelpers(t *testing.T) {
	stream := &mockDebugServerStream{ctx: context.Background()}
	req := &debugpb.DebugRequest{Command: []byte("uptime")}
	if err := sendReqInResponse(stream, req); err != nil {
		t.Fatalf("sendReqInResponse() error: %v", err)
	}
	if err := sendDataInResponse(stream, "hello"); err != nil {
		t.Fatalf("sendDataInResponse() error: %v", err)
	}
	if err := sendStatusInResponse(stream, 42); err != nil {
		t.Fatalf("sendStatusInResponse() error: %v", err)
	}

	responses := stream.getResponses()
	if len(responses) != 3 {
		t.Fatalf("unexpected response count: %d", len(responses))
	}
	if !reflect.DeepEqual(req, responses[0].GetRequest()) {
		t.Fatalf("unexpected request: %+v", responses[0].GetRequest())
	}
	if !bytes.Equal(responses[1].GetData(), []byte("hello")) {
		t.Fatalf("unexpected data: %q", responses[1].GetData())
	}
	if responses[2].GetStatus().GetCode() != 42 {
		t.Fatalf("unexpected status: %d", responses[2].GetStatus().GetCode())
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
