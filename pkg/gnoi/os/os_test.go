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

// Note: Testing HandleVerify requires nsenter permissions, so it would typically
// be tested in integration tests or with mocks. Here we just ensure the function
// signature is correct.
func TestHandleVerifySignature(t *testing.T) {
	// This test just ensures the function compiles with the correct signature
	var _ func(context.Context, *gnoi_os_pb.VerifyRequest) (*gnoi_os_pb.VerifyResponse, error) = HandleVerify
}
