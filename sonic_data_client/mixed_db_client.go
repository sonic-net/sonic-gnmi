package client

// #cgo pkg-config: python3-embed
// #include <Python.h>
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	log "github.com/golang/glog"
	"github.com/Workiva/go-datastructures/queue"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/go-redis/redis"
)

const LOCAL_ADDRESS string = "127.0.0.1"
const REDIS_SOCK string = "/var/run/redis/redis.sock"
const APPL_DB int = 0
// New database for DASH
const DPU_APPL_DB_NAME string = "DPU_APPL_DB"
const APPL_DB_NAME string = "APPL_DB"
const DASH_TABLE_PREFIX string = "DASH_"
const SWSS_TIMEOUT uint = 0
const MAX_RETRY_COUNT uint = 5
const RETRY_DELAY_MILLISECOND uint = 100
const RETRY_DELAY_FACTOR uint = 2
const CHECK_POINT_PATH string = "/etc/sonic"
const ELEM_INDEX_DATABASE = 0
const ELEM_INDEX_INSTANCE = 1
const UPDATE_OPERATION = "add"
const DELETE_OPERATION = "remove"
const REPLACE_OPERATION = "replace"

const (
    opAdd = iota
    opRemove
)

var (
	supportedModels = []gnmipb.ModelData{
		{
			Name:         "sonic-db",
			Organization: "SONiC",
			Version:      "0.1.0",
		},
	}
)

type MixedDbClient struct {
	prefix  *gnmipb.Path
	paths   []*gnmipb.Path
	pathG2S map[*gnmipb.Path][]tablePath
	encoding gnmipb.Encoding
	q       *queue.PriorityQueue
	channel chan struct{}
	target  string
	origin  string
	workPath string
	jClient *JsonClient
	applDB swsscommon.DBConnector
	zmqAddress string
	zmqClient swsscommon.ZmqClient
	tableMap map[string]swsscommon.ProducerStateTable
	zmqTableMap map[string]swsscommon.ZmqProducerStateTable
	// swsscommon introduced dbkey to support multiple database
	dbkey swsscommon.SonicDBKey
	// Convert dbkey to string, namespace:container
	mapkey string
	namespace_cnt int
	container_cnt int

	synced sync.WaitGroup  // Control when to send gNMI sync_response
	w      *sync.WaitGroup // wait for all sub go routines to finish
	mu     sync.RWMutex    // Mutex for data protection among routines for DbClient
}

// redis client connected to each DB
var RedisDbMap map[string]*redis.Client = nil
// Db num from database configuration
var DbInstNum = 0

func Hget(configDbConnector *swsscommon.ConfigDBConnector, table string, key string, field string) (string, error) {
    var fieldValuePairs = configDbConnector.Get_entry(table, key)
    defer swsscommon.DeleteFieldValueMap(fieldValuePairs)

    if fieldValuePairs.Has_key(field) {
        return fieldValuePairs.Get(field), nil
    }

    return "", fmt.Errorf("Can't read %s:%s from %s table", key, field, table)
}

func getDpuAddress(dpuId string) (string, error) {
	// Find DPU address by DPU ID from CONFIG_DB
	// Design doc: https://github.com/sonic-net/SONiC/blob/master/doc/smart-switch/ip-address-assigment/smart-switch-ip-address-assignment.md?plain=1

	var configDbConnector = swsscommon.NewConfigDBConnector()
	defer swsscommon.DeleteConfigDBConnector_Native(configDbConnector.ConfigDBConnector_Native)
	configDbConnector.Connect(false)

	// get bridge plane
	bridgePlane, err := Hget(configDbConnector, "MID_PLANE_BRIDGE", "GLOBAL", "bridge");
	if err != nil {
		return "", err
	}

	// get DPU interface by DPU ID
	dpuInterface, err := Hget(configDbConnector, "DPUS", dpuId, "midplane_interface");
	if err != nil {
		return "", err
	}

	// get DPR address by DPU ID and brdige plane
	var dhcpPortKey = bridgePlane + "|" + dpuInterface
	dpuAddresses, err := Hget(configDbConnector, "DHCP_SERVER_IPV4_PORT", dhcpPortKey, "ips@");
	if err != nil {
		return "", err
	}

	var dpuAddressArray = strings.Split(dpuAddresses, ",")
	if len(dpuAddressArray) == 0 {
		return "", fmt.Errorf("Can't find address of dpu:'%s' from DHCP_SERVER_IPV4_PORT table", dpuId)
	}

	return dpuAddressArray[0], nil
}

func getZmqAddress(container string, zmqPort string) (string, error) {
	// when zmqPort empty, ZMQ feature disabled
	if zmqPort == "" {
		return "", fmt.Errorf("ZMQ port is empty.")
	}

	var dpuAddress, err = getDpuAddress(container)
	if err != nil {
		return "", fmt.Errorf("Get DPU address failed: %v", err)
	}

	// ZMQ address example: "tcp://127.0.0.1:1234"
	return "tcp://" + dpuAddress + ":" + zmqPort, nil
}

var zmqClientMap = map[string]swsscommon.ZmqClient{}

func getZmqClientByAddress(zmqAddress string, vrf string) (swsscommon.ZmqClient, error) {
	client, ok := zmqClientMap[zmqAddress]
	if !ok {
		client = swsscommon.NewZmqClient(zmqAddress, vrf)
		zmqClientMap[zmqAddress] = client
	}

	return client, nil
}

func removeZmqClient(zmqClient swsscommon.ZmqClient) (error) {
	for address, client := range zmqClientMap {
		if client == zmqClient { 
			delete(zmqClientMap, address)
			swsscommon.DeleteZmqClient(client)
			return nil
		}
	}

	return fmt.Errorf("Can't find ZMQ client in zmqClientMap: %v", zmqClient)
}

func getZmqClient(dpuId string, zmqPort string, vrf string) (swsscommon.ZmqClient, error) {
	if zmqPort == "" {
		// ZMQ feature disabled when zmqPort flag not set
		return nil, nil
	}

	if dpuId == sdcfg.SONIC_DEFAULT_CONTAINER {
		// When DPU ID is default, create ZMQ with local address
		return getZmqClientByAddress("tcp://" + LOCAL_ADDRESS + ":" + zmqPort, vrf)
	}

	zmqAddress, err := getZmqAddress(dpuId, zmqPort)
	if err != nil {
		return nil, fmt.Errorf("Get ZMQ address failed: %v", err)
	}

	return getZmqClientByAddress(zmqAddress, vrf)
}

// This function get target present in GNMI Request and
// returns: Is Db valid (bool)
func IsTargetDbByDBKey(dbName string, dbkey swsscommon.SonicDBKey) bool {
	// Check namespace and container
	ns := dbkey.GetNetns()
	container := dbkey.GetContainerName()
	localkey, ok := sdcfg.GetDbInstanceFromTarget(ns, container)
	if !ok {
		return false
	}
	swsscommon.DeleteSonicDBKey(localkey)
	// Get target list for database configuration
	// If target is in database configuration, it's valid
	dbList, err := sdcfg.GetDbListByDBKey(dbkey)
	if err != nil {
		return false
	}
	for _, name := range dbList {
		if name == dbName {
			return true
		}
	}
	return false
}

func GetTableKeySeparatorByDBKey(target string, dbkey swsscommon.SonicDBKey) (string, error) {
	separator, err := sdcfg.GetDbSeparatorByDBKey(target, dbkey)
	return separator, err
}

func parseJson(str []byte) (interface{}, error) {
	var res interface{}
	err := json.Unmarshal(str, &res)
	if err != nil {
		return res, fmt.Errorf("JSON unmarshalling error: %v", err)
	}
	return res, nil
}

// String returns the target the client is querying.
func (c *MixedDbClient) String() string {
	// TODO: print gnmiPaths of this DbClient
	return fmt.Sprintf("MixedDbClient Prefix %v", c.prefix.GetTarget())
}

func (c *MixedDbClient) GetTable(table string) (swsscommon.ProducerStateTable) {
	pt, ok := c.tableMap[table]
	if ok {
		return pt
	}

	pt, ok = c.zmqTableMap[table]
	if ok {
		return pt
	}

	if strings.HasPrefix(table, DASH_TABLE_PREFIX) && c.zmqClient != nil {
		log.V(2).Infof("Create ZmqProducerStateTable:  %s", table)
		zmqTable := swsscommon.NewZmqProducerStateTable(c.applDB, table, c.zmqClient)
		c.zmqTableMap[table] = zmqTable
		pt = zmqTable
	} else {
		log.V(2).Infof("Create ProducerStateTable:  %s", table)
		pt = swsscommon.NewProducerStateTable(c.applDB, table)
		c.tableMap[table] = pt
	}

	return pt
}

func CatchException(err *error) {
    if r := recover(); r != nil {
        *err = fmt.Errorf("%v", r)
    }
}

func ProducerStateTableSetWrapper(pt swsscommon.ProducerStateTable, key string, value swsscommon.FieldValuePairs) (err error) {
	// convert panic to error
	defer CatchException(&err)
	pt.Set(key, value, "SET", "")
	return
}

func ProducerStateTableDeleteWrapper(pt swsscommon.ProducerStateTable, key string) (err error) {
	// convert panic to error
	defer CatchException(&err)
	pt.Delete(key, "DEL", "")
	return
}

type ActionNeedRetry func() error

func RetryHelper(zmqClient swsscommon.ZmqClient, action ActionNeedRetry) error {
	var retry uint = 0
	var retry_delay = time.Duration(RETRY_DELAY_MILLISECOND) * time.Millisecond
	ConnectionResetErr := "zmq connection break"
	for {
		err := action()
		if err != nil {
			if strings.Contains(err.Error(), ConnectionResetErr) {
				if (retry <= MAX_RETRY_COUNT) {
					log.V(6).Infof("RetryHelper: connection reset, reconnect and retry later")
					time.Sleep(retry_delay)
	
					zmqClient.Connect()
					retry_delay *= time.Duration(RETRY_DELAY_FACTOR)
					retry++
					continue
				}

				// Force re-create ZMQ client when connection reset
				removeZmqErr := removeZmqClient(zmqClient)
				if removeZmqErr != nil {
					log.V(6).Infof("RetryHelper: remove ZMQ client error: %v", removeZmqErr)
				}
			}
		}

		return err
	}
}

func (c *MixedDbClient) DbSetTable(table string, key string, values map[string]string) error {
	vec := swsscommon.NewFieldValuePairs()
	defer swsscommon.DeleteFieldValuePairs(vec)
	for k, v := range values {
		pair := swsscommon.NewFieldValuePair(k, v)
		vec.Add(pair)
		swsscommon.DeleteFieldValuePair(pair)
	}

	pt := c.GetTable(table)
	return RetryHelper(
				c.zmqClient,
				func () error {
					return ProducerStateTableSetWrapper(pt, key, vec)
				})
}

func (c *MixedDbClient) DbDelTable(table string, key string) error {
	pt := c.GetTable(table)
	return RetryHelper(
				c.zmqClient,
				func () error {
					return ProducerStateTableDeleteWrapper(pt, key) 
				})
}

// For example, the GNMI path below points to DASH_QOS table in the DPU_APPL_DB database for dpu0:
// /DPU_APPL_DB/dpu0/DASH_QOS
// The first element of the GNMI path is the target database name, which is DPU_APPL_DB in this case.
// The second element is the container name, which is dpu0.
// The third element is the node name, which is DASH_QOS.
// ParseDatabase is used get database target and SonicDBKey from GNMI prefix and path
func (c *MixedDbClient) ParseDatabase(prefix *gnmipb.Path, paths []*gnmipb.Path) (string, swsscommon.SonicDBKey, error) {
	if len(paths) == 0 {
		return "", nil, status.Error(codes.Unimplemented, "No valid path")
	}
	target := ""
	namespace := sdcfg.SONIC_DEFAULT_NAMESPACE
	container := sdcfg.SONIC_DEFAULT_CONTAINER
	prev_instance := ""
	for _, path := range paths {
		if path.GetElem() != nil {
			elems := path.GetElem()
			if prefix != nil {
				elems = append(prefix.GetElem(), elems...)
			}
			if elems == nil {
				return "", nil, status.Error(codes.Unimplemented, "No valid elem")
			}
			if len(elems) < 2 {
				return "", nil, status.Error(codes.Unimplemented, "Invalid elem length")
			}
			// Get target from the first element
			if target == "" {
				target = elems[ELEM_INDEX_DATABASE].GetName()
			} else if target != elems[ELEM_INDEX_DATABASE].GetName() {
				return "", nil, status.Error(codes.Unimplemented, "Target conflict in path")
			}
			elem_name := elems[ELEM_INDEX_INSTANCE].GetName()
			if prev_instance == "" {
				prev_instance = elem_name
			} else if prev_instance != elem_name {
				return "", nil, status.Error(codes.Unimplemented, "Namespace/container conflict in path")
			}
			if c.namespace_cnt > 1 && c.container_cnt > 1 {
				// Support smartswitch with multiple asic NPU
				// The elelement can be "localhost", "asic0", "asic1", ..., "dpu0", "dpu1", ...
				if elem_name != "localhost" {
					// Try namespace
					dbkey1, ok := sdcfg.GetDbInstanceFromTarget(elem_name, sdcfg.SONIC_DEFAULT_CONTAINER)
					if ok {
						namespace = elem_name
						swsscommon.DeleteSonicDBKey(dbkey1)
					} else {
						// Try container
						dbkey2, ok := sdcfg.GetDbInstanceFromTarget(sdcfg.SONIC_DEFAULT_NAMESPACE, elem_name)
						if ok {
							container = elem_name
							swsscommon.DeleteSonicDBKey(dbkey2)
						} else {
							return "", nil, fmt.Errorf("Unsupported namespace/container %s", elem_name)
						}
					}
				}
			} else if c.namespace_cnt > 1 {
				// Get namespace from the second element
				namespace = elem_name
			} else if c.container_cnt > 1 {
				// Get container from the second element
				container = elem_name
			}
		}
	}
	if target == "" {
		return "", nil, status.Error(codes.Unimplemented, "No target specified in path")
	}
	// GNMI path uses localhost as default namespace
	if namespace == "localhost" {
		namespace = sdcfg.SONIC_DEFAULT_NAMESPACE
	}
	// GNMI path uses localhost as default container
	if container == "localhost" {
		container = sdcfg.SONIC_DEFAULT_NAMESPACE
	}
	dbkey := swsscommon.NewSonicDBKey()
	dbkey.SetNetns(namespace)
	dbkey.SetContainerName(container)
	return target, dbkey, nil
}

// Initialize RedisDbMap
func initRedisDbMap() {
	dbkeys, err := sdcfg.GetDbAllInstances()
	if err != nil {
		log.Errorf("init error:  %v", err)
		return
	}
	for _, dbkey := range dbkeys {
		defer swsscommon.DeleteSonicDBKey(dbkey)
	}
	if len(dbkeys) == DbInstNum {
		// DB configuration is the same
		// No need to update
		return
	}
	DbInstNum = len(dbkeys)
	if RedisDbMap == nil {
		RedisDbMap = make(map[string]*redis.Client)
	}
	// Clear outdated configuration
	for mapkey, _ := range(RedisDbMap) {
		delete(RedisDbMap, mapkey)
	}
	for _, dbkey := range dbkeys {
		ns := dbkey.GetNetns()
		container := dbkey.GetContainerName()
		dbList, err := sdcfg.GetDbListByDBKey(dbkey)
		if err != nil {
			log.Errorf("init error:  %v", err)
			return
		}
		for _, dbName := range dbList {
			addr, err := sdcfg.GetDbSockByDBKey(dbName, dbkey)
			if err != nil {
				log.Errorf("init error:  %v", err)
				return
			}
			dbn, err := sdcfg.GetDbIdByDBKey(dbName, dbkey)
			if err != nil {
				log.Errorf("init error:  %v", err)
				return
			}
			// DB connector for direct redis operation
			redisDb := redis.NewClient(&redis.Options{
				Network:     "unix",
				Addr:        addr,
				Password:    "", // no password set
				DB:          int(dbn),
				DialTimeout: 0,
			})
			RedisDbMap[ns+":"+container+":"+dbName] = redisDb
		}
	}
	return
}

// Initialize RedisDbMap
func init() {
	initRedisDbMap()
}

func NewMixedDbClient(paths []*gnmipb.Path, prefix *gnmipb.Path, origin string, encoding gnmipb.Encoding, zmqPort string, vrf string) (Client, error) {
	var err error

	// Initialize RedisDbMap for test
	initRedisDbMap()

	var client = MixedDbClient {
		tableMap : map[string]swsscommon.ProducerStateTable{},
		zmqTableMap : map[string]swsscommon.ZmqProducerStateTable{},
	}

	// Get namespace count and container count from db config
	client.namespace_cnt = 1
	client.container_cnt = 1
	dbkey_list, _ := sdcfg.GetDbAllInstances()
	for _, dbkey := range dbkey_list {
		namespace := dbkey.GetNetns()
		container := dbkey.GetContainerName()
		if namespace != sdcfg.SONIC_DEFAULT_NAMESPACE {
			client.namespace_cnt += 1
		}
		if container != sdcfg.SONIC_DEFAULT_CONTAINER {
			client.container_cnt += 1
		}
		swsscommon.DeleteSonicDBKey(dbkey)
	}
	client.prefix = prefix
	client.target = ""
	client.origin = origin
	client.encoding = encoding
	if prefix != nil {
		elems := prefix.GetElem()
		if elems != nil {
			client.target = elems[0].GetName()
		}
	}
	if paths == nil {
		return &client, nil
	}

	target, dbkey, err := client.ParseDatabase(prefix, paths)
	if err != nil {
		return nil, err
	}
	defer swsscommon.DeleteSonicDBKey(dbkey)
	ok := IsTargetDbByDBKey(target, dbkey)
	if !ok {
		return nil, status.Errorf(codes.Unimplemented, "Invalid target: ns %s, container %s",
								dbkey.GetNetns(), dbkey.GetContainerName())
	}
	// If target is DPU_APPL_DB, this is multiple database, create DB connector for DPU
	// If target is original APPL_DB, create DB connector for backward compatibility
	if target == DPU_APPL_DB_NAME || target == APPL_DB_NAME {
		client.applDB = swsscommon.NewDBConnector(target, SWSS_TIMEOUT, false, dbkey)
	}
	client.target = target
	ns := dbkey.GetNetns()
	container := dbkey.GetContainerName()
	client.mapkey = ns + ":" + container
	client.paths = paths
	client.workPath = common_utils.GNMI_WORK_PATH

	// continer is DPU ID
	client.zmqClient, err = getZmqClient(container, zmqPort, vrf)
	if err != nil {
		return nil, fmt.Errorf("Get ZMQ client failed: %v", err)
	}
	newkey := swsscommon.NewSonicDBKey()
	newkey.SetContainerName(dbkey.GetContainerName())
	newkey.SetNetns(dbkey.GetNetns())
	client.dbkey = newkey
	return &client, nil
}

// gnmiFullPath builds the full path from the prefix and path.
func (c *MixedDbClient) gnmiFullPath(prefix, path *gnmipb.Path) (*gnmipb.Path, error) {
	origin := ""
	if prefix != nil {
		origin = prefix.Origin
	}
	if origin == "" {
		origin = path.Origin
	}
	fullPath := &gnmipb.Path{Origin: origin}
	if path.GetElem() != nil {
		elems := path.GetElem()
		if prefix != nil {
			elems = append(prefix.GetElem(), elems...)
		}
		// Skip first two elem
		// GNMI path schema is /CONFIG_DB/localhost/PORT
		if len(elems) < 2 {
			return nil, fmt.Errorf("Invalid gnmi path: length %d", len(elems))
		}
		fullPath.Elem = elems[2:]
	}
	return fullPath, nil
}

func (c *MixedDbClient) getAllDbtablePath(paths []*gnmipb.Path) (pathList [][]tablePath, err error) {
	for _, path := range paths {
		tblPaths, err := c.getDbtablePath(path, nil)
		if err != nil {
			return nil, err
		}
		pathList = append(pathList, tblPaths)
	}
	return pathList, nil
}

func (c *MixedDbClient) getDbtablePath(path *gnmipb.Path, value *gnmipb.TypedValue) ([]tablePath, error) {
	var buffer bytes.Buffer
	var dbPath string
	var tblPath tablePath

	fullPath, err := c.gnmiFullPath(c.prefix, path)
	if err != nil {
		return nil, err
	}

	stringSlice := []string{c.target}
	separator, _ := GetTableKeySeparatorByDBKey(c.target, c.dbkey)
	elems := fullPath.GetElem()
	if elems != nil {
		for i, elem := range elems {
			log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
			if i != 0 {
				buffer.WriteString(separator)
			}
			buffer.WriteString(elem.GetName())
			stringSlice = append(stringSlice, elem.GetName())
			value, ok := elem.GetKey()["key"]
			if ok {
				buffer.WriteString(value)
				stringSlice = append(stringSlice, value)
			}
		}
		dbPath = buffer.String()
	}

	tblPath.dbNamespace = c.dbkey.GetNetns()
	tblPath.dbName = c.target
	tblPath.tableName = ""
	if len(stringSlice) > 1 {
		tblPath.tableName = stringSlice[1]
		// tables in COUNTERS_DB other than COUNTERS table doesn't have keys
		// Insert a dummy table key
		if tblPath.dbName == "COUNTERS_DB" && tblPath.tableName != "COUNTERS" {
			if len(stringSlice) == 2 {
				stringSlice = append(stringSlice, "")
			} else {
				index := 2
				stringSlice = append(stringSlice[:index+1], stringSlice[index:]...)
				stringSlice[index] = ""
			}
		}
	}
	tblPath.delimitor = separator
	tblPath.operation = opRemove
	tblPath.index = -1
	tblPath.jsonValue = ""
	tblPath.protoValue = ""
	if value != nil {
		tblPath.operation = opAdd
		jv := value.GetJsonIetfVal()
		if jv != nil {
			tblPath.jsonValue = string(jv)
		}
		pv := value.GetProtoBytes()
		if pv != nil {
			tblPath.protoValue = string(pv)
		}
		if jv == nil && pv == nil {
			return nil, fmt.Errorf("Unsupported TypedValue: %v", value)
		}
	}

	var mappedKey string
	if len(stringSlice) > 2 { // tmp, to remove mappedKey
		mappedKey = stringSlice[2]
	}

	redisDb, ok := RedisDbMap[c.mapkey+":"+tblPath.dbName]
	if !ok {
		return nil, fmt.Errorf("Redis Client not present for dbName %v mapkey %v map %+v", tblPath.dbName, c.mapkey, RedisDbMap)
	}

	// The expect real db path could be in one of the formats:
	// <1> DB Table
	// <2> DB Table Key
	// <3> DB Table Field
	// <4> DB Table Key Field
	// <5> DB Table Key Field Index
	switch len(stringSlice) {
	case 1: // only db name provided
	case 2: // only table name provided
		if tblPath.operation == opRemove {
			res, err := redisDb.Keys(tblPath.tableName + "*").Result()
			if err != nil || len(res) < 1 {
				log.V(2).Infof("Invalid db table Path %v %v", c.target, dbPath)
				return nil, fmt.Errorf("Failed to find %v %v %v %v", c.target, dbPath, err, res)
			}
		}
		tblPath.tableKey = ""
	case 3: // Third element must be table key
		if tblPath.operation == opRemove {
			_, err := redisDb.Exists(tblPath.tableName + tblPath.delimitor + mappedKey).Result()
			if err != nil {
				return nil, fmt.Errorf("redis Exists op failed for %v", dbPath)
			}
		}
		tblPath.tableKey = mappedKey
	case 4: // Fourth element must be field name
		if tblPath.operation == opRemove {
			_, err := redisDb.Exists(tblPath.tableName + tblPath.delimitor + mappedKey).Result()
			if err != nil {
				return nil, fmt.Errorf("redis Exists op failed for %v", dbPath)
			}
		}
		tblPath.tableKey = mappedKey
		tblPath.field = stringSlice[3]
	case 5: // Fifth element must be list index
		if tblPath.operation == opRemove {
			_, err := redisDb.Exists(tblPath.tableName + tblPath.delimitor + mappedKey).Result()
			if err != nil {
				return nil, fmt.Errorf("redis Exists op failed for %v", dbPath)
			}
		}
		tblPath.tableKey = mappedKey
		tblPath.field = stringSlice[3]
		index, err := strconv.Atoi(stringSlice[4])
		if err != nil {
			return nil, fmt.Errorf("Invalid index %v", stringSlice[4])
		}
		tblPath.index = index
	default:
		log.V(2).Infof("Invalid db table Path %v", dbPath)
		return nil, fmt.Errorf("Invalid db table Path %v", dbPath)
	}

	tblPaths := []tablePath{tblPath}
	return tblPaths, nil
}

// makeJSON renders the database Key op value_pairs to map[string]interface{} for JSON marshall.
func (c *MixedDbClient) makeJSON_redis(msi *map[string]interface{}, key *string, op *string, mfv map[string]string) error {
	// TODO: Use Yang model to identify leaf-list
	if key == nil && op == nil {
		for f, v := range mfv {
			// There is NULL field in CONFIG DB, we need to remove NULL field from configuration
			// user@sonic:~$ redis-cli -n 4 hgetall "DHCP_SERVER|192.0.0.29"
			// 1) "NULL"
			// 2) "NULL"
			if f == "NULL" {
				continue
			} else if strings.HasSuffix(f, "@") {
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
		if f == "NULL" {
			continue
		} else if strings.HasSuffix(f, "@") {
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

// tableData2Msi renders the redis DB data to map[string]interface{}
// which may be marshaled to JSON format
// If only table name provided in the tablePath, find all keys in the table, otherwise
// Use tableName + tableKey as key to get all field value paires
func (c *MixedDbClient) tableData2Msi(tblPath *tablePath, useKey bool, op *string, msi *map[string]interface{}) error {
	redisDb, ok := RedisDbMap[c.mapkey+":"+tblPath.dbName]
	if !ok {
		return fmt.Errorf("Redis Client not present for dbName %v mapkey %v", tblPath.dbName, c.mapkey)
	}

	var pattern string
	var dbkeys []string
	var err error
	var fv map[string]string

	if tblPath.tableName == "" {
		// Did no provide table name
		// Get all tables in the DB
		// TODO: read all tables in COUNTERS_DB
		if tblPath.dbName == "COUNTERS_DB" {
			return fmt.Errorf("Can not read all tables in COUNTERS_DB")
		}
		pattern = "*" + tblPath.delimitor + "*"
		dbkeys, err = redisDb.Keys(pattern).Result()
		if err != nil {
			log.V(2).Infof("redis Keys failed for %v, pattern %s", tblPath, pattern)
			return fmt.Errorf("redis Keys failed for %v, pattern %s %v", tblPath, pattern, err)
		}
	} else if tblPath.tableKey == "" {
		// Only table name provided
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

	for idx, dbkey := range dbkeys {
		fv, err = redisDb.HGetAll(dbkey).Result()
		if err != nil {
			log.V(2).Infof("redis HGetAll failed for  %v, dbkey %s", tblPath, dbkey)
			return err
		}

		if (tblPath.tableName == "") {
			// Split dbkey string into two parts
			// First part is table name and second part is key in table
			keys := strings.SplitN(dbkey, tblPath.delimitor, 2)
			tableName := keys[0]
			key := keys[1]
			table_msi, ok := (*msi)[tableName].(*map[string]interface{})
			if !ok {
				tm := make(map[string]interface{})
				table_msi = &tm
				(*msi)[tableName] = table_msi
			}
			err = c.makeJSON_redis(table_msi, &key, op, fv)
			if err != nil {
				log.V(2).Infof("makeJSON err %s for fv %v", err, fv)
				return err
			}
		} else if (tblPath.tableKey != "" && !useKey) || tblPath.tableName == dbkey {
			if c.encoding == gnmipb.Encoding_JSON_IETF {
				err = c.makeJSON_redis(msi, nil, op, fv)
				if err != nil {
					log.V(2).Infof("makeJSON err %s for fv %v", err, fv)
					return err
				}
			} else if c.encoding == gnmipb.Encoding_PROTO {
				value, ok := fv["pb"]
				if ok {
					(*msi)["pb"] = []byte(value)
				} else {
					return fmt.Errorf("No proto bytes found in redis %v", fv)
				}
			}
		} else {
			var key string
			// Split dbkey string into two parts and second part is key in table
			keys := strings.SplitN(dbkey, tblPath.delimitor, 2)
			key = keys[1]
			err = c.makeJSON_redis(msi, &key, op, fv)
			if err != nil {
				log.V(2).Infof("makeJSON err %s for fv %v", err, fv)
				return err
			}
		}
		log.V(6).Infof("Added idex %v fv %v ", idx, fv)
	}
	return nil
}

func (c *MixedDbClient) msi2TypedValue(msi map[string]interface{}) (*gnmipb.TypedValue, error) {
	if c.encoding == gnmipb.Encoding_JSON_IETF {
		jv, err := emitJSON(&msi)
		if err != nil {
			log.V(2).Infof("emitJSON err %s for  %v", err, msi)
			return nil, fmt.Errorf("emitJSON err %s for  %v", err, msi)
		}
		return &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: jv,
			}}, nil
	} else if c.encoding == gnmipb.Encoding_PROTO {
		value, ok := msi["pb"]
		if ok {
			return &gnmipb.TypedValue{
				Value: &gnmipb.TypedValue_ProtoBytes{
					ProtoBytes: value.([]byte),
				}}, nil
		} else {
			return nil, fmt.Errorf("No proto bytes found in msi %v", msi)
		}
	} else {
		return nil, fmt.Errorf("Unknown encoding %v", c.encoding)
	}
}

func (c *MixedDbClient) tableData2TypedValue(tblPaths []tablePath, op *string) (*gnmipb.TypedValue, error) {
	var useKey bool
	msi := make(map[string]interface{})
	for _, tblPath := range tblPaths {
		redisDb, ok := RedisDbMap[c.mapkey+":"+tblPath.dbName]
		if !ok {
			return nil, fmt.Errorf("Redis Client not present for dbName %v mapkey %v", tblPath.dbName, c.mapkey)
		}

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

		err := c.tableData2Msi(&tblPath, useKey, nil, &msi)
		if err != nil {
			return nil, err
		}
	}
	return c.msi2TypedValue(msi)
}

func ConvertDbEntry(inputData map[string]interface{}) map[string]string {
	outputData := make(map[string]string)
	for key, value := range inputData {
		switch value.(type) {
		case string:
			outputData[key] = value.(string)
		case []interface{}:
			list := value.([]interface{})
			key_redis := key + "@"
			slice := []string{}
			for _, item := range(list) {
				if str, check := item.(string); check {
					slice = append(slice, str)
				} else {
					continue
				}
			}
			str_val := strings.Join(slice, ",")
			outputData[key_redis] = str_val
		}
	}
	return outputData
}

func (c *MixedDbClient) handleTableData(tblPaths []tablePath) error {
	var pattern string
	var dbkeys []string
	var err error
	var res interface{}

	for _, tblPath := range tblPaths {
		log.V(5).Infof("handleTableData: tblPath %v", tblPath)
		redisDb, ok := RedisDbMap[c.mapkey+":"+tblPath.dbName]
		if !ok {
			return fmt.Errorf("Redis Client not present for dbName %v mapkey %v", tblPath.dbName, c.mapkey)
		}

		if tblPath.jsonField == "" { // Not asked to include field in json value, which means not wildcard query
			// table path includes table, key and field
			if tblPath.field != "" {
				if len(tblPaths) != 1 {
					log.V(2).Infof("WARNING: more than one path exists for field granularity query: %v", tblPaths)
				}
				return fmt.Errorf("Unsupported path %v, can't update field", tblPath)
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
				// Can't remove entry in temporary state table
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
				tableKey := strings.TrimPrefix(dbkey, tblPath.tableName + tblPath.delimitor)
				err = c.DbDelTable(tblPath.tableName, tableKey)
				if err != nil {
					log.V(2).Infof("swsscommon delete failed for  %v, dbkey %s", tblPath, dbkey)
					return err
				}
			}
		} else if tblPath.operation == opAdd {
			if tblPath.tableKey != "" {
				// both table name and key provided
				if len(tblPath.jsonValue) != 0 {
					res, err = parseJson([]byte(tblPath.jsonValue))
					if err != nil {
						return err
					}
					if vtable, ok := res.(map[string]interface{}); ok {
						outputData := ConvertDbEntry(vtable)
						err = c.DbSetTable(tblPath.tableName, tblPath.tableKey, outputData)
						if err != nil {
							log.V(2).Infof("swsscommon update failed for  %v, value %v", tblPath, outputData)
							return err
						}
					} else {
						return fmt.Errorf("Key %v: Unsupported value %v type %v", tblPath.tableKey, res, reflect.TypeOf(res))
					}
				} else {
					// protobytes can be empty
					// If jsonValue is empty, use protoValue
					vtable := make(map[string]interface{})
					vtable["pb"] = tblPath.protoValue
					outputData := ConvertDbEntry(vtable)
					err = c.DbSetTable(tblPath.tableName, tblPath.tableKey, outputData)
					if err != nil {
						log.V(2).Infof("swsscommon update failed for  %v, value %v", tblPath, outputData)
						return err
					}
				}
			} else {
				if len(tblPath.jsonValue) == 0 {
					return fmt.Errorf("No valid value: %v", tblPath)
				}
				res, err = parseJson([]byte(tblPath.jsonValue))
				if err != nil {
					return err
				}
				if vtable, ok := res.(map[string]interface{}); ok {
					for tableKey, tres := range vtable {
						if vt, ret := tres.(map[string]interface{}); ret {
							outputData := ConvertDbEntry(vt)
							err = c.DbSetTable(tblPath.tableName, tableKey, outputData)
							if err != nil {
								log.V(2).Infof("swsscommon update failed for  %v, value %v", tblPath, outputData)
								return err
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

/* Populate the JsonPatch corresponding each GNMI operation. */
func (c *MixedDbClient) ConvertToJsonPatch(prefix *gnmipb.Path, path *gnmipb.Path, t *gnmipb.TypedValue, operation string, output *map[string]interface{}) error {
	if t != nil {
		if len(t.GetJsonIetfVal()) == 0 {
			return fmt.Errorf("Value encoding is not IETF JSON")
		}
	}
	fullPath, err := c.gnmiFullPath(prefix, path)
	if err != nil {
		return err
	}

	elems := fullPath.GetElem()
	(*output)["op"] = operation
	jsonPath := "/"

	if elems != nil {
		/* Iterate through elements. */
		for _, elem := range elems {
			jsonPath += elem.GetName()
			key := elem.GetKey()
			/* If no keys are present end the element with "/" */
			if key == nil {
				jsonPath += `/`
			}

			/* If keys are present , process the keys. */
			if key != nil {
				for k, v := range key {
					jsonPath += `[` + k + `=` + v + `]`
				}

				/* Append "/" after all keys are processed. */
				jsonPath += `/`
			}
		}
	}

	/* Trim the "/" at the end which is not required. */
	jsonPath = strings.TrimSuffix(jsonPath, `/`)
	(*output)["path"] = jsonPath
	if t != nil {
		val, err := parseJson(t.GetJsonIetfVal())
		if err != nil {
			return err
		}
		(*output)["value"] = val
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

var PyCodeForYang string =
`
import sonic_yang
import json

yang_parser = sonic_yang.SonicYang("/usr/local/yang-models")
yang_parser.loadYangModel()
filename = "%s"
with open(filename, 'r') as fp:
	text = fp.read()

	try:
		yang_parser.loadData(configdbJson=json.loads(text))
		yang_parser.validate_data_tree()
	except sonic_yang.SonicYangException as e:
		print("Yang validation error: {}".format(str(e)))
		raise
`

func (c *MixedDbClient) SetIncrementalConfig(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	var err error

	var sc ssc.Service
	sc, err = ssc.NewDbusClient()
	if err != nil {
		return err
	}
	err = sc.CreateCheckPoint(CHECK_POINT_PATH + "/config")
	if err != nil {
		return err
	}
	defer sc.DeleteCheckPoint(CHECK_POINT_PATH + "/config")
	fileName := CHECK_POINT_PATH + "/config.cp.json"
	c.jClient, err = NewJsonClient(fileName)
	if err != nil {
		return err
	}

	var patchList [](map[string]interface{})
	/* DELETE */
	for _, path := range delete {
		fullPath, err := c.gnmiFullPath(c.prefix, path)
		if err != nil {
			return err
		}
		log.V(2).Infof("Path #%v", fullPath)

		stringSlice := []string{}
		elems := fullPath.GetElem()
		if elems != nil {
			for i, elem := range elems {
				// TODO: Usage of key field
				log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
				stringSlice = append(stringSlice, elem.GetName())
			}
			err := c.jClient.Remove(stringSlice)
			if err != nil {
				// Remove failed, ignore
				continue
			}
		}
		curr := map[string]interface{}{}
		err = c.ConvertToJsonPatch(c.prefix, path, nil, DELETE_OPERATION, &curr)
		if err != nil {
			return err
		}
		patchList = append(patchList, curr)
	}

	/* REPLACE */
	for _, path := range replace {
		fullPath, err := c.gnmiFullPath(c.prefix, path.GetPath())
		if err != nil {
			return err
		}
		log.V(2).Infof("Path #%v", fullPath)

		stringSlice := []string{}
		elems := fullPath.GetElem()
		if elems != nil {
			for i, elem := range elems {
				// TODO: Usage of key field
				log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
				stringSlice = append(stringSlice, elem.GetName())
			}
			t := path.GetVal()
			if t == nil {
				err := c.jClient.Remove(stringSlice)
				if err != nil {
					// Remove failed, ignore
					continue
				}
			} else {
				err := c.jClient.Replace(stringSlice, string(t.GetJsonIetfVal()))
				if err != nil {
					// Add failed
					return err
				}
			}
		}
		curr := map[string]interface{}{}
		err = c.ConvertToJsonPatch(c.prefix, path.GetPath(), path.GetVal(), REPLACE_OPERATION, &curr)
		if err != nil {
			return err
		}
		patchList = append(patchList, curr)
	}

	/* UPDATE */
	for _, path := range update {
		fullPath, err := c.gnmiFullPath(c.prefix, path.GetPath())
		if err != nil {
			return err
		}
		log.V(2).Infof("Path #%v", fullPath)

		stringSlice := []string{}
		elems := fullPath.GetElem()
		if elems != nil {
			for i, elem := range elems {
				// TODO: Usage of key field
				log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
				stringSlice = append(stringSlice, elem.GetName())
			}
			t := path.GetVal()
			if t == nil {
				return fmt.Errorf("Invalid update %v", path)
			} else {
				err := c.jClient.Add(stringSlice, string(t.GetJsonIetfVal()))
				if err != nil {
					// Add failed
					return err
				}
			}
		}
		curr := map[string]interface{}{}
		err = c.ConvertToJsonPatch(c.prefix, path.GetPath(), path.GetVal(), UPDATE_OPERATION, &curr)
		if err != nil {
			return err
		}
		patchList = append(patchList, curr)
	}
	if len(patchList) == 0 {
		// No need to apply patch
		return nil
	}
	text, err := json.Marshal(patchList)
	if err != nil {
		return err
	}
	log.V(2).Infof("JsonPatch: %s", text)
	patchFile := c.workPath + "/gcu.patch"
	err = ioutil.WriteFile(patchFile, []byte(text), 0644)
	if err != nil {
		return err
	}

	if c.origin == "sonic-db" {
		err = sc.ApplyPatchDb(string(text))
	}

	if err == nil {
		err = sc.ConfigSave("/etc/sonic/config_db.json")
	}
	return err
}

func (c *MixedDbClient) SetFullConfig(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
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

	PyCodeInGo := fmt.Sprintf(PyCodeForYang, fileName)
	err = RunPyCode(PyCodeInGo)
	if err != nil {
		return fmt.Errorf("Yang validation failed!")
	}

	return nil
}

func (c *MixedDbClient) SetDB(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	/* DELETE */
	deletePathList, err := c.getAllDbtablePath(delete)
	if err != nil {
		return err
	}
	
	for _, tblPaths := range deletePathList {
		err = c.handleTableData(tblPaths)
		if err != nil {
			return err
		}
	}

	/* REPLACE */
	for _, item := range replace {
		tblPaths, err := c.getDbtablePath(item.GetPath(), item.GetVal())
		if err != nil {
			return err
		}
		err = c.handleTableData(tblPaths)
		if err != nil {
			return err
		}
	}

	/* UPDATE */
	for _, item := range update {
		tblPaths, err := c.getDbtablePath(item.GetPath(), item.GetVal())
		if err != nil {
			return err
		}
		err = c.handleTableData(tblPaths)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *MixedDbClient) SetConfigDB(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	// Full configuration will be overwritten next set request
	fileName := c.workPath + "/config_db.json.tmp"
	os.Remove(fileName)

	deleteLen := len(delete)
	replaceLen := len(replace)
	updateLen := len(update)
	if (deleteLen == 1 && replaceLen == 0 && updateLen == 1) {
		deletePath, err := c.gnmiFullPath(c.prefix, delete[0])
		if err != nil {
			return err
		}
		updatePath, err := c.gnmiFullPath(c.prefix, update[0].GetPath())
		if err != nil {
			return err
		}
		if (len(deletePath.GetElem()) == 0) && (len(updatePath.GetElem()) == 0) {
			return c.SetFullConfig(delete, replace, update)
		}
	}
	return c.SetIncrementalConfig(delete, replace, update)
}

func (c *MixedDbClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	if c.target == "CONFIG_DB" {
		return c.SetConfigDB(delete, replace, update)
	} else if c.target == DPU_APPL_DB_NAME || c.target == APPL_DB_NAME {
		// Use DPU_APPL_DB database for DASH
		// Keep APPL_DB for backward compatibility
		return c.SetDB(delete, replace, update)
	}
	return fmt.Errorf("Set RPC does not support %v", c.target)
}

func (c *MixedDbClient) GetCheckPoint() ([]*spb.Value, error) {
	var values []*spb.Value
	var err error
	ts := time.Now()

	fileName := CHECK_POINT_PATH + "/config.cp.json"
	c.jClient, err = NewJsonClient(fileName)
	if err != nil {
		return nil, fmt.Errorf("There's no check point")
	}
	log.V(2).Infof("Getting #%v", c.jClient.jsonData)
	for _, path := range c.paths {
		fullPath, err := c.gnmiFullPath(c.prefix, path)
		if err != nil {
			return nil, err
		}
		log.V(2).Infof("Path #%v", fullPath)

		stringSlice := []string{}
		elems := fullPath.GetElem()
		if elems != nil {
			for i, elem := range elems {
				// TODO: Usage of key field
				log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
				stringSlice = append(stringSlice, elem.GetName())
			}
			jv, err := c.jClient.Get(stringSlice)
			if err != nil {
				return nil, err
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

func (c *MixedDbClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	if c.target == "CONFIG_DB" {
		ret, err := c.GetCheckPoint()
		if err == nil {
			return ret, err
		}
		log.V(6).Infof("Error #%v", err)
	}

	var values []*spb.Value
	ts := time.Now()
	if c.paths != nil {
		for _, gnmiPath := range c.paths {
			tblPaths, err := c.getDbtablePath(gnmiPath, nil)
			if err != nil {
				return nil, err
			}
			val, err := c.tableData2TypedValue(tblPaths, nil)
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
	}

	log.V(6).Infof("Getting #%v", values)
	log.V(4).Infof("Get done, total time taken: %v ms", int64(time.Since(ts)/time.Millisecond))
	return values, nil
}

func (c *MixedDbClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *MixedDbClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
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
		for _, gnmiPath := range c.paths {
			tblPaths, err := c.getDbtablePath(gnmiPath, nil)
			if err != nil {
				log.V(2).Infof("Unable to get table path due to err: %v", err)
				return
			}
			val, err := c.tableData2TypedValue(tblPaths, nil)
			if err != nil {
				log.V(2).Infof("Unable to create gnmi TypedValue due to err: %v", err)
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

func (c *MixedDbClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w
	defer c.w.Done()
	c.q = q
	c.channel = stop

	if subscribe.GetSubscription() == nil {
		log.V(2).Infof("No incoming subscription, it is considered a dialout connection.")
		// NOTE: per https://github.com/sonic-net/sonic-gnmi/blob/master/doc/dialout.md#dialout_client_cli-and-dialout_server_cli
		// TELEMETRY_CLIENT subscription doesn't specificy type of the stream.
		// Handling it as a ON_CHANGE stream for backward compatibility.
		for _, gnmiPath := range c.paths {
			c.w.Add(1)
			c.synced.Add(1)
			go c.streamOnChangeSubscription(gnmiPath)
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
				go c.streamSampleSubscription(sub, subscribe.GetUpdatesOnly())
			} else if subMode == gnmipb.SubscriptionMode_ON_CHANGE {
				c.w.Add(1)
				c.synced.Add(1)
				go c.streamOnChangeSubscription(sub.GetPath())
			} else {
				putFatalMsg(c.q, fmt.Sprintf("unsupported subscription mode, %v", subMode))
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
func (c *MixedDbClient) streamOnChangeSubscription(gnmiPath *gnmipb.Path) {
	tblPaths, err := c.getDbtablePath(gnmiPath, nil)
	if err != nil {
		msg := fmt.Sprintf("streamOnChangeSubscription error:  %v", err)
		putFatalMsg(c.q, msg)
		c.synced.Done()
		c.w.Done()
		return
	}
	log.V(2).Infof("streamOnChangeSubscription gnmiPath: %v", gnmiPath)

	if tblPaths[0].field != "" {
		go c.dbFieldSubscribe(gnmiPath, true, time.Millisecond*200)
	} else {
		// sample interval and update only parameters are not applicable
		go c.dbTableKeySubscribe(gnmiPath, 0, true)
	}
}

// streamSampleSubscription implements Subscription "SAMPLE STREAM" mode
func (c *MixedDbClient) streamSampleSubscription(sub *gnmipb.Subscription, updateOnly bool) {
	samplingInterval, err := validateSampleInterval(sub)
	if err != nil {
		putFatalMsg(c.q, err.Error())
		c.synced.Done()
		c.w.Done()
		return
	}

	gnmiPath := sub.GetPath()
	tblPaths, err := c.getDbtablePath(gnmiPath, nil)
	if err != nil {
		putFatalMsg(c.q, err.Error())
		c.synced.Done()
		c.w.Done()
		return
	}
	log.V(2).Infof("streamSampleSubscription gnmiPath: %v", gnmiPath)
	if tblPaths[0].field != "" {
		c.dbFieldSubscribe(gnmiPath, false, samplingInterval)
	} else {
		c.dbTableKeySubscribe(gnmiPath, samplingInterval, updateOnly)
	}
}

// dbFieldSubscribe would read a field from a single table and put to output queue.
// Handles queries like "COUNTERS/Ethernet0/xyz" where the path translates to a field in a table.
// For SAMPLE mode, it would send periodically regardless of change.
// For ON_CHANGE mode, it would send only if the value has changed since the last update.
func (c *MixedDbClient) dbFieldSubscribe(gnmiPath *gnmipb.Path, onChange bool, interval time.Duration) {
	defer c.w.Done()

	tblPaths, err := c.getDbtablePath(gnmiPath, nil)
	if err != nil {
		putFatalMsg(c.q, err.Error())
		c.synced.Done()
		return
	}
	tblPath := tblPaths[0]
	// run redis get directly for field value
	redisDb, ok := RedisDbMap[c.mapkey+":"+tblPath.dbName]
	if !ok {
		msg := fmt.Sprintf("RedisDbMap not exist:  %v", c.mapkey+":"+tblPath.dbName)
		putFatalMsg(c.q, msg)
		c.synced.Done()
		return
	}

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
	err = sendVal(val)
	if err != nil {
		putFatalMsg(c.q, err.Error())
		c.synced.Done()
		return
	}
	c.synced.Done()

	intervalTicker := GetIntervalTicker()(interval)
	for {
		select {
		case <-c.channel:
			log.V(1).Infof("Stopping dbFieldSubscribe routine for Client %s ", c)
			return
		case <-intervalTicker:
			newVal := readVal()

			if onChange == false || newVal != val {
				if err = sendVal(newVal); err != nil {
					log.V(1).Infof("Queue error:  %v", err)
					return
				}
				val = newVal
			}
		}
		intervalTicker = GetIntervalTicker()(interval)
	}
}

// TODO: For delete operation, the exact content returned is to be clarified.
func (c *MixedDbClient) dbSingleTableKeySubscribe(rsd redisSubData, updateChannel chan map[string]interface{}) {
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
					newMsi["delete"] = "null_value"
				}
			} else if subscr.Payload == "hset" {
				//op := "SET"
				if tblPath.tableKey != "" {
					err = c.tableData2Msi(&tblPath, false, nil, &newMsi)
					if err != nil {
						putFatalMsg(c.q, err.Error())
						return
					}
				} else {
					tblPath := tblPath
					if len(subscr.Channel) < prefixLen {
						log.V(2).Infof("Invalid psubscribe channel notification %v, shorter than %v", subscr.Channel, prefixLen)
						continue
					}
					tblPath.tableKey = subscr.Channel[prefixLen:]
					err = c.tableData2Msi(&tblPath, true, nil, &newMsi)
					if err != nil {
						putFatalMsg(c.q, err.Error())
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
func (c *MixedDbClient) dbTableKeySubscribe(gnmiPath *gnmipb.Path, interval time.Duration, updateOnly bool) {
	defer c.w.Done()

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
		putFatalMsg(c.q, msg)
		signalSync()
	}

	tblPaths, err := c.getDbtablePath(gnmiPath, nil)
	if err != nil {
		handleFatalMsg(fmt.Sprintf("Path error:  %v", err))
		return
	}

	// Helper to send hash data over the stream
	sendMsiData := func(msiData map[string]interface{}) error {
		sendDeleteField := false
		if _, isDelete := msiData["delete"]; isDelete {
			sendDeleteField = true
		}
		delete(msiData, "delete")
		val, err := c.msi2TypedValue(msiData)
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
		if sendDeleteField {
			(*spbv).Delete = []*gnmipb.Path{gnmiPath}
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
		redisDb, ok := RedisDbMap[c.mapkey+":"+tblPath.dbName]
		if !ok {
			handleFatalMsg(fmt.Sprintf("RedisDbMap not exist:  %v", c.mapkey+":"+tblPath.dbName))
			return
		}
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

		err = c.tableData2Msi(&tblPath, false, nil, &msiAll)
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
		go c.dbSingleTableKeySubscribe(rsd, updateChannel)
	}

	// Listen on updates from tables.
	// Depending on the interval, send the updates every interval or on change only.
	intervalTicker := make(<-chan time.Time)
	for {

		// The interval ticker ticks only when the interval is non-zero.
		// Otherwise (e.g. on-change mode) it would never tick.
		if interval > 0 {
			intervalTicker = GetIntervalTicker()(interval)
		}

		select {
		case updatedTable := <-updateChannel:
			log.V(6).Infof("update received: %v", updatedTable)
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
			log.V(6).Infof("ticker received: %v", len(msiAll))

			if err := sendMsiData(msiAll); err != nil {
				handleFatalMsg(err.Error())
				return
			}

			// Clear the payload so that next time it will send only updates
			if updateOnly {
				msiAll = make(map[string]interface{})
				log.V(6).Infof("msiAll cleared: %v", len(msiAll))
			}

		case <-c.channel:
			log.V(1).Infof("Stopping dbTableKeySubscribe routine for %v ", c.pathG2S)
			return
		}
	}
}

func (c *MixedDbClient) Capabilities() []gnmipb.ModelData {
	return supportedModels
}

func (c *MixedDbClient) Close() error {
	for _, pt := range c.tableMap {
		swsscommon.DeleteProducerStateTable(pt)
	}
	for _, pt := range c.zmqTableMap {
		swsscommon.DeleteZmqProducerStateTable(pt)
	}
	if c.applDB != nil{
		swsscommon.DeleteDBConnector(c.applDB)
	}
	if c.dbkey != nil{
		swsscommon.DeleteSonicDBKey(c.dbkey)
	}

	return nil
}

func (c *MixedDbClient) SentOne(val *Value) {
}

func (c *MixedDbClient) FailedSend() {
}
