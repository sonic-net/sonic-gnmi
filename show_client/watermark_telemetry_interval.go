package show_client

import (
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

/*
admin@sonic:~$ redis-cli -n 4 HGETALL "WATERMARK_TABLE|TELEMETRY_INTERVAL"
1) "interval"
2) "30"
admin@sonic:~$ show watermark telemetry interval

Telemetry interval: 30 second(s)

admin@sonic:~$
*/

func getWatermarkTelemetryInterval(prefix, path *gnmipb.Path) ([]byte, error) {
	queries := [][]string{
		{"CONFIG_DB", "WATERMARK_TABLE", "TELEMETRY_INTERVAL"},
	}
	data, err := GetDataFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get data from queries %v, got err: %v", queries, err)
		return nil, err
	}

	log.Infof("Data from GetDataFromQueries: %s", string(data))

	// Check if the response is empty
	if len(data) == 0 || string(data) == "{}" {
		log.V(2).Info("TELEMETRY_INTERVAL not found in CONFIG_DB, returning default value 120s")
		return []byte(`{"interval": "120"}`), nil
	}
	return data, nil
}
