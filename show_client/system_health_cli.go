package show_client

import (
	"encoding/json"
	"fmt"
	"strings"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	swsscommon "github.com/sonic-net/sonic-gnmi/swsscommon"
)

const (
	dpuStateTable = "DPU_STATE"
	chassisServer = "redis_chassis.server"
	chassisPort   = 6380
	chassisDB     = 13
)

type DpuStateDetail struct {
	Name        string `json:"name"`
	OperStatus  string `json:"oper_status"`
	StateDetail string `json:"state_detail"`
	StateValue  string `json:"state_value"`
	Time        string `json:"time"`
	Reason      string `json:"reason"`
}

type DpuStateRow struct {
	Name        string `json:"name"`
	OperStatus  string `json:"oper_status"`
	StateDetail string `json:"state_detail"`
	StateValue  string `json:"state_value"`
	Time        string `json:"time"`
	Reason      string `json:"reason"`
}

// Create database queries for DPU state data
func CreateDpuStateQueries(moduleName string) DbQueries {
	queries := DbQueries{
		State: [][]string{{ChassisStateDB, dpuStateTable}},
	}

	if moduleName != "" && moduleName != "all" {
		queries.State[0] = append(queries.State[0], moduleName)
	}

	return queries
}

// Get and parse DPU state data from database
func getDpuStateData(queries DbQueries) (map[string]interface{}, error) {
	// Get state data
	stateDataBytes, err := GetDataFromQueries(queries.State)
	if err != nil {
		log.Errorf("Unable to get DPU state data from queries %v, got err: %v", queries.State, err)
		return nil, fmt.Errorf("failed to get DPU state data: %w", err)
	}
	log.V(2).Infof("DPU state data bytes: %s", string(stateDataBytes))

	// Parse state data
	var stateData map[string]interface{}
	if err := json.Unmarshal(stateDataBytes, &stateData); err != nil {
		log.Errorf("Failed to unmarshal DPU state data: %v", err)
		return nil, fmt.Errorf("failed to unmarshal DPU state data: %w", err)
	}

	return stateData, nil
}

// Determine operational status based on state values
func determineOperStatus(stateInfo map[string]interface{}) string {
	midplaneDown := false
	upCount := 0

	for key, value := range stateInfo {
		if strValue, ok := value.(string); ok && strings.HasSuffix(key, "_state") {
			if strings.ToLower(strValue) == "up" {
				upCount++
			}
			if strings.Contains(key, "midplane") && strings.ToLower(strValue) == "down" {
				midplaneDown = true
			}
		}
	}

	if midplaneDown {
		return "Offline"
	} else if upCount == 3 {
		return "Online"
	} else {
		return "Partial Online"
	}
}

// Create DPU state rows from flat data structure
func CreateDpuStateRowsFromData(moduleName string, stateInfo map[string]interface{}) []DpuStateRow {
	var rows []DpuStateRow
	operStatus := determineOperStatus(stateInfo)

	// Create rows for each state type (midplane, control, data)
	stateTypes := []string{"midplane", "control", "data"}

	for _, stateType := range stateTypes {
		row := DpuStateRow{
			Name:       moduleName,
			OperStatus: operStatus,
		}

		// Find state, time, and reason fields for this state type
		for key, value := range stateInfo {
			if strValue, ok := value.(string); ok {
				if strings.Contains(key, stateType) {
					if strings.HasSuffix(key, "_state") {
						row.StateDetail = key
						row.StateValue = strValue
						if strings.ToLower(strValue) == "up" {
							row.Reason = ""
						}
					} else if strings.HasSuffix(key, "_time") {
						row.Time = strValue
					} else if strings.HasSuffix(key, "_reason") {
						if strings.ToLower(row.StateValue) != "up" {
							row.Reason = strValue
						}
					}
				}
			}
		}

		// Only add row if we have state information
		if row.StateDetail != "" {
			rows = append(rows, row)
		}
	}

	return rows
}

func getSystemHealthDpu(options sdc.OptionMap) ([]byte, error) {
	log.V(2).Infof("getSystemHealthDpu: called with options: %v", getOptionsKeys(options))

	// Get module name from options
	moduleName := "all" // default to all modules
	if moduleStr, ok := options["dpu"].String(); ok {
		moduleName = moduleStr
		log.V(2).Infof("getSystemHealthDpu: filtering for module: %s", moduleName)
	}

	// Get data for specified module(s) using direct chassis database connection
	log.V(2).Infof("getSystemHealthDpu: getting data for module: %s", moduleName)
	stateData, err := getChassisDataDirect(moduleName)
	if err != nil {
		return nil, err
	}

	// Process the data
	var allRows []DpuStateRow
	for moduleKey, stateInfo := range stateData {
		// Extract module name from key (format: "DPU_STATE|DPU0")
		keyParts := strings.Split(moduleKey, "|")
		if len(keyParts) != 2 {
			log.V(2).Infof("getSystemHealthDpu: skipping invalid key: %s", moduleKey)
			continue
		}
		moduleName := keyParts[1]

		stateInfoMap, ok := stateInfo.(map[string]interface{})
		if !ok {
			log.V(2).Infof("getSystemHealthDpu: skipping invalid state info for module: %s", moduleName)
			continue
		}

		// Create rows for this module
		rows := CreateDpuStateRowsFromData(moduleName, stateInfoMap)
		allRows = append(allRows, rows...)
	}

	// Convert to JSON
	jsonData, err := json.Marshal(allRows)
	if err != nil {
		log.V(2).Infof("getSystemHealthDpu: error marshaling result: %v", err)
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	log.V(2).Infof("getSystemHealthDpu: returning: %s", string(jsonData))
	return jsonData, nil
}

// Get data directly from chassis database using SonicV2Connector
func getChassisDataDirect(moduleName string) (map[string]interface{}, error) {
	// Create SonicV2Connector similar to Python implementation
	chassisStateDB := swsscommon.NewSonicV2Connector_Native(false, "")
	defer chassisStateDB.Close()

	// Connect to the chassis database
	chassisStateDB.Connect("CHASSIS_STATE_DB")

	// Create key pattern
	keyPattern := "DPU_STATE|*"
	if moduleName != "" && moduleName != "all" {
		keyPattern = "DPU_STATE|" + moduleName
	}

	// Get keys from the database
	keys := chassisStateDB.Keys("CHASSIS_STATE_DB", keyPattern)
	if keys.Size() == 0 {
		log.V(2).Infof("No DPU_STATE keys found for pattern: %s", keyPattern)
		return make(map[string]interface{}), nil
	}

	// Build result map
	result := make(map[string]interface{})
	for i := int64(0); i < keys.Size(); i++ {
		key := keys.Get(int(i))
		// Get all fields for this key
		stateInfo := chassisStateDB.Get_all("CHASSIS_STATE_DB", key)

		// Convert to map[string]interface{}
		stateMap := make(map[string]interface{})
		// Get all fields from the FieldValueMap
		// Since FieldValueMap doesn't have a Keys() method, we'll try to get all known fields
		// Based on the actual database structure we observed
		allPossibleFields := []string{
			"id",
			"dpu_midplane_link_state", "dpu_midplane_link_time", "dpu_midplane_link_reason",
			"dpu_control_plane_state", "dpu_control_plane_time", "dpu_control_plane_reason",
			"dpu_data_plane_state", "dpu_data_plane_time", "dpu_data_plane_reason",
		}
		for _, field := range allPossibleFields {
			if stateInfo.Has_key(field) {
				stateMap[field] = stateInfo.Get(field)
			}
		}

		// Log the fields we found for debugging
		log.V(2).Infof("Found fields for key %s: %v", key, stateMap)

		result[key] = stateMap
	}

	return result, nil
}
