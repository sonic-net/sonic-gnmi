// Package system provides pure Go implementations for gNOI System service operations.
// It contains business logic that can be tested without SONiC dependencies.
package system

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	log "github.com/golang/glog"
	syspb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/pkg/exec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// HandleSetPackage implements the business logic for System.SetPackage RPC using sonic-installer.
// It validates the request and calls sonic-installer install to install the local image.
func HandleSetPackage(ctx context.Context, req *syspb.SetPackageRequest) (*syspb.SetPackageResponse, error) {
	log.V(1).Info("HandleSetPackage: processing package installation request")

	// Validate request type
	pkg, ok := req.GetRequest().(*syspb.SetPackageRequest_Package)
	if !ok {
		errMsg := fmt.Sprintf("invalid request type: %T, expected SetPackageRequest_Package", req.GetRequest())
		log.Errorf(errMsg)
		return nil, status.Errorf(codes.InvalidArgument, errMsg)
	}

	// Validate required fields
	if pkg.Package.Filename == "" {
		log.Errorf("Filename is missing in package request")
		return nil, status.Errorf(codes.InvalidArgument, "filename is missing in package request")
	}
	if pkg.Package.Version == "" {
		log.Errorf("Version is missing in package request")
		return nil, status.Errorf(codes.InvalidArgument, "version is missing in package request")
	}

	// Reject RemoteDownload - require local image
	if pkg.Package.RemoteDownload != nil {
		log.Errorf("RemoteDownload is not supported - image must be local")
		return nil, status.Errorf(codes.InvalidArgument, "remote download is not supported, image must be local")
	}

	// Log the package details
	log.V(1).Infof("Installing package: filename=%s, version=%s, activate=%v",
		pkg.Package.Filename, pkg.Package.Version, pkg.Package.Activate)

	// Validate filename is absolute path
	if !filepath.IsAbs(pkg.Package.Filename) {
		log.Errorf("Filename must be an absolute path: %s", pkg.Package.Filename)
		return nil, status.Errorf(codes.InvalidArgument, "filename must be an absolute path")
	}

	// Install the package using sonic-installer
	if err := installPackage(ctx, pkg.Package.Filename); err != nil {
		log.Errorf("Failed to install package %s: %v", pkg.Package.Filename, err)
		return nil, status.Errorf(codes.Internal, "failed to install package: %v", err)
	}

	log.V(1).Infof("Successfully installed package %s", pkg.Package.Filename)

	// If activate is requested, set as next boot image
	if pkg.Package.Activate {
		if err := activatePackage(ctx, pkg.Package.Version); err != nil {
			log.Errorf("Failed to activate package %s: %v", pkg.Package.Version, err)
			return nil, status.Errorf(codes.Internal, "failed to activate package: %v", err)
		}
		log.V(1).Infof("Successfully activated package %s", pkg.Package.Version)
	}

	return &syspb.SetPackageResponse{}, nil
}

// installPackage installs a SONiC image using sonic-installer install command.
func installPackage(ctx context.Context, filename string) error {
	log.V(1).Infof("Installing package: %s", filename)

	// Execute sonic-installer install command with -y flag for non-interactive installation
	// Use a longer timeout as sonic-installer can take several minutes
	opts := &exec.RunHostCommandOptions{
		Timeout: 10 * time.Minute, // Allow up to 10 minutes for installation
	}
	result, err := exec.RunHostCommand(ctx, "sonic-installer", []string{"install", "-y", filename}, opts)
	if err != nil {
		return fmt.Errorf("failed to run sonic-installer install: %v", err)
	}

	if result.Error != nil {
		return fmt.Errorf("sonic-installer install failed with exit code %d: %s",
			result.ExitCode, result.Stderr)
	}

	log.V(1).Infof("sonic-installer install completed successfully: %s", strings.TrimSpace(result.Stdout))
	return nil
}

// activatePackage sets a SONiC image as the next boot image using sonic-installer set-default.
func activatePackage(ctx context.Context, version string) error {
	log.V(1).Infof("Activating package version: %s", version)

	// Execute sonic-installer set-default command
	// Use a longer timeout for consistency
	opts := &exec.RunHostCommandOptions{
		Timeout: 2 * time.Minute, // Allow up to 2 minutes for setting default
	}
	result, err := exec.RunHostCommand(ctx, "sonic-installer", []string{"set-default", version}, opts)
	if err != nil {
		return fmt.Errorf("failed to run sonic-installer set-default: %v", err)
	}

	if result.Error != nil {
		return fmt.Errorf("sonic-installer set-default failed with exit code %d: %s",
			result.ExitCode, result.Stderr)
	}

	log.V(1).Infof("sonic-installer set-default completed successfully: %s", strings.TrimSpace(result.Stdout))
	return nil
}

// HandleDPUReboot implements DPU reboot functionality using the reboot command.
// It reboots the specified DPU using the "reboot -d DPU{index}" command.
// The reboot command is executed asynchronously and may return non-zero exit codes,
// which is normal behavior for reboot operations.
func HandleDPUReboot(ctx context.Context, req *syspb.RebootRequest, dpuIndex string) (*syspb.RebootResponse, error) {
	log.V(1).Infof("HandleDPUReboot: rebooting DPU%s", dpuIndex)

	// Construct the reboot command for the specific DPU
	dpuTarget := fmt.Sprintf("DPU%s", dpuIndex)

	// Execute reboot command for the DPU
	// Note: Reboot commands typically return non-zero exit codes and may not complete
	// before the system reboots, so we don't treat non-zero exit codes as errors
	result, err := exec.RunHostCommand(ctx, "reboot", []string{"-d", dpuTarget}, nil)
	if err != nil {
		log.Errorf("Failed to execute DPU reboot command: %v", err)
		return nil, fmt.Errorf("failed to execute DPU reboot command: %v", err)
	}

	// Log the command output for debugging but don't treat non-zero exit codes as errors
	// This is because reboot commands often return non-zero codes but still work correctly
	if result.ExitCode != 0 {
		log.V(1).Infof("DPU reboot command completed with exit code %d (normal for reboot): stdout=%s, stderr=%s",
			result.ExitCode, strings.TrimSpace(result.Stdout), strings.TrimSpace(result.Stderr))
	} else {
		log.V(1).Infof("DPU reboot command completed with exit code 0: %s", strings.TrimSpace(result.Stdout))
	}

	log.V(1).Infof("Successfully initiated reboot for %s", dpuTarget)
	return &syspb.RebootResponse{}, nil
}
