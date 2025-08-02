package show_client

import (
	"encoding/json"
	"fmt"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"strconv"
	"time"
)

type InterfaceCountersResponse struct {
	State  string
	RxOk   string
	RxBps  string
	RxUtil string
	RxErr  string
	RxDrp  string
	RxOvr  string
	TxOk   string
	TxBps  string
	TxUtil string
	TxErr  string
	TxDrp  string
	TxOvr  string
}

func calculateByteRate(rate string) string {
	if rate == defaultMissingCounterValue {
		return defaultMissingCounterValue
	}
	rateFloatValue, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return defaultMissingCounterValue
	}
	var formatted string
	switch {
	case rateFloatValue > 10*1e6:
		formatted = fmt.Sprintf("%.2f MB", rateFloatValue/1e6)
	case rateFloatValue > 10*1e3:
		formatted = fmt.Sprintf("%.2f KB", rateFloatValue/1e3)
	default:
		formatted = fmt.Sprintf("%.2f B", rateFloatValue)
	}

	return formatted + "/s"
}

func calculateUtil(rate string, portSpeed string) string {
	if rate == defaultMissingCounterValue || portSpeed == defaultMissingCounterValue {
		return defaultMissingCounterValue
	}
	byteRate, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return defaultMissingCounterValue
	}
	portRate, err := strconv.ParseFloat(portSpeed, 64)
	if err != nil {
		return defaultMissingCounterValue
	}
	util := byteRate / (portRate * 1e6 / 8.0) * 100.0
	return fmt.Sprintf("%.2f%%", util)
}

func computeState(iface string, portTable map[string]interface{}) string {
	entry, ok := portTable[iface].(map[string]interface{})
	if !ok {
		return "X"
	}
	adminStatus := fmt.Sprint(entry["admin_status"])
	operStatus := fmt.Sprint(entry["oper_status"])

	switch {
	case adminStatus == "down":
		return "X"
	case adminStatus == "up" && operStatus == "up":
		return "U"
	case adminStatus == "up" && operStatus == "down":
		return "D"
	default:
		return "X"
	}
}

func getInterfaceCounters(prefix, path *gnmipb.Path) ([]byte, error) {
	ifaces := ParseOptionsFromPath(path, "interfaces")
	periodArgs := ParseOptionsFromPath(path, "period")

	takeDiffSnapshot := false
	period := 0
	if len(periodArgs) > 0 {
		periodValue, err := strconv.Atoi(periodArgs[0])
		if err == nil && periodValue <= maxShowCommandPeriod {
			takeDiffSnapshot = true
			period = periodValue
		}
		if periodValue > maxShowCommandPeriod {
			return nil, fmt.Errorf("period value must be <= %v", maxShowCommandPeriod)
		}
	}

	oldSnapshot, err := getInterfaceCountersSnapshot(ifaces)
	if err != nil {
		log.Errorf("Unable to get interfaces counter snapshot due to err: %v", err)
		return nil, err
	}

	if !takeDiffSnapshot {
		return json.Marshal(oldSnapshot)
	}

	time.Sleep(time.Duration(period) * time.Second)

	newSnapshot, err := getInterfaceCountersSnapshot(ifaces)
	if err != nil {
		log.Errorf("Unable to get new interface counters snapshot due to err %v", err)
		return nil, err
	}

	// Compare diff between snapshot
	diffSnapshot := calculateDiffSnapshot(oldSnapshot, newSnapshot)

	return json.Marshal(diffSnapshot)
}

func getInterfaceCountersSnapshot(ifaces []string) (map[string]InterfaceCountersResponse, error) {
	queries := [][]string{
		{"COUNTERS_DB", "COUNTERS", "Ethernet*"},
	}

	aliasCountersOutput, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	portCounters := RemapAliasToPortName(aliasCountersOutput)

	queries = [][]string{
		{"COUNTERS_DB", "RATES", "Ethernet*"},
	}

	aliasRatesOutput, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	portRates := RemapAliasToPortName(aliasRatesOutput)

	queries = [][]string{
		{"APPL_DB", "PORT_TABLE"},
	}

	portTable, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	validatedIfaces := []string{}

	if len(ifaces) == 0 {
		for port, _ := range portCounters {
			validatedIfaces = append(validatedIfaces, port)
		}
	} else { // Validate
		for _, iface := range ifaces {
			_, found := portCounters[iface]
			if found { // Drop none valid interfaces
				validatedIfaces = append(validatedIfaces, iface)
			}
		}
	}

	response := make(map[string]InterfaceCountersResponse, len(ifaces))

	for _, iface := range validatedIfaces {
		state := computeState(iface, portTable)
		portSpeed := GetFieldValueString(portTable, iface, defaultMissingCounterValue, "speed")
		rxBps := GetFieldValueString(portRates, iface, defaultMissingCounterValue, "RX_BPS")
		txBps := GetFieldValueString(portRates, iface, defaultMissingCounterValue, "TX_BPS")

		response[iface] = InterfaceCountersResponse{
			State:  state,
			RxOk:   GetSumFields(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_IN_UCAST_PKTS", "SAI_PORT_STAT_IF_IN_NON_UCAST_PKTS"),
			RxBps:  calculateByteRate(rxBps),
			RxUtil: calculateUtil(rxBps, portSpeed),
			RxErr:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_IN_ERRORS"),
			RxDrp:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_IN_DISCARDS"),
			RxOvr:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_ETHER_RX_OVERSIZE_PKTS"),
			TxOk:   GetSumFields(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_OUT_UCAST_PKTS", "SAI_PORT_STAT_IF_OUT_NON_UCAST_PKTS"),
			TxBps:  calculateByteRate(txBps),
			TxUtil: calculateUtil(txBps, portSpeed),
			TxErr:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_OUT_ERRORS"),
			TxDrp:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_IF_OUT_DISCARDS"),
			TxOvr:  GetFieldValueString(portCounters, iface, defaultMissingCounterValue, "SAI_PORT_STAT_ETHER_TX_OVERSIZE_PKTS"),
		}
	}
	return response, nil
}

func calculateDiffSnapshot(oldSnapshot map[string]InterfaceCountersResponse, newSnapshot map[string]InterfaceCountersResponse) map[string]InterfaceCountersResponse {
	diffResponse := make(map[string]InterfaceCountersResponse, len(newSnapshot))

	for iface, newResp := range newSnapshot {
		oldResp, found := oldSnapshot[iface]
		if !found {
			oldResp = InterfaceCountersResponse{
				RxOk:  "0",
				RxErr: "0",
				RxDrp: "0",
				TxOk:  "0",
				TxErr: "0",
				TxDrp: "0",
				TxOvr: "0",
			}
		}
		diffResponse[iface] = InterfaceCountersResponse{
			State:  newResp.State,
			RxOk:   calculateDiffCounters(oldResp.RxOk, newResp.RxOk, defaultMissingCounterValue),
			RxBps:  newResp.RxBps,
			RxUtil: newResp.RxUtil,
			RxErr:  calculateDiffCounters(oldResp.RxErr, newResp.RxErr, defaultMissingCounterValue),
			RxDrp:  calculateDiffCounters(oldResp.RxDrp, newResp.RxDrp, defaultMissingCounterValue),
			RxOvr:  calculateDiffCounters(oldResp.RxOvr, newResp.RxOvr, defaultMissingCounterValue),
			TxOk:   calculateDiffCounters(oldResp.TxOk, newResp.TxOk, defaultMissingCounterValue),
			TxBps:  newResp.TxBps,
			TxUtil: newResp.TxUtil,
			TxErr:  calculateDiffCounters(oldResp.TxErr, newResp.TxErr, defaultMissingCounterValue),
			TxDrp:  calculateDiffCounters(oldResp.TxDrp, newResp.TxDrp, defaultMissingCounterValue),
			TxOvr:  calculateDiffCounters(oldResp.TxOvr, newResp.TxOvr, defaultMissingCounterValue),
		}
	}
	return diffResponse
}
