package show_client

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"

	log "github.com/golang/glog"
	"github.com/google/shlex"
	natural "github.com/maruel/natural"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const AppDBPortTable = "PORT_TABLE"
const StateDBPortTable = "PORT_TABLE"

// Database names
const StateDB = "STATE_DB"
const ConfigDB = "CONFIG_DB"

const (
	dbIndex    = 0 // The first index for a query will be the DB
	tableIndex = 1 // The second index for a query will be the table

	minQueryLength = 2 // We need to support TARGET/TABLE as a minimum query
	maxQueryLength = 5 // We can support up to 5 elements in query (TARGET/TABLE/(2 KEYS)/FIELD)

	hostNamespace              = "1" // PID 1 is the host init process
	defaultMissingCounterValue = "N/A"
	base10                     = 10
	maxShowCommandPeriod       = 300 // Max time allotted for SHOW commands period argument
)

func GetDataFromHostCommand(command string) (string, error) {
	baseArgs := []string{
		"--target", hostNamespace,
		"--pid", "--mount", "--uts", "--ipc", "--net",
		"--",
	}
	commandParts, err := shlex.Split(command)
	if err != nil {
		return "", err
	}
	cmdArgs := append(baseArgs, commandParts...)
	cmd := exec.Command("nsenter", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func GetDataFromFile(fileName string) ([]byte, error) {
	fileContent, err := sdc.ImplIoutilReadFile(fileName)
	if err != nil {
		log.Errorf("Failed to read'%v', %v", fileName, err)
		return nil, err
	}
	log.V(4).Infof("getDataFromFile, output: %v", string(fileContent))
	return fileContent, nil
}

func GetMapFromQueries(queries [][]string) (map[string]interface{}, error) {
	tblPaths, err := CreateTablePathsFromQueries(queries)
	if err != nil {
		return nil, err
	}
	msi := make(map[string]interface{})
	for _, tblPath := range tblPaths {
		err := sdc.TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			return nil, err
		}
	}
	return msi, nil
}

func GetDataFromQueries(queries [][]string) ([]byte, error) {
	msi, err := GetMapFromQueries(queries)
	if err != nil {
		return nil, err
	}
	return sdc.Msi2Bytes(msi)
}

func CreateTablePathsFromQueries(queries [][]string) ([]sdc.TablePath, error) {
	var allPaths []sdc.TablePath

	// Create and validate gnmi path then create table path
	for _, q := range queries {
		queryLength := len(q)
		if queryLength < minQueryLength || queryLength > maxQueryLength {
			return nil, fmt.Errorf("invalid query %v: must support at least [DB, table] or at most [DB, table, key1, key2, field]", q)
		}

		// Build a gNMI path for validation:
		//   prefix = { Target: dbTarget }
		//   path   = { Elem: [ {Name:table}, {Name:key}, {Name:field} ] }

		dbTarget := q[dbIndex]
		prefix := &gnmipb.Path{Target: dbTarget}

		table := q[tableIndex]
		elems := []*gnmipb.PathElem{{Name: table}}

		// Additional elements like keys and fields
		for i := tableIndex + 1; i < queryLength; i++ {
			elems = append(elems, &gnmipb.PathElem{Name: q[i]})
		}

		path := &gnmipb.Path{Elem: elems}

		if tablePaths, err := sdc.PopulateTablePaths(prefix, path); err != nil {
			return nil, fmt.Errorf("query %v failed: %w", q, err)
		} else {
			allPaths = append(allPaths, tablePaths...)
		}
	}
	return allPaths, nil
}

func RemapAliasToPortName(portData map[string]interface{}) map[string]interface{} {
	aliasMap := sdc.AliasToPortNameMap()
	remapped := make(map[string]interface{})

	needRemap := false

	for key := range portData {
		if _, isAlias := aliasMap[key]; isAlias {
			needRemap = true
			break
		}
	}

	if !needRemap { // Not an alias keyed map, no-op
		return portData
	}

	for alias, val := range portData {
		if portName, ok := aliasMap[alias]; ok {
			remapped[portName] = val
		}
	}
	return remapped
}

func GetFieldValueString(data map[string]interface{}, key string, defaultValue string, field string) string {
	entry, ok := data[key].(map[string]interface{})
	if !ok {
		return defaultValue
	}

	value, ok := entry[field]
	if !ok {
		return defaultValue
	}
	return fmt.Sprint(value)
}

func GetSumFields(data map[string]interface{}, key string, defaultValue string, fields ...string) (sum string) {
	defer func() {
		if r := recover(); r != nil {
			sum = defaultValue
		}
	}()
	var total int64
	for _, field := range fields {
		value := GetFieldValueString(data, key, defaultValue, field)
		if intValue, err := strconv.ParseInt(value, base10, 64); err != nil {
			return defaultValue
		} else {
			total += intValue
		}
	}
	return strconv.FormatInt(total, base10)
}

func calculateDiffCounters(oldCounter string, newCounter string, defaultValue string) string {
	if oldCounter == defaultValue || newCounter == defaultValue {
		return defaultValue
	}
	oldCounterValue, err := strconv.ParseInt(oldCounter, base10, 64)
	if err != nil {
		return defaultValue
	}
	newCounterValue, err := strconv.ParseInt(newCounter, base10, 64)
	if err != nil {
		return defaultValue
	}
	return strconv.FormatInt(newCounterValue-oldCounterValue, base10)
}

func natsortInterfaces(interfaces []string) []string {
	// Naturally sort the port list
	sort.Sort(natural.StringSlice(interfaces))
	return interfaces
}
