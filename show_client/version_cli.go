package show_client

import (
	"bufio"
	"os"
	log "github.com/golang/glog"
	"time"
	"sync"
	"strings"
)

const (
    ContainerPlatformPath = "/usr/share/sonic/platform"
    HostDevicePath        = "/usr/share/sonic/device"
    MachineConfPath = "/host/machine.conf"
    PlatformEnvConfFile   = "platform_env.conf"
    SonicVersionYamlPath = "/etc/sonic/sonic_version.yml"
)

var hwInfoDict map[string]interface{}
var hwInfoOnce sync.Once

func ReadYamlToMap(filePath string) (map[string]interface{}, error) {
	// Read the YAML file content
	yamlFile, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file: %w", err)
	}

	// Declare a map to hold the unmarshaled YAML data
	var data map[string]interface{}

	// Unmarshal the YAML content into the map
	err = yaml.Unmarshal(yamlFile, &data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return data, nil
}

func ReadConfToMap(filePath string) (map[string]interface{}, error){
    file, err := os.Open(filePath)
    if err != nil {
        return nil
    }
    defer file.Close()

    machineVars := make(map[string]string)
    scanner := bufio.NewScanner(file)
    for scanner.Scan() {
        line := scanner.Text()
        tokens := strings.SplitN(line, "=", 2)
        if len(tokens) < 2 {
            continue
        }
        machineVars[tokens[0]] = strings.TrimSpace(tokens[1])
    }
    return machineVars
}

// GetPlatform retrieves the device's platform identifier.
// If the "PLATFORM" environment variable is set, it returns that.
// Otherwise, it tries to read from /host/machine.conf.
// If that fails, it tries to get the value from ConfigDB.
func GetPlatform() string {
    // 1. Check environment variable
    platformEnv := os.Getenv("PLATFORM")
    if platformEnv != "" {
        return platformEnv
    }

    // 2. Try to read from machine.conf
    machineInfo := getMachineInfo()
    if machineInfo != nil {
        if val, ok := machineInfo["onie_platform"]; ok {
            return val
        } else if val, ok := machineInfo["aboot_platform"]; ok {
            return val
        }
    }

    // 3. Try to read from ConfigDB
    return getLocalhostInfo("platform")
}

// getMachineInfo reads key=value pairs from /host/machine.conf and returns them as a map.
func getMachineInfo() map[string]string {
	data, err := ReadConfToMap(MachineConfPath)	
}

// getLocalhostInfo fetches a field from the DEVICE_METADATA table in ConfigDB.
func getLocalhostInfo(field string) string {
    queries := [][]string{
        {"CONFIG_DB", "DEVICE_METADATA"},
    }
    tblPaths, err := CreateTablePathsFromQueries(queries)
    if err != nil {
        log.Errorf("Unable to create table paths from queries %v, %v", queries, err)
        return nil, err
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

func GetHwsku() string {
    return getLocalhostInfo("hwsku")
}

func IsMultiAsic() bool {
    numAsics := GetNumAsics()
    return numAsics > 1
}

// GetNumAsics retrieves the number of asics present in the multi-asic platform.
// You need to implement reading and parsing the asic.conf file as in your Python code.
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

// GetAsicPresenceList returns a slice of ASIC IDs present on the device.
func GetAsicPresenceList() []int {
    var asicsList []int

    if IsMultiAsic() {
        if !IsSupervisor() {
            // Not supervisor: all asics should be present
            numAsics := GetNumAsics()
            for i := 0; i < numAsics; i++ {
                asicsList = append(asicsList, i)
            }
        } else {
            // Supervisor: get asic list from CHASSIS_FABRIC_ASIC_TABLE
            db := NewDBConnector("CHASSIS_STATE_DB")
            asicTable := db.GetTable("CHASSIS_FABRIC_ASIC_TABLE")
            asicKeys := asicTable.GetKeys()
            for _, asic := range asicKeys {
                // asic is like "asic0", "asic1", etc.
                   idStr := GetAsicIDFromName(asic)
                id, err := strconv.Atoi(idStr)
                if err == nil {
                    asicsList = append(asicsList, id)
                }
            }
        }
    } else {
        // Not multi-asic: all asics should be present
        numAsics := GetNumAsics()
        for i := 0; i < numAsics; i++ {
            asicsList = append(asicsList, i)
        }
    }
    return asicsList
}

// Helper: GetAsicIDFromName extracts the numeric ID from a string like "asic0"
func GetAsicIDFromName(asicName string) string {
    const prefix = "asic"
    if len(asicName) > len(prefix) && asicName[:len(prefix)] == prefix {
        return asicName[len(prefix):]
    }
    return ""
}

// IsSupervisor checks if the device is a supervisor card by reading platform_env.conf.
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

// GetPlatformEnvConfFilePath should return the path to platform_env.conf or "" if not found.
func GetPlatformEnvConfFilePath() string {
    // 1. Check container platform path
    candidate := filepath.Join(ContainerPlatformPath, PlatformEnvConfFile)
    if fileExists(candidate) {
        return candidate
    }

    // 2. Check host device path with platform
    platform := GetPlatform(nil)
    if platform != "" {
        candidate = filepath.Join(HostDevicePath, platform, PlatformEnvConfFile)
        if fileExists(candidate) {
            return candidate
        }
    }

    // Not found
    return ""
}

func fileExists(path string) bool {
    info, err := os.Stat(path)
    if err != nil {
        return false
    }
    return !info.IsDir()
}

func getPlatformInfo(versionInfo map[string]string) (map[string]interface{}, error) {
    hwInfoOnce.Do(func() {
        hwInfoDict = make(map[string]interface{})

        hwInfoDict["platform"] = GetPlatform()
        hwInfoDict["hwsku"] = GetHwsku()
        if versionInfo != nil {
            if asicType, ok := versionInfo["asic_type"]; ok {
                hwInfoDict["asic_type"] = asicType
            }
        }

        // get_asic_presence_list is assumed to be implemented elsewhere
        asicCount, err := GetAsicCount()
        if err == nil {
            hwInfoDict["asic_count"] = asicCount
        } else {
            hwInfoDict["asic_count"] = "N/A"
        }

        // Try to get switch_type from configDB
	switchType = getLocalhostInfo("switch_type")
        hwInfoDict["switch_type"] = switchType
    })

    return hwInfoDict
}

func getVersion() ([]byte, error) {
	versionInfo = ReadYamlToMap(SonicVersionYamlPath)
	platformInfo = getPlatformInfo(versionInfo)
	chassisInfo = getChassisInfo()

	uptime = getUptime()
	sysDate = time.Now()
	retun "empty string"
}

func getChassisInfo() map[string]string {
	chassisDict = make(map[string]string)
    queries := [][]string{
		{"STATE_DB", "CHASSIS_INFO"},
	}
}
