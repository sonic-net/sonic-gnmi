package show

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/golang/glog"
)

const (
	// showBinary is the name of the show command.
	showBinary = "show"

	// nsenterBinary is the nsenter command for running in host namespace.
	nsenterBinary = "nsenter"
)

// CommandType represents the type of show command to execute
type CommandType int

const (
	CommandTypeUnspecified CommandType = iota
	CommandTypeChassisModulesMidplaneStatus
	CommandTypeChassisModulesStatus
	CommandTypeSystemHealthDPU
)

// SonicShow provides a wrapper around the show CLI tool.
type SonicShow struct {
	// This struct is kept for future extensibility but currently has no fields
}

// DPUStatus represents the status information of a DPU for chassis commands.
type DPUStatus struct {
	Name         string `json:"name"`
	IPAddress    string `json:"ip_address"`
	Reachability string `json:"reachability"`
	Reachable    bool   `json:"reachable"` // Parsed boolean version
}

// DPUModuleStatus represents the module status information of a DPU.
type DPUModuleStatus struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	PhysicalSlot string `json:"physical_slot"`
	OperStatus   string `json:"oper_status"`
	AdminStatus  string `json:"admin_status"`
	Serial       string `json:"serial"`
}

// SystemHealthDPU represents system health information for a DPU.
type SystemHealthDPU struct {
	Name         string        `json:"name"`
	OperStatus   string        `json:"oper_status"`
	StateDetails []StateDetail `json:"state_details"`
}

// StateDetail represents a single state detail entry.
type StateDetail struct {
	StateName  string `json:"state_name"`
	StateValue string `json:"state_value"`
	Time       string `json:"time"`
	Reason     string `json:"reason"`
}

// CommandResult contains the result of any show command execution.
type CommandResult struct {
	CommandType CommandType `json:"command_type"`
	Target      string      `json:"target"`
	HasError    bool        `json:"has_error"`
	Message     string      `json:"message"`
	RawOutput   string      `json:"raw_output"`

	// Structured output based on command type
	ChassisModules       []DPUStatus       `json:"chassis_modules,omitempty"`
	ChassisModulesStatus []DPUModuleStatus `json:"chassis_modules_status,omitempty"`
	SystemHealthDPUs     []SystemHealthDPU `json:"system_health_dpus,omitempty"`
}

// NewSonicShow creates a new SonicShow instance.
func NewSonicShow() *SonicShow {
	return &SonicShow{}
}

// ExecuteCommand executes a show command of the specified type.
func (ss *SonicShow) ExecuteCommand(commandType CommandType, target string, parameters map[string]string) (*CommandResult, error) {
	switch commandType {
	case CommandTypeChassisModulesMidplaneStatus:
		return ss.executeChassisModulesMidplaneStatus(target)
	case CommandTypeChassisModulesStatus:
		return ss.executeChassisModulesStatus(target)
	case CommandTypeSystemHealthDPU:
		return ss.executeSystemHealthDPU(target)
	default:
		return nil, fmt.Errorf("unsupported command type: %d", commandType)
	}
}

// executeChassisModulesMidplaneStatus executes show chassis modules midplane-status.
func (ss *SonicShow) executeChassisModulesMidplaneStatus(target string) (*CommandResult, error) {
	glog.V(1).Infof("Executing show chassis modules midplane-status %s", target)

	// Build command arguments
	args := []string{"chassis", "modules", "midplane-status"}
	if target != "" {
		if err := ValidateDPUName(target); err != nil {
			return &CommandResult{
				CommandType: CommandTypeChassisModulesMidplaneStatus,
				Target:      target,
				HasError:    true,
				Message:     err.Error(),
			}, nil
		}
		args = append(args, target)
	}

	cmd := ss.buildCommand(args...)
	output, err := cmd.Output()
	if err != nil {
		return &CommandResult{
			CommandType: CommandTypeChassisModulesMidplaneStatus,
			Target:      target,
			HasError:    true,
			Message:     fmt.Sprintf("failed to execute command: %v", err),
			RawOutput:   string(output),
		}, nil
	}

	result, err := ss.parseChassisModulesOutput(string(output))
	if err != nil {
		return &CommandResult{
			CommandType: CommandTypeChassisModulesMidplaneStatus,
			Target:      target,
			HasError:    true,
			Message:     fmt.Sprintf("failed to parse output: %v", err),
			RawOutput:   string(output),
		}, nil
	}

	return &CommandResult{
		CommandType:    CommandTypeChassisModulesMidplaneStatus,
		Target:         target,
		HasError:       result.HasError,
		Message:        result.Message,
		RawOutput:      string(output),
		ChassisModules: result.DPUs,
	}, nil
}

// executeChassisModulesStatus executes show chassis modules status.
func (ss *SonicShow) executeChassisModulesStatus(target string) (*CommandResult, error) {
	glog.V(1).Infof("Executing show chassis modules status %s", target)

	// Build command arguments
	args := []string{"chassis", "modules", "status"}
	if target != "" {
		if err := ValidateDPUName(target); err != nil {
			return &CommandResult{
				CommandType: CommandTypeChassisModulesStatus,
				Target:      target,
				HasError:    true,
				Message:     err.Error(),
			}, nil
		}
		args = append(args, target)
	}

	cmd := ss.buildCommand(args...)
	output, err := cmd.Output()
	if err != nil {
		return &CommandResult{
			CommandType: CommandTypeChassisModulesStatus,
			Target:      target,
			HasError:    true,
			Message:     fmt.Sprintf("failed to execute command: %v", err),
			RawOutput:   string(output),
		}, nil
	}

	modules, err := ss.parseChassisModulesStatusOutput(string(output))
	if err != nil {
		return &CommandResult{
			CommandType: CommandTypeChassisModulesStatus,
			Target:      target,
			HasError:    true,
			Message:     fmt.Sprintf("failed to parse output: %v", err),
			RawOutput:   string(output),
		}, nil
	}

	return &CommandResult{
		CommandType:          CommandTypeChassisModulesStatus,
		Target:               target,
		HasError:             false,
		RawOutput:            string(output),
		ChassisModulesStatus: modules,
	}, nil
}

// executeSystemHealthDPU executes show system-health dpu.
func (ss *SonicShow) executeSystemHealthDPU(target string) (*CommandResult, error) {
	glog.V(1).Infof("Executing show system-health dpu %s", target)

	// Build command arguments
	args := []string{"system-health", "dpu"}
	if target != "" {
		if err := ValidateDPUName(target); err != nil {
			return &CommandResult{
				CommandType: CommandTypeSystemHealthDPU,
				Target:      target,
				HasError:    true,
				Message:     err.Error(),
			}, nil
		}
		args = append(args, target)
	}

	cmd := ss.buildCommand(args...)
	output, err := cmd.Output()
	if err != nil {
		return &CommandResult{
			CommandType: CommandTypeSystemHealthDPU,
			Target:      target,
			HasError:    true,
			Message:     fmt.Sprintf("failed to execute command: %v", err),
			RawOutput:   string(output),
		}, nil
	}

	dpus, err := ss.parseSystemHealthDPUOutput(string(output))
	if err != nil {
		return &CommandResult{
			CommandType: CommandTypeSystemHealthDPU,
			Target:      target,
			HasError:    true,
			Message:     fmt.Sprintf("failed to parse output: %v", err),
			RawOutput:   string(output),
		}, nil
	}

	return &CommandResult{
		CommandType:      CommandTypeSystemHealthDPU,
		Target:           target,
		HasError:         false,
		RawOutput:        string(output),
		SystemHealthDPUs: dpus,
	}, nil
}

// Legacy compatibility methods - keeping for backward compatibility
type ChassisModulesResult struct {
	DPUs     []DPUStatus `json:"dpus"`
	Message  string      `json:"message"`
	HasError bool        `json:"has_error"`
}

func (ss *SonicShow) GetChassisModulesMidplaneStatus(dpuName string) (*ChassisModulesResult, error) {
	result, err := ss.executeChassisModulesMidplaneStatus(dpuName)
	if err != nil {
		return nil, err
	}

	return &ChassisModulesResult{
		DPUs:     result.ChassisModules,
		Message:  result.Message,
		HasError: result.HasError,
	}, nil
}

func (ss *SonicShow) GetAllDPUStatus() (*ChassisModulesResult, error) {
	return ss.GetChassisModulesMidplaneStatus("")
}

func (ss *SonicShow) GetSpecificDPUStatus(dpuName string) (*ChassisModulesResult, error) {
	if dpuName == "" {
		return nil, fmt.Errorf("DPU name cannot be empty")
	}
	return ss.GetChassisModulesMidplaneStatus(dpuName)
}

func (ss *SonicShow) IsDPUReachable(dpuName string) (bool, error) {
	result, err := ss.GetSpecificDPUStatus(dpuName)
	if err != nil {
		return false, err
	}
	if len(result.DPUs) == 0 {
		return false, fmt.Errorf("DPU %s not found", dpuName)
	}
	return result.DPUs[0].Reachable, nil
}

// buildCommand creates an exec.Cmd that runs show command in the host namespace.
func (ss *SonicShow) buildCommand(args ...string) *exec.Cmd {
	// Build the full command with nsenter prefix
	nsenterArgs := []string{"-t", "1", "-m", "-u", "-i", "-n", "-p", "--", showBinary}
	nsenterArgs = append(nsenterArgs, args...)

	return exec.Command(nsenterBinary, nsenterArgs...)
}

// parseChassisModulesOutput parses the output of show chassis modules midplane-status command.
func (ss *SonicShow) parseChassisModulesOutput(output string) (*ChassisModulesResult, error) {
	result := &ChassisModulesResult{
		DPUs: make([]DPUStatus, 0),
	}

	lines := strings.Split(output, "\n")

	// Look for the header line to find where data starts
	dataStarted := false
	headerPattern := regexp.MustCompile(`^\s*Name\s+IP-Address\s+Reachability\s*$`)
	separatorPattern := regexp.MustCompile(`^[-\s]+$`)

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Check for header
		if headerPattern.MatchString(line) {
			glog.V(2).Info("Found header line")
			continue
		}

		// Check for separator line (-----)
		if separatorPattern.MatchString(line) {
			dataStarted = true
			glog.V(2).Info("Found separator, data starts next")
			continue
		}

		// Parse data lines after separator
		if dataStarted {
			dpu, err := ss.parseDPUStatusLine(line)
			if err != nil {
				glog.V(2).Infof("Skipping line that couldn't be parsed: %s", line)
				continue
			}
			result.DPUs = append(result.DPUs, *dpu)
		}
	}

	// If no data was found, check if there's an error message
	if len(result.DPUs) == 0 {
		result.Message = strings.TrimSpace(output)
		if strings.Contains(strings.ToLower(output), "error") ||
			strings.Contains(strings.ToLower(output), "not found") ||
			strings.Contains(strings.ToLower(output), "invalid") {
			result.HasError = true
		}
	}

	return result, nil
}

// parseSystemHealthDPUOutput parses show system-health dpu output.
func (ss *SonicShow) parseSystemHealthDPUOutput(output string) ([]SystemHealthDPU, error) {
	var dpus []SystemHealthDPU
	lines := strings.Split(output, "\n")

	// Parse the tabular output format
	// Expected format:
	// Name    Oper-Status    State-Detail             State-Value    Time                             Reason
	// ------  -------------  -----------------------  -------------  -------------------------------  --------
	// DPU0    Online         dpu_midplane_link_state  up             Fri Jul 18 12:45:12 AM UTC 2025
	//                        dpu_control_plane_state  up             Fri Jul 18 12:46:27 AM UTC 2025

	var currentDPU *SystemHealthDPU
	dataStarted := false
	headerPattern := regexp.MustCompile(`^\s*Name\s+Oper-Status\s+State-Detail\s+State-Value\s+Time\s+Reason\s*$`)
	separatorPattern := regexp.MustCompile(`^[-\s]+$`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if headerPattern.MatchString(line) {
			continue
		}

		if separatorPattern.MatchString(line) {
			dataStarted = true
			continue
		}

		if dataStarted {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				// Check if this is a new DPU entry (first field starts with "DPU")
				isNewDPU := strings.HasPrefix(fields[0], "DPU")

				glog.V(2).Infof("Parsing line: '%s', isNewDPU: %t, first field: '%s'", line, isNewDPU, fields[0])

				if isNewDPU {
					// Save previous DPU if exists
					if currentDPU != nil {
						dpus = append(dpus, *currentDPU)
					}

					// Start new DPU
					currentDPU = &SystemHealthDPU{
						Name:         fields[0],
						OperStatus:   fields[1],
						StateDetails: []StateDetail{},
					}

					// Add first state detail
					detail := StateDetail{
						StateName:  fields[2],
						StateValue: fields[3],
					}
					if len(fields) >= 5 {
						detail.Time = strings.Join(fields[4:len(fields)-1], " ")
					}
					if len(fields) >= 6 {
						detail.Reason = fields[len(fields)-1]
					}
					currentDPU.StateDetails = append(currentDPU.StateDetails, detail)
				} else {
					// This is a continuation line for the same DPU
					if currentDPU != nil {
						detail := StateDetail{
							StateName:  fields[0],
							StateValue: fields[1],
						}
						if len(fields) >= 3 {
							detail.Time = strings.Join(fields[2:len(fields)-1], " ")
						}
						if len(fields) >= 4 {
							detail.Reason = fields[len(fields)-1]
						}
						currentDPU.StateDetails = append(currentDPU.StateDetails, detail)
					}
				}
			}
		}
	}

	// Add the last DPU
	if currentDPU != nil {
		dpus = append(dpus, *currentDPU)
	}

	return dpus, nil
}

// parseChassisModulesStatusOutput parses show chassis modules status output.
func (ss *SonicShow) parseChassisModulesStatusOutput(output string) ([]DPUModuleStatus, error) {
	var modules []DPUModuleStatus
	lines := strings.Split(output, "\n")

	// Parse the tabular output format
	// Expected format:
	// Name             Description    Physical-Slot    Oper-Status    Admin-Status        Serial
	// ------  ----------------------  ---------------  -------------  --------------  ------------
	// DPU0  NVIDIA BlueField-3 DPU              N/A         Online              up  MT2428XZ0JWT

	dataStarted := false
	headerPattern := regexp.MustCompile(`^\s*Name\s+Description\s+Physical-Slot\s+Oper-Status\s+Admin-Status\s+Serial\s*$`)
	separatorPattern := regexp.MustCompile(`^[-\s]+$`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if headerPattern.MatchString(line) {
			continue
		}

		if separatorPattern.MatchString(line) {
			dataStarted = true
			continue
		}

		if dataStarted {
			fields := strings.Fields(line)
			if len(fields) >= 6 {
				module := DPUModuleStatus{
					Name:         fields[0],
					Description:  strings.Join(fields[1:len(fields)-4], " "), // Description can be multiple words
					PhysicalSlot: fields[len(fields)-4],
					OperStatus:   fields[len(fields)-3],
					AdminStatus:  fields[len(fields)-2],
					Serial:       fields[len(fields)-1],
				}
				modules = append(modules, module)
			}
		}
	}

	return modules, nil
}

// parseDPUStatusLine parses a single line of DPU status output.
func (ss *SonicShow) parseDPUStatusLine(line string) (*DPUStatus, error) {
	// Split by whitespace and filter out empty strings
	fields := strings.Fields(line)

	if len(fields) < 3 {
		return nil, fmt.Errorf("insufficient fields in line: %s", line)
	}

	name := fields[0]
	ipAddress := fields[1]
	reachabilityStr := fields[2]

	// Parse reachability as boolean
	reachable := false
	switch strings.ToLower(reachabilityStr) {
	case "true", "yes", "1", "reachable":
		reachable = true
	case "false", "no", "0", "unreachable":
		reachable = false
	default:
		// Keep original string but default to false for boolean
		reachable = false
	}

	return &DPUStatus{
		Name:         name,
		IPAddress:    ipAddress,
		Reachability: reachabilityStr,
		Reachable:    reachable,
	}, nil
}

// ValidateDPUName checks if a DPU name follows expected format (DPU0, DPU1, etc.).
func ValidateDPUName(dpuName string) error {
	if dpuName == "" {
		return fmt.Errorf("DPU name cannot be empty")
	}

	// Check if it matches DPU<number> pattern
	matched, err := regexp.MatchString(`^DPU\d+$`, dpuName)
	if err != nil {
		return fmt.Errorf("regex error: %w", err)
	}

	if !matched {
		return fmt.Errorf("invalid DPU name format: %s (expected format: DPU0, DPU1, etc.)", dpuName)
	}

	return nil
}
