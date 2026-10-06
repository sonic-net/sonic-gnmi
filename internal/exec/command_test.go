package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

type mockCommand struct {
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	startErr  error
	waitErr   error
	stdoutErr error
	stderrErr error
	waitCalls *int
	waitFunc  func() error
}

func (c *mockCommand) StdoutPipe() (io.ReadCloser, error) { return c.stdout, c.stdoutErr }
func (c *mockCommand) StderrPipe() (io.ReadCloser, error) { return c.stderr, c.stderrErr }
func (c *mockCommand) Start() error                       { return c.startErr }
func (c *mockCommand) Wait() error {
	if c.waitCalls != nil {
		*c.waitCalls++
	}
	if c.waitFunc != nil {
		return c.waitFunc()
	}
	return c.waitErr
}

type mockExitError struct {
	code int
}

func (e *mockExitError) Error() string { return fmt.Sprintf("exit code %d", e.code) }
func (e *mockExitError) ExitCode() int { return e.code }

type errorReadCloser struct {
	err error
}

func (r *errorReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r *errorReadCloser) Close() error             { return nil }

func validPlan() ExecutionPlan {
	return ExecutionPlan{
		Executable:  "/usr/bin/uptime",
		User:        "admin",
		Namespaces:  []string{"mount"},
		Timeout:     10 * time.Second,
		OutputLimit: 64 * 1024,
	}
}

func TestRunCommandUsesStructuredShellFreePlan(t *testing.T) {
	original := execCommandWithContext
	t.Cleanup(func() { execCommandWithContext = original })

	var gotName string
	var gotArgs []string
	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return &mockCommand{
			stdout: io.NopCloser(strings.NewReader("OK")),
			stderr: io.NopCloser(strings.NewReader("")),
		}
	}

	outCh := make(chan string, 10)
	errCh := make(chan string, 10)
	code, err := RunCommand(context.Background(), outCh, errCh, validPlan())
	if err != nil {
		t.Fatalf("RunCommand() error: %v", err)
	}
	if code != 0 {
		t.Fatalf("unexpected exit code: %d", code)
	}
	if gotName != "/usr/bin/nsenter" {
		t.Fatalf("unexpected executable: %q", gotName)
	}
	wantArgs := []string{
		"--target", "1", "--mount",
		"/usr/bin/systemd-run",
		"-p", "ProtectSystem=strict",
		"-p", "ProtectHome=true",
		"-p", "PrivateDevices=true",
		"-p", "PrivateTmp=true",
		"-p", "NoNewPrivileges=true",
		"--working-directory=/",
		"--setenv=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"-Pq",
		"--uid=admin",
		"--",
		"/usr/bin/uptime",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("unexpected args:\ngot:  %q\nwant: %q", gotArgs, wantArgs)
	}
	for _, arg := range gotArgs {
		if arg == "sh" || arg == "-c" {
			t.Fatalf("shell argument reached executor: %q", gotArgs)
		}
	}
	if got := strings.Join(drainChannel(outCh), ""); got != "OK" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := strings.Join(drainChannel(errCh), ""); got != "" {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunCommandAppendsValidatedArguments(t *testing.T) {
	original := execCommandWithContext
	t.Cleanup(func() { execCommandWithContext = original })

	var gotArgs []string
	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		gotArgs = append([]string(nil), args...)
		return &mockCommand{
			stdout: io.NopCloser(strings.NewReader("")),
			stderr: io.NopCloser(strings.NewReader("")),
		}
	}

	plan := validPlan()
	plan.Executable = "/usr/bin/ps"
	plan.Args = []string{"-ef"}
	outCh := make(chan string, 10)
	errCh := make(chan string, 10)
	if _, err := RunCommand(context.Background(), outCh, errCh, plan); err != nil {
		t.Fatalf("RunCommand() error: %v", err)
	}
	if got := gotArgs[len(gotArgs)-2:]; !reflect.DeepEqual(got, []string{"/usr/bin/ps", "-ef"}) {
		t.Fatalf("unexpected command tail: %q", got)
	}
}

func TestRunCommandEnforcesTotalOutputLimit(t *testing.T) {
	original := execCommandWithContext
	t.Cleanup(func() { execCommandWithContext = original })

	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		return &mockCommand{
			stdout: io.NopCloser(strings.NewReader("1234567890")),
			stderr: io.NopCloser(strings.NewReader("abcdefghij")),
		}
	}

	plan := validPlan()
	plan.OutputLimit = 7
	outCh := make(chan string, 10)
	errCh := make(chan string, 10)
	if _, err := RunCommand(context.Background(), outCh, errCh, plan); err != nil {
		t.Fatalf("RunCommand() error: %v", err)
	}
	total := len(strings.Join(drainChannel(outCh), "")) + len(strings.Join(drainChannel(errCh), ""))
	if total != 7 {
		t.Fatalf("expected exactly 7 emitted bytes, got %d", total)
	}
}

func TestRunCommandRejectsInvalidPlanBeforeExecution(t *testing.T) {
	original := execCommandWithContext
	t.Cleanup(func() { execCommandWithContext = original })

	calls := 0
	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		calls++
		return &mockCommand{}
	}

	tests := []struct {
		name   string
		mutate func(*ExecutionPlan)
	}{
		{name: "relative executable", mutate: func(p *ExecutionPlan) { p.Executable = "uptime" }},
		{name: "empty user", mutate: func(p *ExecutionPlan) { p.User = "" }},
		{name: "invalid user", mutate: func(p *ExecutionPlan) { p.User = "root --property=X" }},
		{name: "unknown namespace", mutate: func(p *ExecutionPlan) { p.Namespaces = []string{"user"} }},
		{name: "zero timeout", mutate: func(p *ExecutionPlan) { p.Timeout = 0 }},
		{name: "zero output limit", mutate: func(p *ExecutionPlan) { p.OutputLimit = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := validPlan()
			tt.mutate(&plan)
			outCh := make(chan string, 1)
			errCh := make(chan string, 1)
			if _, err := RunCommand(context.Background(), outCh, errCh, plan); err == nil {
				t.Fatal("expected invalid plan to fail")
			}
			if calls != 0 {
				t.Fatalf("executor called for invalid plan: %d", calls)
			}
		})
	}
}

func TestRunCommandReturnsExitStatus(t *testing.T) {
	original := execCommandWithContext
	t.Cleanup(func() { execCommandWithContext = original })

	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		return &mockCommand{
			stdout: io.NopCloser(bytes.NewReader(nil)),
			stderr: io.NopCloser(strings.NewReader("failed")),
			waitErr: &mockExitError{
				code: 42,
			},
		}
	}

	outCh := make(chan string, 10)
	errCh := make(chan string, 10)
	code, err := RunCommand(context.Background(), outCh, errCh, validPlan())
	if err != nil {
		t.Fatalf("RunCommand() error: %v", err)
	}
	if code != 42 {
		t.Fatalf("unexpected exit code: %d", code)
	}
}

func TestRunCommandEnforcesTimeout(t *testing.T) {
	original := execCommandWithContext
	t.Cleanup(func() { execCommandWithContext = original })

	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		return &mockCommand{
			stdout: io.NopCloser(bytes.NewReader(nil)),
			stderr: io.NopCloser(bytes.NewReader(nil)),
			waitFunc: func() error {
				<-ctx.Done()
				return ctx.Err()
			},
		}
	}

	plan := validPlan()
	plan.Timeout = 20 * time.Millisecond
	outCh := make(chan string, 1)
	errCh := make(chan string, 1)
	code, err := RunCommand(context.Background(), outCh, errCh, plan)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunCommand() error = %v, want deadline exceeded", err)
	}
	if code != FAILED_TO_RUN {
		t.Fatalf("unexpected exit code: %d", code)
	}
}

func TestRunCommandWaitsAfterOutputError(t *testing.T) {
	original := execCommandWithContext
	t.Cleanup(func() { execCommandWithContext = original })

	waitCalls := 0
	readErr := errors.New("read")
	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		return &mockCommand{
			stdout:    &errorReadCloser{err: readErr},
			stderr:    io.NopCloser(bytes.NewReader(nil)),
			waitCalls: &waitCalls,
		}
	}

	outCh := make(chan string, 10)
	errCh := make(chan string, 10)
	code, err := RunCommand(context.Background(), outCh, errCh, validPlan())
	if !errors.Is(err, readErr) {
		t.Fatalf("RunCommand() error = %v, want read error", err)
	}
	if code != FAILED_TO_RUN {
		t.Fatalf("unexpected exit code: %d", code)
	}
	if waitCalls != 1 {
		t.Fatalf("Wait() calls = %d, want 1", waitCalls)
	}
}

func TestRunCommandReturnsInfrastructureErrors(t *testing.T) {
	tests := []struct {
		name string
		cmd  *mockCommand
	}{
		{name: "stdout pipe", cmd: &mockCommand{stdoutErr: errors.New("stdout")}},
		{name: "stderr pipe", cmd: &mockCommand{stdout: io.NopCloser(bytes.NewReader(nil)), stderrErr: errors.New("stderr")}},
		{name: "start", cmd: &mockCommand{stdout: io.NopCloser(bytes.NewReader(nil)), stderr: io.NopCloser(bytes.NewReader(nil)), startErr: errors.New("start")}},
		{name: "wait", cmd: &mockCommand{stdout: io.NopCloser(bytes.NewReader(nil)), stderr: io.NopCloser(bytes.NewReader(nil)), waitErr: errors.New("wait")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := execCommandWithContext
			t.Cleanup(func() { execCommandWithContext = original })
			execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
				return tt.cmd
			}
			outCh := make(chan string, 10)
			errCh := make(chan string, 10)
			code, err := RunCommand(context.Background(), outCh, errCh, validPlan())
			if err == nil {
				t.Fatal("expected infrastructure error")
			}
			if code != FAILED_TO_RUN {
				t.Fatalf("unexpected exit code: %d", code)
			}
		})
	}
}

func drainChannel(ch <-chan string) []string {
	var values []string
	for value := range ch {
		values = append(values, value)
	}
	return values
}
