package client

import (
	"fmt"
	log "github.com/golang/glog"
	"strings"
)

// virtual db is to Handle
// <1> different set of redis db data aggreggation
// <2> or non default TARGET_DEFINED stream subscription

// For virtual db path
const (
	DbIdx    uint = iota // DB name is the first element (no. 0) in path slice.
	TblIdx               // Table name is the second element (no. 1) in path slice.
	KeyIdx               // Key name is the first element (no. 2) in path slice.
	FieldIdx             // Field name is the first element (no. 3) in path slice.
)

type v2rTranslate func([]string) ([]tablePath, error)

type pathTransFunc struct {
	path      []string
	transFunc v2rTranslate
}

var (
	v2rTrie *Trie

	// Port name to oid map in COUNTERS table of COUNTERS_DB
	countersPortNameMap = make(map[string]string)

	// Queue name to oid map in COUNTERS table of COUNTERS_DB
	countersQueueNameMap = make(map[string]string)

	// Alias translation: from vendor port name to sonic interface name
	alias2nameMap = make(map[string]string)
	// Alias translation: from sonic interface name to vendor port name
	name2aliasMap = make(map[string]string)

	// path2TFuncTbl is used to populate trie tree which is reponsible
	// for virtual path to real data path translation
	pathTransFuncTbl = []pathTransFunc{
		{ // stats for one or all Ethernet ports
			path:      []string{"COUNTERS_DB", "COUNTERS", "Ethernet*"},
			transFunc: v2rTranslate(v2rEthPortStats),
		}, { // specific field stats for one or all Ethernet ports
			path:      []string{"COUNTERS_DB", "COUNTERS", "Ethernet*", "*"},
			transFunc: v2rTranslate(v2rEthPortFieldStats),
		}, { // Queue stats for one or all Ethernet ports
			path:      []string{"COUNTERS_DB", "COUNTERS", "Ethernet*", "Queues"},
			transFunc: v2rTranslate(v2rEthPortQueStats),
		},
	}
)

func (t *Trie) v2rTriePopulate() {
	for _, pt := range pathTransFuncTbl {
		n := t.Add(pt.path, pt.transFunc)
		if n.meta.(v2rTranslate) == nil {
			log.V(1).Infof("Failed to add trie node for %v with %v", pt.path, pt.transFunc)
		} else {
			log.V(2).Infof("Add trie node for %v with %v", pt.path, pt.transFunc)
		}

	}
}

func initCountersQueueNameMap() error {
	var err error
	if len(countersQueueNameMap) == 0 {
		countersQueueNameMap, err = getCountersMap("COUNTERS_QUEUE_NAME_MAP")
		if err != nil {
			return err
		}
	}
	return nil
}

func initCountersPortNameMap() error {
	var err error
	if len(countersPortNameMap) == 0 {
		countersPortNameMap, err = getCountersMap("COUNTERS_PORT_NAME_MAP")
		if err != nil {
			return err
		}
	}
	return nil
}

func initAliasMap() error {
	var err error
	if len(alias2nameMap) == 0 {
		alias2nameMap, name2aliasMap, err = getAliasMap()
		if err != nil {
			return err
		}
	}
	return nil
}

// Get the mapping between sonic interface name and vendor alias
func getAliasMap() (map[string]string, map[string]string, error) {
	var alias2name_map = make(map[string]string)
	var name2alias_map = make(map[string]string)

	redisDb, _ := Target2RedisDb["CONFIG_DB"]
	_, err := redisDb.Ping().Result()
	if err != nil {
		log.V(1).Infof("Can not connect to CONFIG_DB, %v", err)
		return nil, nil, err
	}
	resp, err := redisDb.Keys("PORT|*").Result()
	if err != nil {
		log.V(1).Infof("redis get keys failed for CONFIG_DB, %v", err)
		return nil, nil, err
	}
	for _, key := range resp {
		alias, err := redisDb.HGet(key, "alias").Result()
		if err != nil {
			log.V(1).Infof("redis get field failes for CONFIG_DB, key = %v, %v", key, err)
			// clear aliasMap
			alias2name_map = make(map[string]string)
			name2alias_map = make(map[string]string)
			return nil, nil, err
		}
		alias2name_map[alias] = key[5:]
		name2alias_map[key[5:]] = alias
	}
	log.V(6).Infof("alias2nameMap: %v", alias2name_map)
	log.V(6).Infof("name2aliasMap: %v", name2alias_map)
	return alias2name_map, name2alias_map, nil
}

// Get the mapping between objects in counters DB, Ex. port name to oid in "COUNTERS_PORT_NAME_MAP" table.
// Aussuming static port name to oid map in COUNTERS table
func getCountersMap(tableName string) (map[string]string, error) {
	redisDb, _ := Target2RedisDb["COUNTERS_DB"]
	fv, err := redisDb.HGetAll(tableName).Result()
	if err != nil {
		log.V(2).Infof("redis HGetAll failed for COUNTERS_DB, tableName: %s", tableName)
		return nil, err
	}
	log.V(6).Infof("tableName: %s, map %v", tableName, fv)
	return fv, nil
}

// Populate real data paths from paths like
// [COUNTER_DB COUNTERS Ethernet*] or [COUNTER_DB COUNTERS Ethernet68]
func v2rEthPortStats(paths []string) ([]tablePath, error) {
	separator, _ := GetTableKeySeparator(paths[DbIdx])
	var tblPaths []tablePath
	if strings.HasSuffix(paths[KeyIdx], "*") { // All Ethernet ports
		for port, oid := range countersPortNameMap {
			var oport string
			if alias, ok := name2aliasMap[port]; ok {
				oport = alias
			} else {
				log.V(2).Infof("%v does not have a vendor alias", port)
				oport = port
			}

			tblPath := tablePath{
				dbName:    paths[DbIdx],
				tableName: paths[TblIdx],
				tableKey:  oid,
				delimitor: separator,
				jsonTableKey: oport,
			}
			tblPaths = append(tblPaths, tblPath)
		}
	} else { //single port
		var alias, name string
		alias = paths[KeyIdx]
		name = alias
		if val, ok := alias2nameMap[alias]; ok {
			name = val
		}
		oid, ok := countersPortNameMap[name]
		if !ok {
			return nil, fmt.Errorf("%v not a valid sonic interface port, vendor alias is %v", name, alias)
		}
		tblPaths = []tablePath{{
			dbName:    paths[DbIdx],
			tableName: paths[TblIdx],
			tableKey:  oid,
			delimitor: separator,
		}}
	}
	log.V(6).Infof("v2rEthPortStats: %v", tblPaths)
	return tblPaths, nil
}

// Supported cases:
// <1> port name having suffix of "*" with specific field;
//     Ex. [COUNTER_DB COUNTERS Ethernet* SAI_PORT_STAT_PFC_0_RX_PKTS]
// <2> exact port name with specific field.
//     Ex. [COUNTER_DB COUNTERS Ethernet68 SAI_PORT_STAT_PFC_0_RX_PKTS]
// case of "*" field could be covered in v2rEthPortStats()
func v2rEthPortFieldStats(paths []string) ([]tablePath, error) {
	separator, _ := GetTableKeySeparator(paths[DbIdx])
	var tblPaths []tablePath
	if strings.HasSuffix(paths[KeyIdx], "*") {
		for port, oid := range countersPortNameMap {
			var oport string
			if alias, ok := name2aliasMap[port]; ok {
				oport = alias
			} else {
				log.V(2).Infof("%v dose not have a vendor alias", port)
				oport = port
			}

			tblPath := tablePath{
				dbName:    paths[DbIdx],
				tableName: paths[TblIdx],
				tableKey:  oid,
				field:     paths[FieldIdx],
				delimitor: separator,
				jsonTableKey: oport,
				jsonField:    paths[FieldIdx],
			}
			tblPaths = append(tblPaths, tblPath)
		}
	} else { //single port
		var alias, name string
		alias = paths[KeyIdx]
		name = alias
		if val, ok := alias2nameMap[alias]; ok {
			name = val
		}
		oid, ok := countersPortNameMap[name]
		if !ok {
			return nil, fmt.Errorf(" %v not a valid sonic interface port, vendor alias is %v ", name, alias)
		}
		tblPaths = []tablePath{{
			dbName:    paths[DbIdx],
			tableName: paths[TblIdx],
			tableKey:  oid,
			field:     paths[FieldIdx],
			delimitor: separator,
		}}
	}
	log.V(6).Infof("v2rEthPortFieldStats: %+v", tblPaths)
	return tblPaths, nil
}

// Populate real data paths from paths like
// [COUNTER_DB COUNTERS Ethernet* Queues] or [COUNTER_DB COUNTERS Ethernet68 Queues]
func v2rEthPortQueStats(paths []string) ([]tablePath, error) {
	separator, _ := GetTableKeySeparator(paths[DbIdx])
	var tblPaths []tablePath
	if strings.HasSuffix(paths[KeyIdx], "*") { // queues on all Ethernet ports
		for que, oid := range countersQueueNameMap {
			// que is in format of "Internal_Ethernet:12"
			names := strings.Split(que, separator)
			var oname string
			if alias, ok := name2aliasMap[names[0]]; ok {
				oname = alias
			} else {
				log.V(2).Infof(" %v dose not have a vendor alias", names[0])
				oname = names[0]
			}
			que = strings.Join([]string{oname, names[1]}, ":")
			tblPath := tablePath{
				dbName:       paths[DbIdx],
				tableName:    paths[TblIdx],
				tableKey:     oid,
				delimitor:    separator,
				jsonTableKey: que,
			}
			tblPaths = append(tblPaths, tblPath)
		}
	} else { //queues on single port
		alias := paths[KeyIdx]
		name := alias
		if val, ok := alias2nameMap[alias]; ok {
			name = val
		}
		for que, oid := range countersQueueNameMap {
			//que is in formate of "Ethernet64:12"
			names := strings.Split(que, separator)
			if name != names[0] {
				continue
			}
			que = strings.Join([]string{alias, names[1]}, ":")
			tblPath := tablePath{
				dbName:       paths[DbIdx],
				tableName:    paths[TblIdx],
				tableKey:     oid,
				delimitor:    separator,
				jsonTableKey: que,
			}
			tblPaths = append(tblPaths, tblPath)
		}
	}
	log.V(6).Infof("v2rEthPortQueStats: %v", tblPaths)
	return tblPaths, nil
}

func lookupV2R(paths []string) ([]tablePath, error) {
	n, ok := v2rTrie.Find(paths)
	if ok {
		v2rTrans := n.meta.(v2rTranslate)
		return v2rTrans(paths)
	}
	return nil, fmt.Errorf("%v not found in virtual path tree", paths)
}

func init() {
	v2rTrie = NewTrie()
	v2rTrie.v2rTriePopulate()
}
