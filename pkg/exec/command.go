package exec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

const (
	// hostPID is the PID of the host's init process
	hostPID = "1"

	// defaultTimeout is the default timeout for command execution
	defaultTimeout = 30 * time.Second
)

// RunHostCommandOptions provides configuration options for RunHostCommand
type RunHostCommandOptions struct {
	// Timeout specifies the maximum duration for command execution
	// If zero, defaultTimeout is used
	Timeout time.Duration

	// Namespaces specifies which namespaces to enter
	// If empty, all standard namespaces (pid, mount, uts, ipc, net) are used
	Namespaces []string

	// WorkingDir specifies the working directory for the command
	// If empty, the current directory is used
	WorkingDir string

	// Environment specifies additional environment variables
	// Format: ["KEY=value", "KEY2=value2"]
	Environment []string
}

// CommandResult represents the result of a command execution
type CommandResult struct {
	// Stdout contains the standard output
	Stdout string

	// Stderr contains the standard error output
	Stderr string

	// ExitCode contains the exit code of the command
	ExitCode int

	// Error contains any error that occurred during execution
	Error error
}

// RunHostCommand executes a command on the host system from within a container using nsenter
// It provides a safe way to run host commands with proper error handling and timeout support
func RunHostCommand(ctx context.Context, command string, args []string, opts *RunHostCommandOptions) (*CommandResult, error) {
	if command == "" {
		return nil, fmt.Errorf("command cannot be empty")
	}

	// Apply default options if not provided
	if opts == nil {
		opts = &RunHostCommandOptions{}
	}

	// Set default timeout if not specified
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build nsenter command
	nsenterArgs := buildNsenterArgs(opts, command, args)

	// Create command
	cmd := exec.CommandContext(ctx, "nsenter", nsenterArgs...)

	// Set working directory if specified
	if opts.WorkingDir != "" {
		cmd.Dir = opts.WorkingDir
	}

	// Set environment variables if specified
	if len(opts.Environment) > 0 {
		cmd.Env = append(cmd.Env, opts.Environment...)
	}

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute command
	err := cmd.Run()

	// Build result
	result := &CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Error:  err,
	}

	// Extract exit code if available
	if exitError, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitError.ExitCode()
	} else {
		// Exit code is unavailable; rely on Error field to indicate issues.
		result.ExitCode = 0
	}

	return result, nil
}

// RunHostCommandSimple is a simplified version of RunHostCommand that returns only stdout
// It's useful for quick command execution where only the output is needed
func RunHostCommandSimple(command string, args ...string) (string, error) {
	result, err := RunHostCommand(context.Background(), command, args, nil)
	if err != nil {
		return "", err
	}

	if result.Error != nil {
		return "", fmt.Errorf("command failed: %v, stderr: %s", result.Error, result.Stderr)
	}

	return result.Stdout, nil
}

// buildNsenterArgs constructs the nsenter command arguments
func buildNsenterArgs(opts *RunHostCommandOptions, command string, args []string) []string {
	nsenterArgs := []string{
		"--target", hostPID,
	}

	// Add namespace flags
	var namespaces []string
	if opts != nil && len(opts.Namespaces) > 0 {
		namespaces = opts.Namespaces
	} else {
		// Default namespaces
		namespaces = []string{"pid", "mount", "uts", "ipc", "net"}
	}

	for _, ns := range namespaces {
		nsenterArgs = append(nsenterArgs, "--"+ns)
	}

	// Add separator
	nsenterArgs = append(nsenterArgs, "--")

	// Add command and arguments
	nsenterArgs = append(nsenterArgs, command)
	nsenterArgs = append(nsenterArgs, args...)

	return nsenterArgs
}

// IsNsenterAvailable checks if nsenter is available in the system
func IsNsenterAvailable() bool {
	cmd := exec.Command("which", "nsenter")
	err := cmd.Run()
	return err == nil
}

// ParseCommand splits a command string into command and arguments
// It handles quoted strings properly using a simple state machine
func ParseCommand(cmdStr string) (string, []string, error) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return "", nil, fmt.Errorf("command string is empty")
	}

	// Parse command string respecting quotes
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for i, r := range cmdStr {
		switch {
		case !inQuote && (r == '"' || r == '\''):
			inQuote = true
			quoteChar = r
		case inQuote && r == quoteChar:
			// Check if it's escaped
			if i > 0 && cmdStr[i-1] == '\\' {
				current.WriteRune(r)
			} else {
				inQuote = false
				quoteChar = 0
			}
		case !inQuote && unicode.IsSpace(r):
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	// Add the last part if any
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	if inQuote {
		return "", nil, fmt.Errorf("unclosed quote in command string")
	}

	if len(parts) == 0 {
		return "", nil, fmt.Errorf("no command found")
	}

	return parts[0], parts[1:], nil
}
