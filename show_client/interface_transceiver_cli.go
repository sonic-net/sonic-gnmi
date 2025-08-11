package show_client

import (
	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

func getTransceiverErrorStatus(options sdc.OptionMap) ([]byte, error) {
	var intfs []string
	if interfaces, ok := options["interface"].Strings(); ok {
		intfs = interfaces
	}

	var queries [][]string
	if len(intfs) == 0 {
		queries = [][]string{
			{"STATE_DB", "TRANSCEIVER_STATUS_SW"},
		}
	} else {
		queries = [][]string{
			{"STATE_DB", "TRANSCEIVER_STATUS_SW", intfs[0]},
		}
	}

	data, err := GetDataFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get data from queries %v, got err: %v", queries, err)
		return nil, err
	}
	return data, nil
}
