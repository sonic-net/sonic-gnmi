package client

// #cgo pkg-config: python3-embed
// #include <Python.h>
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
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
)

const REDIS_SOCK string = "/var/run/redis/redis.sock"
const APPL_DB int = 0
const SWSS_TIMEOUT uint = 0
const CHECK_POINT_PATH string = "/etc/sonic"

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
	q       *queue.PriorityQueue
	channel chan struct{}
	target  string
	origin  string
	workPath string
	jClient *JsonClient
	applDB swsscommon.DBConnector
	tableMap map[string]swsscommon.ProducerStateTable

	synced sync.WaitGroup  // Control when to send gNMI sync_response
	w      *sync.WaitGroup // wait for all sub go routines to finish
	mu     sync.RWMutex    // Mutex for data protection among routines for DbClient
}

func parseJson(str []byte) (interface{}, error) {
	var res interface{}
	err := json.Unmarshal(str, &res)
	if err != nil {
		return res, fmt.Errorf("JSON unmarshalling error: %v", err)
	}
	return res, nil
}

func ParseTarget(target string, paths []*gnmipb.Path) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	for i, path := range paths {
		elems := path.GetElem()
		if elems == nil {
			return "", status.Error(codes.Unimplemented, "No target specified in path")
		}
		if target == "" {
			if i == 0 {
				target = elems[0].GetName()
			}
		} else if target != elems[0].GetName() {
			return "", status.Error(codes.Unimplemented, "Target conflict in path")
		}
	}
	if target == "" {
		return "", status.Error(codes.Unimplemented, "No target specified in path")
	}
	return target, nil
}

func (c *MixedDbClient) DbSetTable(table string, key string, values map[string]string) error {
	pt, ok := c.tableMap[table]
	if !ok {
		pt = swsscommon.NewProducerStateTable(c.applDB, table)
		c.tableMap[table] = pt
	}
	vec := swsscommon.NewFieldValuePairs()
	defer swsscommon.DeleteFieldValuePairs(vec)
	for k, v := range values {
		pair := swsscommon.NewFieldValuePair(k, v)
		vec.Add(pair)
		swsscommon.DeleteFieldValuePair(pair)
	}
	pt.Set(key, vec, "SET", "")
	return nil
}

func (c *MixedDbClient) DbDelTable(table string, key string) error {
	pt, ok := c.tableMap[table]
	if !ok {
		pt = swsscommon.NewProducerStateTable(c.applDB, table)
		c.tableMap[table] = pt
	}
	pt.Delete(key, "DEL", "")
	return nil
}

func NewMixedDbClient(paths []*gnmipb.Path, prefix *gnmipb.Path, origin string) (Client, error) {
	var client MixedDbClient
	var err error

	// Testing program may ask to use redis local tcp connection
	if UseRedisLocalTcpPort {
		useRedisTcpClient()
	}

	client.prefix = prefix
	client.target = ""
	client.origin = origin
	if prefix != nil {
		elems := prefix.GetElem()
		if elems != nil {
			client.target = elems[0].GetName()
		}
	}
	if paths == nil {
		return &client, nil
	}

	if client.target == "" {
		client.target, err = ParseTarget(client.target, paths)
		if err != nil {
			return nil, err
		}
	}
	_, ok, _, _ := IsTargetDb(client.target); 
	if !ok {
		return nil, status.Errorf(codes.Unimplemented, "Invalid target: %s", client.target)
	}
	client.paths = paths
	client.workPath = common_utils.GNMI_WORK_PATH
	client.applDB = swsscommon.NewDBConnector(APPL_DB, REDIS_SOCK, SWSS_TIMEOUT)
	client.tableMap = map[string]swsscommon.ProducerStateTable{}

	return &client, nil
}

// gnmiFullPath builds the full path from the prefix and path.
func (c *MixedDbClient) gnmiFullPath(prefix, path *gnmipb.Path) *gnmipb.Path {

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

func (c *MixedDbClient) populateAllDbtablePath(paths []*gnmipb.Path, pathG2S *map[*gnmipb.Path][]tablePath) error {
	for _, path := range paths {
		err := c.populateDbtablePath(path, nil, pathG2S)
		if err != nil {
			return err
		}
	}
	return nil
}

// Populate table path in DB from gnmi path
func (c *MixedDbClient) populateDbtablePath(path *gnmipb.Path, value *gnmipb.TypedValue, pathG2S *map[*gnmipb.Path][]tablePath) error {
	var buffer bytes.Buffer
	var dbPath string
	var tblPath tablePath

	targetDbName, targetDbNameValid, targetDbNameSpace, _ := IsTargetDb(c.target)
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
	if c.prefix != nil {
		fullPath = c.gnmiFullPath(c.prefix, path)
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
	value_str := ""
	if value != nil {
		value_str = string(value.GetJsonIetfVal())
	}

	tblPath.dbNamespace = dbNamespace
	tblPath.dbName = targetDbName
	tblPath.tableName = stringSlice[1]
	tblPath.delimitor = separator
	tblPath.operation = opRemove
	tblPath.index = -1
	if value != nil {
		tblPath.operation = opAdd
		tblPath.value = value_str
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
				log.V(2).Infof("Invalid db table Path %v %v", c.target, dbPath)
				return fmt.Errorf("Failed to find %v %v %v %v", c.target, dbPath, err, res)
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
func (c *MixedDbClient) makeJSON_redis(msi *map[string]interface{}, key *string, op *string, mfv map[string]string) error {
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

// tableData2Msi renders the redis DB data to map[string]interface{}
// which may be marshaled to JSON format
// If only table name provided in the tablePath, find all keys in the table, otherwise
// Use tableName + tableKey as key to get all field value paires
func (c *MixedDbClient) tableData2Msi(tblPath *tablePath, useKey bool, op *string, msi *map[string]interface{}) error {
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

	for idx, dbkey := range dbkeys {
		fv, err = redisDb.HGetAll(dbkey).Result()
		if err != nil {
			log.V(2).Infof("redis HGetAll failed for  %v, dbkey %s", tblPath, dbkey)
			return err
		}

		if tblPath.jsonTableKey != "" { // If jsonTableKey was prepared, use it
			err = c.makeJSON_redis(msi, &tblPath.jsonTableKey, op, fv)
		} else if (tblPath.tableKey != "" && !useKey) || tblPath.tableName == dbkey {
			err = c.makeJSON_redis(msi, nil, op, fv)
		} else {
			var key string
			// Split dbkey string into two parts and second part is key in table
			keys := strings.SplitN(dbkey, tblPath.delimitor, 2)
			key = keys[1]
			err = c.makeJSON_redis(msi, &key, op, fv)
		}
		if err != nil {
			log.V(2).Infof("makeJSON err %s for fv %v", err, fv)
			return err
		}
		log.V(6).Infof("Added idex %v fv %v ", idx, fv)
	}
	return nil
}

func (c *MixedDbClient) tableData2TypedValue(tblPaths []tablePath, op *string) (*gnmipb.TypedValue, error) {
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

		err := c.tableData2Msi(&tblPath, useKey, nil, &msi)
		if err != nil {
			return nil, err
		}
	}
	return msi2TypedValue(msi)
}

func ConvertDbEntry(inputData map[string]interface{}) map[string]string {
    outputData := map[string]string{}
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
		redisDb := Target2RedisDb[tblPath.dbNamespace][tblPath.dbName]

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
				res, err = parseJson([]byte(tblPath.value))
				if err != nil {
					return err
				}
				if vtable, ok := res.(map[string]interface{}); ok {
					configMap := make(map[string]interface{})
					tableMap := make(map[string]interface{})
					tableMap[tblPath.tableKey] = vtable
					configMap[tblPath.tableName] = tableMap
					ietf_json_val, err := emitJSON(&configMap)
					if err != nil {
						return fmt.Errorf("Translate to json failed!")
					}
					PyCodeInGo := fmt.Sprintf(PyCodeForYang, ietf_json_val)
					err = RunPyCode(PyCodeInGo)
					if err != nil {
						return fmt.Errorf("Yang validation failed!")
					}
					outputData := ConvertDbEntry(vtable)
					c.DbDelTable(tblPath.tableName, tblPath.tableKey)
					err = c.DbSetTable(tblPath.tableName, tblPath.tableKey, outputData)
					if err != nil {
						log.V(2).Infof("swsscommon update failed for  %v, value %v", tblPath, outputData)
						return err
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
					configMap := make(map[string]interface{})
					configMap[tblPath.tableName] = vtable
					ietf_json_val, err := emitJSON(&configMap)
					if err != nil {
						return fmt.Errorf("Translate to json failed!")
					}
					PyCodeInGo := fmt.Sprintf(PyCodeForYang, ietf_json_val)
					err = RunPyCode(PyCodeInGo)
					if err != nil {
						return fmt.Errorf("Yang validation failed!")
					}
					for tableKey, tres := range vtable {
						if vt, ret := tres.(map[string]interface{}); ret {
							outputData := ConvertDbEntry(vt)
							c.DbDelTable(tblPath.tableName, tableKey)
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
func (c *MixedDbClient) ConvertToJsonPatch(prefix *gnmipb.Path, path *gnmipb.Path, t *gnmipb.TypedValue, output *string) error {
	if t != nil {
		if len(t.GetJsonIetfVal()) == 0 {
			return fmt.Errorf("Value encoding is not IETF JSON")
		}
	}
	fullPath := path
	if prefix != nil {
		fullPath = c.gnmiFullPath(prefix, path)
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

var PyCodeForYang string =
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

func (c *MixedDbClient) SetIncrementalConfig(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	var err error
	var curr string

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

	text := `[`
	/* DELETE */
	for _, path := range delete {
		fullPath := path
		if c.prefix != nil {
			fullPath = c.gnmiFullPath(c.prefix, path)
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
		curr = ``
		err = c.ConvertToJsonPatch(c.prefix, path, nil, &curr)
		if err != nil {
			return err
		}
		text += curr + `,`
	}

	/* REPLACE */
	for _, path := range replace {
		fullPath := path.GetPath()
		if c.prefix != nil {
			fullPath = c.gnmiFullPath(c.prefix, path.GetPath())
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
				err := c.jClient.Add(stringSlice, string(t.GetJsonIetfVal()))
				if err != nil {
					// Add failed
					return err
				}
			}
		}
		curr = ``
		err = c.ConvertToJsonPatch(c.prefix, path.GetPath(), path.GetVal(), &curr)
		if err != nil {
			return err
		}
		text += curr + `,`
	}

	/* UPDATE */
	for _, path := range update {
		fullPath := path.GetPath()
		if c.prefix != nil {
			fullPath = c.gnmiFullPath(c.prefix, path.GetPath())
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
		curr = ``
		err = c.ConvertToJsonPatch(c.prefix, path.GetPath(), path.GetVal(), &curr)
		if err != nil {
			return err
		}
		text += curr + `,`
	}
	text = strings.TrimSuffix(text, `,`)
	text += `]`
	log.V(2).Infof("JsonPatch: %s", text)
	if text == `[]` {
		// No need to apply patch
		return nil
	}
	patchFile := c.workPath + "/gcu.patch"
	err = ioutil.WriteFile(patchFile, []byte(text), 0644)
	if err != nil {
		return err
	}

	if c.origin == "sonic-db" {
		err = sc.ApplyPatchDb(text)
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

	PyCodeInGo := fmt.Sprintf(PyCodeForYang, ietf_json_val)
	err = RunPyCode(PyCodeInGo)
	if err != nil {
		return fmt.Errorf("Yang validation failed!")
	}

	return nil
}

func (c *MixedDbClient) SetDB(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	/* DELETE */
	deleteMap := make(map[*gnmipb.Path][]tablePath)
	err := c.populateAllDbtablePath(delete, &deleteMap)
	if err != nil {
		return err
	}
	
	for _, tblPaths := range deleteMap {
		err = c.handleTableData(tblPaths)
		if err != nil {
			return err
		}
	}

	/* REPLACE */
	replaceMap := make(map[*gnmipb.Path][]tablePath)
	for _, item := range replace {
		err = c.populateDbtablePath(item.GetPath(), item.GetVal(), &replaceMap)
		if err != nil {
			return err
		}
	}
	for _, tblPaths := range replaceMap {
		err = c.handleTableData(tblPaths)
		if err != nil {
			return err
		}
	}

	/* UPDATE */
	updateMap := make(map[*gnmipb.Path][]tablePath)
	for _, item := range update {
		err = c.populateDbtablePath(item.GetPath(), item.GetVal(), &updateMap)
		if err != nil {
			return err
		}
	}
	for _, tblPaths := range updateMap {
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
		deletePath := c.gnmiFullPath(c.prefix, delete[0])
		updatePath := c.gnmiFullPath(c.prefix, update[0].GetPath())
		if (len(deletePath.GetElem()) == 0) && (len(updatePath.GetElem()) == 0) {
			return c.SetFullConfig(delete, replace, update)
		}
	}
	return c.SetIncrementalConfig(delete, replace, update)
}

func (c *MixedDbClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	if c.target == "CONFIG_DB" {
		return c.SetConfigDB(delete, replace, update)
	} else if c.target == "APPL_DB" {
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
		fullPath := path
		if c.prefix != nil {
			fullPath = c.gnmiFullPath(c.prefix, path)
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

	if c.paths != nil {
		c.pathG2S = make(map[*gnmipb.Path][]tablePath)
		err := c.populateAllDbtablePath(c.paths, &c.pathG2S)
		if err != nil {
			return nil, err
		}
	}

	var values []*spb.Value
	ts := time.Now()
	for gnmiPath, tblPaths := range c.pathG2S {
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
	log.V(6).Infof("Getting #%v", values)
	log.V(4).Infof("Get done, total time taken: %v ms", int64(time.Since(ts)/time.Millisecond))
	return values, nil
}

func (c *MixedDbClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *MixedDbClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *MixedDbClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *MixedDbClient) Capabilities() []gnmipb.ModelData {
	return supportedModels
}

func (c *MixedDbClient) Close() error {
	for _, pt := range c.tableMap {
		swsscommon.DeleteProducerStateTable(pt)
	}
	swsscommon.DeleteDBConnector(c.applDB)
	return nil
}

func (c *MixedDbClient) SentOne(val *Value) {
}

func (c *MixedDbClient) FailedSend() {
}

