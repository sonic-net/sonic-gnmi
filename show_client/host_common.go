package show_client

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	log "github.com/golang/glog"
)

const (
	AsicConfFilename      = "asic.conf"
	ContainerPlatformPath = "/usr/share/sonic/platform"
	HostDevicePath        = "/usr/share/sonic/device"
	MachineConfPath       = "/host/machine.conf"
	PlatformEnvConfFile   = "platform_env.conf"
)

var hwInfoDict map[string]interface{}
var hwInfoOnce sync.Once

// Utility functions
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func GetValueOrDefault(data map[string]interface{}, key string, defaultValue string) string {
	if val, ok := data[key]; ok {
		return val.(string)
	}
	return defaultValue
}

func GetChassisInfo() (map[string]string, error) {
	chassisDict := make(map[string]string)
	queries := [][]string{
		{"STATE_DB", "CHASSIS_INFO"},
	}

	metadata, err := GetMapFromQueries(queries)

	if err != nil {
		log.Errorf("Failed to get metadata from table paths: %v", err)
		return nil, err
	}

	chassisDict["serial"] = GetValueOrDefault(metadata, "serial", "")
	chassisDict["model"] = GetValueOrDefault(metadata, "model", "")
	chassisDict["revision"] = GetValueOrDefault(metadata, "revision", "")

	return chassisDict, nil
}

func GetUptime() string {
	uptimeCommand := "uptime"
	uptime, err := GetDataFromHostCommand(uptimeCommand)
	if err != nil {
		log.Errorf("Failed to get uptime: %v", err)
		return "N/A"
	}

	return strings.TrimSpace(uptime)
}

func GetDockerInfo() string {
	dockerCmd := "sudo docker images --format '{Repository:{{.Repository}}, Tag:{{.Tag}}, ID:{{.ID}}, Size:{{.Size}}}'"
	dockerInfo, err := GetDataFromHostCommand(dockerCmd)
	if err != nil {
		log.Errorf("Failed to get Docker info: %v", err)
		return "N/A"
	}
	return strings.TrimSpace(dockerInfo)
}

func GetPlatformInfo(versionInfo map[string]interface{}) (map[string]interface{}, error) {
	hwInfoOnce.Do(func() {
		hwInfoDict = make(map[string]interface{})
		hwInfoDict["platform"] = GetPlatform()
		hwInfoDict["hwsku"] = GetHwsku()
		if versionInfo != nil {
			if asicType, ok := versionInfo["asic_type"]; ok {
				hwInfoDict["asic_type"] = asicType
			}
		}
		asicCount, err := GetAsicCount()
		if err == nil {
			hwInfoDict["asic_count"] = asicCount
		} else {
			hwInfoDict["asic_count"] = "N/A"
		}
		switchType := GetLocalhostInfo("switch_type")
		hwInfoDict["switch_type"] = switchType
	})
	return hwInfoDict, nil
}

// Platform and hardware info functions
func GetPlatform() string {
	platformEnv := os.Getenv("PLATFORM")
	if platformEnv != "" {
		return platformEnv
	}
	machineInfo := GetMachineInfo()
	if machineInfo != nil {
		if val, ok := machineInfo["onie_platform"]; ok {
			return val
		} else if val, ok := machineInfo["aboot_platform"]; ok {
			return val
		}
	}
	return GetLocalhostInfo("platform")
}

func GetMachineInfo() map[string]string {
	data, err := ReadConfToMap(MachineConfPath)
	if err != nil {
		return nil
	}
	result := make(map[string]string)
	for k, v := range data {
		if strVal, ok := v.(string); ok {
			result[k] = strVal
		}
	}
	return result
}

func GetHwsku() string {
	return GetLocalhostInfo("hwsku")
}

func GetPlatformEnvConfFilePath() string {
	candidate := filepath.Join(ContainerPlatformPath, PlatformEnvConfFile)
	if FileExists(candidate) {
		return candidate
	}
	platform := GetPlatform()
	if platform != "" {
		candidate = filepath.Join(HostDevicePath, platform, PlatformEnvConfFile)
		if FileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func GetAsicCount() (int, error) {
	val := GetAsicPresenceList()
	if val == nil {
		log.Error("No ASIC presence list found")
		return 0, fmt.Errorf("no ASIC presence list found")
	}
	if len(val) == 0 {
		log.Error("ASIC presence list is empty")
		return 0, fmt.Errorf("ASIC presence list is empty")
	}
	return len(val), nil
}

// ASIC and multi-ASIC functions
func IsMultiAsic() bool {
	numAsics := GetNumAsics()
	return numAsics > 1
}

func GetNumAsics() int {
	asicConfFilePath := GetAsicConfFilePath()
	if asicConfFilePath == "" {
		return 1
	}
	file, err := os.Open(asicConfFilePath)
	if err != nil {
		return 1
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		tokens := strings.SplitN(line, "=", 2)
		if len(tokens) < 2 {
			continue
		}
		if strings.ToLower(tokens[0]) == "num_asic" {
			numAsics, err := strconv.Atoi(strings.TrimSpace(tokens[1]))
			if err == nil {
				return numAsics
			}
		}
	}
	return 1
}

// ConfigDB and info helpers
func GetLocalhostInfo(field string) string {
	queries := [][]string{
		{"CONFIG_DB", "DEVICE_METADATA"},
	}
	metadata, err := GetMapFromQueries(queries)
	if err != nil {
		return ""
	}
	if localhost, ok := metadata["localhost"].(map[string]interface{}); ok {
		if val, ok := localhost[field].(string); ok {
			return val
		}
	}
	return ""
}

// GetAsicConfFilePath retrieves the path to the ASIC configuration file on the device.
// Returns the path as a string if found, or an empty string if not found.
func GetAsicConfFilePath() string {
	// 1. Check container platform path
	candidate := filepath.Join(ContainerPlatformPath, AsicConfFilename)
	if FileExists(candidate) {
		return candidate
	}

	// 2. Check host device path with platform
	platform := GetPlatform()
	if platform != "" {
		candidate = filepath.Join(HostDevicePath, platform, AsicConfFilename)
		if FileExists(candidate) {
			return candidate
		}
	}

	// Not found
	return ""
}

func GetAsicPresenceList() []int {
	var asicsList []int
	if IsMultiAsic() {
		if !IsSupervisor() {
			numAsics := GetNumAsics()
			for i := 0; i < numAsics; i++ {
				asicsList = append(asicsList, i)
			}
		} else {
			queries := [][]string{
				{"CHASSIS_STATE_DB", "CHASSIS_FABRIC_ASIC_TABLE"},
			}
			asicTblData, err := GetMapFromQueries(queries)
			if err != nil {
				log.Errorf("Failed to get metadata from table paths: %v", err)
				return nil
			}
			if asicTblData == nil {
				log.Error("No ASIC data found in CHASSIS_FABRIC_ASIC_TABLE")
				return nil
			}
			// Iterate through ASIC names in the table and extract IDs
			for asicName := range asicTblData {
				idStr := GetAsicIDFromName(asicName)
				id, err := strconv.Atoi(idStr)
				if err == nil {
					asicsList = append(asicsList, id)
				} else {
					log.Errorf("Failed to convert ASIC ID from name %s: %v", asicName, err)
				}
			}
			if len(asicsList) == 0 {
				log.Error("No valid ASIC IDs found in CHASSIS_FABRIC_ASIC_TABLE")
				return nil
			}
		}
	} else {
		numAsics := GetNumAsics()
		for i := 0; i < numAsics; i++ {
			asicsList = append(asicsList, i)
		}
	}
	return asicsList
}

func GetAsicIDFromName(asicName string) string {
	const prefix = "asic"
	if len(asicName) > len(prefix) && asicName[:len(prefix)] == prefix {
		return asicName[len(prefix):]
	}
	return ""
}

func IsSupervisor() bool {
	path := GetPlatformEnvConfFilePath()
	if path == "" {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		tokens := strings.SplitN(line, "=", 2)
		if len(tokens) < 2 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(tokens[0])) == "supervisor" {
			val := strings.TrimSpace(tokens[1])
			if val == "1" {
				return true
			}
		}
	}
	return false
}
