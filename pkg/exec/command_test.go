package exec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const commandHelperEnv = "GO_WANT_COMMAND_HELPER"

func TestCommandHelperProcess(t *testing.T) {
	if os.Getenv(commandHelperEnv) != "1" {
		return
	}

	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == -1 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}

	switch os.Args[separator+1] {
	case "success":
		fmt.Fprintln(os.Stdout, "helper stdout")
		fmt.Fprintln(os.Stderr, "helper stderr")
		os.Exit(0)
	case "failure":
		fmt.Fprintln(os.Stderr, "helper failure")
		os.Exit(23)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

type commandInvocation struct {
	name string
	args []string
}

func useCommandHelper(t *testing.T, behavior string) *commandInvocation {
	t.Helper()

	originalExecCommand := execCommandContext
	invocation := &commandInvocation{}
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		invocation.name = name
		invocation.args = append([]string(nil), args...)

		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCommandHelperProcess$", "--", behavior)
		cmd.Env = append(os.Environ(), commandHelperEnv+"=1")
		return cmd
	}
	t.Cleanup(func() {
		execCommandContext = originalExecCommand
	})

	return invocation
}

func TestRunHostCommand(t *testing.T) {
	invocation := useCommandHelper(t, "success")

	result, err := RunHostCommand(context.Background(), "echo", []string{"hello", "world"}, nil)
	if err != nil {
		t.Fatalf("RunHostCommand() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("RunHostCommand() command error = %v", result.Error)
	}
	if result.ExitCode != 0 {
		t.Errorf("RunHostCommand() exit code = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "helper stdout\n" {
		t.Errorf("RunHostCommand() stdout = %q, want %q", result.Stdout, "helper stdout\n")
	}
	if result.Stderr != "helper stderr\n" {
		t.Errorf("RunHostCommand() stderr = %q, want %q", result.Stderr, "helper stderr\n")
	}
	if invocation.name != "nsenter" {
		t.Errorf("RunHostCommand() executable = %q, want %q", invocation.name, "nsenter")
	}
	wantArgs := []string{
		"--target", "1",
		"--pid", "--mount", "--uts", "--ipc", "--net",
		"--",
		"echo", "hello", "world",
	}
	if !reflect.DeepEqual(invocation.args, wantArgs) {
		t.Errorf("RunHostCommand() args = %q, want %q", invocation.args, wantArgs)
	}
}

func TestRunHostCommandNonZeroExit(t *testing.T) {
	useCommandHelper(t, "failure")

	result, err := RunHostCommand(context.Background(), "false", nil, nil)
	if err != nil {
		t.Fatalf("RunHostCommand() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("RunHostCommand() command error = nil, want non-nil")
	}
	if result.ExitCode != 23 {
		t.Errorf("RunHostCommand() exit code = %d, want 23", result.ExitCode)
	}
	if result.Stderr != "helper failure\n" {
		t.Errorf("RunHostCommand() stderr = %q, want %q", result.Stderr, "helper failure\n")
	}
}

func TestRunHostCommandStartFailure(t *testing.T) {
	originalExecCommand := execCommandContext
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "missing-command"))
	}
	t.Cleanup(func() {
		execCommandContext = originalExecCommand
	})

	result, err := RunHostCommand(context.Background(), "echo", nil, nil)
	if err != nil {
		t.Fatalf("RunHostCommand() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("RunHostCommand() command error = nil, want non-nil")
	}
}

func TestRunHostCommandTimeout(t *testing.T) {
	useCommandHelper(t, "sleep")

	start := time.Now()
	result, err := RunHostCommand(context.Background(), "sleep", []string{"10"}, &RunHostCommandOptions{
		Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RunHostCommand() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("RunHostCommand() command error = nil, want timeout error")
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Errorf("RunHostCommand() elapsed = %v, want less than 1s", elapsed)
	}
}

func TestRunHostCommandRejectsEmptyCommand(t *testing.T) {
	result, err := RunHostCommand(context.Background(), "", nil, nil)
	if err == nil {
		t.Fatal("RunHostCommand() error = nil, want non-nil")
	}
	if result != nil {
		t.Errorf("RunHostCommand() result = %#v, want nil", result)
	}
}

func TestRunHostCommandSimple(t *testing.T) {
	useCommandHelper(t, "success")

	output, err := RunHostCommandSimple("echo", "test")
	if err != nil {
		t.Fatalf("RunHostCommandSimple() error = %v", err)
	}
	if output != "helper stdout\n" {
		t.Errorf("RunHostCommandSimple() = %q, want %q", output, "helper stdout\n")
	}
}

func TestRunHostCommandSimpleFailure(t *testing.T) {
	useCommandHelper(t, "failure")

	output, err := RunHostCommandSimple("false")
	if err == nil {
		t.Fatal("RunHostCommandSimple() error = nil, want non-nil")
	}
	if output != "" {
		t.Errorf("RunHostCommandSimple() output = %q, want empty", output)
	}
	if !strings.Contains(err.Error(), "helper failure") {
		t.Errorf("RunHostCommandSimple() error = %q, want helper stderr", err)
	}
}

func TestBuildNsenterArgs(t *testing.T) {
	tests := []struct {
		name     string
		opts     *RunHostCommandOptions
		command  string
		args     []string
		expected []string
	}{
		{
			name:    "default namespaces",
			opts:    &RunHostCommandOptions{},
			command: "ls",
			args:    []string{"-l"},
			expected: []string{
				"--target", "1",
				"--pid", "--mount", "--uts", "--ipc", "--net",
				"--",
				"ls", "-l",
			},
		},
		{
			name: "custom namespaces",
			opts: &RunHostCommandOptions{
				Namespaces: []string{"pid", "net"},
			},
			command: "ps",
			args:    []string{"aux"},
			expected: []string{
				"--target", "1",
				"--pid", "--net",
				"--",
				"ps", "aux",
			},
		},
		{
			name:    "no arguments",
			opts:    nil,
			command: "hostname",
			args:    nil,
			expected: []string{
				"--target", "1",
				"--pid", "--mount", "--uts", "--ipc", "--net",
				"--",
				"hostname",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildNsenterArgs(tt.opts, tt.command, tt.args)

			if len(result) != len(tt.expected) {
				t.Errorf("buildNsenterArgs() length = %d, want %d", len(result), len(tt.expected))
				t.Errorf("got: %v", result)
				t.Errorf("expected: %v", tt.expected)
				return
			}

			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("buildNsenterArgs()[%d] = %s, want %s", i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name        string
		cmdStr      string
		wantCommand string
		wantArgs    []string
		wantErr     bool
	}{
		{
			name:        "simple command",
			cmdStr:      "ls -la",
			wantCommand: "ls",
			wantArgs:    []string{"-la"},
			wantErr:     false,
		},
		{
			name:        "command with multiple args",
			cmdStr:      "docker ps -a --format json",
			wantCommand: "docker",
			wantArgs:    []string{"ps", "-a", "--format", "json"},
			wantErr:     false,
		},
		{
			name:        "command only",
			cmdStr:      "hostname",
			wantCommand: "hostname",
			wantArgs:    []string{},
			wantErr:     false,
		},
		{
			name:        "empty string",
			cmdStr:      "",
			wantCommand: "",
			wantArgs:    nil,
			wantErr:     true,
		},
		{
			name:        "whitespace only",
			cmdStr:      "   ",
			wantCommand: "",
			wantArgs:    nil,
			wantErr:     true,
		},
		{
			name:        "command with quoted argument",
			cmdStr:      `echo "hello world"`,
			wantCommand: "echo",
			wantArgs:    []string{"hello world"},
			wantErr:     false,
		},
		{
			name:        "command with single quotes",
			cmdStr:      `echo 'hello world'`,
			wantCommand: "echo",
			wantArgs:    []string{"hello world"},
			wantErr:     false,
		},
		{
			name:        "mixed quotes",
			cmdStr:      `cmd "arg 1" 'arg 2' arg3`,
			wantCommand: "cmd",
			wantArgs:    []string{"arg 1", "arg 2", "arg3"},
			wantErr:     false,
		},
		{
			name:        "unclosed quote",
			cmdStr:      `echo "hello world`,
			wantCommand: "",
			wantArgs:    nil,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args, err := ParseCommand(tt.cmdStr)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCommand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if cmd != tt.wantCommand {
				t.Errorf("ParseCommand() command = %s, want %s", cmd, tt.wantCommand)
			}

			if len(args) != len(tt.wantArgs) {
				t.Errorf("ParseCommand() args length = %d, want %d", len(args), len(tt.wantArgs))
				return
			}

			for i, arg := range args {
				if arg != tt.wantArgs[i] {
					t.Errorf("ParseCommand() args[%d] = %s, want %s", i, arg, tt.wantArgs[i])
				}
			}
		})
	}
}

func TestIsNsenterAvailable(t *testing.T) {
	// This test just verifies the function runs without error
	// The actual result depends on the system
	available := IsNsenterAvailable()
	t.Logf("nsenter available: %v", available)
}
