package show_client

import (
	"encoding/json"
	"strings"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const dbName = "COUNTERS_DB"

var separator string

func init() {
	var err error
	separator, err = sdc.GetTableKeySeparator(dbName, "")
	if err != nil {
		log.Warningf("Failed to get table key separator for %s: %v\nUsing the default separator ':'.", dbName, err)
		separator = ":"
	}
}

type QueueCountersResponse struct {
	packets        string `json:"Counter/pkts"`
	bytes          string `json:"Counter/bytes"`
	droppedPackets string `json:"Drop/pkts"`
	droppedBytes   string `json:"Drop/bytes"`
	trimmedPackets string `json:"Trim/pkts"`
}

func RemapAliasToPortNameForQueues(queueData map[string]interface{}) map[string]interface{} {
	aliasMap := sdc.AliasToPortNameMap()
	remapped := make(map[string]interface{})

	for key, val := range queueData {
		port, queueIdx, found := strings.Cut(key, separator)
		if !found {
			log.Warningf("Ignoring the invalid queue '%v'", key)
			continue
		}
		if vendorPortName, ok := aliasMap[port]; ok {
			remapped[vendorPortName+separator+queueIdx] = val
		} else {
			remapped[key] = val
		}
	}

	return remapped
}

func getQueueCountersSnapshot(ifaces []string) (map[string]QueueCountersResponse, error) {
	var queries [][]string
	if len(ifaces) == 0 {
		// Need queue counters for all interfaces
		queries = append(queries, []string{dbName, "COUNTERS", "Ethernet*", "Queues"})
	} else {
		for _, iface := range ifaces {
			queries = append(queries, []string{dbName, "COUNTERS", iface, "Queues"})
		}
	}

	queryMap, err := GetMapFromQueries(queries)
	log.Infof("queryMap = %v", queryMap)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	queueCounters := RemapAliasToPortNameForQueues(queryMap)

	response := make(map[string]QueueCountersResponse)
	for queue, counters := range queueCounters {
		if strings.HasSuffix(queue, ":periodic") {
			// Ignoring periodic queue watermarks
			continue
		}
		countersMap, ok := counters.(map[string]interface{})
		if !ok {
			log.Warningf("Ignoring invalid counters for the queue '%v': %v", queue, counters)
			continue
		}
		response[queue] = QueueCountersResponse{
			packets:        GetValueOrDefault(countersMap, "SAI_QUEUE_STAT_PACKETS", defaultMissingCounterValue),
			bytes:          GetValueOrDefault(countersMap, "SAI_QUEUE_STAT_BYTES", defaultMissingCounterValue),
			droppedPackets: GetValueOrDefault(countersMap, "SAI_QUEUE_STAT_DROPPED_PACKETS", defaultMissingCounterValue),
			droppedBytes:   GetValueOrDefault(countersMap, "SAI_QUEUE_STAT_DROPPED_BYTES", defaultMissingCounterValue),
			trimmedPackets: GetValueOrDefault(countersMap, "SAI_QUEUE_STAT_TRIM_PACKETS", defaultMissingCounterValue),
		}
	}
	log.Infof("response = %v", response)
	return response, nil
}

func getQueueCounters(options sdc.OptionMap) ([]byte, error) {
	var ifaces []string

	if interfaces, ok := options["interfaces"].Strings(); ok {
		ifaces = interfaces
	}

	snapshot, err := getQueueCountersSnapshot(ifaces)
	if err != nil {
		log.Errorf("Unable to get queue counters due to err: %v", err)
		return nil, err
	}

	return json.Marshal(snapshot)
}
