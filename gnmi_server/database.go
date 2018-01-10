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
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// gnmiFullPath builds the full path from the prefix and path.
func gnmiFullPath(prefix, path *gpb.Path) *gpb.Path {

	fullPath := &gpb.Path{Origin: path.Origin}
	if path.GetElement() != nil {
		fullPath.Element = append(prefix.GetElement(), path.GetElement()...)
	}
	if path.GetElem() != nil {
		fullPath.Elem = append(prefix.GetElem(), path.GetElem()...)
	}
	return fullPath
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

// Populate SONiC data path from prefix and subscription path.
func (c *Client) populateDbPath(sublist *gpb.SubscriptionList, tableOnly bool) error {
	var buffer bytes.Buffer

	prefix := sublist.GetPrefix()
	log.V(6).Infof("prefix : %#v SubscribRequest : %#v", sublist)

	// Path target in prefix stores DB name
	c.target = prefix.GetTarget()

	if _, ok := spb.Target_value[c.target]; !ok {
		log.V(1).Infof("Invalid target %s for %s", c.target, c)
		return fmt.Errorf("Invalid target %s for %s", c.target, c)

	}

	separator, err := getTableKeySeparator(c.target)
	if err != nil {
		return err
	}

	subscriptions := sublist.GetSubscription()
	if subscriptions == nil {
		return fmt.Errorf("No Subscription")
	}

	for _, subscription := range subscriptions {
		path := subscription.GetPath()
		fullPath := gnmiFullPath(prefix, path)

		// Element deprecated
		elements := fullPath.GetElement()
		if elements != nil {
			// log.V(2).Infof("path.Element : %#v", elements)
		}

		buffer.Reset()
		elems := fullPath.GetElem()
		if elems != nil {
			if tableOnly && len(elems) != 1 {
				return fmt.Errorf("Invalid table name Elem %s", elems)
			}
			for i, elem := range elems {
				// TODO: Usage of key field
				log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
				if i != 0 {
					buffer.WriteString(separator)
				}
				buffer.WriteString(elem.GetName())
			}
			dbPath := buffer.String()

			// Also save the mapping from sonic path to gNMI path, will need this for populating
			// subsribe response to client.
			c.pathS2G[dbPath] = path
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
	var buffer bytes.Buffer
	var sstables []*swsscommon.SubscriberStateTable

	dbn := spb.Target_value[c.target]
	db := swsscommon.NewDBConnector(int(dbn), swsscommon.DBConnectorDEFAULT_UNIXSOCKET, uint(0))
	defer swsscommon.DeleteDBConnector(db)

	sel := swsscommon.NewSelect()
	defer swsscommon.DeleteSelect(sel)

	// TODO: verify table name and dbn
	for table, _ := range c.pathS2G {
		sstable := swsscommon.NewSubscriberStateTable(db, table)
		defer swsscommon.DeleteSubscriberStateTable(sstable)
		sel.AddSelectable(sstable.SwigGetSelectable())
		sstables = append(sstables, &sstable)
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

				path := (*sstable).GetTableName()
				if _, ok := c.pathS2G[path]; !ok {
					log.V(1).Infof("%v not in gNMI subscription path", path)
					log.Errorf("%v not in gNMI subscription path", path)
				}

				buffer.Reset()
				buffer.WriteString(ko.GetFirst())
				buffer.WriteString(ko.GetSecond())
				for n := vpsr.Size() - 1; n >= 0; n-- {
					fieldpair := vpsr.Get(int(n))
					buffer.WriteString("|")
					buffer.WriteString(fieldpair.GetFirst())
					buffer.WriteString("=")
					buffer.WriteString(fieldpair.GetSecond())
				}
				log.V(6).Infof("path %#v, buffer %v", path, buffer)

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
					Path:      path,
					Timestamp: time.Now().UnixNano(),
					Val: &gpb.TypedValue{
						Value: &gpb.TypedValue_JsonVal{
							JsonVal: jv,
						},
					},
				}

				c.q.Put(Value{spbv})
				log.V(5).Infof("Added spbv #%v", spbv)
			}
		}
	}
}

type tablePath struct {
	tableName string
	tableKey  string
	delimitor string
	table     swsscommon.Table
}

// Support fixed COUNTERS_DB for now
func pollDb(c *Client) {
	var tblPaths []tablePath

	dbn := spb.Target_value[c.target]
	db := swsscommon.NewDBConnector(int(dbn), swsscommon.DBConnectorDEFAULT_UNIXSOCKET, uint(0))
	defer swsscommon.DeleteDBConnector(db)

	separator, _ := getTableKeySeparator(c.target)
	//For polling,  the path could contain both table name and key
	for path, _ := range c.pathS2G {
		var tblPath tablePath
		tblPath.delimitor = separator
		stringSlice := strings.Split(path, tblPath.delimitor)
		tblPath.tableName = stringSlice[0]
		switch len(stringSlice) {
		case 1:
			tblPath.tableKey = ""
		case 2:
			tblPath.tableKey = stringSlice[1]
		case 3:
			tblPath.tableKey = stringSlice[1] + tblPath.delimitor + stringSlice[2]
		default:
			log.Errorf("Invalid db table path %v for polling", path)
			c.Close()
			return
		}
		tbl := swsscommon.NewTable(db, tblPath.tableName)
		defer swsscommon.DeleteTable(tbl)
		tblPath.table = tbl
		tblPaths = append(tblPaths, tblPath)
	}

	// get all keys in DB first
	keys := swsscommon.NewVectorString()
	defer swsscommon.DeleteVectorString(keys)

	dbkeys := []string{}

	for {
		// reset dbkeys slice
		dbkeys = dbkeys[:0]

		_, more := <-c.polled
		if !more {
			log.V(1).Infof("%v polled channel closed, exiting pollDb routine", c)
			return
		}

		for _, tblPath := range tblPaths {
			if tblPath.tableKey == "" {
				// Poll all keys in table
				tblPath.table.GetKeys(keys)
				for i := 0; i < int(keys.Size()); i++ {
					dbkeys = append(dbkeys, keys.Get(i))
				}

				log.V(2).Infof("dbkeys len: %v", len(dbkeys))
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
				}

				log.V(5).Infof("Added idex %v vpsr #%v ", idx, vpsr)
			}

			if len(mpv) == 0 {
				log.V(5).Infof("No data exists for %v ", mpv)
				continue
			}
			jv, err := emitJSON(&mpv)
			if err != nil {
				log.V(2).Infof("emitJSON err %s for  %v", err, mpv)
			}

			var path string
			if tblPath.tableKey == "" {
				path = tblPath.tableName
			} else {
				path = tblPath.tableName + tblPath.delimitor + tblPath.tableKey
			}

			spbv := &spb.Value{
				Path:         path,
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: false,
				Val: &gpb.TypedValue{
					Value: &gpb.TypedValue_JsonVal{
						JsonVal: jv,
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
