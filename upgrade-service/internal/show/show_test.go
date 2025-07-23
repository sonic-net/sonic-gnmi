package show

import (
	"testing"
)

func TestNewSonicShow(t *testing.T) {
	show := NewSonicShow()
	if show == nil {
		t.Error("Expected non-nil show instance")
	}
}

func TestValidateDPUName(t *testing.T) {
	tests := []struct {
		name    string
		dpuName string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid DPU0",
			dpuName: "DPU0",
			wantErr: false,
		},
		{
			name:    "valid DPU1",
			dpuName: "DPU1",
			wantErr: false,
		},
		{
			name:    "valid DPU123",
			dpuName: "DPU123",
			wantErr: false,
		},
		{
			name:    "empty string",
			dpuName: "",
			wantErr: true,
			errMsg:  "DPU name cannot be empty",
		},
		{
			name:    "lowercase dpu",
			dpuName: "dpu0",
			wantErr: true,
			errMsg:  "invalid DPU name format",
		},
		{
			name:    "no number",
			dpuName: "DPU",
			wantErr: true,
			errMsg:  "invalid DPU name format",
		},
		{
			name:    "with space",
			dpuName: "DPU 0",
			wantErr: true,
			errMsg:  "invalid DPU name format",
		},
		{
			name:    "invalid format",
			dpuName: "CPU0",
			wantErr: true,
			errMsg:  "invalid DPU name format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDPUName(tt.dpuName)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateDPUName() expected error but got none")
				} else if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateDPUName() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateDPUName() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestParseDPUStatusLine(t *testing.T) {
	show := NewSonicShow()

	tests := []struct {
		name    string
		line    string
		wantDPU *DPUStatus
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid line with True",
			line: "  DPU0  169.254.200.1            True",
			wantDPU: &DPUStatus{
				Name:         "DPU0",
				IPAddress:    "169.254.200.1",
				Reachability: "True",
				Reachable:    true,
			},
			wantErr: false,
		},
		{
			name: "valid line with False",
			line: "  DPU3  169.254.200.4           False",
			wantDPU: &DPUStatus{
				Name:         "DPU3",
				IPAddress:    "169.254.200.4",
				Reachability: "False",
				Reachable:    false,
			},
			wantErr: false,
		},
		{
			name: "valid line with different spacing",
			line: "DPU1   169.254.200.2   True",
			wantDPU: &DPUStatus{
				Name:         "DPU1",
				IPAddress:    "169.254.200.2",
				Reachability: "True",
				Reachable:    true,
			},
			wantErr: false,
		},
		{
			name:    "insufficient fields",
			line:    "DPU0 169.254.200.1",
			wantDPU: nil,
			wantErr: true,
			errMsg:  "insufficient fields",
		},
		{
			name:    "empty line",
			line:    "",
			wantDPU: nil,
			wantErr: true,
			errMsg:  "insufficient fields",
		},
		{
			name:    "single field",
			line:    "DPU0",
			wantDPU: nil,
			wantErr: true,
			errMsg:  "insufficient fields",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dpu, err := show.parseDPUStatusLine(tt.line)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDPUStatusLine() expected error but got none")
				} else if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("parseDPUStatusLine() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("parseDPUStatusLine() unexpected error = %v", err)
				}
				if dpu == nil {
					t.Errorf("parseDPUStatusLine() returned nil DPU")
				} else {
					if dpu.Name != tt.wantDPU.Name {
						t.Errorf("parseDPUStatusLine() Name = %v, want %v", dpu.Name, tt.wantDPU.Name)
					}
					if dpu.IPAddress != tt.wantDPU.IPAddress {
						t.Errorf("parseDPUStatusLine() IPAddress = %v, want %v", dpu.IPAddress, tt.wantDPU.IPAddress)
					}
					if dpu.Reachability != tt.wantDPU.Reachability {
						t.Errorf("parseDPUStatusLine() Reachability = %v, want %v", dpu.Reachability, tt.wantDPU.Reachability)
					}
					if dpu.Reachable != tt.wantDPU.Reachable {
						t.Errorf("parseDPUStatusLine() Reachable = %v, want %v", dpu.Reachable, tt.wantDPU.Reachable)
					}
				}
			}
		})
	}
}

func TestParseChassisModulesOutput(t *testing.T) {
	show := NewSonicShow()

	tests := []struct {
		name     string
		output   string
		wantDPUs int
		wantErr  bool
	}{
		{
			name: "valid output with multiple DPUs",
			output: `  Name     IP-Address    Reachability
------  -------------  --------------
  DPU0  169.254.200.1            True
  DPU3  169.254.200.4           False`,
			wantDPUs: 2,
			wantErr:  false,
		},
		{
			name: "valid output with single DPU",
			output: `  Name     IP-Address    Reachability
------  -------------  --------------
  DPU0  169.254.200.1            True`,
			wantDPUs: 1,
			wantErr:  false,
		},
		{
			name:     "empty output",
			output:   "",
			wantDPUs: 0,
			wantErr:  false,
		},
		{
			name: "header only",
			output: `  Name     IP-Address    Reachability
------  -------------  --------------`,
			wantDPUs: 0,
			wantErr:  false,
		},
		{
			name:     "error message",
			output:   "Error: Invalid command or DPU not found",
			wantDPUs: 0,
			wantErr:  false, // We handle this as HasError=true, not a parse error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := show.parseChassisModulesOutput(tt.output)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseChassisModulesOutput() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("parseChassisModulesOutput() unexpected error = %v", err)
				}
				if result == nil {
					t.Errorf("parseChassisModulesOutput() returned nil result")
				} else {
					if len(result.DPUs) != tt.wantDPUs {
						t.Errorf("parseChassisModulesOutput() DPU count = %v, want %v", len(result.DPUs), tt.wantDPUs)
					}
				}
			}
		})
	}
}

func TestBuildCommand(t *testing.T) {
	show := NewSonicShow()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "chassis modules midplane-status",
			args: []string{"chassis", "modules", "midplane-status"},
			want: []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "show", "chassis", "modules", "midplane-status"},
		},
		{
			name: "chassis modules midplane-status DPU0",
			args: []string{"chassis", "modules", "midplane-status", "DPU0"},
			want: []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "show", "chassis", "modules", "midplane-status", "DPU0"},
		},
		{
			name: "single argument",
			args: []string{"version"},
			want: []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "show", "version"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := show.buildCommand(tt.args...)
			got := cmd.Args

			if len(got) != len(tt.want) {
				t.Errorf("buildCommand() args length = %v, want %v", len(got), len(tt.want))
			}

			for i, arg := range got {
				if i < len(tt.want) && arg != tt.want[i] {
					t.Errorf("buildCommand() args[%d] = %v, want %v", i, arg, tt.want[i])
				}
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && s[:len(substr)] == substr) ||
		(len(s) > len(substr) && s[len(s)-len(substr):] == substr) ||
		indexContains(s, substr) >= 0)
}

func indexContains(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestParseChassisModulesStatusOutput(t *testing.T) {
	show := NewSonicShow()

	tests := []struct {
		name        string
		output      string
		wantModules int
		wantErr     bool
	}{
		{
			name: "valid output with single module",
			output: `Name             Description    Physical-Slot    Oper-Status    Admin-Status        Serial
------  ----------------------  ---------------  -------------  --------------  ------------
DPU0  NVIDIA BlueField-3 DPU              N/A         Online              up  MT2428XZ0JWT`,
			wantModules: 1,
			wantErr:     false,
		},
		{
			name: "valid output with multiple modules",
			output: `Name             Description    Physical-Slot    Oper-Status    Admin-Status        Serial
------  ----------------------  ---------------  -------------  --------------  ------------
DPU0  NVIDIA BlueField-3 DPU              N/A         Online              up  MT2428XZ0JWT
DPU1  NVIDIA BlueField-3 DPU              N/A        Offline            down  MT2428XZ0ABC`,
			wantModules: 2,
			wantErr:     false,
		},
		{
			name:        "empty output",
			output:      "",
			wantModules: 0,
			wantErr:     false,
		},
		{
			name: "header only",
			output: `Name             Description    Physical-Slot    Oper-Status    Admin-Status        Serial
------  ----------------------  ---------------  -------------  --------------  ------------`,
			wantModules: 0,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modules, err := show.parseChassisModulesStatusOutput(tt.output)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseChassisModulesStatusOutput() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("parseChassisModulesStatusOutput() unexpected error = %v", err)
				}
				if len(modules) != tt.wantModules {
					t.Errorf("parseChassisModulesStatusOutput() module count = %v, want %v", len(modules), tt.wantModules)
				}
				// Test first module details if available
				if len(modules) > 0 {
					module := modules[0]
					if module.Name != "DPU0" {
						t.Errorf("parseChassisModulesStatusOutput() first module name = %v, want DPU0", module.Name)
					}
					if !contains(module.Description, "NVIDIA") {
						t.Errorf("parseChassisModulesStatusOutput() first module description = %v, want to contain 'NVIDIA'", module.Description)
					}
				}
			}
		})
	}
}

func TestParseSystemHealthDPUOutput(t *testing.T) {
	show := NewSonicShow()

	tests := []struct {
		name     string
		output   string
		wantDPUs int
		wantErr  bool
	}{
		{
			name: "valid output with single DPU and multiple state details",
			output: `Name    Oper-Status    State-Detail             State-Value    Time                             Reason
------  -------------  -----------------------  -------------  -------------------------------  --------
DPU0    Online         dpu_midplane_link_state  up             Fri Jul 18 12:45:12 AM UTC 2025
                       dpu_control_plane_state  up             Fri Jul 18 12:46:27 AM UTC 2025`,
			wantDPUs: 1,
			wantErr:  false,
		},
		{
			name: "valid output with multiple DPUs",
			output: `Name    Oper-Status    State-Detail             State-Value    Time                             Reason
------  -------------  -----------------------  -------------  -------------------------------  --------
DPU0    Online         dpu_midplane_link_state  up             Fri Jul 18 12:45:12 AM UTC 2025
                       dpu_control_plane_state  up             Fri Jul 18 12:46:27 AM UTC 2025
DPU1    Offline        dpu_midplane_link_state  down           Fri Jul 18 12:45:12 AM UTC 2025`,
			wantDPUs: 2,
			wantErr:  false,
		},
		{
			name:     "empty output",
			output:   "",
			wantDPUs: 0,
			wantErr:  false,
		},
		{
			name: "header only",
			output: `Name    Oper-Status    State-Detail             State-Value    Time                             Reason
------  -------------  -----------------------  -------------  -------------------------------  --------`,
			wantDPUs: 0,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dpus, err := show.parseSystemHealthDPUOutput(tt.output)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseSystemHealthDPUOutput() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("parseSystemHealthDPUOutput() unexpected error = %v", err)
				}
				if len(dpus) != tt.wantDPUs {
					t.Errorf("parseSystemHealthDPUOutput() DPU count = %v, want %v", len(dpus), tt.wantDPUs)
				}
				// Test first DPU details if available
				if len(dpus) > 0 {
					dpu := dpus[0]
					if dpu.Name != "DPU0" {
						t.Errorf("parseSystemHealthDPUOutput() first DPU name = %v, want DPU0", dpu.Name)
					}
					if len(dpu.StateDetails) == 0 {
						t.Errorf("parseSystemHealthDPUOutput() first DPU should have state details")
					}
				}
			}
		})
	}
}
