package client

import (
	"fmt"
	log "github.com/golang/glog"
	"strings"
)

// virtual db is to Handle
// <1> data not in SONiC redis db
// <2> or different set of redis db data aggreggation
// <3> or non default TARGET_DEFINED stream subscription

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

// Trie implmentation is adpated from https://github.com/derekparker/trie/blob/master/trie.go
type Node struct {
	val       string
	depth     int
	term      bool
	wildcards map[string]*Node //if not empty, it has wildcards children
	children  map[string]*Node
	parent    *Node
	meta      interface{}
}

type Trie struct {
	root *Node
	size int
}

// Creates a new v2r Trie with an initialized root Node.
func NewTrie() *Trie {
	return &Trie{
		root: &Node{children: make(map[string]*Node), wildcards: make(map[string]*Node), depth: 0},
		size: 0,
	}
}

// Returns the root node for the Trie.
func (t *Trie) Root() *Node {
	return t.root
}

// Adds the key to the Trie, including meta data. Meta data
// is stored as `interface{}` and must be type cast by
// the caller.
func (t *Trie) Add(keys []string, meta interface{}) *Node {
	t.size++

	node := t.root
	for _, key := range keys {
		if n, ok := node.children[key]; ok {
			node = n
		} else {
			node = node.NewChild(key, nil, false)
		}
	}
	node = node.NewChild("", meta, true)
	return node
}

// Removes a key from the trie
func (t *Trie) Remove(keys []string) {
	var (
		i    int
		rs   = keys
		node = findNode(t.Root(), keys)
	)

	t.size--
	for n := node.Parent(); n != nil; n = n.Parent() {
		i++
		if len(n.Children()) > 1 {
			r := rs[len(rs)-i]
			n.RemoveChild(r)
			break
		}
	}
}

// Finds and returns node associated
// with `key`.
func (t *Trie) Find(keys []string) (*Node, bool) {
	node := findNode(t.Root(), keys)
	if node == nil {
		return nil, false
	}

	node, ok := node.Children()[""]
	if !ok || !node.term {
		return nil, false
	}

	return node, true
}

// Creates and returns a pointer to a new child for the node.
func (n *Node) NewChild(val string, meta interface{}, term bool) *Node {
	node := &Node{
		val:       val,
		term:      term,
		meta:      meta,
		parent:    n,
		children:  make(map[string]*Node),
		wildcards: make(map[string]*Node),
		depth:     n.depth + 1,
	}
	n.children[val] = node
	if strings.HasSuffix(val, "*") {
		n.wildcards[val] = node
	}
	return node
}

func (n *Node) RemoveChild(r string) {
	delete(n.children, r)
	if _, ok := n.wildcards[r]; ok {
		delete(n.wildcards, r)
	}
}

// Returns the parent of this node.
func (n Node) Parent() *Node {
	return n.parent
}

// Returns the meta information of this node.
func (n Node) Meta() interface{} {
	return n.meta
}

// Returns the children of this node.
func (n Node) Children() map[string]*Node {
	return n.children
}

func (n Node) Val() string {
	return n.val
}

func findNode(node *Node, keys []string) *Node {
	if node == nil {
		return nil
	}

	if len(keys) == 0 {
		return node
	}

	n, ok := node.Children()[keys[0]]
	if !ok {
		var val string
		for val, n = range node.wildcards {
			if strings.HasPrefix(keys[0], val[:len(val)-1]) {
				ok = true
				break
			}
		}
		if !ok {
			return nil
		}
	}

	var nkeys []string
	if len(keys) > 1 {
		nkeys = keys[1:]
	}

	return findNode(n, nkeys)
}
