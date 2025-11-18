// Package os provides pure Go implementations for gNOI OS service operations.
// It contains business logic that can be tested without SONiC dependencies.
package os

import (
	"context"
	"fmt"
	"strings"

	log "github.com/golang/glog"
	gnoi_os_pb "github.com/openconfig/gnoi/os"
	"github.com/sonic-net/sonic-gnmi/pkg/exec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// HandleVerify implements the business logic for OS.Verify RPC using sonic-installer.
// It executes 'sonic-installer list' command and parses the output to get the current version.
func HandleVerify(ctx context.Context, req *gnoi_os_pb.VerifyRequest) (*gnoi_os_pb.VerifyResponse, error) {
	log.V(1).Info("HandleVerify: executing sonic-installer list")

	// Execute sonic-installer list command
	result, err := exec.RunHostCommand(ctx, "sonic-installer", []string{"list"}, nil)
	if err != nil {
		log.Errorf("HandleVerify: failed to run sonic-installer: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to execute sonic-installer: %v", err)
	}

	if result.Error != nil {
		log.Errorf("HandleVerify: sonic-installer failed with exit code %d: %v, stderr: %s",
			result.ExitCode, result.Error, result.Stderr)
		return nil, status.Errorf(codes.Internal,
			"sonic-installer failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}

	// Parse the output to extract current version
	currentVersion, err := parseCurrentVersion(result.Stdout)
	if err != nil {
		log.Errorf("HandleVerify: failed to parse sonic-installer output: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to parse version information: %v", err)
	}

	log.V(1).Infof("HandleVerify: current version is %s", currentVersion)

	return &gnoi_os_pb.VerifyResponse{
		Version: currentVersion,
	}, nil
}

// parseCurrentVersion parses the output of 'sonic-installer list' to extract the current version.
// Expected format:
// Current: SONiC-OS-20250610.08
// Next: SONiC-OS-20250610.08
// Available:
// SONiC-OS-20250610.08
// SONiC-OS-master.0-dirty-20251103.214532
func parseCurrentVersion(output string) (string, error) {
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Current:") {
			// Extract version after "Current: "
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				version := strings.TrimSpace(parts[1])
				if version != "" {
					return version, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not find 'Current:' line in sonic-installer output")
}
