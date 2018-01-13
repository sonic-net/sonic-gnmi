package gnmi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	log "github.com/golang/glog"

	swsscommon "github.com/Azure/sonic-swss-common"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var target2Db = make(map[string]swsscommon.DBConnector)

type tablePath struct {
	dbName    string
	tableName string
	tableKey  string
	delimitor string
	table     swsscommon.Table
}

// Fetch data from the db table path and marshall it into json byte stream
func tableData2Json(tblPath *tablePath) ([]byte, error) {
	dbkeys := []string{}

	log.V(5).Infof("tablePath %v, SONiC table name: %s", *tblPath, tblPath.table.GetTableName())

	if tblPath.tableKey == "" {
		// tables in COUNTERS_DB other than COUNTERS table doesn't have keys
		if tblPath.dbName == "COUNTERS_DB" && tblPath.tableName != "COUNTERS" {
			dbkeys = append(dbkeys, tblPath.tableKey)
		} else {
			keys := swsscommon.NewVectorString()
			defer swsscommon.DeleteVectorString(keys)

			// Poll all keys in table
			tblPath.table.GetKeys(keys)
			for i := 0; i < int(keys.Size()); i++ {
				dbkeys = append(dbkeys, keys.Get(i))
			}
		}

		log.V(5).Infof("dbkeys len: %v", len(dbkeys))
	} else {
		// Poll specific key in table.
		dbkeys = append(dbkeys, tblPath.tableKey)
	}

	mpv := make(map[string]interface{})

	for idx, dbkey := range dbkeys {
		vpsr := swsscommon.NewFieldValuePairs()
		defer swsscommon.DeleteFieldValuePairs(vpsr)

		ret := tblPath.table.Get(dbkey, vpsr)
		if ret != true {
			// TODO: Key might gets deleted
			log.V(1).Infof("%v table get failed", dbkey)
			continue
		}

		var err error
		if tblPath.tableKey != "" {
			err = makeJSON(&mpv, nil, nil, vpsr)
		} else {
			err = makeJSON(&mpv, &dbkey, nil, vpsr)
		}
		if err != nil {
			log.V(2).Infof("makeJSON err %s for vpsr %v", err, vpsr)
			return nil, err
		}

		log.V(5).Infof("Added idex %v vpsr #%v ", idx, vpsr)
	}

	jv, err := emitJSON(&mpv)
	if err != nil {
		log.V(2).Infof("emitJSON err %s for  %v", err, mpv)
		return nil, fmt.Errorf("emitJSON err %s for  %v", err, mpv)
	}
	return jv, nil
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
			db := swsscommon.NewDBConnector(int(dbn), swsscommon.DBConnectorDEFAULT_UNIXSOCKET, uint(0))
			target2Db[dbName] = db
		}
	}
}

func deleteDBConnector() {
	for _, db := range target2Db {
		swsscommon.DeleteDBConnector(db)
	}
}

func getDBConnector(target string) (db swsscommon.DBConnector, err error) {
	db, ok := target2Db[target]
	if !ok {
		return nil, fmt.Errorf("Failed to find db connector for %s", target)
	}
	return db, nil
}

// Populate table path in DB from gnmi path
func createDbTablePath(path, prefix *gnmipb.Path, target string, pathS2G *map[tablePath]*gnmipb.Path) error {
	var buffer bytes.Buffer
	var dbPath string
	var tblPath tablePath

	db, err := getDBConnector(target)
	if err != nil {
		return err
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
	switch len(stringSlice) {
	case 1:
		tblPath.tableKey = ""
	case 2:
		tblPath.tableKey = stringSlice[1]
	case 3:
		tblPath.tableKey = stringSlice[1] + tblPath.delimitor + stringSlice[2]
	default:
		log.Errorf("Invalid db table Path %v for get", dbPath)
		return fmt.Errorf("Invalid db table Path %v for get", dbPath)
	}
	tbl := swsscommon.NewTable(db, tblPath.tableName, separator)
	tblPath.table = tbl
	(*pathS2G)[tblPath] = path
	log.V(5).Infof("tablePath %+v", tblPath)
	return nil
}

func deleteDbTablePath(pathS2G map[tablePath]*gnmipb.Path) {
	for tbl, _ := range pathS2G {
		swsscommon.DeleteTable(tbl.table)
	}
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
		if _, ok := target2Db[c.target]; !ok {
			log.V(1).Infof("Invalid target %s for %s", c.target, c)
			return grpc.Errorf(codes.InvalidArgument, "Invalid target %s for %s", c.target, c)
		}
	} else {
		c.target = "CONFIG_DB"
	}

	return nil
}

// Populate SONiC data path from prefix and subscription path.
func (c *Client) populateDbPathSubscrition(sublist *gnmipb.SubscriptionList, tableOnly bool) error {
	prefix := sublist.GetPrefix()
	log.V(6).Infof("prefix : %#v SubscribRequest : %#v", sublist)

	subscriptions := sublist.GetSubscription()
	if subscriptions == nil {
		return fmt.Errorf("No Subscription")
	}

	for _, subscription := range subscriptions {
		path := subscription.GetPath()

		err := createDbTablePath(path, prefix, c.target, &c.pathS2G)
		if err != nil {
			return err
		}
	}

	log.V(6).Infof("dbpaths : %v", c.pathS2G)
	return nil
}

// makeJSON renders the database Key op value_pairs to map[string]interface{} for JSON marshall.
func makeJSON(mpv *map[string]interface{}, key *string, op *string, vpsr swsscommon.FieldValuePairs) error {
	if key == nil && op == nil {
		for n := vpsr.Size() - 1; n >= 0; n-- {
			fieldpair := vpsr.Get(int(n))
			(*mpv)[fieldpair.GetFirst()] = fieldpair.GetSecond()
		}
		return nil
	}

	fp := map[string]interface{}{}

	for n := vpsr.Size() - 1; n >= 0; n-- {
		fieldpair := vpsr.Get(int(n))
		fp[fieldpair.GetFirst()] = fieldpair.GetSecond()
	}

	if op == nil {
		(*mpv)[*key] = fp
	} else {
		// Also have operation layer
		of := map[string]interface{}{}

		of[*op] = fp
		(*mpv)[*key] = of
	}

	return nil
}

const (
	// indentString represents the default indentation string used for
	// JSON. Two spaces are used here.
	indentString string = "  "
)

// emitJSON marshalls map[string]interface{} to JSON byte stream.
func emitJSON(v *map[string]interface{}) ([]byte, error) {

	j, err := json.MarshalIndent(*v, "", indentString)

	if err != nil {
		return nil, fmt.Errorf("JSON marshalling error: %v", err)
	}

	return j, nil
}

func subscribeDb(c *Client) {
	var sstables []*swsscommon.SubscriberStateTable

	// skipping error check here for it has been done in setClientSubscribeTarget().
	db, _ := getDBConnector(c.target)
	sel := swsscommon.NewSelect()
	defer swsscommon.DeleteSelect(sel)

	sst2GnmiPath := map[*swsscommon.SubscriberStateTable]*gnmipb.Path{}
	// TODO: verify table name
	for table, gnmiPath := range c.pathS2G {
		sstable := swsscommon.NewSubscriberStateTable(db, table.tableName)
		defer swsscommon.DeleteSubscriberStateTable(sstable)
		sel.AddSelectable(sstable.SwigGetSelectable())
		sstables = append(sstables, &sstable)
		sst2GnmiPath[&sstable] = gnmiPath
	}

	for {
		fd := []int{0}
		var timeout uint = 200
		var ret int
		select {
		default:
			ret = sel.Xselect(fd, timeout)
		case <-c.stop:
			log.V(1).Infof("Stoping subscribeDb routine for Client %s ", c)
			return
		}

		if ret == swsscommon.SelectTIMEOUT {
			log.V(6).Infof("SelectTIMEOUT")
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

			log.V(1).Infof("Client %s synced", c)
			c.synced = true
			continue
		}
		if ret != swsscommon.SelectOBJECT {
			log.V(1).Infof("Error: Client %s Expecting : %v", c, swsscommon.SelectOBJECT)
			continue
		}

		for _, sstable := range sstables {
			if sel.IsSelected((*sstable).SwigGetSelectable()) {
				vpsr := swsscommon.NewFieldValuePairs()
				defer swsscommon.DeleteFieldValuePairs(vpsr)

				ko := swsscommon.NewStringPair()
				defer swsscommon.DeleteStringPair(ko)

				(*sstable).Pop(ko, vpsr)

				// check err
				v := map[string]interface{}{}
				key := ko.GetFirst()
				op := ko.GetSecond()

				err := makeJSON(&v, &key, &op, vpsr)
				if err != nil {
					log.V(2).Infof("makeJSON err %s for  %v %v %v", err, key, op, vpsr)
				}

				jv, err := emitJSON(&v)
				if err != nil {
					log.V(2).Infof("emitJSON err %s for  %v", err, v)
				}

				spbv := &spb.Value{
					Path:      sst2GnmiPath[sstable],
					Timestamp: time.Now().UnixNano(),
					Val: &gnmipb.TypedValue{
						Value: &gnmipb.TypedValue_JsonIetfVal{
							JsonIetfVal: jv,
						},
					},
				}

				c.q.Put(Value{spbv})
				log.V(5).Infof("Added spbv #%v", spbv)
			}
		}
	}
}

func pollDb(c *Client) {
	// Upon exit of pollDB, clean table path resource
	defer deleteDbTablePath(c.pathS2G)
	for {
		_, more := <-c.polled
		if !more {
			log.V(1).Infof("%v polled channel closed, exiting pollDb routine", c)
			return
		}

		for tblPath, gnmiPath := range c.pathS2G {
			jv, err := tableData2Json(&tblPath)
			if err != nil {
				return
			}

			spbv := &spb.Value{
				Path:         gnmiPath,
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: false,
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: jv,
					},
				},
			}

			c.q.Put(Value{spbv})
			log.V(5).Infof("Added spbv #%v", spbv)
		}

		c.q.Put(Value{
			&spb.Value{
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: true,
			},
		})
		log.V(2).Infof("Sync done!")
	}
}
