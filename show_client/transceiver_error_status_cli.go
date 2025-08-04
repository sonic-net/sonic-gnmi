package show_client

import (
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

func getTransceiverErrorStatus(prefix, path *gnmipb.Path) ([]byte, error) {
	queries := [][]string{
		{"STATE_DB", "TRANSCEIVER_STATUS_SW"},
	}
	data, err := GetDataFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get data from queries %v, got err: %v", queries, err)
		return nil, err
	}
	return data, nil
}
