package show_client

import log "github.com/golang/glog"

const PreviousRebootCauseFilePath = "/host/reboot-cause/previous-reboot-cause.json"

func getPreviousRebootCause() ([]byte, error) {
	return GetDataFromFile(PreviousRebootCauseFilePath)
}

func getRebootCauseHistory() ([]byte, error) {
	queries := [][]string{
		{"STATE_DB", "REBOOT_CAUSE"},
	}
	tblPaths, err := CreateTablePathsFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to create table paths from queries %v, %v", queries, err)
		return nil, err
	}
	return GetDataFromTablePaths(tblPaths)
}
