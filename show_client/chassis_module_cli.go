package show_client

import (
	"encoding/json"
	"fmt"

	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	stateDB            = "STATE_DB"
	configDB           = "CONFIG_DB"
	chassisModuleTable = "CHASSIS_MODULE_TABLE"
	chassisModule      = "CHASSIS_MODULE"
	defaultAdminStatus = "up"
	defaultSlot        = "N/A"
)

type ChassisModuleStatus struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Slot        string `json:"physical_slot"`
	OperStatus  string `json:"oper_status"`
	AdminStatus string `json:"admin_status"`
	Serial      string `json:"serial"`
}

// Database query helper
type DbQueries struct {
	State  [][]string
	Config [][]string
}

// Create database queries for chassis module data
func CreateChassisModuleQueries(moduleName string) DbQueries {
	queries := DbQueries{
		State:  [][]string{{stateDB, chassisModuleTable}},
		Config: [][]string{{configDB, chassisModule}},
	}

	if moduleName != "" {
		queries.State[0] = append(queries.State[0], moduleName)
		queries.Config[0] = append(queries.Config[0], moduleName)
	}

	return queries
}

// Get and parse data from databases
func getChassisModuleData(queries DbQueries) (map[string]interface{}, map[string]interface{}, error) {
	// Get state data
	stateDataBytes, err := GetDataFromQueries(queries.State)
	if err != nil {
		log.Errorf("Unable to get state data from queries %v, got err: %v", queries.State, err)
		return nil, nil, fmt.Errorf("failed to get state data: %w", err)
	}
	log.V(2).Infof("State data bytes: %s", string(stateDataBytes))

	// Get config data
	configDataBytes, err := GetDataFromQueries(queries.Config)
	if err != nil {
		log.Errorf("Unable to get config data from queries %v, got err: %v", queries.Config, err)
		return nil, nil, fmt.Errorf("failed to get config data: %w", err)
	}
	log.V(2).Infof("Config data bytes: %s", string(configDataBytes))

	// Parse state data
	var stateData map[string]interface{}
	if err := json.Unmarshal(stateDataBytes, &stateData); err != nil {
		log.Errorf("Failed to unmarshal state data: %v", err)
		return nil, nil, fmt.Errorf("failed to unmarshal state data: %w", err)
	}

	// Parse config data
	var configData map[string]interface{}
	if err := json.Unmarshal(configDataBytes, &configData); err != nil {
		log.Errorf("Failed to unmarshal config data: %v", err)
		return nil, nil, fmt.Errorf("failed to unmarshal config data: %w", err)
	}

	return stateData, configData, nil
}

// Create ChassisModuleStatus from flat data structure
func CreateModuleStatusFromFlatData(moduleName string, stateData, configData map[string]interface{}) ChassisModuleStatus {
	module := ChassisModuleStatus{
		Name:        moduleName,
		AdminStatus: defaultAdminStatus,
		Slot:        defaultSlot,
	}

	// Process state data
	for key, value := range stateData {
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

	// Process config data
	for key, value := range configData {
		if strValue, ok := value.(string); ok {
			if key == "admin_status" {
				module.AdminStatus = strValue
			}
		}
	}

	return module
}

func getChassisModuleStatus(options sdc.OptionMap) ([]byte, error) {
	log.V(2).Infof("getChassisModuleStatus: called with options: %v", getOptionsKeys(options))

	// Check if a specific module is requested
	if moduleStr, ok := options["dpu"].String(); ok {
		log.V(2).Infof("getChassisModuleStatus: filtering for module: %s", moduleStr)
		return getChassisModuleStatusByModule(moduleStr)
	}

	// Get data for all modules
	log.V(2).Infof("getChassisModuleStatus: getting all modules")
	queries := CreateChassisModuleQueries("")
	stateData, configData, err := getChassisModuleData(queries)
	if err != nil {
		return nil, err
	}

	// Merge the data
	result := make(map[string]interface{})
	for moduleName, stateInfo := range stateData {
		if stateInfoMap, ok := stateInfo.(map[string]interface{}); ok {
			result[moduleName] = stateInfoMap

			// Add admin_status from CONFIG_DB if available
			if configInfo, exists := configData[moduleName]; exists {
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
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	log.V(2).Infof("getChassisModuleStatus: returning: %s", string(jsonData))
	return jsonData, nil
}

func getChassisModuleStatusByModule(moduleName string) ([]byte, error) {
	if moduleName == "" {
		return nil, status.Error(codes.InvalidArgument, "empty module name")
	}

	log.V(2).Infof("getChassisModuleStatusByModule: processing module: %s", moduleName)

	// Get data for specific module
	queries := CreateChassisModuleQueries(moduleName)
	stateData, configData, err := getChassisModuleData(queries)
	if err != nil {
		return nil, err
	}

	// Check if the module exists in state data
	if len(stateData) == 0 {
		return nil, status.Errorf(codes.NotFound, "module %s not found", moduleName)
	}

	log.V(2).Infof("getChassisModuleStatusByModule: processing state data with keys: %v", getMapKeys(stateData))
	log.V(2).Infof("getChassisModuleStatusByModule: processing config data with keys: %v", getMapKeys(configData))

	// Create module status from flat data structure
	module := CreateModuleStatusFromFlatData(moduleName, stateData, configData)
	log.V(2).Infof("getChassisModuleStatusByModule: created module %s: %+v", moduleName, module)

	// Create result
	result := map[string]interface{}{
		moduleName: module,
	}

	// Convert to JSON
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Errorf("Failed to marshal chassis module status data: %v", err)
		return nil, fmt.Errorf("failed to marshal result: %w", err)
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
	keys := make([]string, 0, len(options))
	for k := range options {
		keys = append(keys, k)
	}
	return keys
}
