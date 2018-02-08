// Package client provides a generic access layer for data available in system
package client

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

	log "github.com/golang/glog"

	"github.com/go-redis/redis"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/workiva/go-datastructures/queue"
)

const (
	// indentString represents the default indentation string used for
	// JSON. Two spaces are used here.
	indentString                 string = "  "
	default_UNIXSOCKET           string = "/var/run/redis/redis.sock"
	default_REDIS_LOCAL_TCP_PORT string = "localhost:6379"
)

// Client defines a set of methods which every client must implement.
// This package provides one implmentation for now: the DbClient
//
type Client interface {
	// StreamRun will start watching service on data source
	// and enqueue data change to the priority queue.
	// It stops all activities upon receiving signal on stop channel
	// It should run as a go routine
	StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup)
	// Poll will  start service to respond poll signal received on poll channel.
	// data read from data source will be enqueued on to the priority queue
	// The service will stop upon detection of poll channel closing.
	// It should run as a go routine
	PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup)
	// Get return data from the data source in format of *spb.Value
	Get(w *sync.WaitGroup) (*spb.Value, error)
	// Close provides implemenation for explicit cleanup of Client
	Close() error
}

// Let it be variable visible to other packages for now.
// May add an interface function for it.
var UseRedisLocalTcpPort bool = false

// Port name to oid map in COUNTERS table of COUNTERS_DB
var countersPortNameMap = make(map[string]string)

// Port oid to name map in COUNTERS table of COUNTERS_DB
var countersPortOidMap = make(map[string]string)

// redis client connected to each DB
var Target2RedisDb = make(map[string]*redis.Client)

type tablePath struct {
	dbName    string
	tableName string
	tableKey  string
	delimitor string
	field     string
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
	pathG2S map[*gnmipb.Path][]tablePath
	q       *queue.PriorityQueue
	channel chan struct{}

	synced sync.WaitGroup  // Control when to send gNMI sync_response
	w      *sync.WaitGroup // wait for all sub go routines to finish
}

func NewDbClient(paths []*gnmipb.Path, prefix *gnmipb.Path) (*DbClient, error) {
	var client DbClient
	// Testing program may ask to use redis local tcp connection
	if UseRedisLocalTcpPort {
		useRedisTcpClient()
	}
	client.pathG2S = make(map[*gnmipb.Path][]tablePath)
	err := populateAllDbtablePath(paths, prefix, &client.pathG2S)
	if err != nil {
		return nil, err
	} else {
		return &client, nil
	}
}

func (c *DbClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup) {
	c.w = w
	defer c.w.Done()
	c.q = q
	c.channel = stop

	for gnmiPath, tblPaths := range c.pathG2S {
		if tblPaths[0].field != "" {
			c.w.Add(1)
			c.synced.Add(1)
			go dbFieldSubscribe(gnmiPath, c)
			continue
		}
		c.w.Add(1)
		c.synced.Add(1)
		go dbTableKeySubscribe(gnmiPath, c)
		continue
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
	log.V(2).Infof("Client %s synced", c)
	for {
		select {
		default:
			time.Sleep(time.Second)
		case <-c.channel:
			log.V(1).Infof("Stopping subscribeDb routine for Client %s ", c)
			return
		}
	}
	log.V(2).Infof("Exiting subscribeDb for %v", c)
}

func (c *DbClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup) {
	c.w = w
	defer c.w.Done()
	c.q = q
	c.channel = poll

	for {
		_, more := <-c.channel
		if !more {
			log.V(1).Infof("%v polled channel closed, exiting pollDb routine", c)
			return
		}
		t1 := time.Now()
		for gnmiPath, tblPaths := range c.pathG2S {
			val, err := tableData2TypedValue_redis(tblPaths, false, nil)
			if err != nil {
				return
			}

			spbv := &spb.Value{
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
		ms := int64(time.Since(t1) / time.Millisecond)
		log.V(4).Infof("Sync done, poll time taken: %v ms", ms)
	}
	log.V(2).Infof("Exiting pollDB for %v", c)
}

func (c *DbClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	// wait sync for Get not used for now
	c.w = w

	var values []*spb.Value
	ts := time.Now()
	for gnmiPath, tblPaths := range c.pathG2S {
		val, err := tableData2TypedValue_redis(tblPaths, false, nil)
		if err != nil {
			return nil, err
		}

		values = append(values, &spb.Value{
			Path:      gnmiPath,
			Timestamp: ts.UnixNano(),
			Val:       val,
		})
	}
	log.V(6).Infof("Getting #%v", values)
	ms := int64(time.Since(ts) / time.Millisecond)
	log.V(4).Infof("Get done, total time taken: %v ms", ms)
	return values, nil
}

func GetTableKeySeparator(target string) (string, error) {
	_, ok := spb.Target_value[target]
	if !ok {
		log.V(1).Infof(" %v not a valid path target", target)
		return "", fmt.Errorf("%v not a valid path target", target)
	}

	var separator string
	switch target {
	case "CONFIG_DB":
		separator = "|"
	case "STATE_DB":
		separator = "|"
	default:
		separator = ":"
	}
	return separator, nil
}

// For testing only
func useRedisTcpClient() {
	for dbName, dbn := range spb.Target_value {
		if dbName != "OTHERS" {
			// DB connector for direct redis operation
			var redisDb *redis.Client
			if UseRedisLocalTcpPort {
				redisDb = redis.NewClient(&redis.Options{
					Network:     "tcp",
					Addr:        default_REDIS_LOCAL_TCP_PORT,
					Password:    "", // no password set
					DB:          int(dbn),
					DialTimeout: 0,
				})
			}
			Target2RedisDb[dbName] = redisDb
		}
	}
}

// Client package sets up connection to all DBs automatically
func init() {
	for dbName, dbn := range spb.Target_value {
		if dbName != "OTHERS" {
			// DB connector for direct redis operation
			var redisDb *redis.Client

			redisDb = redis.NewClient(&redis.Options{
				Network:     "unix",
				Addr:        default_UNIXSOCKET,
				Password:    "", // no password set
				DB:          int(dbn),
				DialTimeout: 0,
			})
			Target2RedisDb[dbName] = redisDb
		}
	}
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

// Do special table key remapping for some db entries.
// Ex port name to oid in "COUNTERS_PORT_NAME_MAP" table of COUNTERS_DB
func remapTableKey(dbName, tableName, keyName string) (string, error) {
	if dbName != "COUNTERS_DB" {
		return keyName, nil
	}
	if tableName != "COUNTERS" {
		return keyName, nil
	}

	var err error
	if len(countersPortNameMap) == 0 {
		countersPortNameMap, err = getCountersMap("COUNTERS_PORT_NAME_MAP")
		if err != nil {
			return "", err
		}
	}
	if mappedKey, ok := countersPortNameMap[keyName]; ok {
		log.V(5).Infof("tableKey %s to be remapped to %s for %s %s ", keyName, mappedKey, dbName, tableName)
		return mappedKey, nil
	}
	return keyName, nil
}

// gnmiFullPath builds the full path from the prefix and path.
func gnmiFullPath(prefix, path *gnmipb.Path) *gnmipb.Path {

	fullPath := &gnmipb.Path{Origin: path.Origin}
	if path.GetElement() != nil {
		fullPath.Element = append(prefix.GetElement(), path.GetElement()...)
	}
	if path.GetElem() != nil {
		fullPath.Elem = append(prefix.GetElem(), path.GetElem()...)
	}
	return fullPath
}

func populateAllDbtablePath(paths []*gnmipb.Path, prefix *gnmipb.Path, pathG2S *map[*gnmipb.Path][]tablePath) error {
	for _, path := range paths {
		err := populateDbtablePath(path, prefix, pathG2S)
		if err != nil {
			return err
		}
	}
	return nil
}

// Populate table path in DB from gnmi path
func populateDbtablePath(path, prefix *gnmipb.Path, pathG2S *map[*gnmipb.Path][]tablePath) error {
	var buffer bytes.Buffer
	var dbPath string
	var tblPath tablePath
	var mappedKey string

	target := prefix.GetTarget()
	// Verify it is a valid db name
	redisDb, ok := Target2RedisDb[target]
	if !ok {
		return fmt.Errorf("Invalid target name %v", target)
	}

	fullPath := path
	if prefix != nil {
		fullPath = gnmiFullPath(prefix, path)
	}

	separator, _ := GetTableKeySeparator(target)
	elems := fullPath.GetElem()
	if elems != nil {
		for i, elem := range elems {
			// TODO: Usage of key field
			log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
			if i != 0 {
				buffer.WriteString(separator)
			}
			buffer.WriteString(elem.GetName())
		}
		dbPath = buffer.String()
	}

	tblPath.dbName = target
	stringSlice := strings.Split(dbPath, separator)
	tblPath.tableName = stringSlice[0]
	tblPath.delimitor = separator

	if len(stringSlice) > 1 {
		// Second slice is table key or first part of the table key
		// Do name map if needed.
		var err error
		mappedKey, err = remapTableKey(tblPath.dbName, tblPath.tableName, stringSlice[1])
		if err != nil {
			return err
		}
	}
	switch len(stringSlice) {
	case 1: // only table name provided
		res, err := redisDb.Keys(tblPath.tableName + "*").Result()
		if err != nil || len(res) < 1 {
			log.V(2).Infof("Invalid db table Path %v %v", target, dbPath)
			return fmt.Errorf("Failed to find %v %v %v %v", target, dbPath, err, res)
		}
		tblPath.tableKey = ""
	case 2: // Second element could be table key; or field name in which case table name itself is the key too
		n, err := redisDb.Exists(tblPath.tableName + tblPath.delimitor + mappedKey).Result()
		if err != nil {
			return fmt.Errorf("redis Exists op failed for %v", dbPath)
		}
		if n == 1 {
			tblPath.tableKey = mappedKey
		} else {
			tblPath.field = mappedKey
		}
	case 3: // Third element could part of the table key or field name
		tblPath.tableKey = mappedKey + tblPath.delimitor + stringSlice[2]
		// verify whether this key exists
		key := tblPath.tableName + tblPath.delimitor + tblPath.tableKey
		n, err := redisDb.Exists(key).Result()
		if err != nil {
			return fmt.Errorf("redis Exists op failed for %v", dbPath)
		}
		// Looks like the third slice is not part of the key
		if n != 1 {
			tblPath.tableKey = mappedKey
			tblPath.field = stringSlice[2]
		}
	case 4: // both second and third element are part of table key, fourth element must be field name
		tblPath.tableKey = mappedKey + tblPath.delimitor + stringSlice[2]
		tblPath.field = stringSlice[3]
	default:
		log.V(2).Infof("Invalid db table Path %v", dbPath)
		return fmt.Errorf("Invalid db table Path %v", dbPath)
	}

	var key string
	if tblPath.tableKey != "" {
		key = tblPath.tableName + tblPath.delimitor + tblPath.tableKey
	} else if tblPath.field != "" {
		key = tblPath.tableName
	}
	if key != "" {
		n, _ := redisDb.Exists(key).Result()
		if n != 1 {
			log.V(2).Infof("No valid entry found on %v", dbPath)
			return fmt.Errorf("No valid entry found on %v", dbPath)
		}
	}

	(*pathG2S)[path] = []tablePath{tblPath}
	log.V(5).Infof("tablePath %+v", tblPath)
	return nil
}

// makeJSON renders the database Key op value_pairs to map[string]interface{} for JSON marshall.
func makeJSON_redis(msi *map[string]interface{}, key *string, op *string, mfv map[string]string) error {
	if key == nil && op == nil {
		for f, v := range mfv {
			(*msi)[f] = v
		}
		return nil
	}

	fp := map[string]interface{}{}
	for f, v := range mfv {
		fp[f] = v
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

// tableData2Msi renders the redis DB data to map[string]interface{}
// which may be marshaled to JSON format
func tableData2Msi(tblPath *tablePath, useKey bool, op *string, msi *map[string]interface{}) error {
	redisDb := Target2RedisDb[tblPath.dbName]

	var pattern string
	var dbkeys []string
	var err error

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
			return err
		}
	} else {
		// both table name and key provided
		dbkeys = []string{tblPath.tableName + tblPath.delimitor + tblPath.tableKey}
	}

	var fv map[string]string

	for idx, dbkey := range dbkeys {
		fv, err = redisDb.HGetAll(dbkey).Result()
		if err != nil {
			log.V(2).Infof("redis HGetAll failed for  %v, dbkey %s", tblPath, dbkey)
			return err
		}

		if (tblPath.tableKey != "" && !useKey) || tblPath.tableName == dbkey {
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
		log.V(5).Infof("Added idex %v fv %v ", idx, fv)
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

func tableData2TypedValue_redis(tblPaths []tablePath, useKey bool, op *string) (*gnmipb.TypedValue, error) {

	msi := make(map[string]interface{})
	for _, tblPath := range tblPaths {
		redisDb := Target2RedisDb[tblPath.dbName]
		// table path includes table, key and field
		if tblPath.field != "" {
			var key string
			if tblPath.tableKey != "" {
				key = tblPath.tableName + tblPath.delimitor + tblPath.tableKey
			} else {
				key = tblPath.tableName
			}

			val, err := redisDb.HGet(key, tblPath.field).Result()
			if err != nil {
				log.V(2).Infof("redis HGet failed for %v", tblPath)
				return nil, err
			}
			// TODO: support multiple table paths
			return &gnmipb.TypedValue{
				Value: &gnmipb.TypedValue_StringVal{
					StringVal: val,
				}}, nil
		}

		err := tableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			return nil, err
		}
	}
	return msi2TypedValue(msi)
}

func enqueFatalMsg(c *DbClient, msg string) {
	c.q.Put(Value{
		&spb.Value{
			Timestamp: time.Now().UnixNano(),
			Fatal:     msg,
		},
	})
}

// for subscribe request with granularity of table field, the value is fetched periodically.
// Upon value change, it will be put to queue for furhter notification
func dbFieldSubscribe(gnmiPath *gnmipb.Path, c *DbClient) {
	defer c.w.Done()

	tblPaths := c.pathG2S[gnmiPath]

	//TODO: support multiple tble paths
	tblPath := tblPaths[0]
	// run redis get directly for field value
	redisDb := Target2RedisDb[tblPath.dbName]

	var key string
	if tblPath.tableKey != "" {
		key = tblPath.tableName + tblPath.delimitor + tblPath.tableKey
	} else {
		key = tblPath.tableName
	}

	var val string
	for {
		select {
		case <-c.channel:
			log.V(1).Infof("Stopping dbFieldSubscribe routine for Client %s ", c)
			return
		default:
			newVal, err := redisDb.HGet(key, tblPath.field).Result()
			if err == redis.Nil {
				log.V(2).Infof("%v doesn't exist with key %v in db", tblPath.field, key)
				c.q.Put(Value{
					&spb.Value{
						Timestamp: time.Now().UnixNano(),
						Fatal:     fmt.Sprintf("%v doesn't exist with key %v in db", tblPath.field, key),
					},
				})
				return
			}
			if err != nil {
				log.V(1).Infof(" redis HGet error on %v with key %v", tblPath.field, key)
				enqueFatalMsg(c, fmt.Sprintf(" redis HGet error on %v with key %v", tblPath.field, key))
				return
			}
			if newVal != val {
				spbv := &spb.Value{
					Path:      gnmiPath,
					Timestamp: time.Now().UnixNano(),
					Val: &gnmipb.TypedValue{
						Value: &gnmipb.TypedValue_StringVal{
							StringVal: newVal,
						},
					},
				}

				if err = c.q.Put(Value{spbv}); err != nil {
					log.V(1).Infof("Queue error:  %v", err)
					return
				}
				// If old val is empty, assumming this is initial sync
				if val == "" {
					c.synced.Done()
				}
				val = newVal
			}
			// check again after 500 millisends, to use configured variable
			time.Sleep(time.Millisecond * 500)
		}
	}
}

func dbTableKeySubscribe(gnmiPath *gnmipb.Path, c *DbClient) {
	defer c.w.Done()

	tblPaths := c.pathG2S[gnmiPath]
	//TODO: support multiple tble paths
	tblPath := tblPaths[0]

	redisDb := Target2RedisDb[tblPath.dbName]

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

	pubsub := redisDb.PSubscribe(pattern)
	defer pubsub.Close()

	msgi, err := pubsub.ReceiveTimeout(time.Second)
	if err != nil {
		log.V(1).Infof("psubscribe to %s failed for %v", pattern, tblPath)
		enqueFatalMsg(c, fmt.Sprintf("psubscribe to %s failed for %v", pattern, tblPath))
		return
	}
	subscr := msgi.(*redis.Subscription)
	if subscr.Channel != pattern {
		log.V(1).Infof("psubscribe to %s failed for %v", pattern, tblPath)
		enqueFatalMsg(c, fmt.Sprintf("psubscribe to %s failed for %v", pattern, tblPath))
		return
	}
	log.V(2).Infof("Psubscribe succeeded for %v: %v", tblPath, subscr)
	msi := make(map[string]interface{})
	err = tableData2Msi(&tblPath, false, nil, &msi)
	if err != nil {
		enqueFatalMsg(c, err.Error())
		return
	}
	val, err := msi2TypedValue(msi)
	if err != nil {
		enqueFatalMsg(c, err.Error())
		return
	}
	var spbv *spb.Value
	spbv = &spb.Value{
		Path:      gnmiPath,
		Timestamp: time.Now().UnixNano(),
		Val:       val,
	}
	if err = c.q.Put(Value{spbv}); err != nil {
		log.V(1).Infof("Queue error:  %v", err)
		return
	}
	// First sync for this key is done
	c.synced.Done()

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
				log.V(2).Infof("pubsub.ReceiveTimeout err %v", err)
				continue
			}

			subscr := msgi.(*redis.Message)
			if subscr.Payload == "del" || subscr.Payload == "hdel" {
				msi := map[string]interface{}{}

				if tblPath.tableKey != "" {
					//msi["DEL"] = ""
				} else {
					fp := map[string]interface{}{}
					//fp["DEL"] = ""
					if len(subscr.Channel) < prefixLen {
						log.V(2).Infof("Invalid psubscribe channel notification %v for pattern %v", subscr.Channel, pattern)
						continue
					}
					key := subscr.Channel[prefixLen:]
					msi[key] = fp
				}

				jv, _ := emitJSON(&msi)
				spbv = &spb.Value{
					Path:      gnmiPath,
					Timestamp: time.Now().UnixNano(),
					Val: &gnmipb.TypedValue{
						Value: &gnmipb.TypedValue_JsonIetfVal{
							JsonIetfVal: jv,
						},
					},
				}
			} else if subscr.Payload == "hset" {
				var val *gnmipb.TypedValue
				newMsi := make(map[string]interface{})
				//op := "SET"
				if tblPath.tableKey != "" {
					err = tableData2Msi(&tblPath, false, nil, &newMsi)
					if err != nil {
						enqueFatalMsg(c, err.Error())
						return
					}
				} else {
					tblPath := tblPath
					if len(subscr.Channel) < prefixLen {
						log.V(2).Infof("Invalid psubscribe channel notification %v for pattern %v", subscr.Channel, pattern)
						continue
					}
					tblPath.tableKey = subscr.Channel[prefixLen:]
					err = tableData2Msi(&tblPath, false, nil, &newMsi)
					if err != nil {
						enqueFatalMsg(c, err.Error())
						return
					}
				}
				if reflect.DeepEqual(newMsi, msi) {
					// No change from previous data
					continue
				}
				msi = newMsi
				val, err := msi2TypedValue(msi)
				if err != nil {
					enqueFatalMsg(c, err.Error())
					return
				}
				spbv = &spb.Value{
					Path:      gnmiPath,
					Timestamp: time.Now().UnixNano(),
					Val:       val,
				}
			} else {
				log.V(2).Infof("Invalid psubscribe payload notification:  %v", subscr.Payload)
				continue
			}
			log.V(5).Infof("dbTableKeySubscribe enque: %v", spbv)
			if err = c.q.Put(Value{spbv}); err != nil {
				log.V(1).Infof("Queue error:  %v", err)
				return
			}
		case <-c.channel:
			log.V(1).Infof("Stopping subscribeDb routine for Client %s ", c)
			return
		}
	}
}
