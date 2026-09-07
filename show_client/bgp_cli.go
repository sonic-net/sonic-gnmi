package show_client

import (
	"encoding/json"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const vtyshBGPRunningConfigCommand = `vtysh -c "show running-config bgp"`

func getBGPRunningConfig(options sdc.OptionMap) ([]byte, error) {
	vtyshOutput, err := GetDataFromHostCommand(vtyshBGPRunningConfigCommand)
	if err != nil {
		log.Errorf("Unable to successfully execute command %v, got error %v", vtyshBGPRunningConfigCommand, err)
		return nil, err
	}

	return json.Marshal(vtyshOutput)
}
