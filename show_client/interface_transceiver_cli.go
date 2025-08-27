package show_client

import (
	"encoding/json"
	"fmt"

	"github.com/facette/natsort"
	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

// Command "show interface transceiver error-status"
func getTransceiverErrorStatus(options sdc.OptionMap) ([]byte, error) {
	var intf string
	if v, ok := options["interface"].String(); ok {
		intf = v
	}

	var queries [][]string
	if intf == "" {
		queries = [][]string{
			{"STATE_DB", "TRANSCEIVER_STATUS_SW"},
		}
	} else {
		queries = [][]string{
			{"STATE_DB", "TRANSCEIVER_STATUS_SW", intf},
		}
	}

	data, err := GetDataFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to get data from queries %v, got err: %v", queries, err)
		return nil, err
	}
	return data, nil
}

// Command "show interface transceiver eeprom"
var CmisDataMap = mergeMaps(QsfPDataMap, QsfpCmisDeltaDataMap)
var CCmisDataMap = mergeMaps(CmisDataMap, CCmisDeltaDataMap)

func getEEPROM(options sdc.OptionMap) (map[string]string, error) {
	var intf string
	if v, ok := options["port"].String(); ok {
		intf = v
	}
	log.Infof("parsed intf = %q", intf)

	var dumpDom bool
	if v, ok := options["dom"].Bool(); ok {
		dumpDom = v
	}

	var queries [][]string
	queries = [][]string{
		{"APPL_DB", "PORT_TABLE"},
	}

	portTable, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	intfEEPROM := make(map[string]string)
	for iface := range portTable {
		if intf != "" && iface != intf {
			continue
		}

		ok, err := isValidPhysicalPort(iface)
		if err != nil {
			return nil, err
		}
		if ok {
			intfEEPROM[iface] = convertInterfaceSfpInfoToCliOutputString(iface, dumpDom)
		}
	}
	return intfEEPROM, nil
}

func getTransceiverEEPROM(options sdc.OptionMap) ([]byte, error) {
	intfEEPROM, _ := getEEPROM(options)
	keys := make([]string, 0, len(intfEEPROM))
	for key := range intfEEPROM {
		keys = append(keys, key)
	}
	natsort.Sort(keys)

	for _, k := range keys {
		fmt.Printf("%s: %s\n", k, intfEEPROM[k])
	}

	data, err := json.Marshal(intfEEPROM)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Command "show interface transceiver info"
func getTransceiverInfo(options sdc.OptionMap) ([]byte, error) {
	intfEEPROM, _ := getEEPROM(options)
	keys := make([]string, 0, len(intfEEPROM))
	for key := range intfEEPROM {
		keys = append(keys, key)
	}
	natsort.Sort(keys)

	for _, k := range keys {
		fmt.Printf("%s: %s\n", k, intfEEPROM[k])
	}

	data, err := json.Marshal(intfEEPROM)
	if err != nil {
		return nil, err
	}
	return data, nil
}
