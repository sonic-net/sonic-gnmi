package show_client

import (
	"encoding/json"
	"fmt"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

/*
admin@sonic:~$ show watermark telemetry interval

Telemetry interval: 30 second(s)

admin@sonic:~$ redis-cli -n 4 HGETALL "WATERMARK_TABLE|TELEMETRY_INTERVAL"
1) "interval"
2) "30"
*/

func getWatermarkTelemetryInterval(options sdc.OptionMap) ([]byte, error) {
	queries := [][]string{
		{"CONFIG_DB", "WATERMARK_TABLE", "TELEMETRY_INTERVAL"},
	}
	data, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get watermark interval data from queries %v, got err: %v", queries, err)
		return nil, err
	}

	// Default value
	interval := "120"

	if val, ok := data["interval"]; ok && val != nil {
		strVal := fmt.Sprintf("%v", val)
		if strVal != "" {
			interval = strVal
		} else {
			log.Warningf("Key 'interval' found but empty in data")
		}
	} else {
		log.Warningf("Key 'interval' not found or empty in data")
	}

	// Append "s" for seconds
	result := map[string]string{
		"interval": interval + "s",
	}
	return json.Marshal(result)
}
