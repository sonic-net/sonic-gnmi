package os

import (
	"context"
	"testing"

	gnoi_os_pb "github.com/openconfig/gnoi/os"
)

func TestParseCurrentVersion(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name: "standard output",
			output: `Current: SONiC-OS-20250610.08
Next: SONiC-OS-20250610.08
Available:
SONiC-OS-20250610.08`,
			want: "SONiC-OS-20250610.08",
		},
		{
			name:   "lowercase label",
			output: "current: SONiC-OS-20250610.08",
			want:   "SONiC-OS-20250610.08",
		},
		{
			name:   "mixed case label",
			output: "CuRrEnT: SONiC-OS-20250610.08",
			want:   "SONiC-OS-20250610.08",
		},
		{
			name:   "output with spaces",
			output: "  Current:   SONiC-OS-20250610.08  \nNext: SONiC-OS-20250610.08",
			want:   "SONiC-OS-20250610.08",
		},
		{
			name:   "output with tabs",
			output: "Current:\tSONiC-OS-20250610.08\nNext: SONiC-OS-20250610.08",
			want:   "SONiC-OS-20250610.08",
		},
		{
			name:   "missing current line",
			output: "Next: SONiC-OS-20250610.08\nAvailable:",
		},
		{
			name:   "empty current value",
			output: "Current: \nNext: SONiC-OS-20250610.08",
		},
		{
			name: "empty output",
		},
		{
			name:   "invalid format",
			output: "Current SONiC-OS-20250610.08",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseCurrentVersion(tt.output); got != tt.want {
				t.Errorf("parseCurrentVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleVerifySignature(t *testing.T) {
	var _ func(context.Context, *gnoi_os_pb.VerifyRequest) (*gnoi_os_pb.VerifyResponse, error) = HandleVerify
}
