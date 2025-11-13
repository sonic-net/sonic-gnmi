package os

import (
	"context"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_os_pb "github.com/openconfig/gnoi/os"
	"github.com/sonic-net/sonic-gnmi/pkg/exec"
)

func TestHandleVerify_SuccessPath(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return successful sonic-installer output
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return &exec.CommandResult{
			Stdout:   "Current: SONiC-OS-20250610.08\nNext: SONiC-OS-20250610.08\nAvailable:\n SONiC-OS-20250610.08",
			Stderr:   "",
			ExitCode: 0,
			Error:    nil,
		}, nil
	})

	ctx := context.Background()
	req := &gnoi_os_pb.VerifyRequest{}

	resp, err := HandleVerify(ctx, req)
	if err != nil {
		t.Fatalf("HandleVerify() returned error: %v", err)
	}

	if resp == nil {
		t.Fatal("HandleVerify() returned nil response")
	}

	expectedVersion := "SONiC-OS-20250610.08"
	if resp.Version != expectedVersion {
		t.Errorf("HandleVerify() version = %v, want %v", resp.Version, expectedVersion)
	}
}

func TestHandleVerify_ExecCommandError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return an error
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return nil, fmt.Errorf("permission denied")
	})

	ctx := context.Background()
	req := &gnoi_os_pb.VerifyRequest{}

	_, err := HandleVerify(ctx, req)
	if err == nil {
		t.Fatal("HandleVerify() should return error when exec.RunHostCommand fails")
	}

	// Should contain "failed to execute sonic-installer"
	if !contains(err.Error(), "failed to execute sonic-installer") {
		t.Errorf("HandleVerify() error = %v, should contain 'failed to execute sonic-installer'", err)
	}
}

func TestHandleVerify_ParseVersionError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock exec.RunHostCommand to return output that cannot be parsed
	patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
		return &exec.CommandResult{
			Stdout:   "Invalid output without Current line",
			Stderr:   "",
			ExitCode: 0,
			Error:    nil,
		}, nil
	})

	ctx := context.Background()
	req := &gnoi_os_pb.VerifyRequest{}

	_, err := HandleVerify(ctx, req)
	if err == nil {
		t.Fatal("HandleVerify() should return error when parsing fails")
	}

	// Should contain "failed to parse version information"
	if !contains(err.Error(), "failed to parse version information") {
		t.Errorf("HandleVerify() error = %v, should contain 'failed to parse version information'", err)
	}
}

func TestHandleVerify_DifferentVersionFormats(t *testing.T) {
	testCases := []struct {
		name           string
		output         string
		expectedVersion string
	}{
		{
			name:           "standard format",
			output:         "Current: SONiC-OS-20250610.08\nNext: SONiC-OS-20250610.08",
			expectedVersion: "SONiC-OS-20250610.08",
		},
		{
			name:           "master build",
			output:         "Current: SONiC-OS-master.0-dirty-20251103.214532\nNext: SONiC-OS-master.0-dirty-20251103.214532",
			expectedVersion: "SONiC-OS-master.0-dirty-20251103.214532",
		},
		{
			name:           "with spaces",
			output:         "Current:   SONiC-OS-20250610.08   \nNext: SONiC-OS-20250610.08",
			expectedVersion: "SONiC-OS-20250610.08",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Mock exec.RunHostCommand to return the test output
			patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				return &exec.CommandResult{
					Stdout:   tc.output,
					Stderr:   "",
					ExitCode: 0,
					Error:    nil,
				}, nil
			})

			ctx := context.Background()
			req := &gnoi_os_pb.VerifyRequest{}

			resp, err := HandleVerify(ctx, req)
			if err != nil {
				t.Fatalf("HandleVerify() returned error: %v", err)
			}

			if resp.Version != tc.expectedVersion {
				t.Errorf("HandleVerify() version = %v, want %v", resp.Version, tc.expectedVersion)
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		containsAt(s, substr))))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}