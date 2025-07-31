package show_client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/golang/glog"
)

const (
	AsicConfFilename      = "asic.conf"
	ContainerPlatformPath = "/usr/share/sonic/platform"
	HostDevicePath        = "/usr/share/sonic/device"
	MachineConfPath       = "/host/machine.conf"
	PlatformEnvConfFile   = "platform_env.conf"
	SonicVersionYamlPath  = "/etc/sonic/sonic_version.yml"
)

var hwInfoDict map[string]interface{}
var hwInfoOnce sync.Once

type VersionOutput struct {
	SonicSoftwareVersion string `json:"sonic_software_version"`
	SonicOSVersion       string `json:"sonic_os_version"`
	Distribution         string `json:"distribution"`
	Kernel               string `json:"kernel"`
	BuildCommit          string `json:"build_commit"`
	BuildDate            string `json:"build_date"`
	BuiltBy              string `json:"built_by"`
	Platform             string `json:"platform"`
	HwSKU                string `json:"hwsku"`
	ASIC                 string `json:"asic"`
	ASICCount            string `json:"asic_count"`
	SerialNumber         string `json:"serial_number"`
	ModelNumber          string `json:"model_number"`
	HardwareRevision     string `json:"hardware_revision"`
	Uptime               string `json:"uptime"`
	Date                 string `json:"date"`
	DockerInfo           string `json:"docker_info"`
}

// Utility functions

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// Platform and hardware info functions

func getPlatform() string {
	platformEnv := os.Getenv("PLATFORM")
	if platformEnv != "" {
		return platformEnv
	}
	machineInfo := getMachineInfo()
	if machineInfo != nil {
		if val, ok := machineInfo["onie_platform"]; ok {
			return val
		} else if val, ok := machineInfo["aboot_platform"]; ok {
			return val
		}
	}
	return getLocalhostInfo("platform")
}

func getHwsku() string {
	return getLocalhostInfo("hwsku")
}

func getPlatformEnvConfFilePath() string {
	candidate := filepath.Join(ContainerPlatformPath, PlatformEnvConfFile)
	if fileExists(candidate) {
		return candidate
	}
	platform := getPlatform()
	if platform != "" {
		candidate = filepath.Join(HostDevicePath, platform, PlatformEnvConfFile)
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

// ASIC and multi-ASIC functions

func isMultiAsic() bool {
	numAsics := getNumAsics()
	return numAsics > 1
}

func getNumAsics() int {
	asicConfFilePath := getAsicConfFilePath()
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

// GetAsicConfFilePath retrieves the path to the ASIC configuration file on the device.
// Returns the path as a string if found, or an empty string if not found.
func getAsicConfFilePath() string {
	// 1. Check container platform path
	candidate := filepath.Join(ContainerPlatformPath, AsicConfFilename)
	if fileExists(candidate) {
		return candidate
	}

	// 2. Check host device path with platform
	platform := getPlatform()
	if platform != "" {
		candidate = filepath.Join(HostDevicePath, platform, AsicConfFilename)
		if fileExists(candidate) {
			return candidate
		}
	}

	// Not found
	return ""
}

func getAsicPresenceList() []int {
	var asicsList []int
	if isMultiAsic() {
		if !isSupervisor() {
			numAsics := getNumAsics()
			for i := 0; i < numAsics; i++ {
				asicsList = append(asicsList, i)
			}
		} else {
			queries := [][]string{
				{"CHASSIS_DB", "CHASSIS_FABRIC_ASIC_TABLE"},
			}
			tblPaths, err := CreateTablePathsFromQueries(queries)
			if err != nil {
				log.Errorf("Unable to create table paths from queries %v, %v", queries, err)
				return nil
			}
			asicTblData, err := GetMapFromTablePaths(tblPaths)
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
				idStr := getAsicIDFromName(asicName)
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
		numAsics := getNumAsics()
		for i := 0; i < numAsics; i++ {
			asicsList = append(asicsList, i)
		}
	}
	return asicsList
}

func getAsicIDFromName(asicName string) string {
	const prefix = "asic"
	if len(asicName) > len(prefix) && asicName[:len(prefix)] == prefix {
		return asicName[len(prefix):]
	}
	return ""
}

func isSupervisor() bool {
	path := getPlatformEnvConfFilePath()
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

// ConfigDB and info helpers

func getLocalhostInfo(field string) string {
	queries := [][]string{
		{"CONFIG_DB", "DEVICE_METADATA"},
	}
	tblPaths, err := CreateTablePathsFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to create table paths from queries %v, %v", queries, err)
		return ""
	}
	metadata, err := GetMapFromTablePaths(tblPaths)
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

// Vijay to remove
func CreateTablePathsFromQueries(queries [][]string) ([]string, error) {
	panic("unimplemented")
}

func GetMapFromTablePaths(tblPaths []string) (map[string]interface{}, error) {
	panic("unimplemented")
}

func GetDataFromHostCommand(uptimeCommand string) (string, error) {
	panic("unimplemented")
}

func getMachineInfo() map[string]string {
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

func getAsicCount() (int, error) {
	val := getAsicPresenceList()
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

func getPlatformInfo(versionInfo map[string]interface{}) (map[string]interface{}, error) {
	hwInfoOnce.Do(func() {
		hwInfoDict = make(map[string]interface{})
		hwInfoDict["platform"] = getPlatform()
		hwInfoDict["hwsku"] = getHwsku()
		if versionInfo != nil {
			if asicType, ok := versionInfo["asic_type"]; ok {
				hwInfoDict["asic_type"] = asicType
			}
		}
		asicCount, err := getAsicCount()
		if err == nil {
			hwInfoDict["asic_count"] = asicCount
		} else {
			hwInfoDict["asic_count"] = "N/A"
		}
		switchType := getLocalhostInfo("switch_type")
		hwInfoDict["switch_type"] = switchType
	})
	return hwInfoDict, nil
}

func getChassisInfo() (map[string]string, error) {
	chassisDict := make(map[string]string)
	queries := [][]string{
		{"STATE_DB", "CHASSIS_INFO"},
	}

	tblPaths, err := CreateTablePathsFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to create table paths from queries %v, %v", queries, err)
		return nil, err
	}
	metadata, err := GetMapFromTablePaths(tblPaths)

	if err != nil {
		log.Errorf("Failed to get metadata from table paths: %v", err)
		return nil, err
	}

	chassisDict["serial"] = metadata["serial"].(string)
	chassisDict["model"] = metadata["model"].(string)
	chassisDict["revision"] = metadata["revision"].(string)

	return chassisDict, nil
}

func getUptime() string {
	uptimeCommand := "uptime"
	uptime, err := GetDataFromHostCommand(uptimeCommand)
	if err != nil {
		log.Errorf("Failed to get uptime: %v", err)
		return "N/A"
	}

	return strings.TrimSpace(uptime)
}

func getDockerInfo() string {
	dockerCmd := "sudo docker images --format \"table {{.Repository}}\\t{{.Tag}}\\t{{.ID}}\\t{{.Size}}\""
	dockerInfo, err := GetDataFromHostCommand(dockerCmd)
	if err != nil {
		log.Errorf("Failed to get Docker info: %v", err)
		return "N/A"
	}
	return strings.TrimSpace(dockerInfo)
}

func getVersion() ([]byte, error) {
	versionInfo, errorInVersionInfo := ReadYamlToMap(SonicVersionYamlPath)
	if errorInVersionInfo != nil {
		log.Errorf("Failed to read version info from %s: %v", SonicVersionYamlPath, errorInVersionInfo)
		return nil, errorInVersionInfo
	}
	platformInfo, errorInPlatformInfo := getPlatformInfo(versionInfo)
	if errorInPlatformInfo != nil {
		log.Errorf("Failed to get platform info: %v", errorInPlatformInfo)
		return nil, errorInPlatformInfo
	}
	chassisInfo, errorChassisInfo := getChassisInfo()
	if errorChassisInfo != nil {
		log.Errorf("Failed to get chassis info: %v", errorChassisInfo)
		return nil, errorChassisInfo
	}
	uptime := getUptime()
	sysDate := time.Now()
	dockerInfo := getDockerInfo()

	out := VersionOutput{
		SonicSoftwareVersion: fmt.Sprintf("SONiC.%v", versionInfo["build_version"]),
		SonicOSVersion:       fmt.Sprintf("%v", versionInfo["sonic_os_version"]),
		Distribution:         fmt.Sprintf("Debian %v", versionInfo["debian_version"]),
		Kernel:               fmt.Sprintf("%v", versionInfo["kernel_version"]),
		BuildCommit:          fmt.Sprintf("%v", versionInfo["commit_id"]),
		BuildDate:            fmt.Sprintf("%v", versionInfo["build_date"]),
		BuiltBy:              fmt.Sprintf("%v", versionInfo["built_by"]),
		Platform:             fmt.Sprintf("%v", platformInfo["platform"]),
		HwSKU:                fmt.Sprintf("%v", platformInfo["hwsku"]),
		ASIC:                 fmt.Sprintf("%v", platformInfo["asic_type"]),
		ASICCount:            fmt.Sprintf("%v", platformInfo["asic_count"]),
		SerialNumber:         fmt.Sprintf("%v", chassisInfo["serial"]),
		ModelNumber:          fmt.Sprintf("%v", chassisInfo["model"]),
		HardwareRevision:     fmt.Sprintf("%v", chassisInfo["revision"]),
		Uptime:               uptime,
		Date:                 sysDate.Format("Mon 02 Jan 2006 15:04:05"),
		DockerInfo:           dockerInfo,
	}

	jsonBytes, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Errorf("Failed to marshal version info to JSON: %v", err)
		return nil, err
	}
	return jsonBytes, nil
}
