package show_client

import (
	"encoding/json"
	"fmt"
	"time"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const (
	SonicVersionYamlPath = "/etc/sonic/sonic_version.yml"
)

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

func getVersion(options sdc.OptionMap) ([]byte, error) {
	versionInfo, errorInVersionInfo := ReadYamlToMap(SonicVersionYamlPath)
	if errorInVersionInfo != nil {
		log.Errorf("Failed to read version info from %s: %v", SonicVersionYamlPath, errorInVersionInfo)
		return nil, errorInVersionInfo
	}
	platformInfo, errorInPlatformInfo := GetPlatformInfo(versionInfo)
	if errorInPlatformInfo != nil {
		log.Errorf("Failed to get platform info: %v", errorInPlatformInfo)
		return nil, errorInPlatformInfo
	}
	chassisInfo, errorChassisInfo := GetChassisInfo()
	if errorChassisInfo != nil {
		log.Errorf("Failed to get chassis info: %v", errorChassisInfo)
		return nil, errorChassisInfo
	}
	uptime := GetUptime()
	sysDate := time.Now()
	dockerInfo := GetDockerInfo()

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
