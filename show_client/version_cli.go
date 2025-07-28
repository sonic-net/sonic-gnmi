package show_client

import (
	log "github.com/golang/glog"
	"time"
)

SonicVersionYamlPath = "/etc/sonic/sonic_version.yml"

func getVersion() ([]byte, error) {
	versionInfo = GetDataFromFile(SonicVersionYamlPath)
	platformInfo = GetPlatformInfo()
	chassisInfo = GetChassisInfo()

	uptime = GetUptime()
	sysDate = time.Now()
	retun "empty string"
}
