package show_cli

import (
	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const PreviousRebootCauseFilePath = "/host/reboot-cause/previous-reboot-cause.json"

func getPreviousRebootCause() ([]byte, error) {
	return sdc.GetDataFromFile(PreviousRebootCauseFilePath)
}

func getRebootCauseHistory() ([]byte, error) {
	queries := [][]string{
		{"STATE_DB", "REBOOT_CAUSE"},
	}
	tblPaths, err := sdc.CreateTablePathsFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to create table paths from queries %v, %v", queries, err)
		return nil, err
	}
	return sdc.GetDataFromTablePaths(tblPaths)
}
