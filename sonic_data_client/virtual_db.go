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

	// path2TFuncTbl is used to populate trie tree which is reponsible
	// for virtual path to real data path translation
	pathTransFuncTbl = []pathTransFunc{
		{ // stats for one or all Ethernet ports
			path:      []string{"COUNTERS_DB", "COUNTERS", "Ethernet*"},
			transFunc: v2rTranslate(v2rEthPortStats),
		}, { // specific field stats for one or all Ethernet ports
			path:      []string{"COUNTERS_DB", "COUNTERS", "Ethernet*", "*"},
			transFunc: v2rTranslate(v2rEthPortFieldStats),
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
			tblPath := tablePath{
				dbName:       paths[DbIdx],
				tableName:    paths[TblIdx],
				tableKey:     oid,
				delimitor:    separator,
				jsonTableKey: port,
			}
			tblPaths = append(tblPaths, tblPath)
		}
	} else { //single port
		oid, ok := countersPortNameMap[paths[KeyIdx]]
		if !ok {
			return nil, fmt.Errorf(" %v not a valid port ", paths[KeyIdx])
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
			tblPath := tablePath{
				dbName:       paths[DbIdx],
				tableName:    paths[TblIdx],
				tableKey:     oid,
				field:        paths[FieldIdx],
				delimitor:    separator,
				jsonTableKey: port,
				jsonField:    paths[FieldIdx],
			}
			tblPaths = append(tblPaths, tblPath)
		}
	} else { //single port
		oid, ok := countersPortNameMap[paths[KeyIdx]]
		if !ok {
			return nil, fmt.Errorf(" %v not a valid port ", paths[KeyIdx])
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
