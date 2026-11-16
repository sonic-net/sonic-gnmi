// Package os provides pure Go implementations for gNOI OS service operations.
// It contains business logic that can be tested without SONiC dependencies.
package os

import (
	"context"
	"strings"
	"time"

	log "github.com/golang/glog"
	gnoi_os_pb "github.com/openconfig/gnoi/os"
	"github.com/sonic-net/sonic-gnmi/pkg/exec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	sonicInstallerPath          = "/usr/local/bin/sonic-installer"
	verifyOperationTimeout      = 60 * time.Second
	verifyCommandFailureMessage = "failed to query current SONiC image"
	verifyTimeoutMessage        = "OS Verify operation timed out"
)

type hostCommandRunner func(context.Context, string, []string, *exec.RunHostCommandOptions) (*exec.CommandResult, error)

// HandleVerify implements the business logic for OS.Verify RPC using sonic-installer.
func HandleVerify(ctx context.Context, _ *gnoi_os_pb.VerifyRequest) (*gnoi_os_pb.VerifyResponse, error) {
	return handleVerify(ctx, verifyOperationTimeout, exec.RunHostCommand)
}

func handleVerify(ctx context.Context, timeout time.Duration, run hostCommandRunner) (*gnoi_os_pb.VerifyResponse, error) {
	log.V(1).Info("HandleVerify: querying the current SONiC image")

	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := run(operationCtx, sonicInstallerPath, []string{"list"}, &exec.RunHostCommandOptions{
		Timeout: timeout,
	})
	if err != nil {
		log.Error("HandleVerify: host command executor failed")
		return nil, verifyCommandError(ctx, operationCtx)
	}

	if result == nil {
		log.Error("HandleVerify: host command executor returned no result")
		return nil, verifyCommandError(ctx, operationCtx)
	}

	if result.Error != nil {
		log.Errorf("HandleVerify: host command failed with exit code %d", result.ExitCode)
		return nil, verifyCommandError(ctx, operationCtx)
	}

	currentVersion := parseCurrentVersion(result.Stdout)
	log.V(1).Infof("HandleVerify: current version is %s", currentVersion)

	return &gnoi_os_pb.VerifyResponse{
		Version: currentVersion,
	}, nil
}

func verifyCommandError(requestCtx, operationCtx context.Context) error {
	switch requestCtx.Err() {
	case context.Canceled:
		return status.Error(codes.Canceled, "OS Verify request canceled")
	case context.DeadlineExceeded:
		return status.Error(codes.DeadlineExceeded, "OS Verify request deadline exceeded")
	}

	if operationCtx.Err() == context.DeadlineExceeded {
		return status.Error(codes.DeadlineExceeded, verifyTimeoutMessage)
	}

	return status.Error(codes.Internal, verifyCommandFailureMessage)
}

func parseCurrentVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		label, version, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(label), "Current") {
			return strings.TrimSpace(version)
		}
	}

	return ""
}
