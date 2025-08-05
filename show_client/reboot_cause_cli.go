package show_client

import (
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

const PreviousRebootCauseFilePath = "/host/reboot-cause/previous-reboot-cause.json"

func getPreviousRebootCause(prefix, path *gnmipb.Path) ([]byte, error) {
	data, err := GetDataFromFile(PreviousRebootCauseFilePath)
	if err != nil {
		log.Errorf("Unable to get data from file %v, got err: %v", PreviousRebootCauseFilePath, err)
		return nil, err
	}
	return data, nil
}

func getRebootCauseHistory(prefix, path *gnmipb.Path) ([]byte, error) {
	queries := [][]string{
		{"STATE_DB", "REBOOT_CAUSE"},
	}
	data, err := GetDataFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get data from queries %v, got err: %v", queries, err)
		return nil, err
	}
	return data, nil
}
