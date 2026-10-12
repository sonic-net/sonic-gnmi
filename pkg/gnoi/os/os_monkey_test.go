package os

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sonic-net/sonic-gnmi/pkg/exec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHandleVerifyCommandConfiguration(t *testing.T) {
	var (
		gotCommand   string
		gotArgs      []string
		gotTimeout   time.Duration
		gotRemaining time.Duration
	)

	resp, err := handleVerify(context.Background(), verifyOperationTimeout,
		func(ctx context.Context, command string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
			gotCommand = command
			gotArgs = append([]string(nil), args...)
			if opts != nil {
				gotTimeout = opts.Timeout
			}
			if deadline, ok := ctx.Deadline(); ok {
				gotRemaining = time.Until(deadline)
			}
			return &exec.CommandResult{
				Stdout: "Current: SONiC-OS-20261012.01",
			}, nil
		})
	if err != nil {
		t.Fatalf("handleVerify() error = %v", err)
	}
	if resp.GetVersion() != "SONiC-OS-20261012.01" {
		t.Fatalf("handleVerify() version = %q, want %q", resp.GetVersion(), "SONiC-OS-20261012.01")
	}
	if gotCommand != sonicInstallerPath {
		t.Errorf("command = %q, want %q", gotCommand, sonicInstallerPath)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "list" {
		t.Errorf("args = %q, want [list]", gotArgs)
	}
	if gotTimeout != verifyOperationTimeout {
		t.Errorf("executor timeout = %v, want %v", gotTimeout, verifyOperationTimeout)
	}
	if gotRemaining <= verifyOperationTimeout-time.Second || gotRemaining > verifyOperationTimeout {
		t.Errorf("operation context remaining = %v, want approximately %v", gotRemaining, verifyOperationTimeout)
	}
}

func TestHandleVerifySuccessfulEmptyVersion(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
	}{
		{
			name:   "missing current line",
			stdout: "Next: SONiC-OS-20261012.01\nAvailable:",
		},
		{
			name:   "empty current value",
			stdout: "Current: \nNext: SONiC-OS-20261012.01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := handleVerify(context.Background(), verifyOperationTimeout,
				func(context.Context, string, []string, *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
					return &exec.CommandResult{Stdout: tt.stdout}, nil
				})
			if err != nil {
				t.Fatalf("handleVerify() error = %v", err)
			}
			if resp.GetVersion() != "" {
				t.Errorf("handleVerify() version = %q, want empty", resp.GetVersion())
			}
		})
	}
}

func TestHandleVerifySanitizesCommandFailures(t *testing.T) {
	const (
		stdoutSentinel = "stdout-secret-sentinel"
		stderrSentinel = "stderr-secret-sentinel"
		errorSentinel  = "/host/secret/error-sentinel"
	)

	tests := []struct {
		name string
		run  hostCommandRunner
	}{
		{
			name: "executor error",
			run: func(context.Context, string, []string, *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				return nil, errors.New(errorSentinel)
			},
		},
		{
			name: "command error",
			run: func(context.Context, string, []string, *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				return &exec.CommandResult{
					Stdout:   stdoutSentinel,
					Stderr:   stderrSentinel,
					ExitCode: 37,
					Error:    errors.New(errorSentinel),
				}, nil
			},
		},
		{
			name: "nil result",
			run: func(context.Context, string, []string, *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				return nil, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := handleVerify(context.Background(), verifyOperationTimeout, tt.run)
			if resp != nil {
				t.Fatalf("handleVerify() response = %#v, want nil", resp)
			}
			if status.Code(err) != codes.Internal {
				t.Fatalf("handleVerify() code = %v, want %v", status.Code(err), codes.Internal)
			}
			message := status.Convert(err).Message()
			if message != verifyCommandFailureMessage {
				t.Errorf("handleVerify() message = %q, want %q", message, verifyCommandFailureMessage)
			}
			for _, sentinel := range []string{stdoutSentinel, stderrSentinel, errorSentinel, sonicInstallerPath} {
				if strings.Contains(message, sentinel) {
					t.Errorf("handleVerify() message %q contains sensitive sentinel %q", message, sentinel)
				}
			}
		})
	}
}

func TestHandleVerifyRequestContextPrecedence(t *testing.T) {
	t.Run("canceled request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		resp, err := handleVerify(ctx, verifyOperationTimeout,
			func(context.Context, string, []string, *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				return nil, errors.New("executor failure")
			})
		if resp != nil {
			t.Fatalf("handleVerify() response = %#v, want nil", resp)
		}
		if status.Code(err) != codes.Canceled {
			t.Errorf("handleVerify() code = %v, want %v", status.Code(err), codes.Canceled)
		}
	})

	t.Run("request deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		resp, err := handleVerify(ctx, verifyOperationTimeout,
			func(context.Context, string, []string, *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				return nil, errors.New("executor failure")
			})
		if resp != nil {
			t.Fatalf("handleVerify() response = %#v, want nil", resp)
		}
		if status.Code(err) != codes.DeadlineExceeded {
			t.Errorf("handleVerify() code = %v, want %v", status.Code(err), codes.DeadlineExceeded)
		}
	})

	t.Run("request cancellation wins operation race", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		resp, err := handleVerify(ctx, verifyOperationTimeout,
			func(operationCtx context.Context, _ string, _ []string, _ *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				cancel()
				<-operationCtx.Done()
				return &exec.CommandResult{Error: operationCtx.Err()}, nil
			})
		if resp != nil {
			t.Fatalf("handleVerify() response = %#v, want nil", resp)
		}
		if status.Code(err) != codes.Canceled {
			t.Errorf("handleVerify() code = %v, want %v", status.Code(err), codes.Canceled)
		}
	})
}

func TestHandleVerifyOperationTimeout(t *testing.T) {
	const timeout = 20 * time.Millisecond

	resp, err := handleVerify(context.Background(), timeout,
		func(ctx context.Context, _ string, _ []string, _ *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
			<-ctx.Done()
			return &exec.CommandResult{Error: ctx.Err()}, nil
		})
	if resp != nil {
		t.Fatalf("handleVerify() response = %#v, want nil", resp)
	}
	if status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("handleVerify() code = %v, want %v", status.Code(err), codes.DeadlineExceeded)
	}
	if message := status.Convert(err).Message(); message != verifyTimeoutMessage {
		t.Errorf("handleVerify() message = %q, want %q", message, verifyTimeoutMessage)
	}
}

func TestHandleVerifyContextErrorWithoutExpiredContextIsInternal(t *testing.T) {
	resp, err := handleVerify(context.Background(), verifyOperationTimeout,
		func(context.Context, string, []string, *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
			return &exec.CommandResult{Error: context.DeadlineExceeded}, nil
		})
	if resp != nil {
		t.Fatalf("handleVerify() response = %#v, want nil", resp)
	}
	if status.Code(err) != codes.Internal {
		t.Errorf("handleVerify() code = %v, want %v", status.Code(err), codes.Internal)
	}
}
