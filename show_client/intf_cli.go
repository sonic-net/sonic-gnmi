package show_client

import (
	"encoding/json"
	"strings"
	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const ALL_PORT_ERRORS = [][]string{
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

func getIntfErrors(intf string) ([]byte, error) {
	// Query Port Operational Errors Table from STATE_DB
	queries := [][]string{
		{"STATE_DB", "PORT_OPERR_TABLE", intf},
	}
	tblPaths, err := CreateTablePathsFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to create table paths from queries %v, %v", queries, err)
		return nil, err
	}
	portErrorsTbl, _ := GetMapFromTablePaths(tblPaths)

	portErrors := make([][]string, 0, len(ALL_PORT_ERRORS) + 1)
	// Append the table header
	portErrors = append(portErrors, []string{"Port Errors", "Count", "Last timestamp(UTC)"})
	// Iterate through all port errors types and create the result
	for _, portError := range ALL_PORT_ERRORS {
		count := "0"
		timestamp := "Never"
		if portErrorsTbl != nil {
			if val, ok := portErrorsTbl[portError[0]]; ok {
				count = val.(string)
			}
			if val, ok := portErrorsTbl[portError[1]]; ok {
				timestamp = val.(string)
			}
		}

		portErrors = append(portErrors, []string{
			strings.Replace(strings.Replace(portError[0], "_", " ", -1), " count", "", -1),
			count,
			timestamp}
		)
	}

	// Convert [][]string to []byte using JSON serialization
	return json.Marshal(portErrors)
}