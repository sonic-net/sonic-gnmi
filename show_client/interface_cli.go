package show_client

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
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

func getInterfaceCounters(options sdc.OptionMap) ([]byte, error) {
	var ifaces []string
	period := 0
	takeDiffSnapshot := false

	if interfaces, ok := options["interfaces"].Strings(); ok {
		ifaces = interfaces
	}

	if periodValue, ok := options["period"].Int(); ok {
		takeDiffSnapshot = true
		period = periodValue
	}

	if period > maxShowCommandPeriod {
		return nil, fmt.Errorf("period value must be <= %v", maxShowCommandPeriod)
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

var allPortErrors = [][]string{
	{"oper_error_status", "oper_error_status_time"},
	{"mac_local_fault_count", "mac_local_fault_time"},
	{"mac_remote_fault_count", "mac_remote_fault_time"},
	{"fec_sync_loss_count", "fec_sync_loss_time"},
	{"fec_alignment_loss_count", "fec_alignment_loss_time"},
	{"high_ser_error_count", "high_ser_error_time"},
	{"high_ber_error_count", "high_ber_error_time"},
	{"data_unit_crc_error_count", "data_unit_crc_error_time"},
	{"data_unit_misalignment_error_count", "data_unit_misalignment_error_time"},
	{"signal_local_error_count", "signal_local_error_time"},
	{"crc_rate_count", "crc_rate_time"},
	{"data_unit_size_count", "data_unit_size_time"},
	{"code_group_error_count", "code_group_error_time"},
	{"no_rx_reachability_count", "no_rx_reachability_time"},
}

func getIntfErrors(options sdc.OptionMap) ([]byte, error) {
	intf, ok := options["interface"].String()
	if !ok {
		return nil, fmt.Errorf("No interface name passed in as option")
	}

	// Query Port Operational Errors Table from STATE_DB
	queries := [][]string{
		{"STATE_DB", "PORT_OPERR_TABLE", intf},
	}
	portErrorsTbl, _ := GetMapFromQueries(queries)
	portErrorsTbl = RemapAliasToPortName(portErrorsTbl)

	// Format the port errors data
	portErrors := make([][]string, 0, len(allPortErrors)+1)
	// Append the table header
	portErrors = append(portErrors, []string{"Port Errors", "Count", "Last timestamp(UTC)"})
	// Iterate through all port errors types and create the result
	for _, portError := range allPortErrors {
		count := "0"
		timestamp := "Never"
		if portErrorsTbl != nil {
			if val, ok := portErrorsTbl[portError[0]]; ok {
				count = fmt.Sprintf("%v", val)
			}
			if val, ok := portErrorsTbl[portError[1]]; ok {
				timestamp = fmt.Sprintf("%v", val)
			}
		}

		portErrors = append(portErrors, []string{
			strings.Replace(strings.Replace(portError[0], "_", " ", -1), " count", "", -1),
			count,
			timestamp},
		)
	}

	// Convert [][]string to []byte using JSON serialization
	return json.Marshal(portErrors)
}
