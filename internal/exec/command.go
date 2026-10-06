package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	FAILED_TO_RUN = -1
	nsenterPath   = "/usr/bin/nsenter"
	systemdPath   = "/usr/bin/systemd-run"
)

var (
	systemdRunArgs = []string{
		"-p", "ProtectSystem=strict",
		"-p", "ProtectHome=true",
		"-p", "PrivateDevices=true",
		"-p", "PrivateTmp=true",
		"-p", "NoNewPrivileges=true",
		"--working-directory=/",
		"--setenv=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"-Pq",
	}
	validUser = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

	execCommandWithContext = func(ctx context.Context, name string, args ...string) ExecutableCommand {
		// The executable and argv come from a compiled action policy.
		// nosemgrep:dangerous-exec-command
		return exec.CommandContext(ctx, name, args...)
	}
)

type ExecutableCommand interface {
	Start() error
	Wait() error
	StderrPipe() (io.ReadCloser, error)
	StdoutPipe() (io.ReadCloser, error)
}

type ExitError interface {
	ExitCode() int
}

type outputLimiter struct {
	mu        sync.Mutex
	remaining int64
}

type limitedChannelWriter struct {
	ch      chan<- string
	limiter *outputLimiter
}

func (w *limitedChannelWriter) Write(p []byte) (int, error) {
	w.limiter.mu.Lock()
	defer w.limiter.mu.Unlock()

	emit := int64(len(p))
	if emit > w.limiter.remaining {
		emit = w.limiter.remaining
	}
	if emit > 0 {
		w.ch <- string(p[:emit])
		w.limiter.remaining -= emit
	}

	// Report the full input as consumed so the pipes continue to drain after
	// the response limit is reached.
	return len(p), nil
}

func outputReaderToChannel(reader io.Reader, outCh chan<- string, limiter *outputLimiter) error {
	_, err := io.Copy(&limitedChannelWriter{ch: outCh, limiter: limiter}, reader)
	return err
}

func RunCommand(ctx context.Context, outCh chan<- string, errCh chan<- string, plan ExecutionPlan) (int, error) {
	defer func() {
		close(outCh)
		close(errCh)
	}()

	if err := validatePlan(plan); err != nil {
		return FAILED_TO_RUN, err
	}

	ctx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()

	fullArgs := make([]string, 0, 2+len(plan.Namespaces)+1+len(systemdRunArgs)+4+len(plan.Args))
	fullArgs = append(fullArgs, "--target", "1")
	for _, namespace := range plan.Namespaces {
		fullArgs = append(fullArgs, "--"+namespace)
	}
	fullArgs = append(fullArgs, systemdPath)
	fullArgs = append(fullArgs, systemdRunArgs...)
	fullArgs = append(fullArgs, "--uid="+plan.User, "--", plan.Executable)
	fullArgs = append(fullArgs, plan.Args...)

	command := execCommandWithContext(ctx, nsenterPath, fullArgs...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return FAILED_TO_RUN, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return FAILED_TO_RUN, err
	}
	if err := command.Start(); err != nil {
		return FAILED_TO_RUN, err
	}

	limiter := &outputLimiter{remaining: plan.OutputLimit}
	readErrors := make(chan error, 2)
	go func() {
		readErrors <- outputReaderToChannel(stdout, outCh, limiter)
	}()
	go func() {
		readErrors <- outputReaderToChannel(stderr, errCh, limiter)
	}()

	var readErr error
	for range 2 {
		if err := <-readErrors; err != nil && readErr == nil {
			readErr = err
			cancel()
			_ = stdout.Close()
			_ = stderr.Close()
		}
	}

	waitErr := command.Wait()
	if readErr != nil {
		return FAILED_TO_RUN, readErr
	}
	if waitErr != nil {
		var exitErr ExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return FAILED_TO_RUN, waitErr
	}

	return 0, nil
}

func validatePlan(plan ExecutionPlan) error {
	if !filepath.IsAbs(plan.Executable) || filepath.Clean(plan.Executable) != plan.Executable {
		return fmt.Errorf("execution plan requires a clean absolute executable path")
	}
	if strings.ContainsRune(plan.Executable, '\x00') {
		return fmt.Errorf("execution plan executable contains a NUL byte")
	}
	if !validUser.MatchString(plan.User) {
		return fmt.Errorf("execution plan contains invalid user %q", plan.User)
	}
	if plan.Timeout <= 0 {
		return fmt.Errorf("execution plan timeout must be positive")
	}
	if plan.OutputLimit <= 0 {
		return fmt.Errorf("execution plan output limit must be positive")
	}

	seenNamespaces := make(map[string]struct{}, len(plan.Namespaces))
	for _, namespace := range plan.Namespaces {
		switch namespace {
		case "mount", "uts", "ipc", "net", "pid", "cgroup", "time":
		default:
			return fmt.Errorf("execution plan contains unsupported namespace %q", namespace)
		}
		if _, exists := seenNamespaces[namespace]; exists {
			return fmt.Errorf("execution plan contains duplicate namespace %q", namespace)
		}
		seenNamespaces[namespace] = struct{}{}
	}
	for _, arg := range plan.Args {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("execution plan argument contains a NUL byte")
		}
	}
	return nil
}
