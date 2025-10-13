package debug

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

// --- Test chanWriter ---

func TestChanWriter(t *testing.T) {
	ch := make(chan string, 1)
	writer := &chanWriter{ch: ch}

	testString := "hello world"
	p := []byte(testString)

	n, err := writer.Write(p)
	if err != nil {
		t.Fatalf("Write() returned an unexpected error: %v", err)
	}
	if n != len(p) {
		t.Fatalf("Write() returned an incorrect length: got %d, want %d", n, len(p))
	}

	select {
	case received := <-ch:
		if received != testString {
			t.Errorf("Channel received incorrect string: got %q, want %q", received, testString)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for channel write")
	}
}

// --- Test outputReaderToChannel ---

// mockErrorReader always returns an error on Read.
type mockErrorReader struct{}

func (r *mockErrorReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("forced read error")
}

func TestOutputReaderToChannel(t *testing.T) {
	testCases := []struct {
		name        string
		reader      io.Reader
		byteLimit   int64
		expectedOut []string
		expectErr   bool
	}{
		{
			name:        "Success without byte limit",
			reader:      strings.NewReader("line1\nline2"),
			byteLimit:   0,
			expectedOut: []string{"line1\nline2"},
			expectErr:   false,
		},
		{
			name:        "Success with byte limit",
			reader:      strings.NewReader("some buffered data"),
			byteLimit:   5,
			expectedOut: []string{"some ", "buffe", "red d", "ata"}, // io.CopyBuffer behavior
			expectErr:   false,
		},
		{
			name:        "Empty reader",
			reader:      strings.NewReader(""),
			byteLimit:   0,
			expectedOut: []string{},
			expectErr:   false,
		},
		{
			name:        "Error on read",
			reader:      &mockErrorReader{},
			byteLimit:   0,
			expectedOut: []string{},
			expectErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			outCh := make(chan string, 10)

			err := outputReaderToChannel(tc.reader, outCh, tc.byteLimit)
			close(outCh)

			var receivedOut []string
			for s := range outCh {
				receivedOut = append(receivedOut, s)
			}

			if tc.expectErr && err == nil {
				t.Error("Expected an error, but got nil")
			}
			if !tc.expectErr && err != nil {
				t.Errorf("Did not expect an error, but got: %v", err)
			}

			// For CopyBuffer, the chunks are not guaranteed, so we join them for comparison.
			if !reflect.DeepEqual(strings.Join(receivedOut, ""), strings.Join(tc.expectedOut, "")) {
				t.Errorf("Mismatched output:\ngot:  %q\nwant: %q", receivedOut, tc.expectedOut)
			}
		})
	}
}

// --- Test RunCommand ---

// mockCmd is a mock for exec.Cmd
type mockCmd struct {
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	startErr  error
	waitErr   error
	stdoutErr error
	stderrErr error
}

func (c *mockCmd) StdoutPipe() (io.ReadCloser, error) { return c.stdout, c.stdoutErr }
func (c *mockCmd) StderrPipe() (io.ReadCloser, error) { return c.stderr, c.stderrErr }
func (c *mockCmd) Start() error                       { return c.startErr }
func (c *mockCmd) Wait() error                        { return c.waitErr }

// mockReadCloser helps wrap an io.Reader into an io.ReadCloser
type mockReadCloser struct {
	io.Reader
}

func (m *mockReadCloser) Close() error { return nil }

// mockExitError implements the error interface, along with the custom exit error interface
type mockExitError struct {
	code int
}

func (e mockExitError) Error() string  { return fmt.Sprintf("exit code %d", e.code) }
func (e *mockExitError) ExitCode() int { return e.code }

func TestRunCommand(t *testing.T) {
	originalExecCommand := execCommandWithContext
	defer func() { execCommandWithContext = originalExecCommand }()

	testCases := []struct {
		name             string
		mock             mockCmd
		cmdStr           string
		roleAccount      string
		ctx              context.Context
		expectedExitCode int
		expectErr        bool
		expectedStdout   string
		expectedStderr   string
		expectedArgs     []string
	}{
		{
			name: "Successful execution with default user",
			mock: mockCmd{
				stdout:   io.NopCloser(strings.NewReader("OK")),
				stderr:   io.NopCloser(strings.NewReader("")),
				startErr: nil,
				waitErr:  nil,
			},
			cmdStr:           "echo 'test'",
			roleAccount:      "",
			ctx:              context.Background(),
			expectedExitCode: 0,
			expectErr:        false,
			expectedStdout:   "OK",
			expectedStderr:   "",
			expectedArgs:     []string{"--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "su", "-", "admin", "-c", "echo 'test'"},
		},
		{
			name: "Successful execution with custom user",
			mock: mockCmd{
				stdout:   io.NopCloser(bytes.NewReader([]byte("Data"))),
				stderr:   io.NopCloser(bytes.NewReader(nil)),
				startErr: nil,
				waitErr:  nil,
			},
			cmdStr:           "ls",
			roleAccount:      "testuser",
			ctx:              context.Background(),
			expectedExitCode: 0,
			expectErr:        false,
			expectedStdout:   "Data",
			expectedStderr:   "",
			expectedArgs:     []string{"--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "su", "-", "testuser", "-c", "ls"},
		},
		{
			name: "Command fails with non-zero exit code",
			mock: mockCmd{
				stdout:   io.NopCloser(strings.NewReader("")),
				stderr:   io.NopCloser(strings.NewReader("Command not found")),
				startErr: nil,
				waitErr:  &mockExitError{code: 127},
			},
			cmdStr:           "invalid-cmd",
			roleAccount:      "admin",
			ctx:              context.Background(),
			expectedExitCode: 127,
			expectErr:        false, // ExitError is not a framework error
			expectedStdout:   "",
			expectedStderr:   "Command not found",
		},
		{
			name: "Start fails",
			mock: mockCmd{
				startErr: errors.New("failed to start"),
			},
			cmdStr:           "any",
			roleAccount:      "admin",
			ctx:              context.Background(),
			expectedExitCode: FAILED_TO_RUN,
			expectErr:        true,
		},
		{
			name: "StdoutPipe fails",
			mock: mockCmd{
				stdoutErr: errors.New("stdout pipe failed"),
			},
			cmdStr:           "any",
			roleAccount:      "admin",
			ctx:              context.Background(),
			expectedExitCode: FAILED_TO_RUN,
			expectErr:        true,
		},
		{
			name: "StderrPipe fails",
			mock: mockCmd{
				stderrErr: errors.New("stderr pipe failed"),
			},
			cmdStr:           "any",
			roleAccount:      "admin",
			ctx:              context.Background(),
			expectedExitCode: FAILED_TO_RUN,
			expectErr:        true,
		},
		{
			name: "Wait fails with generic error",
			mock: mockCmd{
				stdout:   io.NopCloser(strings.NewReader("")),
				stderr:   io.NopCloser(strings.NewReader("")),
				startErr: nil,
				waitErr:  errors.New("wait failed unexpectedly"),
			},
			cmdStr:           "any",
			roleAccount:      "admin",
			ctx:              context.Background(),
			expectedExitCode: FAILED_TO_RUN,
			expectErr:        true,
		},
		{
			name: "Context cancellation",
			mock: mockCmd{
				stdout:   io.NopCloser(strings.NewReader("")),
				stderr:   io.NopCloser(strings.NewReader("")),
				startErr: nil,
				waitErr:  context.Canceled,
			},
			cmdStr:      "sleep 10",
			roleAccount: "admin",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Immediately cancel
				return ctx
			}(),
			expectedExitCode: FAILED_TO_RUN,
			expectErr:        true, // Expect context.Canceled error
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedArgs []string
			execCommandWithContext = func(ctx context.Context, command string, args ...string) ExecutableCommand {
				capturedArgs = append(capturedArgs, args...)
				return &tc.mock
			}

			outCh := make(chan string, 10)
			errCh := make(chan string, 10)

			args := strings.Split(tc.cmdStr, " ")
			exitCode, err := RunCommand(tc.ctx, outCh, errCh, tc.roleAccount, 0, args[0], args[1:]...)

			if exitCode != tc.expectedExitCode {
				t.Errorf("Expected exit code %d, but got %d", tc.expectedExitCode, exitCode)
			}

			if tc.expectErr && err == nil {
				t.Error("Expected an error, but got nil")
			}
			if !tc.expectErr && err != nil {
				t.Errorf("Did not expect an error, but got: %v", err)
			}

			if tc.expectedArgs != nil && !reflect.DeepEqual(capturedArgs, tc.expectedArgs) {
				t.Errorf("Mismatched arguments:\ngot:  %q\nwant: %q", capturedArgs, tc.expectedArgs)
			}

			if tc.expectedStdout != "" {
				var stdout bytes.Buffer
				for s := range outCh {
					stdout.WriteString(s)
				}

				if stdout.String() != tc.expectedStdout {
					t.Errorf("Mismatched stdout:\ngot:  %q\nwant: %q", stdout.String(), tc.expectedStdout)
				}
			}

			if tc.expectedStderr != "" {
				var stderr bytes.Buffer
				for s := range errCh {
					stderr.WriteString(s)
				}

				if stderr.String() != tc.expectedStderr {
					t.Errorf("Mismatched stderr:\ngot:  %q\nwant: %q", stderr.String(), tc.expectedStderr)
				}
			}
		})
	}
}
