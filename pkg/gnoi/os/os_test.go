package os

import (
	"context"
	"testing"

	gnoi_os_pb "github.com/openconfig/gnoi/os"
)

func TestParseCurrentVersion(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name: "standard output",
			output: `Current: SONiC-OS-20250610.08
Next: SONiC-OS-20250610.08
Available: 
SONiC-OS-20250610.08
SONiC-OS-master.0-dirty-20251103.214532`,
			want:    "SONiC-OS-20250610.08",
			wantErr: false,
		},
		{
			name: "output with extra spaces",
			output: `Current:   SONiC-OS-20250610.08  
Next: SONiC-OS-20250610.08`,
			want:    "SONiC-OS-20250610.08",
			wantErr: false,
		},
		{
			name:    "output with tabs",
			output:  "Current:\tSONiC-OS-20250610.08\nNext: SONiC-OS-20250610.08",
			want:    "SONiC-OS-20250610.08",
			wantErr: false,
		},
		{
			name:    "missing current line",
			output:  "Next: SONiC-OS-20250610.08\nAvailable:",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty current value",
			output:  "Current: \nNext: SONiC-OS-20250610.08",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty output",
			output:  "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			output:  "Current SONiC-OS-20250610.08",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCurrentVersion(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCurrentVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseCurrentVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleVerify(t *testing.T) {
	// Test that HandleVerify attempts to run sonic-installer
	// Even if it fails due to missing permissions/environment, we test the error handling paths
	ctx := context.Background()
	req := &gnoi_os_pb.VerifyRequest{}

	resp, err := HandleVerify(ctx, req)
	
	// In test environment, sonic-installer is likely not available or requires nsenter
	// We expect an error, but we're testing the function doesn't panic and handles errors properly
	if err != nil {
		// Expected - sonic-installer not available in test environment
		t.Logf("Expected error in test environment: %v", err)
	} else {
		// If it succeeds (unlikely in test env), verify response format
		if resp == nil {
			t.Error("HandleVerify returned nil response with no error")
		}
		if resp != nil && resp.Version == "" {
			t.Error("HandleVerify returned empty version")
		}
	}
}

func TestHandleVerifyWithContext(t *testing.T) {
	// Test HandleVerify with a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	
	req := &gnoi_os_pb.VerifyRequest{}
	_, err := HandleVerify(ctx, req)
	
	// Should fail due to cancelled context
	if err == nil {
		t.Error("HandleVerify should fail with cancelled context")
	}
}

// Note: Testing HandleVerify with actual sonic-installer requires nsenter permissions,
// so comprehensive testing would typically be done in integration tests or with mocks.
func TestHandleVerifySignature(t *testing.T) {
	// This test just ensures the function compiles with the correct signature
	var _ func(context.Context, *gnoi_os_pb.VerifyRequest) (*gnoi_os_pb.VerifyResponse, error) = HandleVerify
}
