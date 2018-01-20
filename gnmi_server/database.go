package gnmi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	log "github.com/golang/glog"

	"github.com/go-redis/redis"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const (
	// indentString represents the default indentation string used for
	// JSON. Two spaces are used here.
	indentString       string = "  "
	default_UNIXSOCKET string = "/var/run/redis/redis.sock"
)

var target2RedisDb = make(map[string]*redis.Client)

type tablePath struct {
	dbTable
	tableKey  string
	delimitor string
	field     string
}

type dbTable struct {
	dbName    string
	tableName string
}

func getTableKeySeparator(target string) (string, error) {
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

func createDBConnector() {
	for dbName, dbn := range spb.Target_value {
		if dbName != "OTHERS" {
			// DB connector for direct redis operation
			redisDb := redis.NewClient(&redis.Options{
				Network:     "unix",
				Addr:        default_UNIXSOCKET,
				Password:    "", // no password set
				DB:          int(dbn),
				DialTimeout: 0,
			})
			target2RedisDb[dbName] = redisDb
		}
	}
}

// Populate table path in DB from gnmi path
// TODO: validate DB path
func populateDbTablePath(path, prefix *gnmipb.Path, target string, pathS2G *map[tablePath]*gnmipb.Path) error {
	var buffer bytes.Buffer
	var dbPath string
	var tblPath tablePath

	// Verify it is a valid db name
	_, ok := target2RedisDb[target]
	if !ok {
		return fmt.Errorf("Invalid target name %v", target)
	}

	fullPath := path
	if prefix != nil {
		fullPath = gnmiFullPath(prefix, path)
	}

	separator, _ := getTableKeySeparator(target)

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
	tblPath.delimitor = separator
	stringSlice := strings.Split(dbPath, tblPath.delimitor)
	// TODO: For counter table in COUNTERS_DB and FLEX_COUNTER_DB/PFC_WD_DB, remapping is needed
	tblPath.tableName = stringSlice[0]

	redisDb := target2RedisDb[tblPath.dbName]
	switch len(stringSlice) {
	case 1:
		tblPath.tableKey = ""
	case 2:
		tblPath.tableKey = stringSlice[1]
	case 3:
		tblPath.tableKey = stringSlice[1] + tblPath.delimitor + stringSlice[2]
		// verify whether this key exists
		key := tblPath.tableName + tblPath.delimitor + tblPath.tableKey
		n, err := redisDb.Exists(key).Result()
		if err != nil {
			return fmt.Errorf("redis Exists op failed for %v", dbPath)
		}
		// Looks like the third slice is not part of the key
		if n != 1 {
			tblPath.tableKey = stringSlice[1]
			tblPath.field = stringSlice[2]
		}
	case 4:
		tblPath.tableKey = stringSlice[1] + tblPath.delimitor + stringSlice[2]
		tblPath.field = stringSlice[3]
	default:
		log.V(2).Infof("Invalid db table Path %v", dbPath)
		return fmt.Errorf("Invalid db table Path %v", dbPath)
	}

	if tblPath.tableKey != "" {
		key := tblPath.tableName + tblPath.delimitor + tblPath.tableKey
		n, _ := redisDb.Exists(key).Result()
		if n != 1 {
			log.V(2).Infof("No valid entry found on %v", dbPath)
			return fmt.Errorf("No valid entry found on %v", dbPath)
		}
	}

	(*pathS2G)[tblPath] = path
	log.V(5).Infof("tablePath %+v", tblPath)
	return nil
}

// set DB target for subscribe request processing
func setClientSubscribeTarget(c *Client) error {
	prefix := c.subscribe.GetPrefix()

	if prefix == nil {
		// use CONFIG_DB as target of subscription by default
		c.target = "CONFIG_DB"
		return nil
	}
	// Path target in prefix stores DB name
	c.target = prefix.GetTarget()

	if c.target == "OTHERS" {
		return grpc.Errorf(codes.Unimplemented, "Unsupported target %s for %s", c.target, c)
	}
	if c.target != "" {
		if _, ok := target2RedisDb[c.target]; !ok {
			log.V(1).Infof("Invalid target %s for %s", c.target, c)
			return grpc.Errorf(codes.InvalidArgument, "Invalid target %s for %s", c.target, c)
		}
	} else {
		c.target = "CONFIG_DB"
	}

	return nil
}

// Populate SONiC data path from prefix and subscription path.
func (c *Client) populateDbPathSubscrition(sublist *gnmipb.SubscriptionList) error {
	prefix := sublist.GetPrefix()
	log.V(6).Infof("prefix : %#v SubscribRequest : %#v", sublist)

	subscriptions := sublist.GetSubscription()
	if subscriptions == nil {
		return fmt.Errorf("No Subscription")
	}

	for _, subscription := range subscriptions {
		path := subscription.GetPath()

		err := populateDbTablePath(path, prefix, c.target, &c.pathS2G)
		if err != nil {
			return err
		}
	}

	log.V(6).Infof("dbpaths : %v", c.pathS2G)
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

	j, err := json.MarshalIndent(*v, "", indentString)

	if err != nil {
		return nil, fmt.Errorf("JSON marshalling error: %v", err)
	}

	return j, nil
}

func tableData2TypedValue_redis(tblPath *tablePath, useKey bool, op *string) (*gnmipb.TypedValue, error) {
	redisDb := target2RedisDb[tblPath.dbName]

	// table path includes table, key and field
	if tblPath.field != "" {
		key := tblPath.tableName + tblPath.delimitor + tblPath.tableKey
		val, err := redisDb.HGet(key, tblPath.field).Result()
		if err != nil {
			log.V(2).Infof("redis HGet failed for %v", tblPath)
			return nil, err
		}
		return &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_StringVal{
				StringVal: val,
			}}, nil
	}

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
			return nil, err
		}
	} else {
		// both table name and key provided
		dbkeys = []string{tblPath.tableName + tblPath.delimitor + tblPath.tableKey}
	}

	msi := make(map[string]interface{})
	var fv map[string]string

	for idx, dbkey := range dbkeys {
		fv, err = redisDb.HGetAll(dbkey).Result()
		if err != nil {
			log.V(2).Infof("redis HGetAll failed for  %v, dbkey %s", tblPath, dbkey)
			return nil, err
		}

		if (tblPath.tableKey != "" && !useKey) || tblPath.tableName == dbkey {
			err = makeJSON_redis(&msi, nil, op, fv)
		} else {
			key := dbkey[len(tblPath.tableName+tblPath.delimitor):]
			err = makeJSON_redis(&msi, &key, op, fv)
		}
		if err != nil {
			log.V(2).Infof("makeJSON err %s for fv %v", err, fv)
			return nil, err
		}
		log.V(5).Infof("Added idex %v fv %v ", idx, fv)
	}

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

func enqueFatalMsg(c *Client, msg string) {
	c.q.Put(Value{
		&spb.Value{
			Timestamp: time.Now().UnixNano(),
			Fatal:     msg,
		},
	})
}

// for subscribe request with granularity of table field, the value is fetched periodically.
// Upon value change, it will be put to queue for furhter notification
func dbFieldSubscribe(tblPath tablePath, c *Client) {
	c.w.Add(1)
	defer c.w.Done()
	// run redis get directly for field value
	redisDb := target2RedisDb[tblPath.dbName]
	key := tblPath.tableName + tblPath.delimitor + tblPath.tableKey

	var val string
	for {
		select {
		case <-c.stop:
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
				val = newVal
				spbv := &spb.Value{
					Path:      c.pathS2G[tblPath],
					Timestamp: time.Now().UnixNano(),
					Val: &gnmipb.TypedValue{
						Value: &gnmipb.TypedValue_StringVal{
							StringVal: val,
						},
					},
				}

				if err = c.q.Put(Value{spbv}); err != nil {
					log.V(1).Infof("Queue error:  %v", err)
					return
				}
			}
			// check again after 500 millisends
			time.Sleep(time.Millisecond * 500)
		}
	}

}

func dbTableKeySubscribe(tblPath tablePath, c *Client) {
	c.w.Add(1)
	defer c.w.Done()

	redisDb := target2RedisDb[tblPath.dbName]

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

	val, err := tableData2TypedValue_redis(&tblPath, false, nil)
	if err != nil {
		enqueFatalMsg(c, err.Error())
		return
	}
	var spbv *spb.Value
	spbv = &spb.Value{
		Path:      c.pathS2G[tblPath],
		Timestamp: time.Now().UnixNano(),
		Val:       val,
	}
	if err = c.q.Put(Value{spbv}); err != nil {
		log.V(1).Infof("Queue error:  %v", err)
		return
	}

	for {
		select {
		default:
			msgi, err := pubsub.ReceiveTimeout(time.Millisecond * 1000)
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
					Path:      c.pathS2G[tblPath],
					Timestamp: time.Now().UnixNano(),
					Val: &gnmipb.TypedValue{
						Value: &gnmipb.TypedValue_JsonIetfVal{
							JsonIetfVal: jv,
						},
					},
				}
			} else if subscr.Payload == "hset" {
				var val *gnmipb.TypedValue
				//op := "SET"
				if tblPath.tableKey != "" {
					val, err = tableData2TypedValue_redis(&tblPath, false, nil)
				} else {
					tblPath := tblPath
					if len(subscr.Channel) < prefixLen {
						log.V(2).Infof("Invalid psubscribe channel notification %v for pattern %v", subscr.Channel, pattern)
						continue
					}
					tblPath.tableKey = subscr.Channel[prefixLen:]
					val, err = tableData2TypedValue_redis(&tblPath, true, nil)
				}
				spbv = &spb.Value{
					Path:      c.pathS2G[tblPath],
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
		case <-c.stop:
			log.V(1).Infof("Stopping subscribeDb routine for Client %s ", c)
			return
		}
	}
}

// TODO:  Upon error, inform creator of this routine
func subscribeDb(c *Client) {
	c.w.Add(1)
	defer c.w.Done()

	for table, _ := range c.pathS2G {
		if table.field != "" {
			go dbFieldSubscribe(table, c)
			continue
		}
		go dbTableKeySubscribe(table, c)
		continue
	}

	for {
		select {
		default:
			time.Sleep(time.Second)
			if c.synced == true {
				continue
			}
			// Inject sync message after first timeout.
			c.q.Put(Value{
				&spb.Value{
					Timestamp:    time.Now().UnixNano(),
					SyncResponse: true,
				},
			})

			log.V(2).Infof("Client %s synced", c)
			c.synced = true
		case <-c.stop:
			log.V(1).Infof("Stopping subscribeDb routine for Client %s ", c)
			return
		}
	}
	log.V(2).Infof("Exiting subscribeDb for %v", c)
}

// Read db upon poll signal
func pollDb(c *Client) {
	c.w.Add(1)
	defer c.w.Done()
	for {
		_, more := <-c.polled
		if !more {
			log.V(1).Infof("%v polled channel closed, exiting pollDb routine", c)
			return
		}

		for tblPath, gnmiPath := range c.pathS2G {
			val, err := tableData2TypedValue_redis(&tblPath, false, nil)
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
		log.V(4).Infof("Sync done!")
	}
	log.V(2).Infof("Exiting pollDB for %v", c)
}
