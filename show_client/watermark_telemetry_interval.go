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

	// Default value
	interval := "120"

	if len(data) != 0 && string(data) != "{}" {
		var parsed map[string]string
		if err := json.Unmarshal(data, &parsed); err != nil {
			log.Errorf("Failed to unmarshal data: %v", err)
			return nil, err
		}
		if val, ok := parsed["interval"]; ok {
			interval = val
		} else {
			log.Warningf("Key 'interval' not found in data: %v", parsed)
		}
	}

	// Append "s" for seconds
	result := map[string]string{
		"interval": interval + "s",
	}
	return json.Marshal(result)
}
