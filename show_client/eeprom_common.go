package show_client

import (
	"encoding/json"
	"fmt"
	"github.com/facette/natsort"
	log "github.com/golang/glog"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func isRoleInternal(role string) bool {
	return role == "Int" || role == "Inb" || role == "Rec" || role == "Dpc"
}

func isFrontPanelPort(iface string, role string) bool {
	if !strings.HasPrefix(iface, "Ethernet") {
		return false
	}
	if strings.HasPrefix(iface, "Ethernet-BP") || strings.HasPrefix(iface, "Ethernet-IB") || strings.HasPrefix(iface, "Ethernet-Rec") {
		return false
	}
	if strings.Contains(iface, ".") {
		return false
	}
	return !isRoleInternal(role)
}

func isValidPhysicalPort(iface string) (bool, error) {
	queries := [][]string{
		{"APPL_DB", "PORT_TABLE"},
	}
	portTable, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return false, err
	}
	role := GetFieldValueString(portTable, iface, defaultMissingCounterValue, "role")
	return isFrontPanelPort(iface, role), nil
}

func readPorttabMappings() (map[string][]int, map[int][]string, error) {
	logicalToPhysical := make(map[string][]int)
	physicalToLogic := make(map[int][]string)
	logical := []string{}

	queries := [][]string{
		{"CONFIG_DB", "PORT"},
	}
	portTable, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, nil, err
	}
	for iface := range portTable {
		if isFrontPanelPort(iface, GetFieldValueString(portTable, iface, defaultMissingCounterValue, "role")) {
			logical = append(logical, iface)
		}
	}

	for _, intfName := range logical {
		fpPortIndex := 1
		if v, ok := portTable[intfName].(map[string]interface{}); ok {
			indexStr := v["index"].(string)
			if val, err := strconv.Atoi(indexStr); err == nil {
				fpPortIndex = val
			}
			logicalToPhysical[intfName] = []int{fpPortIndex}
		}

		if _, ok := physicalToLogic[fpPortIndex]; !ok {
			physicalToLogic[fpPortIndex] = []string{intfName}
		} else {
			physicalToLogic[fpPortIndex] = append(physicalToLogic[fpPortIndex], intfName)
		}
	}

	return logicalToPhysical, physicalToLogic, nil
}

func getLogicalToPhysical(logicalPort string) []int {
	logicalToPhysical, _, _ := readPorttabMappings()
	return logicalToPhysical[logicalPort]
}

func getPhysicalToLogic(physicalPort int) []string {
	_, physicalToLogic, _ := readPorttabMappings()
	return physicalToLogic[physicalPort]
}

func getFirstSubPort(logicalPort string) string {
	physicalPort := getLogicalToPhysical(logicalPort)
	if len(physicalPort) != 0 {
		logicalPortList := getPhysicalToLogic(physicalPort[0])
		if len(logicalPortList) != 0 {
			return logicalPortList[0]
		}
	}
	return ""
}

func isTransceiverCmis(sfpInfoDict map[string]interface{}) bool {
	if sfpInfoDict == nil {
		return false
	}
	_, ok := sfpInfoDict["cmis_rev"]
	return ok
}

func isTransceiverCCmis(sfpInfoDict map[string]interface{}) bool {
	if sfpInfoDict == nil {
		return false
	}
	_, ok := sfpInfoDict["supported_max_tx_power"]
	return ok
}

func mergeMaps(a, b map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

func getTransceiverDataMap(sfpInfoDict map[string]interface{}) map[string]string {
	if sfpInfoDict == nil {
		return QsfPDataMap
	}
	isSfpCmis := isTransceiverCmis(sfpInfoDict)
	isSfpCCmis := isTransceiverCCmis(sfpInfoDict)

	if isSfpCCmis {
		return CCmisDataMap
	} else if isSfpCmis {
		return CmisDataMap
	} else {
		return QsfPDataMap
	}
}

func covertApplicationAdvertisementToOutputString(indent string, sfpInfoDict map[string]interface{}) string {
	key := "application_advertisement"
	fieldName := fmt.Sprintf("%s%s: ", indent, QsfPDataMap[key])
	output := fieldName

	appAdvStr, ok := sfpInfoDict[key].(string)
	if !ok || appAdvStr == "" {
		output += "N/A\n"
		return output
	}

	appAdvStr = strings.ReplaceAll(appAdvStr, "'", "\"")
	re := regexp.MustCompile(`(\{|,)\s*(\d+)\s*:`)
	appAdvStr = re.ReplaceAllString(appAdvStr, `$1 "$2":`)

	var appAdvDict map[string]interface{}
	if err := json.Unmarshal([]byte(appAdvStr), &appAdvDict); err != nil {
		output += fmt.Sprintf("%s\n", appAdvStr)
		return output
	}
	if len(appAdvDict) == 0 {
		output += "N/A\n"
		return output
	}

	lines := []string{}
	for _, item := range appAdvDict {
		if dict, ok := item.(map[string]interface{}); ok {
			hostInterfaceId := dict["host_electrical_interface_id"].(string)
			if hostInterfaceId == "" {
				continue
			}

			elements := []string{hostInterfaceId}

			hostAssignOptions := "Unknown"
			if val, ok := dict["host_lane_assignment_options"].(float64); ok {
				hostAssignOptions = fmt.Sprintf("0x%x", int(val))
			}
			elements = append(elements, fmt.Sprintf("Host Assign (%s)", hostAssignOptions))

			mediaID := "Unknown"
			if val, ok := dict["module_media_interface_id"].(string); ok && val != "" {
				mediaID = val
			}
			elements = append(elements, mediaID)

			mediaAssignOptions := "Unknown"
			if val, ok := dict["media_lane_assignment_options"].(float64); ok {
				mediaAssignOptions = fmt.Sprintf("0x%x", int(val))
			}
			elements = append(elements, fmt.Sprintf("Media Assign (%s)", mediaAssignOptions))

			lines = append(lines, strings.Join(elements, " - "))
		}
	}
	sep := "\n" + strings.Repeat(" ", len(fieldName))
	output += strings.Join(lines, sep) + "\n"

	return output
}

func getDataMapSortKey(keys []string, dataMap map[string]string) []string {
	sort.Slice(keys, func(i, j int) bool {
		ki, iKnown := dataMap[keys[i]]
		kj, jKnown := dataMap[keys[j]]

		if iKnown && !jKnown {
			return true
		}
		if !iKnown && jKnown {
			return false
		}
		if iKnown && jKnown {
			return ki < kj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func convertSfpInfoToOutputString(sfpInfoDict map[string]interface{}, sfpFirmwareInfoDict map[string]interface{}) string {
	indent := "        "
	output := ""
	isSfpCmis := isTransceiverCmis(sfpInfoDict)
	dataMap := getTransceiverDataMap(sfpInfoDict)

	combinedDict := make(map[string]interface{})
	for k, v := range sfpInfoDict {
		combinedDict[k] = v
	}
	if len(sfpFirmwareInfoDict) != 0 {
		for k, v := range sfpFirmwareInfoDict {
			combinedDict[k] = v
		}
	}

	keys := make([]string, 0, len(combinedDict))
	for k := range combinedDict {
		keys = append(keys, k)
	}

	sortedKeys := getDataMapSortKey(keys, dataMap)

	for _, key := range sortedKeys {
		switch key {
		case "cable_type":
			output += fmt.Sprintf("%s%s: %s\n", indent, sfpInfoDict["cable_type"], sfpInfoDict["cable_length"])
		case "cable_length":
		case "specification_compliance":
			if !isSfpCmis {
				if sfpInfoDict["type"] == "QSFP-DD Double Density 8X Pluggable Transceiver" {
					output += fmt.Sprintf("%s%s: %v\n", indent, QsfPDataMap[key], sfpInfoDict[key])
				} else {
					output += fmt.Sprintf("%s%s:\n", indent, QsfPDataMap[key])

					specComplianceDict := make(map[string]interface{})
					specStr, ok := sfpInfoDict["specification_compliance"]
					if ok && specStr != "" {
						if err := json.Unmarshal([]byte(specStr.(string)), &specComplianceDict); err != nil {
							output += fmt.Sprintf("%sN/A\n", indent+indent)
						} else {
							keys := make([]string, 0, len(specComplianceDict))
							for k := range specComplianceDict {
								keys = append(keys, k)
							}
							natsort.Sort(keys)

							for _, k := range keys {
								output += fmt.Sprintf("%s%s: %s\n", indent+indent, k, specComplianceDict[k])
							}
						}
					}
				}
			} else {
				if v, ok := dataMap[key]; ok && v != "" {
					value := "N/A"
					if v, ok := sfpInfoDict[key]; ok {
						value = fmt.Sprintf("%v", v)
					} else if len(sfpFirmwareInfoDict) != 0 {
						if v, ok := sfpFirmwareInfoDict[key]; ok {
							value = fmt.Sprintf("%v", v)
						}
					}
					output += fmt.Sprintf("%s%s: %v\n", indent, QsfPDataMap[key], value)
				}
			}
		case "application_advertisement":
			output += covertApplicationAdvertisementToOutputString(indent, sfpInfoDict)
		case "active_firmware", "inactive_firmware":
			val := "N/A"
			if v, ok := sfpFirmwareInfoDict[key]; ok {
				val = fmt.Sprintf("%v", v)
			}
			output += fmt.Sprintf("%s%s: %v\n", indent, dataMap[key], val)
		default:
			if strings.HasPrefix(key, "e1_") || strings.HasPrefix(key, "e2_") {
				if v, ok := sfpFirmwareInfoDict[key]; ok {
					output += fmt.Sprintf("%s%s: %v\n", indent, dataMap[key], v)
				}
			} else {
				displayName := key

				if v, ok := dataMap[key]; ok && v != "" {
					displayName = v

					value := "N/A"
					if v, ok := sfpInfoDict[key]; ok {
						value = fmt.Sprintf("%v", v)
					} else if len(sfpFirmwareInfoDict) != 0 {
						if v, ok := sfpFirmwareInfoDict[key]; ok {
							value = fmt.Sprintf("%v", v)
						}
					}
					output += fmt.Sprintf("%s%s: %v\n", indent, displayName, value)
				}
			}
		}
	}
	return output
}

func formatDictValueToString(sortedKeyTable []string, domInfoDict map[string]interface{}, domValueMap map[string]string, domUnitMap map[string]string, alignment int) string {
	output := ""
	indent := strings.Repeat(" ", 8)
	separator := ": "

	for _, key := range sortedKeyTable {
		value, ok := domInfoDict[key].(string)
		if !ok || value == "N/A" {
			continue
		}

		units := ""
		if value != "Unknown" && !strings.HasSuffix(value, domUnitMap[key]) {
			units = domUnitMap[key]
		}

		padLen := len(separator) + alignment - len(domValueMap[key])
		if padLen < 0 {
			padLen = 0
		}
		pad := strings.Repeat(" ", padLen)
		output += fmt.Sprintf("%s%s%s%s%s\n", indent+indent, domValueMap[key], pad+separator, value, units)
	}
	return output
}

func convertDomToOutputString(sfpType string, isSfpCmis bool, domInfoDict map[string]interface{}) string {
	indent := strings.Repeat(" ", 8)
	outputDom := ""
	channelThresholdAlign := 18
	moduleThresholdAlign := 15
	defaultAlignment := 0

	if strings.HasPrefix(sfpType, "QSFP") || strings.HasPrefix(sfpType, "OSFP") {
		outputDom += indent + "ChannelMonitorValues:\n"
		sortedKeyTable := []string{}
		var domMap map[string]string

		if isSfpCmis {
			sortedKeyTable = make([]string, 0, len(CmisDomChannelMonitorMap))
			for k := range CmisDomChannelMonitorMap {
				sortedKeyTable = append(sortedKeyTable, k)
			}
			natsort.Sort(sortedKeyTable)
			outputChannel := formatDictValueToString(sortedKeyTable, domInfoDict, CmisDomChannelMonitorMap, QsfpDdDomValueUnitMap, defaultAlignment)
			outputDom += outputChannel
		} else {
			sortedKeyTable = make([]string, 0, len(QsfpDomChannelMonitorMap))
			for k := range QsfpDomChannelMonitorMap {
				sortedKeyTable = append(sortedKeyTable, k)
			}
			natsort.Sort(sortedKeyTable)
			outputChannel := formatDictValueToString(sortedKeyTable, domInfoDict, QsfpDomChannelMonitorMap, DomValueUnitMap, defaultAlignment)
			outputDom += outputChannel
		}

		if isSfpCmis {
			domMap = SfpDomChannelThresholdMap
		} else {
			domMap = QsfpDomChannelThresholdMap
		}
		outputDom += indent + "ChannelThresholdValues:\n"
		sortedKeyTable = make([]string, 0, len(domMap))
		for k := range domMap {
			sortedKeyTable = append(sortedKeyTable, k)
		}
		natsort.Sort(sortedKeyTable)
		outputChannelThreshold := formatDictValueToString(sortedKeyTable, domInfoDict, domMap, DomChannelThresholdUnitMap, channelThresholdAlign)
		outputDom += outputChannelThreshold

		outputDom += indent + "ModuleMonitorValues:\n"
		sortedKeyTable = make([]string, 0, len(DomModuleMonitorMap))
		for k := range DomModuleMonitorMap {
			sortedKeyTable = append(sortedKeyTable, k)
		}
		natsort.Sort(sortedKeyTable)
		outputModule := formatDictValueToString(sortedKeyTable, domInfoDict, DomModuleMonitorMap, DomValueUnitMap, defaultAlignment)
		outputDom += outputModule

		outputDom += indent + "ModuleThresholdValues:\n"
		sortedKeyTable = make([]string, 0, len(DomModuleThresholdMap))
		outputModuleThreshold := formatDictValueToString(sortedKeyTable, domInfoDict, DomModuleThresholdMap, DomModuleThresholdUnitMap, moduleThresholdAlign)
		outputDom += outputModuleThreshold
	} else {
		outputDom += indent + "MonitorData:\n"
		sortedKeyTable := make([]string, 0, len(SfpDomChannelMonitorMap))
		for k := range SfpDomChannelMonitorMap {
			sortedKeyTable = append(sortedKeyTable, k)
		}
		natsort.Sort(sortedKeyTable)
		outputChannel := formatDictValueToString(sortedKeyTable, domInfoDict, SfpDomChannelMonitorMap, DomValueUnitMap, defaultAlignment)
		outputDom += outputChannel

		sortedKeyTable = make([]string, 0, len(DomModuleMonitorMap))
		for k := range DomModuleMonitorMap {
			sortedKeyTable = append(sortedKeyTable, k)
		}
		natsort.Sort(sortedKeyTable)
		outputModule := formatDictValueToString(sortedKeyTable, domInfoDict, DomModuleMonitorMap, DomValueUnitMap, defaultAlignment)
		outputDom += outputModule

		outputDom += indent + "ThresholdData:\n"
		sortedKeyTable = make([]string, 0, len(DomModuleThresholdMap))
		for k := range DomModuleThresholdMap {
			sortedKeyTable = append(sortedKeyTable, k)
		}
		natsort.Sort(sortedKeyTable)
		outputModuleThreshold := formatDictValueToString(sortedKeyTable, domInfoDict, DomModuleThresholdMap, DomModuleThresholdUnitMap, moduleThresholdAlign)
		outputDom += outputModuleThreshold

		sortedKeyTable = make([]string, 0, len(SfpDomChannelThresholdMap))
		for k := range SfpDomChannelThresholdMap {
			sortedKeyTable = append(sortedKeyTable, k)
		}
		natsort.Sort(sortedKeyTable)
		outputChannelThreshold := formatDictValueToString(sortedKeyTable, domInfoDict, SfpDomChannelThresholdMap, DomChannelThresholdUnitMap, channelThresholdAlign)
		outputDom += outputChannelThreshold
	}
	return outputDom
}

func IsRj45Port(iface string) bool {
	queries := [][]string{
		{"STATE_DB", "TRANSCEIVER_INFO", iface},
	}
	sfpInfoDict, _ := GetMapFromQueries(queries)
	portType, _ := sfpInfoDict["type"].(string)
	return portType == "RJ45"
}

func convertInterfaceSfpInfoToCliOutputString(iface string, dumpDom bool) string {
	output := ""
	var queries [][]string

	firstPort := getFirstSubPort(iface)
	if firstPort == "" {
		fmt.Printf("Error: Unable to get first subport for %s while converting SFP info\n", iface)
		output = "SFP EEPROM Not detected\n"
		return output
	}

	queries = [][]string{
		{"STATE_DB", "TRANSCEIVER_INFO", iface},
	}
	sfpInfoDict, _ := GetMapFromQueries(queries)

	queries = [][]string{
		{"STATE_DB", "TRANSCEIVER_FIRMWARE_INFO", iface},
	}
	sfpFirmwareInfoDict, _ := GetMapFromQueries(queries)

	if len(sfpInfoDict) != 0 {
		isSfpCmis := isTransceiverCmis(sfpInfoDict)
		if portType, ok := sfpInfoDict["type"].(string); ok && portType == "RJ45" {
			output = "SFP EEPROM is not applicable for RJ45 port\n"
		} else {
			output = "SFP EEPROM detected\n"
			sfpInfoOutput := convertSfpInfoToOutputString(sfpInfoDict, sfpFirmwareInfoDict)
			output += sfpInfoOutput

			if dumpDom {
				sfpType := sfpInfoDict["type"].(string)
				queries = [][]string{
					{"STATE_DB", "TRANSCEIVER_DOM_SENSOR", firstPort},
				}
				domInfoDict, err := GetMapFromQueries(queries)
				if err != nil {
					domInfoDict = make(map[string]interface{})
				}

				queries = [][]string{
					{"STATE_DB", "TRANSCEIVER_DOM_THRESHOLD", firstPort},
				}
				domThresHoldDict, err := GetMapFromQueries(queries)
				if err != nil {
					domThresHoldDict = make(map[string]interface{})
				}
				if len(domThresHoldDict) != 0 {
					for k, v := range domThresHoldDict {
						domInfoDict[k] = v
					}
				}

				dumOutput := convertDomToOutputString(sfpType, isSfpCmis, domInfoDict)
				output += dumOutput
			}
		}
	} else {
		if IsRj45Port(iface) {
			output = "SFP EEPROM is not applicable for RJ45 port\n"
		} else {
			output = "SFP EEPROM Not detected\n"
		}
	}
	return output
}
