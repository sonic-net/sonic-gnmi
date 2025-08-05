package show_client

import (
	"encoding/json"
	"fmt"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

func getAllPortsFromConfigDB() ([]string, error) {
	queries := [][]string{
		{"CONFIG_DB", "PORT"},
	}
	data, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get data from CONFIG_DB queries %v, got err: %v", queries, err)
		return nil, err
	}
	ports := make([]string, 0, len(data))
	for iface, _ := range data {
		ports = append(ports, iface)
	}
	return ports, nil
}

func getInterfaceTransceiverPresence(options sdc.OptionMap) ([]byte, error) {
	ports, error := getAllPortsFromConfigDB()
	if error != nil {
		log.Errorf("Unable to get all ports from CONFIG_DB, %v", error)
		return nil, error
	}

	status := make(map[string]string)
	queries := make([][]string, 0, len(ports))
	for _, port := range ports {
		queries = append(queries, []string{"STATE_DB", fmt.Sprintf("TRANSCEIVER_INFO|%s", port)})
	}
	log.Infof("Prepared transceiver info queries: %v", queries)

	data, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get transceiver data from STATE_DB queries %v, got err: %v", queries, err)
		return nil, err
	}

	for _, port := range ports {
		if _, exist := data[port]; exist {
			status[port] = "Present"
		} else {
			status[port] = "Not Present"
		}
	}
	return json.Marshal(status)
}
