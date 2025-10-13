package debug

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
)

const (
	FAILED_TO_RUN = -1
	NSENTER_CMD   = "nsenter"
	DEFAULT_ACC   = "admin"
)

var (
	NSENTER_ARGS = []string{
		"--target",
		"1",
		"--mount",
		"--uts",
		"--ipc",
		"--net",
		"--pid",
	}
	USER_ARGS = []string{
		"su",
		"-",
	}
	SHELL_ARGS = []string{
		"-c",
	}

	USER_AND_CMD = 2
	// Length required for nsenter's args, user args, shell args, the user, the command
	STATIC_ARG_LEN = len(NSENTER_ARGS) + len(USER_ARGS) + len(SHELL_ARGS) + USER_AND_CMD

	// Allow DI for mocking
	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		// Below is passed through AST validation + whitelist sanitisation, and is as safe as possible
		// nosemgrep:dangerous-exec-command
		return exec.CommandContext(ctx, name, args...)
	}
)

// Interface containing the methods we use from the exec.Cmd struct
type ExecutableCommand interface {
	Start() error
	Wait() error
	StderrPipe() (io.ReadCloser, error)
	StdoutPipe() (io.ReadCloser, error)
}

// Interface containing methods used from exec.ExitError
type ExitError interface {
	ExitCode() int
}

// Small wrapper to provide io.Writer interface impl for channel
type chanWriter struct {
	ch chan<- string
}

func (w *chanWriter) Write(p []byte) (n int, err error) {
	w.ch <- string(p)
	return len(p), nil
}

// Reads from reader, piping into the provided channel until EOF.
func outputReaderToChannel(reader io.Reader, outCh chan<- string, byteLimit int64) error {
	writer := &chanWriter{
		ch: outCh,
	}

	// nil by default, unless valid limit provided
	var buf []byte
	if byteLimit > 0 {
		buf = make([]byte, byteLimit)
	}

	_, err := io.CopyBuffer(writer, reader, buf)

	return err
}

// Runs a specified command on the host device.
//
// Takes channels for stdout and stderr, which are copied in real time during execution.
// Optionally runs command as the specified user (default is 'admin'), and has an optional byte limit for responses.
//
// Returns status code of the operation, with optional error.
func RunCommand(ctx context.Context, outCh chan<- string, errCh chan<- string, roleAccount string, byteLimit int64, cmd string, args ...string) (int, error) {
	defer func() {
		close(outCh)
		close(errCh)
	}()

	var fullCmd strings.Builder
	fullCmd.WriteString(cmd)
	for _, arg := range args {
		fullCmd.WriteString(" ")
		fullCmd.WriteString(arg)
	}

	fullArgs := make([]string, 0, STATIC_ARG_LEN+len(args))
	fullArgs = append(fullArgs, NSENTER_ARGS...)
	fullArgs = append(fullArgs, USER_ARGS...)
	if roleAccount == "" {
		fullArgs = append(fullArgs, DEFAULT_ACC)
	} else {
		fullArgs = append(fullArgs, roleAccount)
	}
	fullArgs = append(fullArgs, SHELL_ARGS...)
	fullArgs = append(fullArgs, fullCmd.String())

	command := execCommandWithContext(ctx, NSENTER_CMD, fullArgs...)

	stdout, err := command.StdoutPipe()
	if err != nil {
		return FAILED_TO_RUN, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return FAILED_TO_RUN, err
	}

	err = command.Start()
	if err != nil {
		return FAILED_TO_RUN, err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		outputReaderToChannel(stdout, outCh, byteLimit)
		wg.Done()
	}()
	go func() {
		outputReaderToChannel(stderr, errCh, byteLimit)
		wg.Done()
	}()
	wg.Wait()

	err = command.Wait()
	if err != nil {
		switch err.(type) {
		case ExitError:
			// If the command fails, just return exit code - no issue with the infrastructure
			castErr := err.(ExitError)
			return castErr.ExitCode(), nil
		default:
			return FAILED_TO_RUN, err
		}
	}

	return 0, nil
}
