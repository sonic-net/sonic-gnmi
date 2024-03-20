package common_utils

import (
	"fmt"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"sync"
)

type DbSubscriber struct {
	configDB swsscommon.DBConnector
	dbSelect swsscommon.Select
	subscribeTableMap map[string]swsscommon.SubscriberStateTable
	tableData map[string]map[string]map[string]string
	stopChannel chan bool
	tableDataMutex sync.Mutex
}

var pDbSubscriber *DbSubscriber = nil

func ResetDbSubscriber() {
	pDbSubscriber = nil
}

func GetDbSubscriber() *DbSubscriber {
	if pDbSubscriber == nil {
		pDbSubscriber = new(DbSubscriber)
		pDbSubscriber.configDB = swsscommon.NewDBConnector("CONFIG_DB", uint(0), true)
		pDbSubscriber.dbSelect = swsscommon.NewSelect()
		pDbSubscriber.subscribeTableMap = map[string]swsscommon.SubscriberStateTable{}
		pDbSubscriber.tableData = map[string]map[string]map[string]string{}
		pDbSubscriber.stopChannel = make(chan bool)
		pDbSubscriber.tableDataMutex = sync.Mutex{}
	}

	return pDbSubscriber;
}

func (d *DbSubscriber) getKeyFieldMap(table string) map[string]map[string]string {
	keyFieldMap, ok := d.tableData[table]
	if !ok {
		keyFieldMap = map[string]map[string]string{}
		d.tableData[table] = keyFieldMap
	}

	return keyFieldMap
}

func (d *DbSubscriber) getFieldMap(table string, key string) map[string]string {
	keyFieldMap := d.getKeyFieldMap(table)
	fieldMap, ok := keyFieldMap[key]
	if !ok {
		fieldMap = map[string]string{}
		keyFieldMap[key] = fieldMap
	}

	return fieldMap
}

func (d *DbSubscriber) pops(subscribeTable swsscommon.SubscriberStateTable) error {
	table := subscribeTable.GetTableName()
	operationQueue := swsscommon.NewKeyOpFieldsValuesQueue()
	subscribeTable.Pops(operationQueue)

	for !operationQueue.Empty() {
		operation := operationQueue.Front()

		// Following code can't move to a new method because SWIG not support std::tuple
		key := swsscommon.TupleHelperGetKey(operation)
		op := swsscommon.TupleHelperGetOp(operation)
		if op == "SET" {
			d.tableDataMutex.Lock()

			// When delete field, if there still field remaining, will receive 'SET' operation with remaining data
			keyFieldMap := d.getKeyFieldMap(table)
			fieldMap := map[string]string{}
			keyFieldMap[key] = fieldMap

			values := swsscommon.TupleHelperGetFieldsValues(operation)
			size := int(values.Size())
			for i:=0; i < size; i++ {
				fvp := values.Get(i)
				field := swsscommon.TupleHelperGetField(fvp)
				value := swsscommon.TupleHelperGetValue(fvp)

				fieldMap[field] = value
			}

			d.tableDataMutex.Unlock()
		} else if op == "DEL" {
			d.tableDataMutex.Lock()

			// When delete field, if no fields remaining, will receive 'DEL' operation
			keyFieldMap := d.getKeyFieldMap(table)
			delete(keyFieldMap, key)

			d.tableDataMutex.Unlock()
		} else {
			return fmt.Errorf("Unsupported operation %s", op)
		}

		operationQueue.Pop_front()
	}

	return nil
}

func (d *DbSubscriber) subscribeTable(table string) {
	tmpTable := swsscommon.NewSubscriberStateTable(d.configDB, table)
	d.subscribeTableMap[table] = tmpTable
	d.dbSelect.AddSelectable(tmpTable.SwigGetSelectable())
	d.pops(tmpTable)
}

func (d *DbSubscriber) updateRoutine() {
	selectHelper := swsscommon.NewSelectHelper()
	for {
		select {
			case <- d.stopChannel:
				return
			default:
				selectHelper.DoSelect(d.dbSelect, 10, true)
				if selectHelper.GetResult() == swsscommon.SelectOBJECT {
					subscribeTable := swsscommon.CastSelectableToSubscriberTableObj(selectHelper.GetSelectable())
					d.pops(subscribeTable)
				}
		}
	}
}

func (d *DbSubscriber) InitializeDbSubscriber() {
	d.subscribeTable("MID_PLANE_BRIDGE")
	d.subscribeTable("DPUS")
	d.subscribeTable("DHCP_SERVER_IPV4_PORT")

	// receive CONFIG_DB change in background routine
	go d.updateRoutine()
}

func (d *DbSubscriber) stopRoutine() {
	d.stopChannel <- true
}

func (d *DbSubscriber) GetData(table string, key string, field string) (string, error) {
	d.tableDataMutex.Lock()
	keyFieldMap, ok := d.tableData[table]
	if !ok {
		d.tableDataMutex.Unlock()
		return "", fmt.Errorf("Table: %s does not exist", table)
	}
	
	fieldMap, ok := keyFieldMap[key]
	if !ok {
		d.tableDataMutex.Unlock()
		return "", fmt.Errorf("Key: %s does not exist in Table: %s", key, table)
	}
	
	value, ok := fieldMap[field]
	if !ok {
		d.tableDataMutex.Unlock()
		return "", fmt.Errorf("Field: %s does not exist in Table: %s, Key: %s", field, table, key)
	}

	d.tableDataMutex.Unlock()
	return value, nil
}