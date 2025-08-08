package show_client

import (
	"encoding/json"

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
	data, err := GetDataFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get watermark interval data from queries %v, got err: %v", queries, err)
		return nil, err
	}

	log.Infof("Data from GetDataFromQueries: %v", data)

	if len(data) == 0 || string(data) == "{}" {
		log.V(2).Info("TELEMETRY_INTERVAL not found in CONFIG_DB, returning default value 120s")
		return json.Marshal(`{"interval": "120"}`)
	}
	return data, nil
}
