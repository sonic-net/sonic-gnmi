// Package client provides a generic access layer for data available in system
package client

// #cgo pkg-config: python3-embed
// #include <Python.h>
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
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
	var err error

	// Testing program may ask to use redis local tcp connection
	if UseRedisLocalTcpPort {
		useRedisTcpClient()
	}

	client.prefix = prefix
	client.target = target
	client.origin = origin
	client.testMode = testMode
	client.workPath = "/etc/sonic/gnmi"

	if paths != nil {
		client.pathG2S = make(map[*gnmipb.Path][]tablePath)
		err = populateAllDbtablePath(prefix, target, paths, &client.pathG2S)
	}

	if err != nil {
		return nil, err
	} else {
		return &client, nil
	}
}

// String returns the target the client is querying.
func (c *DbClient) String() string {
	// TODO: print gnmiPaths of this DbClient
	return fmt.Sprintf("DbClient Prefix %v  sendMsg %v, recvMsg %v",
		c.prefix.GetTarget(), c.sendMsg, c.recvMsg)
}

func (c *DbClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w
	defer c.w.Done()
	c.q = q
	c.channel = stop

	if subscribe.GetSubscription() == nil {
		log.V(2).Infof("No incoming subscription, it is considered a dialout connection.")
		// NOTE: per https://github.com/Azure/sonic-telemetry/blob/master/doc/dialout.md#dialout_client_cli-and-dialout_server_cli
		// TELEMETRY_CLIENT subscription doesn't specificy type of the stream.
		// Handling it as a ON_CHANGE stream for backward compatibility.
		for gnmiPath := range c.pathG2S {
			c.w.Add(1)
			c.synced.Add(1)
			go streamOnChangeSubscription(c, gnmiPath)
		}
	} else {
		log.V(2).Infof("Stream subscription request received, mode: %v, subscription count: %v",
			subscribe.GetMode(),
			len(subscribe.GetSubscription()))

		for _, sub := range subscribe.GetSubscription() {
			log.V(2).Infof("Sub mode: %v, path: %v", sub.GetMode(), sub.GetPath())
			subMode := sub.GetMode()

			if subMode == gnmipb.SubscriptionMode_SAMPLE {
				c.w.Add(1)      // wait group to indicate the streaming session is complete.
				c.synced.Add(1) // wait group to indicate whether sync_response is sent.
				go streamSampleSubscription(c, sub, subscribe.GetUpdatesOnly())
			} else if subMode == gnmipb.SubscriptionMode_ON_CHANGE {
				c.w.Add(1)
				c.synced.Add(1)
				go streamOnChangeSubscription(c, sub.GetPath())
			} else {
				enqueueFatalMsg(c, fmt.Sprintf("unsupported subscription mode, %v", subMode))
				return
			}
		}
	}

	// Wait until all data values corresponding to the path(s) specified
	// in the SubscriptionList has been transmitted at least once
	c.synced.Wait()
	// Inject sync message
	c.q.Put(Value{
		&spb.Value{
			Timestamp:    time.Now().UnixNano(),
			SyncResponse: true,
		},
	})
	log.V(2).Infof("%v Synced", c.pathG2S)

	<-c.channel
	log.V(1).Infof("Exiting StreamRun routine for Client %v", c)
}

// streamOnChangeSubscription implements Subscription "ON_CHANGE STREAM" mode
func streamOnChangeSubscription(c *DbClient, gnmiPath *gnmipb.Path) {
	tblPaths := c.pathG2S[gnmiPath]
	log.V(2).Infof("streamOnChangeSubscription gnmiPath: %v", gnmiPath)

	if tblPaths[0].field != "" {
		if len(tblPaths) > 1 {
			go dbFieldMultiSubscribe(c, gnmiPath, true, time.Millisecond*200, false)
		} else {
			go dbFieldSubscribe(c, gnmiPath, true, time.Millisecond*200)
		}
	} else {
		// sample interval and update only parameters are not applicable
		go dbTableKeySubscribe(c, gnmiPath, 0, true)
	}
}

// streamSampleSubscription implements Subscription "SAMPLE STREAM" mode
func streamSampleSubscription(c *DbClient, sub *gnmipb.Subscription, updateOnly bool) {
	samplingInterval, err := validateSampleInterval(sub)
	if err != nil {
		enqueueFatalMsg(c, err.Error())
		c.synced.Done()
		c.w.Done()
		return
	}

	gnmiPath := sub.GetPath()
	tblPaths := c.pathG2S[gnmiPath]
	log.V(2).Infof("streamSampleSubscription gnmiPath: %v", gnmiPath)
	if tblPaths[0].field != "" {
		if len(tblPaths) > 1 {
			dbFieldMultiSubscribe(c, gnmiPath, false, samplingInterval, updateOnly)
		} else {
			dbFieldSubscribe(c, gnmiPath, false, samplingInterval)
		}
	} else {
		dbTableKeySubscribe(c, gnmiPath, samplingInterval, updateOnly)
	}
}

func (c *DbClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w
	defer c.w.Done()
	c.q = q
	c.channel = poll

	for {
		_, more := <-c.channel
		if !more {
			log.V(1).Infof("%v poll channel closed, exiting pollDb routine", c)
			return
		}
		t1 := time.Now()
		for gnmiPath, tblPaths := range c.pathG2S {
			val, err := tableData2TypedValue(tblPaths, nil)
			if err != nil {
				return
			}

			spbv := &spb.Value{
				Prefix:       c.prefix,
				Path:         gnmiPath,
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: false,
				Val:          val,
			}

			c.q.Put(Value{spbv})
			log.V(6).Infof("Added spbv #%v", spbv)
		}

		c.q.Put(Value{
			&spb.Value{
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: true,
			},
		})
		log.V(4).Infof("Sync done, poll time taken: %v ms", int64(time.Since(t1)/time.Millisecond))
	}
}
func (c *DbClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}
func (c *DbClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	// wait sync for Get, not used for now
	c.w = w

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

// dbFieldMultiSubscribe would read a field from multiple tables and put to output queue.
// It handles queries like "COUNTERS/Ethernet*/xyz" where the path translates to a field  in multiple tables.
// For SAMPLE mode, it would send periodically regardless of change.
// However, if `updateOnly` is true, the payload would include only the changed fields.
// For ON_CHANGE mode, it would send only if the value has changed since the last update.
func dbFieldMultiSubscribe(c *DbClient, gnmiPath *gnmipb.Path, onChange bool, interval time.Duration, updateOnly bool) {
	defer c.w.Done()

	tblPaths := c.pathG2S[gnmiPath]

	// Init the path to value map, it saves the previous value
	path2ValueMap := make(map[tablePath]string)

	readVal := func() map[string]interface{} {
		msi := make(map[string]interface{})
		for _, tblPath := range tblPaths {
			var key string
			if tblPath.tableKey != "" {
				key = tblPath.tableName + tblPath.delimitor + tblPath.tableKey
			} else {
				key = tblPath.tableName
			}
			// run redis get directly for field value
			redisDb := Target2RedisDb[tblPath.dbNamespace][tblPath.dbName]
			val, err := redisDb.HGet(key, tblPath.field).Result()
			if err == redis.Nil {
				if tblPath.jsonField != "" {
					// ignore non-existing field which was derived from virtual path
					continue
				}
				log.V(2).Infof("%v doesn't exist with key %v in db", tblPath.field, key)
				val = ""
			} else if err != nil {
				log.V(1).Infof(" redis HGet error on %v with key %v", tblPath.field, key)
				val = ""
			}

			// This value was saved before and it hasn't changed since then
			_, valueMapped := path2ValueMap[tblPath]
			if (onChange || updateOnly) && valueMapped && val == path2ValueMap[tblPath] {
				continue
			}

			path2ValueMap[tblPath] = val
			fv := map[string]string{tblPath.jsonField: val}
			msi[tblPath.jsonTableKey] = fv
			log.V(6).Infof("new value %v for %v", val, tblPath)
		}

		return msi
	}

	sendVal := func(msi map[string]interface{}) error {
		val, err := msi2TypedValue(msi)
		if err != nil {
			enqueueFatalMsg(c, err.Error())
			return err
		}

		spbv := &spb.Value{
			Prefix:    c.prefix,
			Path:      gnmiPath,
			Timestamp: time.Now().UnixNano(),
			Val:       val,
		}

		if err = c.q.Put(Value{spbv}); err != nil {
			log.V(1).Infof("Queue error:  %v", err)
			return err
		}

		return nil
	}

	msi := readVal()
	if err := sendVal(msi); err != nil {
		c.synced.Done()
		return
	}
	c.synced.Done()

	for {
		select {
		case <-c.channel:
			log.V(1).Infof("Stopping dbFieldMultiSubscribe routine for Client %s ", c)
			return
		case <-IntervalTicker(interval):
			msi := readVal()

			if onChange == false || len(msi) != 0 {
				if err := sendVal(msi); err != nil {
					log.Errorf("Queue error:  %v", err)
					return
				}
			}
		}
	}
}

// dbFieldSubscribe would read a field from a single table and put to output queue.
// Handles queries like "COUNTERS/Ethernet0/xyz" where the path translates to a field in a table.
// For SAMPLE mode, it would send periodically regardless of change.
// For ON_CHANGE mode, it would send only if the value has changed since the last update.
func dbFieldSubscribe(c *DbClient, gnmiPath *gnmipb.Path, onChange bool, interval time.Duration) {
	defer c.w.Done()

	tblPaths := c.pathG2S[gnmiPath]
	tblPath := tblPaths[0]
	// run redis get directly for field value
	redisDb := Target2RedisDb[tblPath.dbNamespace][tblPath.dbName]

	var key string
	if tblPath.tableKey != "" {
		key = tblPath.tableName + tblPath.delimitor + tblPath.tableKey
	} else {
		key = tblPath.tableName
	}

	readVal := func() string {
		newVal, err := redisDb.HGet(key, tblPath.field).Result()
		if err == redis.Nil {
			log.V(2).Infof("%v doesn't exist with key %v in db", tblPath.field, key)
			newVal = ""
		} else if err != nil {
			log.V(1).Infof(" redis HGet error on %v with key %v", tblPath.field, key)
			newVal = ""
		}

		return newVal
	}

	sendVal := func(newVal string) error {
		spbv := &spb.Value{
			Prefix:    c.prefix,
			Path:      gnmiPath,
			Timestamp: time.Now().UnixNano(),
			Val: &gnmipb.TypedValue{
				Value: &gnmipb.TypedValue_StringVal{
					StringVal: newVal,
				},
			},
		}

		if err := c.q.Put(Value{spbv}); err != nil {
			log.V(1).Infof("Queue error:  %v", err)
			return err
		}

		return nil
	}

	// Read the initial value and signal sync after sending it
	val := readVal()
	err := sendVal(val)
	if err != nil {
		putFatalMsg(c.q, err.Error())
		c.synced.Done()
		return
	}
	c.synced.Done()

	for {
		select {
		case <-c.channel:
			log.V(1).Infof("Stopping dbFieldSubscribe routine for Client %s ", c)
			return
		case <-IntervalTicker(interval):
			newVal := readVal()

			if onChange == false || newVal != val {
				if err = sendVal(newVal); err != nil {
					log.V(1).Infof("Queue error:  %v", err)
					return
				}
				val = newVal
			}
		}
	}
}

type redisSubData struct {
	tblPath   tablePath
	pubsub    *redis.PubSub
	prefixLen int
}

// TODO: For delete operation, the exact content returned is to be clarified.
func dbSingleTableKeySubscribe(c *DbClient, rsd redisSubData, updateChannel chan map[string]interface{}) {
	tblPath := rsd.tblPath
	pubsub := rsd.pubsub
	prefixLen := rsd.prefixLen
	msi := make(map[string]interface{})

	log.V(2).Infof("Starting dbSingleTableKeySubscribe routine for %+v", tblPath)

	for {
		select {
		default:
			msgi, err := pubsub.ReceiveTimeout(time.Millisecond * 500)
			if err != nil {
				neterr, ok := err.(net.Error)
				if ok {
					if neterr.Timeout() == true {
						continue
					}
				}

				// Do not log errors if stop is signaled
				if _, activeCh := <-c.channel; activeCh {
					log.V(2).Infof("pubsub.ReceiveTimeout err %v", err)
				}

				continue
			}
			newMsi := make(map[string]interface{})
			subscr := msgi.(*redis.Message)

			// TODO: support for "Delete []*Path"
			if subscr.Payload == "del" || subscr.Payload == "hdel" {
				if tblPath.tableKey != "" {
					//msi["DEL"] = ""
				} else {
					fp := map[string]interface{}{}
					//fp["DEL"] = ""
					if len(subscr.Channel) < prefixLen {
						log.V(2).Infof("Invalid psubscribe channel notification %v, shorter than %v", subscr.Channel, prefixLen)
						continue
					}
					key := subscr.Channel[prefixLen:]
					newMsi[key] = fp
				}
			} else if subscr.Payload == "hset" {
				//op := "SET"
				if tblPath.tableKey != "" {
					err = tableData2Msi(&tblPath, false, nil, &newMsi)
					if err != nil {
						enqueueFatalMsg(c, err.Error())
						return
					}
				} else {
					tblPath := tblPath
					if len(subscr.Channel) < prefixLen {
						log.V(2).Infof("Invalid psubscribe channel notification %v, shorter than %v", subscr.Channel, prefixLen)
						continue
					}
					tblPath.tableKey = subscr.Channel[prefixLen:]
					err = tableData2Msi(&tblPath, false, nil, &newMsi)
					if err != nil {
						enqueueFatalMsg(c, err.Error())
						return
					}
				}
				if reflect.DeepEqual(newMsi, msi) {
					// No change from previous data
					continue
				}
				msi = newMsi
			} else {
				log.V(2).Infof("Invalid psubscribe payload notification:  %v", subscr.Payload)
				continue
			}

			if len(newMsi) > 0 {
				updateChannel <- newMsi
			}

		case <-c.channel:
			log.V(2).Infof("Stopping dbSingleTableKeySubscribe routine for %+v", tblPath)
			return
		}
	}
}

// dbTableKeySubscribe subscribes to tables using a table keys.
// Handles queries like "COUNTERS/Ethernet0" or "COUNTERS/Ethernet*"
// This function handles both ON_CHANGE and SAMPLE modes. "interval" being 0 is interpreted as ON_CHANGE mode.
func dbTableKeySubscribe(c *DbClient, gnmiPath *gnmipb.Path, interval time.Duration, updateOnly bool) {
	defer c.w.Done()

	tblPaths := c.pathG2S[gnmiPath]
	msiAll := make(map[string]interface{})
	rsdList := []redisSubData{}
	synced := false

	// Helper to signal sync
	signalSync := func() {
		if !synced {
			c.synced.Done()
			synced = true
		}
	}

	// Helper to handle fatal case.
	handleFatalMsg := func(msg string) {
		log.V(1).Infof(msg)
		enqueueFatalMsg(c, msg)
		signalSync()
	}

	// Helper to send hash data over the stream
	sendMsiData := func(msiData map[string]interface{}) error {
		val, err := msi2TypedValue(msiData)
		if err != nil {
			return err
		}

		var spbv *spb.Value
		spbv = &spb.Value{
			Prefix:    c.prefix,
			Path:      gnmiPath,
			Timestamp: time.Now().UnixNano(),
			Val:       val,
		}
		if err = c.q.Put(Value{spbv}); err != nil {
			return fmt.Errorf("Queue error:  %v", err)
		}

		return nil
	}

	// Go through the paths and identify the tables to register.
	for _, tblPath := range tblPaths {
		// Subscribe to keyspace notification
		pattern := "__keyspace@" + strconv.Itoa(int(spb.Target_value[tblPath.dbName])) + "__:"
		pattern += tblPath.tableName
		if tblPath.dbName == "COUNTERS_DB" && tblPath.tableName != "COUNTERS" {
			// tables in COUNTERS_DB other than COUNTERS don't have keys, skip delimitor
		} else {
			pattern += tblPath.delimitor
		}

		var prefixLen int
		if tblPath.tableKey != "" {
			pattern += tblPath.tableKey
			prefixLen = len(pattern)
		} else {
			prefixLen = len(pattern)
			pattern += "*"
		}
		redisDb := Target2RedisDb[tblPath.dbNamespace][tblPath.dbName]
		pubsub := redisDb.PSubscribe(pattern)
		defer pubsub.Close()

		msgi, err := pubsub.ReceiveTimeout(time.Second)
		if err != nil {
			handleFatalMsg(fmt.Sprintf("psubscribe to %s failed for %v", pattern, tblPath))
			return
		}
		subscr := msgi.(*redis.Subscription)
		if subscr.Channel != pattern {
			handleFatalMsg(fmt.Sprintf("psubscribe to %s failed for %v", pattern, tblPath))
			return
		}
		log.V(2).Infof("Psubscribe succeeded for %v: %v", tblPath, subscr)

		err = tableData2Msi(&tblPath, false, nil, &msiAll)
		if err != nil {
			handleFatalMsg(err.Error())
			return
		}
		rsd := redisSubData{
			tblPath:   tblPath,
			pubsub:    pubsub,
			prefixLen: prefixLen,
		}
		rsdList = append(rsdList, rsd)
	}

	// Send all available data and signal the synced flag.
	if err := sendMsiData(msiAll); err != nil {
		handleFatalMsg(err.Error())
		return
	}
	signalSync()

	// Clear the payload so that next time it will send only updates
	if updateOnly {
		msiAll = make(map[string]interface{})
	}

	// Start routines to listen on the table changes.
	updateChannel := make(chan map[string]interface{})
	for _, rsd := range rsdList {
		go dbSingleTableKeySubscribe(c, rsd, updateChannel)
	}

	// Listen on updates from tables.
	// Depending on the interval, send the updates every interval or on change only.
	intervalTicker := make(<-chan time.Time)
	for {

		// The interval ticker ticks only when the interval is non-zero.
		// Otherwise (e.g. on-change mode) it would never tick.
		if interval > 0 {
			intervalTicker = IntervalTicker(interval)
		}

		select {
		case updatedTable := <-updateChannel:
			log.V(1).Infof("update received: %v", updatedTable)
			if interval == 0 {
				// on-change mode, send the updated data.
				if err := sendMsiData(updatedTable); err != nil {
					handleFatalMsg(err.Error())
					return
				}
			} else {
				// Update the overall table, it will be sent when the interval ticks.
				for k := range updatedTable {
					msiAll[k] = updatedTable[k]
				}
			}
		case <-intervalTicker:
			log.V(1).Infof("ticker received: %v", len(msiAll))

			if err := sendMsiData(msiAll); err != nil {
				handleFatalMsg(err.Error())
				return
			}

			// Clear the payload so that next time it will send only updates
			if updateOnly {
				msiAll = make(map[string]interface{})
				log.V(1).Infof("msiAll cleared: %v", len(msiAll))
			}

		case <-c.channel:
			log.V(1).Infof("Stopping dbTableKeySubscribe routine for %v ", c.pathG2S)
			return
		}
	}
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
	if c.origin == "sonic-db" {
		err = sc.ApplyPatchDb(patchFile)
	} else if c.origin == "sonic-yang" {
		err = sc.ApplyPatchYang(patchFile)
	} else {
		return fmt.Errorf("Invalid schema %s", c.origin)
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

// validateSampleInterval validates the sampling interval of the given subscription.
func validateSampleInterval(sub *gnmipb.Subscription) (time.Duration, error) {
	requestedInterval := time.Duration(sub.GetSampleInterval())
	if requestedInterval == 0 {
		// If the sample_interval is set to 0, the target MUST create the subscription
		// and send the data with the lowest samplingInterval possible for the target
		return MinSampleInterval, nil
	} else if requestedInterval < MinSampleInterval {
		return 0, fmt.Errorf("invalid interval: %v. It cannot be less than %v", requestedInterval, MinSampleInterval)
	} else {
		return requestedInterval, nil
	}
}
