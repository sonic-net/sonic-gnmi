// Package client provides a generic access layer for data available in system
package client

// #cgo pkg-config: python3-embed
// #include <Python.h>
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
	"io/ioutil"

	log "github.com/golang/glog"

	spb "github.com/sonic-net/sonic-gnmi/proto"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"github.com/Workiva/go-datastructures/queue"
	"github.com/go-redis/redis"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

const (
	// indentString represents the default indentation string used for
	// JSON. Two spaces are used here.
	indentString string = "  "
)

const (
    opAdd = iota
    opRemove
)

// Client defines a set of methods which every client must implement.
// This package provides one implmentation for now: the DbClient
//
type Client interface {
	// StreamRun will start watching service on data source
	// and enqueue data change to the priority queue.
	// It stops all activities upon receiving signal on stop channel
	// It should run as a go routine
	StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList)
	// Poll will  start service to respond poll signal received on poll channel.
	// data read from data source will be enqueued on to the priority queue
	// The service will stop upon detection of poll channel closing.
	// It should run as a go routine
	PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList)
	OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList)
	// Get return data from the data source in format of *spb.Value
	Get(w *sync.WaitGroup) ([]*spb.Value, error)
	// Set data based on path and value
	Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error
	// Capabilities of the switch
	Capabilities() []gnmipb.ModelData

	// Close provides implemenation for explicit cleanup of Client
	Close() error
}

type Stream interface {
	Send(m *gnmipb.SubscribeResponse) error
}

// Let it be variable visible to other packages for now.
// May add an interface function for it.
var UseRedisLocalTcpPort bool = false

// redis client connected to each DB
var Target2RedisDb = make(map[string]map[string]*redis.Client)

// MinSampleInterval is the lowest sampling interval for streaming subscriptions.
// Any non-zero value that less than this threshold is considered invalid argument.
var MinSampleInterval = time.Second

// IntervalTicker is a factory method to implement interval ticking.
// Exposed for UT purposes.
var IntervalTicker = func(interval time.Duration) <-chan time.Time {
	return time.After(interval)
}

type tablePath struct {
	dbNamespace string
	dbName      string
	tableName   string
	tableKey    string
	delimitor   string
	field       string
	value       string
	index       int
	operation   int
	// path name to be used in json data which may be different
	// from the real data path. Ex. in Counters table, real tableKey
	// is oid:0x####, while key name like Ethernet## may be put
	// in json data. They are to be filled in populateDbtablePath()
	jsonTableName string
	jsonTableKey  string
	jsonDelimitor string
	jsonField     string
}

type Value struct {
	*spb.Value
}

// Implement Compare method for priority queue
func (val Value) Compare(other queue.Item) int {
	oval := other.(Value)
	if val.GetTimestamp() > oval.GetTimestamp() {
		return 1
	} else if val.GetTimestamp() == oval.GetTimestamp() {
		return 0
	}
	return -1
}

type DbClient struct {
	prefix  *gnmipb.Path
	paths   []*gnmipb.Path
	pathG2S map[*gnmipb.Path][]tablePath
	q       *queue.PriorityQueue
	channel chan struct{}
	target  string
	origin  string
	workPath string
	testMode bool

	synced sync.WaitGroup  // Control when to send gNMI sync_response
	w      *sync.WaitGroup // wait for all sub go routines to finish
	mu     sync.RWMutex    // Mutex for data protection among routines for DbClient

	sendMsg int64
	recvMsg int64
	errors  int64
}

func NewDbClient(paths []*gnmipb.Path, prefix *gnmipb.Path, target string, origin string, testMode bool) (Client, error) {
	var client DbClient

	// Testing program may ask to use redis local tcp connection
	if UseRedisLocalTcpPort {
		useRedisTcpClient()
	}

	client.prefix = prefix
	client.target = target
	client.origin = origin
	client.paths = paths
	client.testMode = testMode
	client.workPath = "/etc/sonic/gnmi"

	return &client, nil
}

func (c *DbClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *DbClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *DbClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func PathExists(path string) (bool, error) {
    _, err := os.Stat(path)
    if err == nil {
        return true, nil
    }
    if os.IsNotExist(err) {
        return false, nil
    }
    return false, err
}

func DecodeJsonTable(database map[string]interface{}, tableName string) (map[string]interface{}, error) {
	vtable, ok := database[tableName]
	if !ok {
		log.V(2).Infof("Invalid database %v", database)
		return nil, fmt.Errorf("Invalid database %v", database)
	}
	v, ok := vtable.(map[string]interface{})
	if !ok {
		log.V(2).Infof("Invalid table %v", vtable)
		return nil, fmt.Errorf("Invalid table %v", vtable)
	}
	return v, nil
}

func DecodeJsonEntry(table map[string]interface{}, entryName string) (map[string]interface{}, error) {
	ventry, ok := table[entryName]
	if !ok {
		log.V(2).Infof("Invalid entry %v", table)
		return nil, fmt.Errorf("Invalid entry %v", table)
	}
	v, ok := ventry.(map[string]interface{})
	if !ok {
		log.V(2).Infof("Invalid entry %v", ventry)
		return nil, fmt.Errorf("Invalid entry %v", ventry)
	}
	return v, nil
}

func DecodeJsonField(entry map[string]interface{}, fieldName string) (*string, []interface{}, error) {
	vfield, ok := entry[fieldName]
	if !ok {
		log.V(2).Infof("Invalid entry %v", entry)
		return nil, nil, fmt.Errorf("Invalid entry %v", entry)
	}
	str, ok := vfield.(string)
	if ok {
		return &str, nil, nil
	}
	list, ok := vfield.([]interface{})
	if ok {
		return nil, list, nil
	}
	return nil, nil, fmt.Errorf("Invalid field %v", vfield)
}

func DecodeJsonListItem(list []interface{}, index string) (*string, error) {
	id, err := strconv.Atoi(index)
	if err != nil {
		log.V(2).Infof("Invalid index %v", index)
		return nil, fmt.Errorf("Invalid index %v", index)
	}
	if id < 0 || id >= len(list) {
		log.V(2).Infof("Invalid index %v", index)
		return nil, fmt.Errorf("Invalid index %v", index)
	}
	vitem := list[id]
	str, ok := vitem.(string)
	if ok {
		return &str, nil
	}
	return nil, fmt.Errorf("Invalid item %v", vitem)
}

func (c *DbClient) GetCheckPoint() ([]*spb.Value, error) {
	fileName := c.workPath + "/config.cp.json"
	ok, err := PathExists(fileName)
	if ok == false {
		return nil, fmt.Errorf("No check point") 
	}
	jsonFile, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer jsonFile.Close()
 
	jsonData, err := ioutil.ReadAll(jsonFile)
	if err!= nil {
		return nil, err
	}
	res, err := parseJson([]byte(jsonData))
	if err != nil {
		return nil, err
	}
	vdatabase, ok := res.(map[string]interface{})
	if !ok {
		log.V(2).Infof("Invalid checkpoint %v", fileName)
		return nil, fmt.Errorf("Invalid checkpoint %v", fileName)
	}

	var values []*spb.Value
	ts := time.Now()

	log.V(2).Infof("Getting #%v", res)
	for _, path := range c.paths {
		fullPath := path
		if c.prefix != nil {
			fullPath = gnmiFullPath(c.prefix, path)
		}
		log.V(2).Infof("Path #%v", fullPath)

		stringSlice := []string{}
		elems := fullPath.GetElem()
		jv := []byte{}
		if elems != nil {
			for i, elem := range elems {
				// TODO: Usage of key field
				log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
				stringSlice = append(stringSlice, elem.GetName())
			}
			// The expect real db path could be in one of the formats:
			// <1> DB Table
			// <2> DB Table Key
			// <3> DB Table Key Field
			// <4> DB Table Key Field Index
			switch len(stringSlice) {
			case 1: // only table name provided
				vtable, err := DecodeJsonTable(vdatabase, stringSlice[0])
				if err != nil {
					return nil, err
				}
				jv, err = emitJSON(&vtable)
				if err != nil {
					return nil, err
				}
			case 2: // Second element must be table key
				vtable, err := DecodeJsonTable(vdatabase, stringSlice[0])
				if err != nil {
					return nil, err
				}
				ventry, err := DecodeJsonEntry(vtable, stringSlice[1])
				if err != nil {
					return nil, err
				}
				jv, err = emitJSON(&ventry)
				if err != nil {
					return nil, err
				}
			case 3: // Third element must be field name
				vtable, err := DecodeJsonTable(vdatabase, stringSlice[0])
				if err != nil {
					return nil, err
				}
				ventry, err := DecodeJsonEntry(vtable, stringSlice[1])
				if err != nil {
					return nil, err
				}
				vstr, vlist, err := DecodeJsonField(ventry, stringSlice[2])
				if err != nil {
					return nil, err
				}
				if vstr != nil {
					jv = []byte(`"` + *vstr + `"`)
				} else if vlist != nil {
					jv, err = json.Marshal(vlist)
					if err != nil {
						return nil, err
					}
				}
			case 4: // Fourth element must be list index
				vtable, err := DecodeJsonTable(vdatabase, stringSlice[0])
				if err != nil {
					return nil, err
				}
				ventry, err := DecodeJsonEntry(vtable, stringSlice[1])
				if err != nil {
					return nil, err
				}
				_, vlist, err := DecodeJsonField(ventry, stringSlice[2])
				if err != nil {
					return nil, err
				}
				vstr, err := DecodeJsonListItem(vlist, stringSlice[3])
				if err != nil {
					return nil, err
				}
				if vstr != nil {
					jv = []byte(`"` + *vstr + `"`)
				} else {
					return nil, fmt.Errorf("Invalid db table Path %v", stringSlice)
				}
			default:
				log.V(2).Infof("Invalid db table Path %v", stringSlice)
				return nil, fmt.Errorf("Invalid db table Path %v", stringSlice)
			}

			val := gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: jv},
				}
			values = append(values, &spb.Value{
				Prefix:    c.prefix,
				Path:      path,
				Timestamp: ts.UnixNano(),
				Val:       &val,
			})
		}
	}

	return values, nil
}

func (c *DbClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	// wait sync for Get, not used for now
	c.w = w

	if c.target == "CONFIG_DB" {
		ret, err := c.GetCheckPoint()
		if err == nil {
			return ret, err
		}
		log.V(6).Infof("Error #%v", err)
	}

	if c.paths != nil {
		c.pathG2S = make(map[*gnmipb.Path][]tablePath)
		err := populateAllDbtablePath(c.prefix, c.target, c.paths, &c.pathG2S)
		if err != nil {
			return nil, err
		}
	}

	var values []*spb.Value
	ts := time.Now()
	for gnmiPath, tblPaths := range c.pathG2S {
		val, err := tableData2TypedValue(tblPaths, nil)
		if err != nil {
			return nil, err
		}

		values = append(values, &spb.Value{
			Prefix:    c.prefix,
			Path:      gnmiPath,
			Timestamp: ts.UnixNano(),
			Val:       val,
		})
	}
	log.V(6).Infof("Getting #%v", values)
	log.V(4).Infof("Get done, total time taken: %v ms", int64(time.Since(ts)/time.Millisecond))
	return values, nil
}

// TODO: Log data related to this session
func (c *DbClient) Close() error {
	return nil
}

// Convert from SONiC Value to its corresponding gNMI proto stream
// response type.
func ValToResp(val Value) (*gnmipb.SubscribeResponse, error) {
	switch val.GetSyncResponse() {
	case true:
		return &gnmipb.SubscribeResponse{
			Response: &gnmipb.SubscribeResponse_SyncResponse{
				SyncResponse: true,
			},
		}, nil
	default:
		// In case the subscribe/poll routines encountered fatal error
		if fatal := val.GetFatal(); fatal != "" {
			return nil, fmt.Errorf("%s", fatal)
		}

		return &gnmipb.SubscribeResponse{
			Response: &gnmipb.SubscribeResponse_Update{
				Update: &gnmipb.Notification{
					Timestamp: val.GetTimestamp(),
					Prefix:    val.GetPrefix(),
					Update: []*gnmipb.Update{
						{
							Path: val.GetPath(),
							Val:  val.GetVal(),
						},
					},
				},
			},
		}, nil
	}
}

func GetTableKeySeparator(target string, ns string) (string, error) {
	_, ok := spb.Target_value[target]
	if !ok {
		log.V(1).Infof(" %v not a valid path target", target)
		return "", fmt.Errorf("%v not a valid path target", target)
	}

	var separator string = sdcfg.GetDbSeparator(target, ns)
	return separator, nil
}

func GetRedisClientsForDb(target string) map[string]*redis.Client {
	redis_client_map := make(map[string]*redis.Client)
	if sdcfg.CheckDbMultiNamespace() {
		ns_list := sdcfg.GetDbNonDefaultNamespaces()
		for _, ns := range ns_list {
			redis_client_map[ns] = Target2RedisDb[ns][target]
		}
	} else {
		ns := sdcfg.GetDbDefaultNamespace()
		redis_client_map[ns] = Target2RedisDb[ns][target]
	}
	return redis_client_map
}

// This function get target present in GNMI Request and
// returns: 1. DbName (string) 2. Is DbName valid (bool)
//          3. DbNamespace (string) 4. Is DbNamespace present in Target (bool)
func IsTargetDb(target string) (string, bool, string, bool) {
	targetname := strings.Split(target, "/")
	dbName := targetname[0]
	dbNameSpaceExist := false
	dbNamespace := sdcfg.GetDbDefaultNamespace()

	if len(targetname) > 2 {
		log.V(1).Infof("target format is not correct")
		return dbName, false, dbNamespace, dbNameSpaceExist
	}

	if len(targetname) > 1 {
		dbNamespace = targetname[1]
		dbNameSpaceExist = true
	}
	for name, _ := range spb.Target_value {
		if name == dbName {
			return dbName, true, dbNamespace, dbNameSpaceExist
		}
	}

	return dbName, false, dbNamespace, dbNameSpaceExist
}

// For testing only
func useRedisTcpClient() {
	if !UseRedisLocalTcpPort {
		return
	}
	for _, dbNamespace := range sdcfg.GetDbAllNamespaces() {
		Target2RedisDb[dbNamespace] = make(map[string]*redis.Client)
		for dbName, dbn := range spb.Target_value {
			if dbName != "OTHERS" {
				// DB connector for direct redis operation
				redisDb := redis.NewClient(&redis.Options{
					Network:     "tcp",
					Addr:        sdcfg.GetDbTcpAddr(dbName, dbNamespace),
					Password:    "", // no password set
					DB:          int(dbn),
					DialTimeout: 0,
				})
				Target2RedisDb[dbNamespace][dbName] = redisDb
			}
		}
	}
}

// Client package prepare redis clients to all DBs automatically
func init() {
	for _, dbNamespace := range sdcfg.GetDbAllNamespaces() {
		Target2RedisDb[dbNamespace] = make(map[string]*redis.Client)
		for dbName, dbn := range spb.Target_value {
			if dbName != "OTHERS" {
				// DB connector for direct redis operation
				redisDb := redis.NewClient(&redis.Options{
					Network:     "unix",
					Addr:        sdcfg.GetDbSock(dbName, dbNamespace),
					Password:    "", // no password set
					DB:          int(dbn),
					DialTimeout: 0,
				})
				Target2RedisDb[dbNamespace][dbName] = redisDb
			}
		}
	}
}

// gnmiFullPath builds the full path from the prefix and path.
func gnmiFullPath(prefix, path *gnmipb.Path) *gnmipb.Path {

	fullPath := &gnmipb.Path{Origin: path.Origin}
	if path.GetElement() != nil {
		elements := path.GetElement()
		if prefix != nil {
			elements = append(prefix.GetElement(), elements...)
		}
		// Skip first elem
		fullPath.Element = elements[1:]
	}
	if path.GetElem() != nil {
		elems := path.GetElem()
		if prefix != nil {
			elems = append(prefix.GetElem(), elems...)
		}
		// Skip first elem
		fullPath.Elem = elems[1:]
	}
	return fullPath
}

func populateAllDbtablePath(prefix *gnmipb.Path, target string, paths []*gnmipb.Path, pathG2S *map[*gnmipb.Path][]tablePath) error {
	for _, path := range paths {
		err := populateDbtablePath(prefix, target, path, nil, pathG2S)
		if err != nil {
			return err
		}
	}
	return nil
}

// Populate table path in DB from gnmi path
func populateDbtablePath(prefix *gnmipb.Path, target string, path *gnmipb.Path, value *gnmipb.TypedValue, pathG2S *map[*gnmipb.Path][]tablePath) error {
	var buffer bytes.Buffer
	var dbPath string
	var tblPath tablePath

	targetDbName, targetDbNameValid, targetDbNameSpace, _ := IsTargetDb(target)
	// Verify it is a valid db name
	if !targetDbNameValid {
		return fmt.Errorf("Invalid target dbName %v", targetDbName)
	}

	// Verify Namespace is valid
	dbNamespace, ok := sdcfg.GetDbNamespaceFromTarget(targetDbNameSpace)
	if !ok {
		return fmt.Errorf("Invalid target dbNameSpace %v", targetDbNameSpace)
	}

	fullPath := path
	if prefix != nil {
		fullPath = gnmiFullPath(prefix, path)
	}

	stringSlice := []string{targetDbName}
	separator, _ := GetTableKeySeparator(targetDbName, dbNamespace)
	elems := fullPath.GetElem()
	if elems != nil {
		for i, elem := range elems {
			// TODO: Usage of key field
			log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
			if i != 0 {
				buffer.WriteString(separator)
			}
			buffer.WriteString(elem.GetName())
			stringSlice = append(stringSlice, elem.GetName())
		}
		dbPath = buffer.String()
	}

	tblPath.dbNamespace = dbNamespace
	tblPath.dbName = targetDbName
	tblPath.tableName = stringSlice[1]
	tblPath.delimitor = separator
	tblPath.operation = opRemove
	tblPath.index = -1
	if value != nil {
		tblPath.operation = opAdd
		tblPath.value = string(value.GetJsonIetfVal())
	}

	var mappedKey string
	if len(stringSlice) > 2 { // tmp, to remove mappedKey
		mappedKey = stringSlice[2]
	}

	redisDb, ok := Target2RedisDb[tblPath.dbNamespace][tblPath.dbName]
	if !ok {
		return fmt.Errorf("Redis Client not present for dbName %v dbNamespace %v", targetDbName, dbNamespace)
	}

	// The expect real db path could be in one of the formats:
	// <1> DB Table
	// <2> DB Table Key
	// <3> DB Table Field
	// <4> DB Table Key Field
	// <5> DB Table Key Field Index
	switch len(stringSlice) {
	case 2: // only table name provided
		if tblPath.operation == opRemove {
			res, err := redisDb.Keys(tblPath.tableName + "*").Result()
			if err != nil || len(res) < 1 {
				log.V(2).Infof("Invalid db table Path %v %v", target, dbPath)
				return fmt.Errorf("Failed to find %v %v %v %v", target, dbPath, err, res)
			}
		}
		tblPath.tableKey = ""
	case 3: // Third element must be table key
		if tblPath.operation == opRemove {
			_, err := redisDb.Exists(tblPath.tableName + tblPath.delimitor + mappedKey).Result()
			if err != nil {
				return fmt.Errorf("redis Exists op failed for %v", dbPath)
			}
		}
		tblPath.tableKey = mappedKey
	case 4: // Fourth element must be field name
		if tblPath.operation == opRemove {
			_, err := redisDb.Exists(tblPath.tableName + tblPath.delimitor + mappedKey).Result()
			if err != nil {
				return fmt.Errorf("redis Exists op failed for %v", dbPath)
			}
		}
		tblPath.tableKey = mappedKey
		tblPath.field = stringSlice[3]
	case 5: // Fifth element must be list index
		if tblPath.operation == opRemove {
			_, err := redisDb.Exists(tblPath.tableName + tblPath.delimitor + mappedKey).Result()
			if err != nil {
				return fmt.Errorf("redis Exists op failed for %v", dbPath)
			}
		}
		tblPath.tableKey = mappedKey
		tblPath.field = stringSlice[3]
		index, err := strconv.Atoi(stringSlice[4])
		if err != nil {
			return fmt.Errorf("Invalid index %v", stringSlice[4])
		}
		tblPath.index = index
	default:
		log.V(2).Infof("Invalid db table Path %v", dbPath)
		return fmt.Errorf("Invalid db table Path %v", dbPath)
	}

	(*pathG2S)[path] = []tablePath{tblPath}
	log.V(5).Infof("tablePath %+v", tblPath)
	return nil
}

// makeJSON renders the database Key op value_pairs to map[string]interface{} for JSON marshall.
func makeJSON_redis(msi *map[string]interface{}, key *string, op *string, mfv map[string]string) error {
	// TODO: Use Yang model to identify leaf-list
	if key == nil && op == nil {
		for f, v := range mfv {
			if strings.HasSuffix(f, "@") {
				k := strings.TrimSuffix(f, "@")
				slice := strings.Split(v, ",")
				(*msi)[k] = slice
			} else {
				(*msi)[f] = v
			}
		}
		return nil
	}

	fp := map[string]interface{}{}
	for f, v := range mfv {
		if strings.HasSuffix(f, "@") {
			k := strings.TrimSuffix(f, "@")
			slice := strings.Split(v, ",")
			fp[k] = slice
		} else {
			fp[f] = v
		}		
	}

	if key == nil {
		(*msi)[*op] = fp
	} else if op == nil {
		(*msi)[*key] = fp
	} else {
		// Also have operation layer
		of := map[string]interface{}{}

		of[*op] = fp
		(*msi)[*key] = of
	}
	return nil
}

// emitJSON marshalls map[string]interface{} to JSON byte stream.
func emitJSON(v *map[string]interface{}) ([]byte, error) {
	//j, err := json.MarshalIndent(*v, "", indentString)
	j, err := json.Marshal(*v)
	if err != nil {
		return nil, fmt.Errorf("JSON marshalling error: %v", err)
	}

	return j, nil
}

func parseJson(str []byte) (interface{}, error) {
	var res interface{}
	err := json.Unmarshal(str, &res)
	if err != nil {
		return res, fmt.Errorf("JSON unmarshalling error: %v", err)
	}
	return res, nil
}

// tableData2Msi renders the redis DB data to map[string]interface{}
// which may be marshaled to JSON format
// If only table name provided in the tablePath, find all keys in the table, otherwise
// Use tableName + tableKey as key to get all field value paires
func tableData2Msi(tblPath *tablePath, useKey bool, op *string, msi *map[string]interface{}) error {
	redisDb := Target2RedisDb[tblPath.dbNamespace][tblPath.dbName]

	var pattern string
	var dbkeys []string
	var err error
	var fv map[string]string

	//Only table name provided
	if tblPath.tableKey == "" {
		// tables in COUNTERS_DB other than COUNTERS table doesn't have keys
		if tblPath.dbName == "COUNTERS_DB" && tblPath.tableName != "COUNTERS" {
			pattern = tblPath.tableName
		} else {
			pattern = tblPath.tableName + tblPath.delimitor + "*"
		}
		dbkeys, err = redisDb.Keys(pattern).Result()
		if err != nil {
			log.V(2).Infof("redis Keys failed for %v, pattern %s", tblPath, pattern)
			return fmt.Errorf("redis Keys failed for %v, pattern %s %v", tblPath, pattern, err)
		}
	} else {
		// both table name and key provided
		dbkeys = []string{tblPath.tableName + tblPath.delimitor + tblPath.tableKey}
	}

	// Asked to use jsonField and jsonTableKey in the final json value
	if tblPath.jsonField != "" && tblPath.jsonTableKey != "" {
		val, err := redisDb.HGet(dbkeys[0], tblPath.field).Result()
		if err != nil {
			log.V(3).Infof("redis HGet failed for %v %v", tblPath, err)
			// ignore non-existing field which was derived from virtual path
			return nil
		}
		fv = map[string]string{tblPath.jsonField: val}
		makeJSON_redis(msi, &tblPath.jsonTableKey, op, fv)
		log.V(6).Infof("Added json key %v fv %v ", tblPath.jsonTableKey, fv)
		return nil
	}

	for idx, dbkey := range dbkeys {
		fv, err = redisDb.HGetAll(dbkey).Result()
		if err != nil {
			log.V(2).Infof("redis HGetAll failed for  %v, dbkey %s", tblPath, dbkey)
			return err
		}

		if tblPath.jsonTableKey != "" { // If jsonTableKey was prepared, use it
			err = makeJSON_redis(msi, &tblPath.jsonTableKey, op, fv)
		} else if (tblPath.tableKey != "" && !useKey) || tblPath.tableName == dbkey {
			err = makeJSON_redis(msi, nil, op, fv)
		} else {
			var key string
			// Split dbkey string into two parts and second part is key in table
			keys := strings.SplitN(dbkey, tblPath.delimitor, 2)
			key = keys[1]
			err = makeJSON_redis(msi, &key, op, fv)
		}
		if err != nil {
			log.V(2).Infof("makeJSON err %s for fv %v", err, fv)
			return err
		}
		log.V(6).Infof("Added idex %v fv %v ", idx, fv)
	}
	return nil
}

func msi2TypedValue(msi map[string]interface{}) (*gnmipb.TypedValue, error) {
	jv, err := emitJSON(&msi)
	if err != nil {
		log.V(2).Infof("emitJSON err %s for  %v", err, msi)
		return nil, fmt.Errorf("emitJSON err %s for  %v", err, msi)
	}
	return &gnmipb.TypedValue{
		Value: &gnmipb.TypedValue_JsonIetfVal{
			JsonIetfVal: jv,
		}}, nil
}

func tableData2TypedValue(tblPaths []tablePath, op *string) (*gnmipb.TypedValue, error) {
	var useKey bool
	msi := make(map[string]interface{})
	for _, tblPath := range tblPaths {
		redisDb := Target2RedisDb[tblPath.dbNamespace][tblPath.dbName]

		if tblPath.jsonField == "" { // Not asked to include field in json value, which means not wildcard query
			// table path includes table, key and field
			if tblPath.field != "" {
				if len(tblPaths) != 1 {
					log.V(2).Infof("WARNING: more than one path exists for field granularity query: %v", tblPaths)
				}
				var key string
				if tblPath.tableKey != "" {
					key = tblPath.tableName + tblPath.delimitor + tblPath.tableKey
				} else {
					key = tblPath.tableName
				}

				// TODO: Use Yang model to identify leaf-list
				if tblPath.index >= 0 {
					field := tblPath.field + "@"
					val, err := redisDb.HGet(key, field).Result()
					if err != nil {
						log.V(2).Infof("redis HGet failed for %v", tblPath)
						return nil, err
					}
					slice := strings.Split(val, ",")
					if tblPath.index >= len(slice) {
						return nil, fmt.Errorf("Invalid index %v for %v", tblPath.index, slice)
					}
					return &gnmipb.TypedValue{
						Value: &gnmipb.TypedValue_JsonIetfVal{
							JsonIetfVal: []byte(`"` + slice[tblPath.index] + `"`),
						}}, nil
				} else {
					field := tblPath.field
					val, err := redisDb.HGet(key, field).Result()
					if err == nil {
						return &gnmipb.TypedValue{
							Value: &gnmipb.TypedValue_JsonIetfVal{
								JsonIetfVal: []byte(`"` + val + `"`),
							}}, nil
					}
					field = field + "@"
					val, err = redisDb.HGet(key, field).Result()
					if err == nil {
						var output []byte
						slice := strings.Split(val, ",")
						output, err = json.Marshal(slice)
						if err != nil {
							return nil, err
						}
						return &gnmipb.TypedValue{
							Value: &gnmipb.TypedValue_JsonIetfVal{
								JsonIetfVal: []byte(output),
							}}, nil
					}
					log.V(2).Infof("redis HGet failed for %v", tblPath)
					return nil, err
				}
			}
		}

		err := tableData2Msi(&tblPath, useKey, nil, &msi)
		if err != nil {
			return nil, err
		}
	}
	return msi2TypedValue(msi)
}

func handleTableData(tblPaths []tablePath) error {
	//var useKey bool
	var pattern string
	var dbkeys []string
	var err error
	var fv map[string]string
	var res interface{}
	//msi := make(map[string]interface{})
	for _, tblPath := range tblPaths {
		log.V(5).Infof("handleTableData: tblPath %v", tblPath)
		redisDb := Target2RedisDb[tblPath.dbNamespace][tblPath.dbName]

		if tblPath.jsonField == "" { // Not asked to include field in json value, which means not wildcard query
			// table path includes table, key and field
			if tblPath.field != "" {
				if len(tblPaths) != 1 {
					log.V(2).Infof("WARNING: more than one path exists for field granularity query: %v", tblPaths)
				}
				var key string
				if tblPath.tableKey != "" {
					key = tblPath.tableName + tblPath.delimitor + tblPath.tableKey
				} else {
					key = tblPath.tableName
				}
				if tblPath.operation == opRemove {
					log.V(5).Infof("handleTableData: HDel key %v field %v index %v", key, tblPath.field, tblPath.index)
					var val string
					// TODO: Use Yang model to identify leaf-list
					field := tblPath.field
					if tblPath.index >= 0 {
						field = field + "@"
						val, err = redisDb.HGet(key, field).Result()
						if err != nil {
							log.V(2).Infof("redis HGet failed for %v", tblPath)
							return err
						}
						slice := strings.Split(val, ",")
						if tblPath.index >= len(slice) {
							return fmt.Errorf("Invalid index %v for %v", tblPath.index, slice)
						}
						slice = append(slice[:tblPath.index], slice[tblPath.index+1:]...)
						val = strings.Join(slice, ",")
						err = redisDb.HSet(key, field, val).Err()
					} else {
						err = redisDb.HDel(key, field).Err()
						if err != nil {
							log.V(5).Infof("HDel key %v field %v err %v", key, field, err)
						}
						field = field + "@"
						err = redisDb.HDel(key, field).Err()
						if err != nil {
							log.V(5).Infof("HDel key %v field %v err %v", key, field, err)
						}
					}
				} else if tblPath.operation == opAdd {
					var value string
					res, err = parseJson([]byte(tblPath.value))
					if err != nil {
						return err
					}
					if tblPath.index >= 0 {
						if val, ok := res.(string); ok {
							field := tblPath.field + "@"
							value, err = redisDb.HGet(key, field).Result()
							if err != nil {
								log.V(2).Infof("redis HGet failed for %v", tblPath)
								return err
							}
							slice := strings.Split(value, ",")
							if tblPath.index > len(slice) {
								return fmt.Errorf("Invalid index %v for %v", tblPath.index, slice)
							}
							slice = append(slice[:tblPath.index], append([]string{val}, slice[tblPath.index:]...)...)
							value = strings.Join(slice, ",")
							log.V(5).Infof("handleTableData: HSet key %v field %v value %v", key, field, value)
							err = redisDb.HSet(key, field, value).Err()
						} else {
							return fmt.Errorf("Unsupported value %v type %v", res, reflect.TypeOf(res))
						}
					} else {
						if val, ok := res.(string); ok {
							log.V(5).Infof("handleTableData: HSet key %v field %v value %v", key, tblPath.field, val)
							err = redisDb.HSet(key, tblPath.field, val).Err()
						} else if list, ok := res.([]interface{}); ok {
							field := tblPath.field + "@"
							slice := []string{}
							for _, item := range(list) {
								if str, check := item.(string); check {
									slice = append(slice, str)
								} else {
									return fmt.Errorf("Unsupported value %v type %v", item, reflect.TypeOf(item))
								}
							}
							value = strings.Join(slice, ",")
							log.V(5).Infof("handleTableData: HSet key %v field %v value %v", key, field, value)
							err = redisDb.HSet(key, field, value).Err()
						} else {
							return fmt.Errorf("Unsupported value %v type %v", res, reflect.TypeOf(res))
						}
					}
				} else {
					return fmt.Errorf("Unsupported operation %s", tblPath.operation)
				}

				if err != nil {
					log.V(2).Infof("redis operation failed for %v", tblPath)
					return err
				}

				return nil
			}
		}

		if tblPath.operation == opRemove {
			//Only table name provided
			if tblPath.tableKey == "" {
				// tables in COUNTERS_DB other than COUNTERS table doesn't have keys
				if tblPath.dbName == "COUNTERS_DB" && tblPath.tableName != "COUNTERS" {
					pattern = tblPath.tableName
				} else {
					pattern = tblPath.tableName + tblPath.delimitor + "*"
				}
				dbkeys, err = redisDb.Keys(pattern).Result()
				if err != nil {
					log.V(2).Infof("redis Keys failed for %v, pattern %s", tblPath, pattern)
					return fmt.Errorf("redis Keys failed for %v, pattern %s %v", tblPath, pattern, err)
				}
			} else {
				// both table name and key provided
				dbkeys = []string{tblPath.tableName + tblPath.delimitor + tblPath.tableKey}
			}

			for _, dbkey := range dbkeys {
				fv, err = redisDb.HGetAll(dbkey).Result()
				if err != nil {
					log.V(2).Infof("redis HGetAll failed for  %v, dbkey %s", tblPath, dbkey)
					return err
				}
				for field, _ := range fv {
					log.V(5).Infof("handleTableData: HDel key %v field %v", dbkey, field)
					err = redisDb.HDel(dbkey, field).Err()
					if err != nil {
						log.V(2).Infof("redis operation failed for %v", dbkey)
						return err
					}
				}
			}
		} else if tblPath.operation == opAdd {
			if tblPath.tableKey != "" {
				// both table name and key provided
				dbkey := tblPath.tableName + tblPath.delimitor + tblPath.tableKey
				res, err = parseJson([]byte(tblPath.value))
				if err != nil {
					return err
				}
				if vtable, ok := res.(map[string]interface{}); ok {
					for field, vres := range vtable {
						log.V(2).Infof("field %v, vres %v", field, vres)
						if val, good := vres.(string); good {
							err = redisDb.HSet(dbkey, field, val).Err()
							if err != nil {
								return err
							}
						} else if list, good := vres.([]interface{}); good {
							field = field + "@"
							slice := []string{}
							for _, item := range(list) {
								if str, check := item.(string); check {
									slice = append(slice, str)
								} else {
									return fmt.Errorf("Unsupported value %v type %v", item, reflect.TypeOf(item))
								}
							}							
							value := strings.Join(slice, ",")
							log.V(5).Infof("handleTableData: HSet key %v field %v value %v", dbkey, field, value)
							err = redisDb.HSet(dbkey, field, value).Err()
							if err != nil {
								return err
							}
						} else {
							return fmt.Errorf("Unsupported value %v type %v", vres, reflect.TypeOf(vres))
						}
					}
				} else {
					return fmt.Errorf("Key %v: Unsupported value %v type %v", tblPath.tableKey, res, reflect.TypeOf(res))
				}
			} else {
				res, err = parseJson([]byte(tblPath.value))
				if err != nil {
					return err
				}
				if vtable, ok := res.(map[string]interface{}); ok {
					for tableKey, tres := range vtable {
						if vt, ret := tres.(map[string]interface{}); ret {
							for field, fres := range vt {
								dbkey := tblPath.tableName + tblPath.delimitor + tableKey
								if val, good := fres.(string); good {
									log.V(5).Infof("handleTableData: HSet key %v field %v value %v", dbkey, field, val)
									err = redisDb.HSet(dbkey, field, val).Err()
									if err != nil {
										return err
									}
								} else if list, good := fres.([]interface{}); good {
									field = field + "@"
									slice := []string{}
									for _, item := range(list) {
										if str, check := item.(string); check {
											slice = append(slice, str)
										} else {
											return fmt.Errorf("Unsupported value %v type %v", item, reflect.TypeOf(item))
										}				
									}
									value := strings.Join(slice, ",")
									log.V(5).Infof("handleTableData: HSet key %v field %v value %v", dbkey, field, value)
									err = redisDb.HSet(dbkey, field, value).Err()
									if err != nil {
										return err
									}
								} else {
									return fmt.Errorf("Unsupported value %v type %v", fres, reflect.TypeOf(fres))
								}
							}
						} else {
							return fmt.Errorf("Key %v: Unsupported value %v type %v", tableKey, tres, reflect.TypeOf(tres))
						}
					}
				} else {
					return fmt.Errorf("Unsupported value %v type %v", res, reflect.TypeOf(res))
				}
			}
		} else {
			return fmt.Errorf("Unsupported operation %v", tblPath.operation)
		}

	}
	return nil
}

func enqueueFatalMsg(c *DbClient, msg string) {
	putFatalMsg(c.q, msg)
}

func putFatalMsg(q *queue.PriorityQueue, msg string) {
	q.Put(Value{
		&spb.Value{
			Timestamp: time.Now().UnixNano(),
			Fatal:     msg,
		},
	})
}

/* Populate the JsonPatch corresponding each GNMI operation. */
func ConvertToJsonPatch(prefix *gnmipb.Path, path *gnmipb.Path, t *gnmipb.TypedValue, output *string) error {
	if t != nil {
		if len(t.GetJsonIetfVal()) == 0 {
			return fmt.Errorf("Value encoding is not IETF JSON")
		}
	}
	fullPath := path
	if prefix != nil {
		fullPath = gnmiFullPath(prefix, path)
	}

	elems := fullPath.GetElem()
	if t == nil {
		*output = `{"op": "remove", "path": "/`
	} else {
		*output = `{"op": "add", "path": "/`
	}

	if elems != nil {
		/* Iterate through elements. */
		for _, elem := range elems {
			*output += elem.GetName()
			key := elem.GetKey()
			/* If no keys are present end the element with "/" */
			if key == nil {
				*output += `/`
			}

			/* If keys are present , process the keys. */
			if key != nil {
				for k, v := range key {
					*output += `[` + k + `=` + v + `]`
				}

				/* Append "/" after all keys are processed. */
				*output += `/`
			}
		}
	}

	/* Trim the "/" at the end which is not required. */
	*output = strings.TrimSuffix(*output, `/`)
	if t == nil {
		*output += `"}`
	} else {
		str := string(t.GetJsonIetfVal())
		val := strings.Replace(str, "\n", "", -1)
		*output += `", "value": ` + val + `}`
	}
	return nil
}

func RunPyCode(text string) error {
	defer C.Py_Finalize()
	C.Py_Initialize()
	PyCodeInC := C.CString(text)
	defer C.free(unsafe.Pointer(PyCodeInC))
	CRet := C.PyRun_SimpleString(PyCodeInC)
	if int(CRet) != 0 {
		return fmt.Errorf("Python failure")
	}
	return nil
}

func (c *DbClient) SetIncrementalConfig(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	var err error
	var curr string
	text := `[`
	/* DELETE */
	for _, path := range delete {
		curr = ``
		err = ConvertToJsonPatch(c.prefix, path, nil, &curr)
		if err != nil {
			return err
		}
		text += curr + `,`
	}

	/* REPLACE */
	for _, path := range replace {
		curr = ``
		err = ConvertToJsonPatch(c.prefix, path.GetPath(), path.GetVal(), &curr)
		if err != nil {
			return err
		}
		text += curr + `,`
	}

	/* UPDATE */
	for _, path := range update {
		curr = ``
		err = ConvertToJsonPatch(c.prefix, path.GetPath(), path.GetVal(), &curr)
		if err != nil {
			return err
		}
		text += curr + `,`
	}
	text = strings.TrimSuffix(text, `,`)
	text += `]`
	log.V(2).Infof("JsonPatch: %s", text)
	patchFile := c.workPath + "/gcu.patch"
	err = ioutil.WriteFile(patchFile, []byte(text), 0644)
	if err != nil {
		return err
	}

	var sc ssc.Service
	sc, err = ssc.NewDbusClient(c.testMode)
	if err != nil {
		return err
	}
	err = sc.CreateCheckPoint(c.workPath + "/config")
	if err != nil {
		return err
	}
	defer sc.DeleteCheckPoint(c.workPath + "/config")
	if c.origin == "sonic-db" {
		err = sc.ApplyPatchDb(patchFile)
	} else if c.origin == "sonic-yang" {
		err = sc.ApplyPatchYang(patchFile)
	} else {
		return fmt.Errorf("Invalid schema %s", c.origin)
	}

	if err == nil {
		err = sc.ConfigSave("/etc/sonic/config_db.json")
	}
	return err
}

func RebootSystem(fileName string, testMode bool) error {
	log.V(2).Infof("Rebooting with %s...", fileName)
	sc, err := ssc.NewDbusClient(testMode)
	if err != nil {
		return err
	}
	err = sc.ConfigReload(fileName)
	return err
}

func (c *DbClient) SetFullConfig(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	val := update[0].GetVal()
	ietf_json_val := val.GetJsonIetfVal()
	if len(ietf_json_val) == 0 {
		return fmt.Errorf("Value encoding is not IETF JSON")
	}
	content := []byte(ietf_json_val)
	fileName := c.workPath + "/config_db.json.tmp"
	err := ioutil.WriteFile(fileName, content, 0644)
	if err != nil {
		return err
	}

	if c.testMode == false {
		// TODO: Add Yang validation
		PyCodeInGo :=
`
import sonic_yang
import json

yang_parser = sonic_yang.SonicYang("/usr/local/yang-models")
yang_parser.loadYangModel()
text = '''%s'''

try:
    yang_parser.loadData(configdbJson=json.loads(text))
    yang_parser.validate_data_tree()
except sonic_yang.SonicYangException as e:
    print("Yang validation error: {}".format(str(e)))
    raise
`

		PyCodeInGo = fmt.Sprintf(PyCodeInGo, ietf_json_val)
		err = RunPyCode(PyCodeInGo)
		if err != nil {
			return fmt.Errorf("Yang validation failed!")
		}
	}

	go func() {
		time.Sleep(10 * time.Second)
		RebootSystem(fileName, c.testMode)
	} ()

	return nil
}

func (c *DbClient) SetDB(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	/* DELETE */
	deleteMap := make(map[*gnmipb.Path][]tablePath)
	err := populateAllDbtablePath(c.prefix, c.target, delete, &deleteMap)
	if err != nil {
		return err
	}
	
	for _, tblPaths := range deleteMap {
		err = handleTableData(tblPaths)
		if err != nil {
			return err
		}
	}

	/* REPLACE */
	replaceMap := make(map[*gnmipb.Path][]tablePath)
	for _, item := range replace {
		err = populateDbtablePath(c.prefix, c.target, item.GetPath(), item.GetVal(), &replaceMap)
		if err != nil {
			return err
		}
	}
	for _, tblPaths := range replaceMap {
		err = handleTableData(tblPaths)
		if err != nil {
			return err
		}
	}

	/* UPDATE */
	updateMap := make(map[*gnmipb.Path][]tablePath)
	for _, item := range update {
		err = populateDbtablePath(c.prefix, c.target, item.GetPath(), item.GetVal(), &updateMap)
		if err != nil {
			return err
		}
	}
	for _, tblPaths := range updateMap {
		err = handleTableData(tblPaths)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *DbClient) SetConfigDB(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	deleteLen := len(delete)
	replaceLen := len(replace)
	updateLen := len(update)
	if (deleteLen == 0 && replaceLen == 0 && updateLen == 0) {
		/* Empty request. */
		return nil
	} else if (deleteLen == 1 && replaceLen == 0 && updateLen == 1) {
		deletePath := gnmiFullPath(c.prefix, delete[0])
		updatePath := gnmiFullPath(c.prefix, update[0].GetPath())
		if (len(deletePath.GetElem()) == 0) && (len(updatePath.GetElem()) == 0) {
			return c.SetFullConfig(delete, replace, update)
		}
	}
	return c.SetIncrementalConfig(delete, replace, update)
}

func (c *DbClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	if c.target == "CONFIG_DB" {
		return c.SetConfigDB(delete, replace, update)
	} else if c.target == "APPL_DB" {
		return c.SetDB(delete, replace, update)
	} else if c.target == "DASH_APP_DB" {
		return c.SetDB(delete, replace, update)
	}
	return fmt.Errorf("Set RPC does not support %v", c.target)
}

func (c *DbClient) Capabilities() []gnmipb.ModelData {
	return nil
}

