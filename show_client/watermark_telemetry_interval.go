package show_client

import (
	"fmt"

	log "github.com/golang/glog"
)

/*
admin@str3-8102-01:~$ redis-cli -n 4 HGETALL "WATERMARK_TABLE|TELEMETRY_INTERVAL"
1) "interval"
2) "30"
admin@str3-8102-01:~$ show watermark telemetry interval

Telemetry interval: 30 second(s)

admin@str3-8102-01:~$
*/

func getWatermarkTelemetryInterval() ([]byte, error) {
	queries := [][]string{
		{"CONFIG_DB", "WATERMARK_TABLE", "TELEMETRY_INTERVAL"},
	}

	tblPaths, err := CreateTablePathsFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to create table paths from queries %v, %v", queries, err)
		return nil, err
	}

	return GetDataFromTablePaths(tblPaths)
}
