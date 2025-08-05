package show_client

import (
	"fmt"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

/*
admin@sonic:~$ show watermark telemetry interval

Telemetry interval: 30 second(s)

admin@sonic:~$ redis-cli -n 4 HGETALL "WATERMARK_TABLE|TELEMETRY_INTERVAL"
1) "interval"
2) "30"
*/

func getWatermarkTelemetryInterval(prefix, path *gnmipb.Path) ([]byte, error) {
	queries := [][]string{
		{"CONFIG_DB", "WATERMARK_TABLE", "TELEMETRY_INTERVAL"},
	}
	dataMap, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get data from queries %v, got err: %v", queries, err)
		return nil, err
	}

	log.Infof("Data from GetMapFromQueries: %v", dataMap)

	interval := "120" // Default value if not found
	if val, ok := dataMap["interval"]; ok {
		interval = fmt.Sprintf("%v", val)
	} else {
		log.Info("Interval key not found, using default value 120s")
	}
	response := fmt.Sprintf("Telemetry interval: %s second(s)", interval)
	return []byte(response), nil
}
