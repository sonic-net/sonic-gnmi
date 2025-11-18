package system

import (
	"context"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	syspb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/pkg/exec"
)

func TestHandleSetPackage_SuccessWithoutActivation(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return successful installation
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		if cmd == "sonic-installer" && len(args) >= 1 && args[0] == "install" {
			return &exec.CommandResult{
				Stdout:   "Installation completed successfully",
				Stderr:   "",
				ExitCode: 0,
				Error:    nil,
			}, nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", cmd, args)
	})

	ctx := context.Background()
	req := &syspb.SetPackageRequest{
		Request: &syspb.SetPackageRequest_Package{
			Package: &syspb.Package{
				Filename: "/tmp/test-image.bin",
				Version:  "test-version-1.0.0",
				Activate: false,
			},
		},
	}

	resp, err := HandleSetPackage(ctx, req)
	if err != nil {
		t.Fatalf("HandleSetPackage() returned error: %v", err)
	}

	if resp == nil {
		t.Fatal("HandleSetPackage() returned nil response")
	}
}

func TestHandleSetPackage_SuccessWithActivation(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to handle both install and set-default commands
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		if cmd == "sonic-installer" {
			if len(args) >= 1 && args[0] == "install" {
				return &exec.CommandResult{
					Stdout:   "Installation completed successfully",
					Stderr:   "",
					ExitCode: 0,
					Error:    nil,
				}, nil
			}
			if len(args) >= 1 && args[0] == "set-default" {
				return &exec.CommandResult{
					Stdout:   "Default image set successfully",
					Stderr:   "",
					ExitCode: 0,
					Error:    nil,
				}, nil
			}
		}
		return nil, fmt.Errorf("unexpected command: %s %v", cmd, args)
	})

	ctx := context.Background()
	req := &syspb.SetPackageRequest{
		Request: &syspb.SetPackageRequest_Package{
			Package: &syspb.Package{
				Filename: "/tmp/test-image.bin",
				Version:  "test-version-1.0.0",
				Activate: true,
			},
		},
	}

	resp, err := HandleSetPackage(ctx, req)
	if err != nil {
		t.Fatalf("HandleSetPackage() returned error: %v", err)
	}

	if resp == nil {
		t.Fatal("HandleSetPackage() returned nil response")
	}
}

func TestHandleSetPackage_InstallCommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return command execution error
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return nil, fmt.Errorf("permission denied")
	})

	ctx := context.Background()
	req := &syspb.SetPackageRequest{
		Request: &syspb.SetPackageRequest_Package{
			Package: &syspb.Package{
				Filename: "/tmp/test-image.bin",
				Version:  "test-version-1.0.0",
				Activate: false,
			},
		},
	}

	_, err := HandleSetPackage(ctx, req)
	if err == nil {
		t.Fatal("HandleSetPackage() should return error when command fails")
	}

	if !containsSubstring(err.Error(), "failed to install package") {
		t.Errorf("HandleSetPackage() error = %v, should contain 'failed to install package'", err)
	}
}

func TestHandleSetPackage_ActivateCommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand: install succeeds, set-default fails
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		if cmd == "sonic-installer" {
			if len(args) >= 1 && args[0] == "install" {
				return &exec.CommandResult{
					Stdout:   "Installation completed successfully",
					Stderr:   "",
					ExitCode: 0,
					Error:    nil,
				}, nil
			}
			if len(args) >= 1 && args[0] == "set-default" {
				return nil, fmt.Errorf("set-default command failed")
			}
		}
		return nil, fmt.Errorf("unexpected command: %s %v", cmd, args)
	})

	ctx := context.Background()
	req := &syspb.SetPackageRequest{
		Request: &syspb.SetPackageRequest_Package{
			Package: &syspb.Package{
				Filename: "/tmp/test-image.bin",
				Version:  "test-version-1.0.0",
				Activate: true,
			},
		},
	}

	_, err := HandleSetPackage(ctx, req)
	if err == nil {
		t.Fatal("HandleSetPackage() should return error when activation fails")
	}

	if !containsSubstring(err.Error(), "failed to activate package") {
		t.Errorf("HandleSetPackage() error = %v, should contain 'failed to activate package'", err)
	}
}

func TestInstallPackage_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return successful installation
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return &exec.CommandResult{
			Stdout:   "Package installed successfully",
			Stderr:   "",
			ExitCode: 0,
			Error:    nil,
		}, nil
	})

	ctx := context.Background()
	err := installPackage(ctx, "/tmp/test-image.bin")
	if err != nil {
		t.Fatalf("installPackage() returned error: %v", err)
	}
}

func TestInstallPackage_CommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return command execution error
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return nil, fmt.Errorf("command not found")
	})

	ctx := context.Background()
	err := installPackage(ctx, "/tmp/test-image.bin")
	if err == nil {
		t.Fatal("installPackage() should return error when command fails")
	}

	if !containsSubstring(err.Error(), "failed to run sonic-installer install") {
		t.Errorf("installPackage() error = %v, should contain 'failed to run sonic-installer install'", err)
	}
}

func TestActivatePackage_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return successful activation
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return &exec.CommandResult{
			Stdout:   "Default image set successfully",
			Stderr:   "",
			ExitCode: 0,
			Error:    nil,
		}, nil
	})

	ctx := context.Background()
	err := activatePackage(ctx, "test-version-1.0.0")
	if err != nil {
		t.Fatalf("activatePackage() returned error: %v", err)
	}
}

func TestActivatePackage_CommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return command execution error
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return nil, fmt.Errorf("permission denied")
	})

	ctx := context.Background()
	err := activatePackage(ctx, "test-version-1.0.0")
	if err == nil {
		t.Fatal("activatePackage() should return error when command fails")
	}

	if !containsSubstring(err.Error(), "failed to run sonic-installer set-default") {
		t.Errorf("activatePackage() error = %v, should contain 'failed to run sonic-installer set-default'", err)
	}
}

func TestHandleDPUReboot_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return successful reboot initiation
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		if cmd == "reboot" && len(args) == 2 && args[0] == "-d" && args[1] == "DPU0" {
			return &exec.CommandResult{
				Stdout:   "Reboot initiated",
				Stderr:   "",
				ExitCode: 0,
				Error:    nil,
			}, nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", cmd, args)
	})

	ctx := context.Background()
	req := &syspb.RebootRequest{
		Method: syspb.RebootMethod_COLD,
	}

	resp, err := HandleDPUReboot(ctx, req, "0")
	if err != nil {
		t.Fatalf("HandleDPUReboot() returned error: %v", err)
	}

	if resp == nil {
		t.Fatal("HandleDPUReboot() returned nil response")
	}
}

func TestHandleDPUReboot_CommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return command execution error
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return nil, fmt.Errorf("reboot command not found")
	})

	ctx := context.Background()
	req := &syspb.RebootRequest{
		Method: syspb.RebootMethod_COLD,
	}

	_, err := HandleDPUReboot(ctx, req, "1")
	if err == nil {
		t.Fatal("HandleDPUReboot() should return error when command fails")
	}

	if !containsSubstring(err.Error(), "failed to execute DPU reboot command") {
		t.Errorf("HandleDPUReboot() error = %v, should contain 'failed to execute DPU reboot command'", err)
	}
}

func TestHandleDPUReboot_DifferentIndices(t *testing.T) {
	testCases := []struct {
		name     string
		dpuIndex string
		expected string
	}{
		{"DPU 0", "0", "DPU0"},
		{"DPU 1", "1", "DPU1"},
		{"DPU 10", "10", "DPU10"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Mock exec.RunHostCommand to verify correct DPU target
			patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				if cmd == "reboot" && len(args) == 2 && args[0] == "-d" && args[1] == tc.expected {
					return &exec.CommandResult{
						Stdout:   "Reboot initiated for " + tc.expected,
						Stderr:   "",
						ExitCode: 0,
						Error:    nil,
					}, nil
				}
				return nil, fmt.Errorf("unexpected target: expected %s, got %v", tc.expected, args)
			})

			ctx := context.Background()
			req := &syspb.RebootRequest{}

			resp, err := HandleDPUReboot(ctx, req, tc.dpuIndex)
			if err != nil {
				t.Fatalf("HandleDPUReboot() returned error: %v", err)
			}

			if resp == nil {
				t.Fatal("HandleDPUReboot() returned nil response")
			}
		})
	}
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
