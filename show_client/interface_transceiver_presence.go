package show_client

import (
	"encoding/json"

	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
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
	log.Infof("Data from CONFIG_DB: %v", data)
	ports := make([]string, 0, len(data))
	for iface, _ := range data {
		ports = append(ports, iface)
	}
	return ports, nil
}

func getInterfaceTransceiverPresence(prefix, path *gnmipb.Path) ([]byte, error) {
	ports, err := getAllPortsFromConfigDB()
	if err != nil {
		log.Errorf("Unable to get all ports from CONFIG_DB, %v", err)
		return nil, err
	}

	status := make(map[string]string)
	queries := [][]string{
		{"STATE_DB", "TRANSCEIVER_INFO"},
	}
	data, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get transceiver data from STATE_DB queries %v, got err: %v", queries, err)
		return nil, err
	}
	log.V(6).Infof("TRANSCEIVER_INFO Data from STATE_DB: %v", data)
	for _, port := range ports {
		if _, exist := data[port]; exist {
			status[port] = "Present"
		} else {
			status[port] = "Not Present"
		}
	}
	log.V(6).Infof("Transceiver presence status: %v", status)

	return json.Marshal(status)
}
