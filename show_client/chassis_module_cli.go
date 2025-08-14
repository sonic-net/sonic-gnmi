package show_client

import (
	"encoding/json"
	"fmt"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

type ChassisModuleStatus struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Slot        string `json:"physical_slot"`
	OperStatus  string `json:"oper_status"`
	AdminStatus string `json:"admin_status"`
	Serial      string `json:"serial"`
}

func getChassisModuleStatus(options sdc.OptionMap) ([]byte, error) {
	log.V(2).Infof("getChassisModuleStatus: called with options: %v", getOptionsKeys(options))

	// Check if a specific module is requested
	if moduleStr, ok := options["dpu"].String(); ok {
		log.V(2).Infof("getChassisModuleStatus: filtering for module: %s", moduleStr)
		return getChassisModuleStatusByModule(options)
	}

	// Original logic for all modules
	log.V(2).Infof("getChassisModuleStatus: getting all modules")

	// Query both STATE_DB and CONFIG_DB
	stateQueries := [][]string{
		{"STATE_DB", "CHASSIS_MODULE_TABLE"},
	}

	configQueries := [][]string{
		{"CONFIG_DB", "CHASSIS_MODULE"},
	}

	// Get data from both databases
	stateData, err := GetDataFromQueries(stateQueries)
	if err != nil {
		log.V(2).Infof("getChassisModuleStatus: error getting state data: %v", err)
		return nil, err
	}

	configData, err := GetDataFromQueries(configQueries)
	if err != nil {
		log.V(2).Infof("getChassisModuleStatus: error getting config data: %v", err)
		return nil, err
	}

	log.V(2).Infof("getChassisModuleStatus: state data: %s", string(stateData))
	log.V(2).Infof("getChassisModuleStatus: config data: %s", string(configData))

	// Parse the JSON data
	var stateMap map[string]interface{}
	if err := json.Unmarshal(stateData, &stateMap); err != nil {
		log.V(2).Infof("getChassisModuleStatus: error unmarshaling state data: %v", err)
		return nil, err
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(configData, &configMap); err != nil {
		log.V(2).Infof("getChassisModuleStatus: error unmarshaling config data: %v", err)
		return nil, err
	}

	// Merge the data
	result := make(map[string]interface{})
	for moduleName, stateInfo := range stateMap {
		if stateInfoMap, ok := stateInfo.(map[string]interface{}); ok {
			result[moduleName] = stateInfoMap

			// Add admin_status from CONFIG_DB if available
			if configInfo, exists := configMap[moduleName]; exists {
				if configInfoMap, ok := configInfo.(map[string]interface{}); ok {
					if adminStatus, hasAdmin := configInfoMap["admin_status"]; hasAdmin {
						result[moduleName].(map[string]interface{})["admin_status"] = adminStatus
					}
				}
			}
		}
	}

	// Convert to JSON
	jsonData, err := json.Marshal(result)
	if err != nil {
		log.V(2).Infof("getChassisModuleStatus: error marshaling result: %v", err)
		return nil, err
	}

	log.V(2).Infof("getChassisModuleStatus: returning: %s", string(jsonData))
	return jsonData, nil
}

func getChassisModuleStatusByModule(options sdc.OptionMap) ([]byte, error) {
	// Extract module name from the path
	// The path will be like ["SHOW", "chassis", "module", "status", "DPU0"]
	// We need to get the last element which is the module name
	moduleName := ""

	// Try different ways to get the module name from the path
	if moduleStr, ok := options["dpu"].String(); ok {
		moduleName = moduleStr
		log.V(2).Infof("getChassisModuleStatusByModule: module from dpu option: %s", moduleName)
	} else {
		// Try to get from the path elements
		log.V(2).Infof("getChassisModuleStatusByModule: options keys: %v", getOptionsKeys(options))
		return nil, fmt.Errorf("No module name specified in path")
	}

	if moduleName == "" {
		return nil, fmt.Errorf("Empty module name")
	}

	log.V(2).Infof("getChassisModuleStatusByModule: processing module: %s", moduleName)

	// Query both STATE_DB and CONFIG_DB for the specific module
	stateQueries := [][]string{
		{"STATE_DB", "CHASSIS_MODULE_TABLE", moduleName},
	}

	configQueries := [][]string{
		{"CONFIG_DB", "CHASSIS_MODULE", moduleName},
	}

	log.V(2).Infof("getChassisModuleStatusByModule: state queries: %v", stateQueries)
	log.V(2).Infof("getChassisModuleStatusByModule: config queries: %v", configQueries)

	// Get state data
	stateDataBytes, err := GetDataFromQueries(stateQueries)
	if err != nil {
		log.Errorf("Unable to get state data from queries %v, got err: %v", stateQueries, err)
		return nil, err
	}
	log.V(2).Infof("getChassisModuleStatusByModule: state data bytes: %s", string(stateDataBytes))

	// Get config data
	configDataBytes, err := GetDataFromQueries(configQueries)
	if err != nil {
		log.Errorf("Unable to get config data from queries %v, got err: %v", configQueries, err)
		return nil, err
	}
	log.V(2).Infof("getChassisModuleStatusByModule: config data bytes: %s", string(configDataBytes))

	// Parse the JSON data
	var stateData map[string]interface{}
	if err := json.Unmarshal(stateDataBytes, &stateData); err != nil {
		log.Errorf("Failed to unmarshal state data: %v", err)
		return nil, err
	}

	var configData map[string]interface{}
	if err := json.Unmarshal(configDataBytes, &configData); err != nil {
		log.Errorf("Failed to unmarshal config data: %v", err)
		return nil, err
	}

	// Check if the module exists in state data
	if len(stateData) == 0 {
		return nil, fmt.Errorf("Module %s not found", moduleName)
	}

	// Process the data for the specific module
	result := make(map[string]interface{})
	moduleStatusMap := make(map[string]ChassisModuleStatus)

	// Process STATE_DB data - when querying specific module, data is returned as flat structure
	log.V(2).Infof("getChassisModuleStatusByModule: processing state data with keys: %v", getMapKeys(stateData))

	// For specific module queries, the data is returned as flat structure, not nested
	module := ChassisModuleStatus{
		Name:        moduleName,
		AdminStatus: "up",  // Default value
		Slot:        "N/A", // Default value
	}

	// Process the flat state data structure
	for key, value := range stateData {
		log.V(2).Infof("getChassisModuleStatusByModule: processing state key: %s", key)
		if strValue, ok := value.(string); ok {
			switch key {
			case "desc":
				module.Description = strValue
			case "slot":
				module.Slot = strValue
			case "oper_status":
				module.OperStatus = strValue
			case "serial":
				module.Serial = strValue
			}
		}
	}

	moduleStatusMap[moduleName] = module
	log.V(2).Infof("getChassisModuleStatusByModule: added module %s: %+v", moduleName, module)

	// Process CONFIG_DB data to get admin_status - also flat structure
	log.V(2).Infof("getChassisModuleStatusByModule: processing config data with keys: %v", getMapKeys(configData))
	for key, value := range configData {
		log.V(2).Infof("getChassisModuleStatusByModule: processing config key: %s", key)
		if strValue, ok := value.(string); ok {
			if key == "admin_status" {
				if existingModule, exists := moduleStatusMap[moduleName]; exists {
					existingModule.AdminStatus = strValue
					moduleStatusMap[moduleName] = existingModule
					log.V(2).Infof("getChassisModuleStatusByModule: updated admin_status for module %s: %s", moduleName, strValue)
				}
			}
		}
	}

	log.V(2).Infof("getChassisModuleStatusByModule: moduleStatusMap has %d modules", len(moduleStatusMap))

	// Return the specific module
	if module, exists := moduleStatusMap[moduleName]; exists {
		result[moduleName] = module
		log.V(2).Infof("getChassisModuleStatusByModule: returning module %s", moduleName)
	} else {
		log.V(2).Infof("getChassisModuleStatusByModule: module %s not found in moduleStatusMap", moduleName)
		return nil, fmt.Errorf("Module %s not found", moduleName)
	}

	// Convert to JSON bytes
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Errorf("Failed to marshal chassis module status data: %v", err)
		return nil, err
	}

	log.V(4).Infof("getChassisModuleStatusByModule, output: %v", string(jsonBytes))
	return jsonBytes, nil
}

// Helper function to get map keys for debugging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Helper function to get options keys for debugging
func getOptionsKeys(options sdc.OptionMap) []string {
	keys := make([]string, 0)
	for k := range options {
		keys = append(keys, k)
	}
	return keys
}
