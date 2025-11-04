package exec

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunHostCommand(t *testing.T) {
	// Skip tests if not running on Linux
	if runtime.GOOS != "linux" {
		t.Skip("nsenter tests can only run on Linux")
	}

	// Skip if nsenter is not available
	if !IsNsenterAvailable() {
		t.Skip("nsenter is not available on this system")
	}

	// Check if we have permission to use nsenter (requires root or CAP_SYS_ADMIN)
	// Try a simple test command first
	testResult, _ := RunHostCommand(context.Background(), "true", nil, nil)
	if testResult != nil && testResult.Error != nil && strings.Contains(testResult.Stderr, "Permission denied") {
		t.Skip("Insufficient permissions to run nsenter tests (need root or CAP_SYS_ADMIN)")
	}

	tests := []struct {
		name    string
		command string
		args    []string
		opts    *RunHostCommandOptions
		wantErr bool
		check   func(t *testing.T, result *CommandResult)
	}{
		{
			name:    "simple echo command",
			command: "echo",
			args:    []string{"hello", "world"},
			opts:    nil,
			wantErr: false,
			check: func(t *testing.T, result *CommandResult) {
				if result.ExitCode != 0 {
					t.Errorf("expected exit code 0, got %d", result.ExitCode)
				}
				if !strings.Contains(result.Stdout, "hello world") {
					t.Errorf("expected output to contain 'hello world', got %s", result.Stdout)
				}
			},
		},
		{
			name:    "command with timeout",
			command: "sleep",
			args:    []string{"0.1"},
			opts: &RunHostCommandOptions{
				Timeout: 1 * time.Second,
			},
			wantErr: false,
			check: func(t *testing.T, result *CommandResult) {
				if result.ExitCode != 0 {
					t.Errorf("expected exit code 0, got %d", result.ExitCode)
				}
			},
		},
		{
			name:    "empty command",
			command: "",
			args:    nil,
			opts:    nil,
			wantErr: true,
			check:   nil,
		},
		{
			name:    "non-existent command",
			command: "this-command-does-not-exist",
			args:    nil,
			opts:    nil,
			wantErr: false,
			check: func(t *testing.T, result *CommandResult) {
				if result.ExitCode == 0 {
					t.Errorf("expected non-zero exit code for non-existent command")
				}
			},
		},
		{
			name:    "command with custom namespaces",
			command: "pwd",
			args:    nil,
			opts: &RunHostCommandOptions{
				Namespaces: []string{"pid", "net"},
			},
			wantErr: false,
			check: func(t *testing.T, result *CommandResult) {
				if result.ExitCode != 0 {
					t.Errorf("expected exit code 0, got %d", result.ExitCode)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RunHostCommand(context.Background(), tt.command, tt.args, tt.opts)

			if (err != nil) != tt.wantErr {
				t.Errorf("RunHostCommand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.check != nil && result != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestRunHostCommandSimple(t *testing.T) {
	// Skip tests if not running on Linux
	if runtime.GOOS != "linux" {
		t.Skip("nsenter tests can only run on Linux")
	}

	// Skip if nsenter is not available
	if !IsNsenterAvailable() {
		t.Skip("nsenter is not available on this system")
	}

	// Check permissions
	testResult, _ := RunHostCommand(context.Background(), "true", nil, nil)
	if testResult != nil && testResult.Error != nil && strings.Contains(testResult.Stderr, "Permission denied") {
		t.Skip("Insufficient permissions to run nsenter tests")
	}

	// Test simple command execution
	output, err := RunHostCommandSimple("echo", "test")
	if err != nil {
		t.Errorf("RunHostCommandSimple() error = %v", err)
		return
	}

	if !strings.Contains(output, "test") {
		t.Errorf("expected output to contain 'test', got %s", output)
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
