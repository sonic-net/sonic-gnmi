package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/google/gnxi/utils/xpath"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/redis/go-redis/v9"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"github.com/sonic-net/sonic-gnmi/test_utils"
)

var testFile string = "/etc/sonic/ut.cp.json"

func JsonEqual(a, b []byte) (bool, error) {
	var j1, j2 interface{}
	var err error
	if err = json.Unmarshal(a, &j1); err != nil {
		return false, err
	}
	if err = json.Unmarshal(b, &j2); err != nil {
		return false, err
	}
	return reflect.DeepEqual(j1, j2), nil
}

func TestJsonClientNegative(t *testing.T) {
	os.Remove(testFile)
	_, err := NewJsonClient(testFile, "")
	if err == nil {
		t.Errorf("Should fail without checkpoint")
	}

	text := "{"
	err = ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	_, err = NewJsonClient(testFile, "")
	if err == nil {
		t.Errorf("Should fail with invalid checkpoint")
	}
}

func TestJsonClientNamespace(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	_, err = NewJsonClient(testFile, "localhost")
	if err == nil {
		t.Errorf("Should fail with unexpected namespace")
	}

	text = `{"localhost": "localhost"}`
	err = ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	_, err = NewJsonClient(testFile, "localhost")
	if err == nil {
		t.Errorf("Should fail with invalid namespace")
	}
}

func TestJsonAdd(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	client, err := NewJsonClient(testFile, "")
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string{
		[]string{
			"DASH_QOS",
		},
		[]string{
			"DASH_QOS",
			"qos_02",
		},
		[]string{
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string{
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
		[]string{
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"0",
		},
	}
	value_list := []string{
		`{"qos_01": {"bw": "54321", "cps": "1000", "flows": "300"}}`,
		`{"bw": "10001", "cps": "1001", "flows": "101"}`,
		`"20001"`,
		`["10.250.0.0", "192.168.3.0", "139.66.72.9"]`,
		`"6.6.6.6"`,
	}
	for i := 0; i < len(path_list); i++ {
		path := path_list[i]
		value := value_list[i]
		err = client.Add(path, value)
		if err != nil {
			t.Errorf("Add %v fail: %v", path, err)
		}
		res, err := client.Get(path)
		if err != nil {
			t.Errorf("Get %v fail: %v", path, err)
		}
		ok, err := JsonEqual([]byte(value), res)
		if err != nil {
			t.Errorf("Compare json fail: %v", err)
			return
		}
		if ok != true {
			t.Errorf("%v and %v do not match", value, string(res))
		}
	}
	path := []string{}
	res, err := client.Get(path)
	if err != nil {
		t.Errorf("Get %v fail: %v", path, err)
	}
	t.Logf("Result %s", string(res))
}

func TestJsonAddNegative(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	client, err := NewJsonClient(testFile, "")
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string{
		[]string{
			"DASH_QOS",
		},
		[]string{
			"DASH_QOS",
			"qos_02",
		},
		[]string{
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string{
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
		[]string{
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"0",
		},
		[]string{
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"abc",
		},
		[]string{
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"100",
		},
	}
	value_list := []string{
		`{"qos_01": {"bw": "54321", "cps": "1000", "flows": "300"}`,
		`{"bw": "10001", "cps": "1001", "flows": "101"`,
		`20001`,
		`["10.250.0.0", "192.168.3.0", "139.66.72.9"`,
		`"6.6.6.6`,
		`"6.6.6.6"`,
		`"6.6.6.6"`,
	}
	for i := 0; i < len(path_list); i++ {
		path := path_list[i]
		value := value_list[i]
		err = client.Add(path, value)
		if err == nil {
			t.Errorf("Add %v should fail: %v", path, err)
		}
	}
}

func TestJsonReplace(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	client, err := NewJsonClient(testFile, "")
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string{
		[]string{
			"DASH_QOS",
		},
		[]string{
			"DASH_QOS",
			"qos_02",
		},
		[]string{
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string{
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
		[]string{
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"0",
		},
	}
	value_list := []string{
		`{"qos_01": {"bw": "54321", "cps": "1000", "flows": "300"}}`,
		`{"bw": "10001", "cps": "1001", "flows": "101"}`,
		`"20001"`,
		`["10.250.0.0", "192.168.3.0", "139.66.72.9"]`,
		`"6.6.6.6"`,
	}
	replace_value_list := []string{
		`{"qos_01": {"bw": "12345", "cps": "2000", "flows": "500"}}`,
		`{"bw": "20001", "cps": "2002", "flows": "300"}`,
		`"6666"`,
		`["10.250.0.1", "192.168.3.1", "139.66.72.10"]`,
		`"8.8.8.8"`,
	}
	for i := 0; i < len(path_list); i++ {
		path := path_list[i]
		value := value_list[i]
		replace_value := replace_value_list[i]
		err = client.Add(path, value)
		if err != nil {
			t.Errorf("Add %v fail: %v", path, err)
		}
		err = client.Replace(path, replace_value)
		if err != nil {
			t.Errorf("Replace %v fail: %v", path, err)
		}
		res, err := client.Get(path)
		if err != nil {
			t.Errorf("Get %v fail: %v", path, err)
		}
		ok, err := JsonEqual([]byte(replace_value), res)
		if err != nil {
			t.Errorf("Compare json fail: %v", err)
			return
		}
		if ok != true {
			t.Errorf("%v and %v do not match", replace_value, string(res))
		}
	}
	path := []string{}
	res, err := client.Get(path)
	if err != nil {
		t.Errorf("Get %v fail: %v", path, err)
	}
	t.Logf("Result %s", string(res))
}

func TestJsonRemove(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	client, err := NewJsonClient(testFile, "")
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string{
		[]string{
			"DASH_QOS",
		},
		[]string{
			"DASH_QOS",
			"qos_02",
		},
		[]string{
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string{
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
		[]string{
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"0",
		},
	}
	value_list := []string{
		`{"qos_01": {"bw": "54321", "cps": "1000", "flows": "300"}}`,
		`{"bw": "10001", "cps": "1001", "flows": "101"}`,
		`"20001"`,
		`["10.250.0.0", "192.168.3.0", "139.66.72.9"]`,
		`"6.6.6.6"`,
	}
	for i := 0; i < len(path_list); i++ {
		path := path_list[i]
		value := value_list[i]
		err = client.Add(path, value)
		if err != nil {
			t.Errorf("Add %v fail: %v", path, err)
		}
		err = client.Remove(path)
		if err != nil {
			t.Errorf("Remove %v fail: %v", path, err)
		}
		_, err := client.Get(path)
		if err == nil {
			t.Errorf("Get %v should fail: %v", path, err)
		}
	}
}

func TestJsonRemoveNegative(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	client, err := NewJsonClient(testFile, "")
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string{
		[]string{
			"DASH_QOS",
		},
		[]string{
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
	}
	value_list := []string{
		`{"qos_01": {"bw": "54321", "cps": "1000", "flows": "300"}}`,
		`["10.250.0.0", "192.168.3.0", "139.66.72.9"]`,
	}
	for i := 0; i < len(path_list); i++ {
		path := path_list[i]
		value := value_list[i]
		err = client.Add(path, value)
		if err != nil {
			t.Errorf("Add %v fail: %v", path, err)
		}
	}

	remove_list := [][]string{
		[]string{
			"DASH_QOS",
			"qos_02",
		},
		[]string{
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string{
			"DASH_VNET",
			"vnet001",
			"address_spaces",
			"abc",
		},
		[]string{
			"DASH_VNET",
			"vnet001",
			"address_spaces",
			"100",
		},
	}
	for i := 0; i < len(remove_list); i++ {
		path := remove_list[i]
		err = client.Remove(path)
		if err == nil {
			t.Errorf("Remove %v should fail: %v", path, err)
		}
	}
}

func TestParseDatabase(t *testing.T) {
	var test_paths []*gnmipb.Path
	var prefix *gnmipb.Path
	var err error

	client := MixedDbClient{
		namespace_cnt: 1,
		container_cnt: 1,
	}
	_, _, err = client.ParseDatabase(prefix, test_paths)
	if err == nil {
		t.Errorf("ParseDatabase should fail for empty path: %v", err)
	}

	test_target := "TEST_DB"
	path, err := xpath.ToGNMIPath("sonic-db:" + test_target + "/localhost" + "/VLAN")
	test_paths = append(test_paths, path)
	target, dbkey1, err := client.ParseDatabase(prefix, test_paths)
	if err != nil {
		t.Errorf("ParseDatabase failed to get target: %v", err)
	}
	defer swsscommon.DeleteSonicDBKey(dbkey1)
	if target != test_target {
		t.Errorf("ParseDatabase return wrong target: %v", target)
	}

	// Smartswitch with multiple asic NPU
	client = MixedDbClient{
		namespace_cnt: 2,
		container_cnt: 2,
	}

	test_target = "TEST_DB"
	path, err = xpath.ToGNMIPath("sonic-db:" + test_target + "/localhost" + "/VLAN")
	test_paths = append(test_paths, path)
	target, dbkey2, err := client.ParseDatabase(prefix, test_paths)
	if err != nil {
		t.Errorf("ParseDatabase failed to get target: %v", err)
	}
	defer swsscommon.DeleteSonicDBKey(dbkey2)
	if target != test_target {
		t.Errorf("ParseDatabase return wrong target: %v", target)
	}

	test_target = "TEST_DB"
	path, err = xpath.ToGNMIPath("sonic-db:" + test_target + "/xyz" + "/VLAN")
	test_paths = append(test_paths, path)
	target, _, err = client.ParseDatabase(prefix, test_paths)
	if err == nil {
		t.Errorf("ParseDatabase should fail for namespace/container")
	}
}

func TestSubscribeInternal(t *testing.T) {
	// Test StreamRun
	{
		pq := queue.NewPriorityQueue(1, false)
		w := sync.WaitGroup{}
		stop := make(chan struct{}, 1)
		client := MixedDbClient{}
		req := gnmipb.SubscriptionList{
			Subscription: nil,
		}
		path, _ := xpath.ToGNMIPath("/abc/dummy")
		client.paths = append(client.paths, path)
		client.dbkey = swsscommon.NewSonicDBKey()
		defer swsscommon.DeleteSonicDBKey(client.dbkey)
		RedisDbMap = nil
		stop <- struct{}{}
		w.Add(1)
		client.StreamRun(pq, stop, &w, &req)
	}

	// Test streamSampleSubscription
	{
		pq := queue.NewPriorityQueue(1, false)
		w := sync.WaitGroup{}
		client := MixedDbClient{}
		sub := gnmipb.Subscription{
			SampleInterval: 1000,
		}
		client.q = pq
		client.w = &w
		client.w.Add(1)
		client.synced.Add(1)
		client.streamSampleSubscription(&sub, false)
	}

	// Test streamSampleSubscription
	{
		pq := queue.NewPriorityQueue(1, false)
		w := sync.WaitGroup{}
		client := MixedDbClient{}
		path, _ := xpath.ToGNMIPath("/abc/dummy")
		sub := gnmipb.Subscription{
			SampleInterval: 1000000000,
			Path:           path,
		}
		RedisDbMap = nil
		client.q = pq
		client.w = &w
		client.w.Add(1)
		client.synced.Add(1)
		client.dbkey = swsscommon.NewSonicDBKey()
		defer swsscommon.DeleteSonicDBKey(client.dbkey)
		client.streamSampleSubscription(&sub, false)
	}

	// Test dbFieldSubscribe
	{
		pq := queue.NewPriorityQueue(1, false)
		w := sync.WaitGroup{}
		client := MixedDbClient{}
		path, _ := xpath.ToGNMIPath("/abc/dummy")
		RedisDbMap = nil
		client.q = pq
		client.w = &w
		client.w.Add(1)
		client.synced.Add(1)
		client.dbkey = swsscommon.NewSonicDBKey()
		defer swsscommon.DeleteSonicDBKey(client.dbkey)
		client.dbFieldSubscribe(path, true, time.Second)
	}

	// Test dbTableKeySubscribe
	{
		pq := queue.NewPriorityQueue(1, false)
		w := sync.WaitGroup{}
		client := MixedDbClient{}
		path, _ := xpath.ToGNMIPath("/abc/dummy")
		RedisDbMap = nil
		client.q = pq
		client.w = &w
		client.w.Add(1)
		client.synced.Add(1)
		client.dbkey = swsscommon.NewSonicDBKey()
		defer swsscommon.DeleteSonicDBKey(client.dbkey)
		client.dbTableKeySubscribe(path, time.Second, true)
	}
}

func mockGetFunc() ([]byte, error) {
	return nil, errors.New("mock error")
}

func TestNonDbClientGetError(t *testing.T) {
	var gnmipbPath *gnmipb.Path = &gnmipb.Path{
		Element: []string{"mockPath"},
	}

	path2Getter := map[*gnmipb.Path]dataGetFunc{
		gnmipbPath: mockGetFunc,
	}

	// Create a NonDbClient with the mocked dataGetFunc
	client := &NonDbClient{
		path2Getter: path2Getter,
	}

	var w *sync.WaitGroup
	_, err := client.Get(w)
	if errors.Is(err, errors.New("mock error")) {
		t.Errorf("Expected error from NonDbClient.Get, got nil")
	}
}

/*
Helper method for receive data from ZmqConsumerStateTable

	consumer: Receive data from consumer
	return:
		true: data received
		false: not receive any data after retry
*/
func ReceiveFromZmq(consumer swsscommon.ZmqConsumerStateTable) bool {
	receivedData := swsscommon.NewKeyOpFieldsValuesQueue()
	defer swsscommon.DeleteKeyOpFieldsValuesQueue(receivedData)
	retry := 0
	for {
		// sender's ZMQ may disconnect, wait and retry for reconnect
		time.Sleep(time.Duration(1000) * time.Millisecond)
		consumer.Pops(receivedData)
		if receivedData.Size() == 0 {
			retry++
			if retry >= 10 {
				return false
			}
		} else {
			return true
		}
	}
}

func TestZmqReconnect(t *testing.T) {
	// create ZMQ server
	db := swsscommon.NewDBConnector(APPL_DB_NAME, SWSS_TIMEOUT, false)
	zmqServer := swsscommon.NewZmqServer("tcp://*:1234")
	var TEST_TABLE string = "DASH_ROUTE"
	consumer := swsscommon.NewZmqConsumerStateTable(db, TEST_TABLE, zmqServer)

	// create ZMQ client side
	zmqAddress := "tcp://127.0.0.1:1234"
	client := MixedDbClient{
		applDB:      swsscommon.NewDBConnector(APPL_DB_NAME, SWSS_TIMEOUT, false),
		tableMap:    map[string]swsscommon.ProducerStateTable{},
		zmqTableMap: map[string]swsscommon.ZmqProducerStateTable{},
		zmqClient:   swsscommon.NewZmqClient(zmqAddress),
	}

	data := map[string]string{}
	var TEST_KEY string = "TestKey"
	client.DbSetTable(TEST_TABLE, TEST_KEY, data)
	if !ReceiveFromZmq(consumer) {
		t.Errorf("Receive data from ZMQ failed")
	}

	// recreate ZMQ server to trigger re-connect
	swsscommon.DeleteZmqConsumerStateTable(consumer)
	swsscommon.DeleteZmqServer(zmqServer)
	zmqServer = swsscommon.NewZmqServer("tcp://*:1234")
	consumer = swsscommon.NewZmqConsumerStateTable(db, TEST_TABLE, zmqServer)

	// send data again, client will reconnect
	client.DbSetTable(TEST_TABLE, TEST_KEY, data)
	if !ReceiveFromZmq(consumer) {
		t.Errorf("Receive data from ZMQ failed")
	}

	client.Close()
	swsscommon.DeleteZmqConsumerStateTable(consumer)
	swsscommon.DeleteZmqClient(client.zmqClient)
	swsscommon.DeleteZmqServer(zmqServer)
	swsscommon.DeleteDBConnector(db)

	for _, client := range zmqClientMap {
		swsscommon.DeleteZmqClient(client)
	}
}

func TestRetryHelper(t *testing.T) {
	// create ZMQ server
	zmqServer := swsscommon.NewZmqServer("tcp://*:2234")

	// create ZMQ client side
	zmqAddress := "tcp://127.0.0.1:2234"
	zmqClient := swsscommon.NewZmqClient(zmqAddress)
	returnError := true
	exeCount := 0
	RetryHelper(
		zmqClient,
		func() (err error) {
			exeCount++
			if returnError {
				returnError = false
				return fmt.Errorf("zmq connection break, endpoint: tcp://127.0.0.1:2234")
			}
			return nil
		})

	if exeCount == 1 {
		t.Errorf("RetryHelper does not retry")
	}

	if exeCount > 2 {
		t.Errorf("RetryHelper retry too much")
	}

	swsscommon.DeleteZmqClient(zmqClient)
	swsscommon.DeleteZmqServer(zmqServer)
}

func TestGetDpuAddress(t *testing.T) {
	// prepare data according to design doc
	// Design doc: https://github.com/sonic-net/SONiC/blob/master/doc/smart-switch/ip-address-assigment/smart-switch-ip-address-assignment.md?plain=1

	if !swsscommon.SonicDBConfigIsInit() {
		swsscommon.SonicDBConfigInitialize()
	}

	var configDb = swsscommon.NewDBConnector("CONFIG_DB", uint(0), true)
	configDb.Flushdb()

	var midPlaneTable = swsscommon.NewTable(configDb, "MID_PLANE_BRIDGE")
	var dpusTable = swsscommon.NewTable(configDb, "DPUS")
	var dhcpPortTable = swsscommon.NewTable(configDb, "DHCP_SERVER_IPV4_PORT")

	// test get DPU address when database not ready
	address, err := getDpuAddress("dpu0")
	if err == nil {
		t.Errorf("get DPU address should failed: %v, but get %s", err, address)
	}

	midPlaneTable.Hset("GLOBAL", "bridge", "bridge-midplane")
	dpusTable.Hset("dpu0", "midplane_interface", "dpu0")

	// test get DPU address when DHCP_SERVER_IPV4_PORT table not ready
	address, err = getDpuAddress("dpu0")
	if err == nil {
		t.Errorf("get DPU address should failed: %v, but get %s", err, address)
	}

	dhcpPortTable.Hset("bridge-midplane|dpu0", "invalidfield", "")

	// test get DPU address when DHCP_SERVER_IPV4_PORT table broken
	address, err = getDpuAddress("dpu0")
	if err == nil {
		t.Errorf("get DPU address should failed: %v, but get %s", err, address)
	}

	dhcpPortTable.Hset("bridge-midplane|dpu0", "ips@", "127.0.0.2,127.0.0.1")

	// test get valid DPU address
	address, err = getDpuAddress("dpu0")
	if err != nil {
		t.Errorf("get DPU address failed: %v", err)
	}

	if address != "127.0.0.2" {
		t.Errorf("get DPU address failed: %v", address)
	}

	// test get invalid DPU address
	address, err = getDpuAddress("dpu_x")
	if err == nil {
		t.Errorf("get invalid DPU address failed")
	}

	if address != "" {
		t.Errorf("get invalid DPU address failed: %v", address)
	}

	// test get ZMQ address
	address, err = getZmqAddress("dpu0", "1234")
	if address != "tcp://127.0.0.2:1234" {
		t.Errorf("get invalid DPU address failed")
	}

	address, err = getZmqAddress("dpu0", "")
	if err == nil {
		t.Errorf("get invalid ZMQ address failed")
	}

	address, err = getZmqAddress("", "1234")
	if err == nil {
		t.Errorf("get invalid ZMQ address failed")
	}

	swsscommon.DeleteTable(midPlaneTable)
	swsscommon.DeleteTable(dpusTable)
	swsscommon.DeleteTable(dhcpPortTable)
	swsscommon.DeleteDBConnector(configDb)
}

func TestGetZmqClient(t *testing.T) {
	if !swsscommon.SonicDBConfigIsInit() {
		swsscommon.SonicDBConfigInitialize()
	}

	var configDb = swsscommon.NewDBConnector("CONFIG_DB", uint(0), true)
	configDb.Flushdb()

	var midPlaneTable = swsscommon.NewTable(configDb, "MID_PLANE_BRIDGE")
	var dpusTable = swsscommon.NewTable(configDb, "DPUS")
	var dhcpPortTable = swsscommon.NewTable(configDb, "DHCP_SERVER_IPV4_PORT")

	midPlaneTable.Hset("GLOBAL", "bridge", "bridge-midplane")
	dpusTable.Hset("dpu0", "midplane_interface", "dpu0")
	dhcpPortTable.Hset("bridge-midplane|dpu0", "ips@", "127.0.0.2,127.0.0.1")

	client, err := getZmqClient("dpu0", "", "")
	if client != nil || err != nil {
		t.Errorf("empty ZMQ port should not get ZMQ client")
	}

	client, err = getZmqClient("dpu0", "1234", "")
	if client == nil {
		t.Errorf("get ZMQ client failed")
	}

	client, err = getZmqClient("", "1234", "")
	if client == nil {
		t.Errorf("get ZMQ client failed")
	}

	err = removeZmqClient(client)
	if err != nil {
		t.Errorf("Remove ZMQ client failed")
	}

	// Remove a removed client should failed
	err = removeZmqClient(client)
	if err == nil {
		t.Errorf("Remove ZMQ client should failed")
	}

	swsscommon.DeleteTable(midPlaneTable)
	swsscommon.DeleteTable(dpusTable)
	swsscommon.DeleteTable(dhcpPortTable)
	swsscommon.DeleteDBConnector(configDb)

	for _, client := range zmqClientMap {
		swsscommon.DeleteZmqClient(client)
	}
}

// saveAndResetTarget2RedisDb saves the current Target2RedisDb map and returns
// a cleanup function that restores it.
func saveAndResetTarget2RedisDb() func() {
	orig := Target2RedisDb
	Target2RedisDb = make(map[string]map[string]*redis.Client)
	return func() { Target2RedisDb = orig }
}

func TestInitRedisDbClients(t *testing.T) {
	ns := ""

	t.Run("SkipUnavailableDb", func(t *testing.T) {
		defer saveAndResetTarget2RedisDb()()

		getDbSockCalls := 0
		patches := gomonkey.ApplyFunc(sdcfg.GetDbAllNamespaces, func() ([]string, error) {
			return []string{ns}, nil
		})
		defer patches.Reset()

		patches.ApplyFunc(sdcfg.GetDbSock, func(dbName string, _ string) (string, error) {
			getDbSockCalls++
			if dbName == "CHASSIS_STATE_DB" {
				return "", fmt.Errorf("database not available")
			}
			return "/var/run/redis/redis.sock", nil
		})

		initRedisDbClients()

		nsMap, ok := Target2RedisDb[ns]
		if !ok {
			t.Fatal("Expected namespace to exist in Target2RedisDb")
		}
		if _, exists := nsMap["CHASSIS_STATE_DB"]; exists {
			t.Error("CHASSIS_STATE_DB should have been skipped")
		}
		for _, dbName := range []string{"CONFIG_DB", "APPL_DB", "STATE_DB"} {
			if _, exists := nsMap[dbName]; !exists {
				t.Errorf("Expected %s to be initialized", dbName)
			}
		}
		if getDbSockCalls < 2 {
			t.Errorf("Expected GetDbSock to be called multiple times, got %d", getDbSockCalls)
		}
	})

	t.Run("AllDbsAvailable", func(t *testing.T) {
		defer saveAndResetTarget2RedisDb()()

		patches := gomonkey.ApplyFunc(sdcfg.GetDbAllNamespaces, func() ([]string, error) {
			return []string{ns}, nil
		})
		defer patches.Reset()

		patches.ApplyFunc(sdcfg.GetDbSock, func(_ string, _ string) (string, error) {
			return "/var/run/redis/redis.sock", nil
		})

		initRedisDbClients()

		nsMap, ok := Target2RedisDb[ns]
		if !ok {
			t.Fatal("Expected namespace to exist in Target2RedisDb")
		}
		for dbName := range spb.Target_value {
			if dbName == "OTHERS" {
				continue
			}
			if _, exists := nsMap[dbName]; !exists {
				t.Errorf("Expected %s to be initialized", dbName)
			}
		}
		if _, exists := nsMap["OTHERS"]; exists {
			t.Error("OTHERS should not be initialized")
		}
	})

	t.Run("GetDbAllNamespacesFails", func(t *testing.T) {
		defer saveAndResetTarget2RedisDb()()

		patches := gomonkey.ApplyFunc(sdcfg.GetDbAllNamespaces, func() ([]string, error) {
			return nil, fmt.Errorf("namespace retrieval failed")
		})
		defer patches.Reset()

		initRedisDbClients()

		if len(Target2RedisDb) != 0 {
			t.Errorf("Expected Target2RedisDb to be empty, got %d entries", len(Target2RedisDb))
		}
	})

	t.Run("MultipleDbsFail", func(t *testing.T) {
		defer saveAndResetTarget2RedisDb()()

		failingDbs := map[string]bool{
			"CHASSIS_STATE_DB": true,
			"ASIC_DB":          true,
		}

		patches := gomonkey.ApplyFunc(sdcfg.GetDbAllNamespaces, func() ([]string, error) {
			return []string{ns}, nil
		})
		defer patches.Reset()

		patches.ApplyFunc(sdcfg.GetDbSock, func(dbName string, _ string) (string, error) {
			if failingDbs[dbName] {
				return "", fmt.Errorf("DB %s not found", dbName)
			}
			return "/var/run/redis/redis.sock", nil
		})

		initRedisDbClients()

		nsMap, ok := Target2RedisDb[ns]
		if !ok {
			t.Fatal("Expected namespace to exist in Target2RedisDb")
		}
		for dbName := range failingDbs {
			if _, exists := nsMap[dbName]; exists {
				t.Errorf("%s should have been skipped", dbName)
			}
		}
		for _, dbName := range []string{"CONFIG_DB", "APPL_DB", "STATE_DB"} {
			if _, exists := nsMap[dbName]; !exists {
				t.Errorf("Expected %s to be initialized despite other DBs failing", dbName)
			}
		}
	})
}

func TestUseRedisTcpClient(t *testing.T) {
	ns := ""

	t.Run("Disabled", func(t *testing.T) {
		origFlag := UseRedisLocalTcpPort
		UseRedisLocalTcpPort = false
		defer func() { UseRedisLocalTcpPort = origFlag }()

		err := useRedisTcpClient()
		if err != nil {
			t.Fatalf("Expected nil when UseRedisLocalTcpPort is false, got: %v", err)
		}
	})

	t.Run("SkipUnavailableDb", func(t *testing.T) {
		defer saveAndResetTarget2RedisDb()()
		origFlag := UseRedisLocalTcpPort
		UseRedisLocalTcpPort = true
		defer func() { UseRedisLocalTcpPort = origFlag }()

		patches := gomonkey.ApplyFunc(sdcfg.GetDbAllNamespaces, func() ([]string, error) {
			return []string{ns}, nil
		})
		defer patches.Reset()

		patches.ApplyFunc(sdcfg.GetDbTcpAddr, func(dbName string, _ string) (string, error) {
			if dbName == "CHASSIS_STATE_DB" {
				return "", fmt.Errorf("DB CHASSIS_STATE_DB not found")
			}
			return "127.0.0.1:6379", nil
		})

		err := useRedisTcpClient()
		if err != nil {
			t.Fatalf("Expected no error when skipping unavailable DB, got: %v", err)
		}

		nsMap, ok := Target2RedisDb[ns]
		if !ok {
			t.Fatal("Expected namespace to exist in Target2RedisDb")
		}
		if _, exists := nsMap["CHASSIS_STATE_DB"]; exists {
			t.Error("CHASSIS_STATE_DB should have been skipped in TCP mode")
		}
		for _, dbName := range []string{"CONFIG_DB", "APPL_DB", "STATE_DB"} {
			if _, exists := nsMap[dbName]; !exists {
				t.Errorf("Expected %s to be initialized in TCP mode", dbName)
			}
		}
	})

	t.Run("GetDbAllNamespacesFails", func(t *testing.T) {
		defer saveAndResetTarget2RedisDb()()
		origFlag := UseRedisLocalTcpPort
		UseRedisLocalTcpPort = true
		defer func() { UseRedisLocalTcpPort = origFlag }()

		patches := gomonkey.ApplyFunc(sdcfg.GetDbAllNamespaces, func() ([]string, error) {
			return nil, fmt.Errorf("namespace error")
		})
		defer patches.Reset()

		err := useRedisTcpClient()
		if err == nil {
			t.Fatal("Expected error when GetDbAllNamespaces fails")
		}
		if len(Target2RedisDb) != 0 {
			t.Errorf("Expected Target2RedisDb to be empty, got %d entries", len(Target2RedisDb))
		}
	})
}

// setupTestTarget2RedisDb populates Target2RedisDb with real TCP Redis clients
// using the same gomonkey pattern as TestUseRedisTcpClient.
func setupTestTarget2RedisDb(t *testing.T) func() {
	t.Helper()
	restore := saveAndResetTarget2RedisDb()
	origFlag := UseRedisLocalTcpPort
	UseRedisLocalTcpPort = true

	ns := ""
	patches := gomonkey.ApplyFunc(sdcfg.GetDbAllNamespaces, func() ([]string, error) {
		return []string{ns}, nil
	})
	patches.ApplyFunc(sdcfg.GetDbTcpAddr, func(dbName string, _ string) (string, error) {
		return "127.0.0.1:6379", nil
	})

	err := useRedisTcpClient()
	patches.Reset()
	UseRedisLocalTcpPort = origFlag

	if err != nil {
		restore()
		t.Fatalf("useRedisTcpClient failed: %v", err)
	}

	return func() {
		for _, nsMap := range Target2RedisDb {
			for _, rc := range nsMap {
				rc.FlushDB(context.Background())
			}
		}
		restore()
	}
}

func TestParsePath(t *testing.T) {
	t.Run("TwoElements_TableOnly", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "STATE_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("parsePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableName != "NEIGH_STATE_TABLE" {
				t.Errorf("expected tableName NEIGH_STATE_TABLE, got %v", tblPaths[0].tableName)
			}
			if tblPaths[0].tableKey != "" {
				t.Errorf("expected empty tableKey, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("ThreeElements_STATE_DB", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "STATE_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}, {Name: "10.0.0.57"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("parsePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "10.0.0.57" {
				t.Errorf("expected tableKey 10.0.0.57, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("ThreeElements_APPL_DB_EscapedRoute", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "APPL_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "ROUTE_TABLE"}, {Name: "0.0.0.0/0"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("parsePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "0.0.0.0/0" {
				t.Errorf("expected tableKey 0.0.0.0/0, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("FourElements_CompositeKey", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "STATE_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "VLAN_MEMBER_TABLE"}, {Name: "Vlan100"}, {Name: "Ethernet0"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("parsePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "Vlan100|Ethernet0" {
				t.Errorf("expected composite key Vlan100|Ethernet0, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("FiveElements_CompositeKeyAndField", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "STATE_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "VLAN_MEMBER_TABLE"}, {Name: "Vlan100"}, {Name: "Ethernet0"}, {Name: "tagging_mode"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("parsePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "Vlan100|Ethernet0" {
				t.Errorf("expected key Vlan100|Ethernet0, got %v", tblPaths[0].tableKey)
			}
			if tblPaths[0].field != "tagging_mode" {
				t.Errorf("expected field tagging_mode, got %v", tblPaths[0].field)
			}
		}
	})

	t.Run("InvalidPath_TooManyElements", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "STATE_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "A"}, {Name: "B"}, {Name: "C"}, {Name: "D"}, {Name: "E"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err == nil {
			t.Errorf("expected error for 6-element path")
		}
	})

	t.Run("COUNTERS_DB_VirtualPath_Ethernet68", func(t *testing.T) {
		cleanup := setupTestTarget2RedisDb(t)
		defer cleanup()

		ns := ""
		rclient := Target2RedisDb[ns]["COUNTERS_DB"]
		rclient.HSet(context.Background(), "COUNTERS_PORT_NAME_MAP", "Ethernet68", "oid:0x1000000000039")

		configDb := Target2RedisDb[ns]["CONFIG_DB"]
		configDb.HSet(context.Background(), "PORT|Ethernet68", "alias", "Ethernet68", "index", "68")
		defer configDb.Del(context.Background(), "PORT|Ethernet68")

		os.Setenv("UNIT_TEST", "1")
		ClearMappings()
		os.Unsetenv("UNIT_TEST")

		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "COUNTERS_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet68"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("parsePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if !tblPaths[0].isVirtualPath {
				t.Errorf("expected isVirtualPath=true for COUNTERS/Ethernet68")
			}
			if tblPaths[0].tableKey != "oid:0x1000000000039" {
				t.Errorf("expected tableKey oid:0x1000000000039, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("COUNTERS_DB_VirtualPath_EthernetWildcard", func(t *testing.T) {
		cleanup := setupTestTarget2RedisDb(t)
		defer cleanup()

		ns := ""
		rclient := Target2RedisDb[ns]["COUNTERS_DB"]
		rclient.HSet(context.Background(), "COUNTERS_PORT_NAME_MAP", "Ethernet68", "oid:0x1000000000039")
		rclient.HSet(context.Background(), "COUNTERS_PORT_NAME_MAP", "Ethernet1", "oid:0x1000000000003")

		configDb := Target2RedisDb[ns]["CONFIG_DB"]
		configDb.HSet(context.Background(), "PORT|Ethernet68", "alias", "Ethernet68", "index", "68")
		configDb.HSet(context.Background(), "PORT|Ethernet1", "alias", "Ethernet1", "index", "1")
		defer configDb.Del(context.Background(), "PORT|Ethernet68", "PORT|Ethernet1")

		os.Setenv("UNIT_TEST", "1")
		ClearMappings()
		os.Unsetenv("UNIT_TEST")

		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "COUNTERS_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet*"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("parsePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if len(tblPaths) < 2 {
				t.Fatalf("expected at least 2 tablePaths for Ethernet*, got %d", len(tblPaths))
			}
			for _, tp := range tblPaths {
				if !tp.isVirtualPath {
					t.Errorf("expected isVirtualPath=true for all wildcard paths")
				}
			}
		}
	})
}

func TestProbePathElements(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()
	ns := ""

	t.Run("StateDB_KeyExists_StaysAsKey", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["STATE_DB"]
		rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "state", "Established")
		defer rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}},
		}
		if err := probePathElements(&pathG2S); err != nil {
			t.Fatalf("probePathElements failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "10.0.0.57" {
				t.Errorf("expected tableKey 10.0.0.57, got %v", tblPaths[0].tableKey)
			}
			if tblPaths[0].field != "" {
				t.Errorf("expected empty field, got %v", tblPaths[0].field)
			}
		}
	})

	t.Run("StateDB_FieldExists_ReclassifiedAsField", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["STATE_DB"]
		rclient.HSet(context.Background(), "SWITCH_CAPABILITY", "test_field", "test_value")
		defer rclient.Del(context.Background(), "SWITCH_CAPABILITY")

		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "SWITCH_CAPABILITY", tableKey: "test_field", delimitor: "|"}},
		}
		if err := probePathElements(&pathG2S); err != nil {
			t.Fatalf("probePathElements failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].field != "test_field" {
				t.Errorf("expected field test_field, got %v", tblPaths[0].field)
			}
			if tblPaths[0].tableKey != "" {
				t.Errorf("expected empty tableKey, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("StateDB_NeitherExists_DefaultsToKey", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|"}},
		}
		if err := probePathElements(&pathG2S); err != nil {
			t.Fatalf("probePathElements failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "10.0.0.99" {
				t.Errorf("expected tableKey 10.0.0.99, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("APPL_DB_RouteExists_StaysAsKey", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["APPL_DB"]
		rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "10.0.0.1")
		defer rclient.Del(context.Background(), "ROUTE_TABLE:0.0.0.0/0")

		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "APPL_DB", tableName: "ROUTE_TABLE", tableKey: "0.0.0.0/0", delimitor: ":"}},
		}
		if err := probePathElements(&pathG2S); err != nil {
			t.Fatalf("probePathElements failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "0.0.0.0/0" {
				t.Errorf("expected tableKey 0.0.0.0/0, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("APPL_DB_NonExistentRoute_DefaultsToKey", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "APPL_DB", tableName: "ROUTE_TABLE", tableKey: "0.0.0.0/0", delimitor: ":"}},
		}
		if err := probePathElements(&pathG2S); err != nil {
			t.Fatalf("probePathElements failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "0.0.0.0/0" {
				t.Errorf("expected tableKey 0.0.0.0/0, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("VirtualPath_Skipped", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "COUNTERS_DB", tableName: "COUNTERS", tableKey: "oid:0x123", delimitor: ":", isVirtualPath: true}},
		}
		if err := probePathElements(&pathG2S); err != nil {
			t.Fatalf("probePathElements failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "oid:0x123" {
				t.Errorf("virtual path should not be modified")
			}
		}
	})

	t.Run("CompositeKey_Exists", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["STATE_DB"]
		rclient.HSet(context.Background(), "VLAN_MEMBER_TABLE|Vlan100|Ethernet0", "tagging_mode", "tagged")
		defer rclient.Del(context.Background(), "VLAN_MEMBER_TABLE|Vlan100|Ethernet0")

		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "VLAN_MEMBER_TABLE", tableKey: "Vlan100|Ethernet0", delimitor: "|"}},
		}
		if err := probePathElements(&pathG2S); err != nil {
			t.Fatalf("probePathElements failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "Vlan100|Ethernet0" {
				t.Errorf("expected composite key Vlan100|Ethernet0, got %v", tblPaths[0].tableKey)
			}
			if tblPaths[0].field != "" {
				t.Errorf("expected empty field, got %v", tblPaths[0].field)
			}
		}
	})

	t.Run("CompositeKey_SecondPartIsField", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["STATE_DB"]
		rclient.HSet(context.Background(), "VLAN_MEMBER_TABLE|Vlan100", "tagging_mode", "tagged")
		defer rclient.Del(context.Background(), "VLAN_MEMBER_TABLE|Vlan100")

		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "VLAN_MEMBER_TABLE", tableKey: "Vlan100|tagging_mode", delimitor: "|"}},
		}
		if err := probePathElements(&pathG2S); err != nil {
			t.Fatalf("probePathElements failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "Vlan100" {
				t.Errorf("expected tableKey Vlan100, got %v", tblPaths[0].tableKey)
			}
			if tblPaths[0].field != "tagging_mode" {
				t.Errorf("expected field tagging_mode, got %v", tblPaths[0].field)
			}
		}
	})

	t.Run("MissingRedisClient_ReturnsError", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: "bad_ns", dbName: "NONEXISTENT_DB", tableName: "TABLE", tableKey: "key", delimitor: "|"}},
		}
		err := probePathElements(&pathG2S)
		if err == nil {
			t.Errorf("expected error for missing Redis client")
		}
	})
}

func TestPopulateDbtablePath(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()

	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "state", "Established")
	defer rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

	t.Run("ExistingKey", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "STATE_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}, {Name: "10.0.0.57"}}}
		err := resolveSubscribePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("resolveSubscribePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "10.0.0.57" {
				t.Errorf("expected tableKey 10.0.0.57, got %v", tblPaths[0].tableKey)
			}
		}
	})

	t.Run("NonExistentKey_DefaultsToKey", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "STATE_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}, {Name: "10.0.0.99"}}}
		err := resolveSubscribePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("resolveSubscribePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if tblPaths[0].tableKey != "10.0.0.99" {
				t.Errorf("expected tableKey 10.0.0.99, got %v", tblPaths[0].tableKey)
			}
		}
	})
}

func TestPopulateAllDbtablePath(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()

	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "state", "Established")
	defer rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

	pathG2S := make(map[*gnmipb.Path][]tablePath)
	prefix := &gnmipb.Path{Target: "STATE_DB"}
	paths := []*gnmipb.Path{
		{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}, {Name: "10.0.0.57"}}},
		{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}, {Name: "10.0.0.99"}}},
	}
	err := populateAllDbtablePath(prefix, paths, &pathG2S)
	if err != nil {
		t.Fatalf("populateAllDbtablePath failed: %v", err)
	}
	if len(pathG2S) != 2 {
		t.Errorf("expected 2 entries, got %d", len(pathG2S))
	}
}

func TestValidatePath(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()
	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "state", "Established")

	t.Run("KeyExists_NoError", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}},
		}
		if err := ensureKeysExistInRedis(&pathG2S); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("KeyMissing_ReturnsError", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|"}},
		}
		if err := ensureKeysExistInRedis(&pathG2S); err == nil {
			t.Errorf("expected error for missing key")
		}
	})

	t.Run("EmptyTableKey_Skipped", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "", delimitor: "|"}},
		}
		if err := ensureKeysExistInRedis(&pathG2S); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("VirtualPath_Skipped", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: ns, dbName: "COUNTERS_DB", tableName: "COUNTERS", tableKey: "oid:0xDEAD", delimitor: ":", isVirtualPath: true}},
		}
		if err := ensureKeysExistInRedis(&pathG2S); err != nil {
			t.Errorf("expected no error for virtual path, got %v", err)
		}
	})

	t.Run("MissingRedisClient_ReturnsError", func(t *testing.T) {
		pathG2S := map[*gnmipb.Path][]tablePath{
			{}: {{dbNamespace: "bad_ns", dbName: "NONEXISTENT_DB", tableName: "TABLE", tableKey: "key", delimitor: "|"}},
		}
		if err := ensureKeysExistInRedis(&pathG2S); err == nil {
			t.Errorf("expected error for missing Redis client")
		}
	})
}

func TestTableData2Msi_SkipsEmptyData(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()
	ns := ""

	t.Run("STATE_DB_NonExistentKey_Skipped", func(t *testing.T) {
		msi := make(map[string]interface{})
		tblPath := tablePath{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|"}
		err := TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(msi) != 0 {
			t.Errorf("expected empty msi, got %v", msi)
		}
	})

	t.Run("STATE_DB_ExistingKey_ReturnsData", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["STATE_DB"]
		rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP", "state", "Established")
		defer rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

		msi := make(map[string]interface{})
		tblPath := tablePath{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}
		err := TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(msi) == 0 {
			t.Errorf("expected data, got empty msi")
		}
	})

	t.Run("APPL_DB_NonExistentRoute_Skipped", func(t *testing.T) {
		msi := make(map[string]interface{})
		tblPath := tablePath{dbNamespace: ns, dbName: "APPL_DB", tableName: "ROUTE_TABLE", tableKey: "0.0.0.0/0", delimitor: ":"}
		err := TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(msi) != 0 {
			t.Errorf("expected empty msi, got %v", msi)
		}
	})

	t.Run("APPL_DB_ExistingRoute_ReturnsData", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["APPL_DB"]
		rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "10.0.0.1", "ifname", "Ethernet0")
		defer rclient.Del(context.Background(), "ROUTE_TABLE:0.0.0.0/0")

		msi := make(map[string]interface{})
		tblPath := tablePath{dbNamespace: ns, dbName: "APPL_DB", tableName: "ROUTE_TABLE", tableKey: "0.0.0.0/0", delimitor: ":"}
		err := TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(msi) == 0 {
			t.Errorf("expected data, got empty msi")
		}
	})

	t.Run("COUNTERS_DB_VirtualPath_ExistingData", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["COUNTERS_DB"]
		rclient.HSet(context.Background(), "COUNTERS:oid:0x1000000000039", "SAI_PORT_STAT_IF_IN_OCTETS", "100", "SAI_PORT_STAT_IF_OUT_OCTETS", "200")
		defer rclient.Del(context.Background(), "COUNTERS:oid:0x1000000000039")

		msi := make(map[string]interface{})
		tblPath := tablePath{dbNamespace: ns, dbName: "COUNTERS_DB", tableName: "COUNTERS", tableKey: "oid:0x1000000000039", delimitor: ":", isVirtualPath: true}
		err := TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(msi) == 0 {
			t.Errorf("expected counter data, got empty msi")
		}
	})

	t.Run("COUNTERS_DB_VirtualPath_MissingData_Skipped", func(t *testing.T) {
		msi := make(map[string]interface{})
		tblPath := tablePath{dbNamespace: ns, dbName: "COUNTERS_DB", tableName: "COUNTERS", tableKey: "oid:0xDEADBEEF", delimitor: ":", isVirtualPath: true}
		err := TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(msi) != 0 {
			t.Errorf("expected empty msi for missing counter OID, got %v", msi)
		}
	})

	t.Run("COUNTERS_DB_JsonTableKey_ReturnsData", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["COUNTERS_DB"]
		rclient.HSet(context.Background(), "COUNTERS:oid:0x1000000000039", "SAI_PORT_STAT_IF_IN_OCTETS", "100")
		defer rclient.Del(context.Background(), "COUNTERS:oid:0x1000000000039")

		msi := make(map[string]interface{})
		tblPath := tablePath{dbNamespace: ns, dbName: "COUNTERS_DB", tableName: "COUNTERS", tableKey: "oid:0x1000000000039", delimitor: ":", jsonTableKey: "Ethernet68", isVirtualPath: true}
		err := TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if _, ok := msi["Ethernet68"]; !ok {
			t.Errorf("expected Ethernet68 in msi, got %v", msi)
		}
	})
}

func TestSubscribeTableData2TypedValue(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()
	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]

	t.Run("DataExists_ReturnsTrue", func(t *testing.T) {
		rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP")
		defer rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

		tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}}
		val, err, updateReceived := subscribeTableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !updateReceived {
			t.Errorf("expected updateReceived=true")
		}
		if val == nil {
			t.Errorf("expected non-nil value")
		}
	})

	t.Run("NoData_ReturnsFalse", func(t *testing.T) {
		tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|"}}
		_, err, updateReceived := subscribeTableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if updateReceived {
			t.Errorf("expected updateReceived=false")
		}
	})

	t.Run("MissingField_ContinuesNotErrors", func(t *testing.T) {
		tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|", field: "nonexistent"}}
		_, err, updateReceived := subscribeTableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if updateReceived {
			t.Errorf("expected updateReceived=false")
		}
	})

	t.Run("APPL_DB_NonExistentRoute_ReturnsFalse", func(t *testing.T) {
		tblPaths := []tablePath{{dbNamespace: ns, dbName: "APPL_DB", tableName: "ROUTE_TABLE", tableKey: "0.0.0.0/0", delimitor: ":"}}
		_, err, updateReceived := subscribeTableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if updateReceived {
			t.Errorf("expected updateReceived=false")
		}
	})

	t.Run("JsonField_VirtualPath_ExistingData", func(t *testing.T) {
		countersClient := Target2RedisDb[ns]["COUNTERS_DB"]
		countersClient.HSet(context.Background(), "COUNTERS:oid:0x1000000000039", "SAI_PORT_STAT_PFC_7_RX_PKTS", "2")
		defer countersClient.Del(context.Background(), "COUNTERS:oid:0x1000000000039")

		tblPaths := []tablePath{{
			dbNamespace:   ns,
			dbName:        "COUNTERS_DB",
			tableName:     "COUNTERS",
			tableKey:      "oid:0x1000000000039",
			delimitor:     ":",
			jsonField:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
			jsonTableKey:  "Ethernet68",
			field:         "SAI_PORT_STAT_PFC_7_RX_PKTS",
			isVirtualPath: true,
		}}
		val, err, updateReceived := subscribeTableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !updateReceived {
			t.Errorf("expected updateReceived=true")
		}
		if val == nil {
			t.Errorf("expected non-nil value")
		}
	})

	t.Run("JsonField_VirtualPath_MissingData", func(t *testing.T) {
		tblPaths := []tablePath{{
			dbNamespace:   ns,
			dbName:        "COUNTERS_DB",
			tableName:     "COUNTERS",
			tableKey:      "oid:0xDEADBEEF",
			delimitor:     ":",
			jsonField:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
			jsonTableKey:  "Ethernet99",
			field:         "SAI_PORT_STAT_PFC_7_RX_PKTS",
			isVirtualPath: true,
		}}
		_, err, updateReceived := subscribeTableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if updateReceived {
			t.Errorf("expected updateReceived=false")
		}
	})
}

func TestPollRunDeleteTracking(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()
	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP")

	gnmiPath := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}, {Name: "10.0.0.57"}}}
	c := DbClient{
		pathG2S: map[*gnmipb.Path][]tablePath{
			gnmiPath: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}},
		},
	}

	q := queue.NewPriorityQueue(1, false)
	poll := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)

	go c.PollRun(q, poll, &wg, nil)

	poll <- struct{}{}
	time.Sleep(100 * time.Millisecond)

	rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

	poll <- struct{}{}
	time.Sleep(100 * time.Millisecond)

	close(poll)
	wg.Wait()

	var gotUpdate, gotDelete, gotSync bool
	for !q.Empty() {
		items, _ := q.Get(1)
		val := items[0].(Value)
		if val.GetSyncResponse() {
			gotSync = true
		} else if val.GetDelete() != nil {
			gotDelete = true
		} else if val.GetVal() != nil {
			gotUpdate = true
		}
	}
	if !gotUpdate {
		t.Errorf("expected update notification")
	}
	if !gotDelete {
		t.Errorf("expected delete notification")
	}
	if !gotSync {
		t.Errorf("expected sync responses")
	}
}

func TestValidatePaths(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()
	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "state", "Established")

	t.Run("ExistingPath_NoError", func(t *testing.T) {
		c := DbClient{
			pathG2S: map[*gnmipb.Path][]tablePath{
				{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}},
			},
		}
		if err := c.ValidatePaths(); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("MissingPath_ReturnsError", func(t *testing.T) {
		c := DbClient{
			pathG2S: map[*gnmipb.Path][]tablePath{
				{}: {{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|"}},
			},
		}
		if err := c.ValidatePaths(); err == nil {
			t.Errorf("expected error for missing path")
		}
	})

	t.Run("VirtualPath_Skipped", func(t *testing.T) {
		c := DbClient{
			pathG2S: map[*gnmipb.Path][]tablePath{
				{}: {{dbNamespace: ns, dbName: "COUNTERS_DB", tableName: "COUNTERS", tableKey: "oid:0xDEAD", delimitor: ":", isVirtualPath: true}},
			},
		}
		if err := c.ValidatePaths(); err != nil {
			t.Errorf("expected no error for virtual path, got %v", err)
		}
	})
}

// setupMixedDbRedis sets up RedisDbMap for MixedDbClient tests.
func setupMixedDbRedis(t *testing.T, mapkey string) func() {
	t.Helper()
	cleanup := setupTestTarget2RedisDb(t)
	ns := ""
	origRedisDbMap := RedisDbMap
	RedisDbMap = make(map[string]*redis.Client)
	for dbName, rc := range Target2RedisDb[ns] {
		RedisDbMap[mapkey+":"+dbName] = rc
	}
	return func() {
		RedisDbMap = origRedisDbMap
		cleanup()
	}
}

func TestMixedDbClientTableData2TypedValue(t *testing.T) {
	mapkey := ":"
	cleanup := setupMixedDbRedis(t, mapkey)
	defer cleanup()

	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]

	c := MixedDbClient{mapkey: mapkey, encoding: gnmipb.Encoding_JSON_IETF}

	t.Run("DataExists_ReturnsTrue", func(t *testing.T) {
		rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP")
		defer rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

		tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}}
		val, err, updateReceived := c.tableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !updateReceived {
			t.Errorf("expected updateReceived=true")
		}
		if val == nil {
			t.Errorf("expected non-nil value")
		}
	})

	t.Run("NoData_ReturnsFalse", func(t *testing.T) {
		tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|"}}
		_, err, updateReceived := c.tableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if updateReceived {
			t.Errorf("expected updateReceived=false")
		}
	})

	t.Run("MissingField_ContinuesNotErrors", func(t *testing.T) {
		tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|", field: "nonexistent", index: -1}}
		_, err, updateReceived := c.tableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if updateReceived {
			t.Errorf("expected updateReceived=false")
		}
	})

	t.Run("MissingRedisClient_ReturnsError", func(t *testing.T) {
		tblPaths := []tablePath{{dbNamespace: ns, dbName: "NONEXISTENT_DB", tableName: "TABLE", tableKey: "key", delimitor: "|"}}
		_, err, _ := c.tableData2TypedValue(tblPaths, nil)
		if err == nil {
			t.Errorf("expected error for missing Redis client")
		}
	})

	t.Run("IndexField_MissingData_Continues", func(t *testing.T) {
		tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.99", delimitor: "|", field: "nonexistent", index: 0}}
		_, err, updateReceived := c.tableData2TypedValue(tblPaths, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if updateReceived {
			t.Errorf("expected updateReceived=false")
		}
	})
}

func TestMixedDbClientPollRun(t *testing.T) {
	mapkey := ":"
	cleanup := setupMixedDbRedis(t, mapkey)
	defer cleanup()

	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP")

	gnmiPath := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}, {Name: "10.0.0.57"}}}
	tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}}

	c := MixedDbClient{
		mapkey:   mapkey,
		encoding: gnmipb.Encoding_JSON_IETF,
		paths:    []*gnmipb.Path{gnmiPath},
	}

	patches := gomonkey.ApplyPrivateMethod(&c, "getDbtablePath", func(_ *MixedDbClient, _ *gnmipb.Path, _ *gnmipb.Path) ([]tablePath, error) {
		return tblPaths, nil
	})
	defer patches.Reset()

	q := queue.NewPriorityQueue(1, false)
	poll := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)

	go c.PollRun(q, poll, &wg, nil)

	poll <- struct{}{}
	time.Sleep(100 * time.Millisecond)

	rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

	poll <- struct{}{}
	time.Sleep(100 * time.Millisecond)

	close(poll)
	wg.Wait()

	var gotUpdate, gotDelete, gotSync bool
	for !q.Empty() {
		items, _ := q.Get(1)
		val := items[0].(Value)
		if val.GetSyncResponse() {
			gotSync = true
		} else if val.GetDelete() != nil {
			gotDelete = true
		} else if val.GetVal() != nil {
			gotUpdate = true
		}
	}
	if !gotUpdate {
		t.Errorf("expected update notification")
	}
	if !gotDelete {
		t.Errorf("expected delete notification")
	}
	if !gotSync {
		t.Errorf("expected sync responses")
	}
}

func TestMixedDbClientGet(t *testing.T) {
	mapkey := ":"
	cleanup := setupMixedDbRedis(t, mapkey)
	defer cleanup()

	ns := ""
	rclient := Target2RedisDb[ns]["STATE_DB"]

	gnmiPath := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "NEIGH_STATE_TABLE"}, {Name: "10.0.0.57"}}}
	tblPaths := []tablePath{{dbNamespace: ns, dbName: "STATE_DB", tableName: "NEIGH_STATE_TABLE", tableKey: "10.0.0.57", delimitor: "|"}}

	c := MixedDbClient{
		mapkey:   mapkey,
		encoding: gnmipb.Encoding_JSON_IETF,
		paths:    []*gnmipb.Path{gnmiPath},
	}

	patches := gomonkey.ApplyPrivateMethod(&c, "getDbtablePath", func(_ *MixedDbClient, _ *gnmipb.Path, _ *gnmipb.Path) ([]tablePath, error) {
		return tblPaths, nil
	})
	defer patches.Reset()

	t.Run("DataExists_ReturnsValues", func(t *testing.T) {
		rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP")
		defer rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")

		values, err := c.Get(nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(values) == 0 {
			t.Errorf("expected values, got empty")
		}
	})

	t.Run("NoData_ReturnsError", func(t *testing.T) {
		values, err := c.Get(nil)
		if err == nil {
			t.Errorf("expected NOT_FOUND error for missing specific-key path, got nil")
		}
		if len(values) != 0 {
			t.Errorf("expected no values for missing data, got %d", len(values))
		}
	})
}

func TestMain(m *testing.M) {
	defer test_utils.MemLeakCheck()
	m.Run()
}

// TestGetTableDashHA verifies that DASH_HA_ tables use Table (not ProducerStateTable
// or ZmqProducerStateTable), even when ZMQ client is available.
// This is required because sonic-dash-ha subscribes to DASH_HA_ tables
// using SubscriberStateTable.
func TestGetTableDashHA(t *testing.T) {
	if !swsscommon.SonicDBConfigIsInit() {
		swsscommon.SonicDBConfigInitialize()
	}

	// Create ZMQ server and client
	zmqServer := swsscommon.NewZmqServer("tcp://*:3234")
	zmqAddress := "tcp://127.0.0.1:3234"
	zmqClient := swsscommon.NewZmqClient(zmqAddress)
	applDB := swsscommon.NewDBConnector(APPL_DB_NAME, SWSS_TIMEOUT, false)

	client := MixedDbClient{
		applDB:        applDB,
		tableMap:      map[string]swsscommon.ProducerStateTable{},
		zmqTableMap:   map[string]swsscommon.ZmqProducerStateTable{},
		plainTableMap: map[string]swsscommon.Table{},
		zmqClient:     zmqClient,
	}

	// Test DASH_ROUTE table - should use ZmqProducerStateTable
	_ = client.GetTable("DASH_ROUTE")
	if _, ok := client.zmqTableMap["DASH_ROUTE"]; !ok {
		t.Errorf("DASH_ROUTE should use ZmqProducerStateTable")
	}
	if _, ok := client.tableMap["DASH_ROUTE"]; ok {
		t.Errorf("DASH_ROUTE should not use ProducerStateTable")
	}

	// Test DASH_HA_SET_CONFIG_TABLE table - should use Table (plainTableMap), not ProducerStateTable or ZMQ
	pt := client.GetTable("DASH_HA_SET_CONFIG_TABLE")
	if pt != nil {
		t.Errorf("DASH_HA_SET_CONFIG_TABLE GetTable should return nil")
	}
	if _, ok := client.plainTableMap["DASH_HA_SET_CONFIG_TABLE"]; !ok {
		t.Errorf("DASH_HA_SET_CONFIG_TABLE should use Table (plainTableMap)")
	}
	if _, ok := client.tableMap["DASH_HA_SET_CONFIG_TABLE"]; ok {
		t.Errorf("DASH_HA_SET_CONFIG_TABLE should not use ProducerStateTable")
	}
	if _, ok := client.zmqTableMap["DASH_HA_SET_CONFIG_TABLE"]; ok {
		t.Errorf("DASH_HA_SET_CONFIG_TABLE should not use ZmqProducerStateTable")
	}

	// Test DASH_HA_SCOPE_CONFIG_TABLE table - should use Table (plainTableMap), not ProducerStateTable or ZMQ
	pt = client.GetTable("DASH_HA_SCOPE_CONFIG_TABLE")
	if pt != nil {
		t.Errorf("DASH_HA_SCOPE_CONFIG_TABLE GetTable should return nil")
	}
	if _, ok := client.plainTableMap["DASH_HA_SCOPE_CONFIG_TABLE"]; !ok {
		t.Errorf("DASH_HA_SCOPE_CONFIG_TABLE should use Table (plainTableMap)")
	}
	if _, ok := client.tableMap["DASH_HA_SCOPE_CONFIG_TABLE"]; ok {
		t.Errorf("DASH_HA_SCOPE_CONFIG_TABLE should not use ProducerStateTable")
	}
	if _, ok := client.zmqTableMap["DASH_HA_SCOPE_CONFIG_TABLE"]; ok {
		t.Errorf("DASH_HA_SCOPE_CONFIG_TABLE should not use ZmqProducerStateTable")
	}

	// Test DbSetTable for DASH_HA_ table - should use Table.Set
	testData := map[string]string{"field1": "value1", "field2": "value2"}
	err := client.DbSetTable("DASH_HA_SET_CONFIG_TABLE", "test_key", testData)
	if err != nil {
		t.Errorf("DbSetTable for DASH_HA_SET_CONFIG_TABLE failed: %v", err)
	}

	// Test DbDelTable for DASH_HA_ table - should use Table.Delete
	err = client.DbDelTable("DASH_HA_SET_CONFIG_TABLE", "test_key")
	if err != nil {
		t.Errorf("DbDelTable for DASH_HA_SET_CONFIG_TABLE failed: %v", err)
	}

	// Cleanup in reverse order of dependencies:
	// 1. Delete ZmqProducerStateTable entries (they reference both applDB and zmqClient)
	for _, zmqTable := range client.zmqTableMap {
		swsscommon.DeleteZmqProducerStateTable(zmqTable)
	}
	client.zmqTableMap = map[string]swsscommon.ZmqProducerStateTable{}

	// 2. Delete Table entries (they reference applDB)
	for _, plainTable := range client.plainTableMap {
		plainTable.Flush()
		swsscommon.DeleteTable(plainTable)
	}
	client.plainTableMap = map[string]swsscommon.Table{}

	// 3. Delete applDB
	swsscommon.DeleteDBConnector(applDB)
	client.applDB = nil

	// 4. Delete ZMQ client and server
	swsscommon.DeleteZmqClient(zmqClient)
	swsscommon.DeleteZmqServer(zmqServer)
}

func TestV2rEniStats(t *testing.T) {
	// Save and restore the ENI maps
	origEniNameMap := countersEniNameMap
	origEniOidNameMap := countersEniOidNameMap
	defer func() {
		countersEniNameMap = origEniNameMap
		countersEniOidNameMap = origEniOidNameMap
	}()

	countersEniNameMap = map[string]string{
		"eni1": "oid:0xENI1",
		"eni2": "oid:0xENI2",
	}
	countersEniOidNameMap = map[string]string{
		"oid:0xENI1": "eni1",
		"oid:0xENI2": "eni2",
	}

	t.Run("Wildcard_AllENIs", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "COUNTERS", "ENI", "*"}
		tblPaths, err := v2rEniStats(paths)
		if err != nil {
			t.Fatalf("v2rEniStats failed: %v", err)
		}
		if len(tblPaths) != 2 {
			t.Fatalf("expected 2 tablePaths, got %d", len(tblPaths))
		}
		eniFound := map[string]bool{}
		for _, tp := range tblPaths {
			if tp.dbName != "DPU_COUNTERS_DB" {
				t.Errorf("expected dbName DPU_COUNTERS_DB, got %v", tp.dbName)
			}
			if tp.tableName != "COUNTERS" {
				t.Errorf("expected tableName COUNTERS, got %v", tp.tableName)
			}
			eniFound[tp.jsonTableKey] = true
		}
		if !eniFound["eni1"] || !eniFound["eni2"] {
			t.Errorf("expected both eni1 and eni2 in results, got %v", eniFound)
		}
	})

	t.Run("Specific_ENI", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "COUNTERS", "ENI", "eni1"}
		tblPaths, err := v2rEniStats(paths)
		if err != nil {
			t.Fatalf("v2rEniStats failed: %v", err)
		}
		if len(tblPaths) != 1 {
			t.Fatalf("expected 1 tablePath, got %d", len(tblPaths))
		}
		if tblPaths[0].tableKey != "oid:0xENI1" {
			t.Errorf("expected tableKey oid:0xENI1, got %v", tblPaths[0].tableKey)
		}
	})

	t.Run("Invalid_ENI", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "COUNTERS", "ENI", "nonexistent"}
		_, err := v2rEniStats(paths)
		if err == nil {
			t.Errorf("expected error for invalid ENI name")
		}
	})
}

func TestV2rDashMeterByEniAndClass(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()

	// Save and restore the ENI maps
	origEniNameMap := countersEniNameMap
	origEniOidNameMap := countersEniOidNameMap
	defer func() {
		countersEniNameMap = origEniNameMap
		countersEniOidNameMap = origEniOidNameMap
	}()

	countersEniNameMap = map[string]string{
		"eni1": "oid:0xENI1",
	}
	countersEniOidNameMap = map[string]string{
		"oid:0xENI1": "eni1",
	}

	ns := ""
	rclient := Target2RedisDb[ns]["DPU_COUNTERS_DB"]
	if rclient == nil {
		t.Skip("DPU_COUNTERS_DB Redis client not available")
	}

	// Insert a DASH_METER key
	dashKey := `COUNTERS:{"eni_id":"oid:0xENI1","meter_class":"100","switch_id":"oid:0xSW1"}`
	rclient.HSet(context.Background(), dashKey, "bytes", "1000", "packets", "10")
	defer rclient.Del(context.Background(), dashKey)

	t.Run("Specific_ENI_And_Class", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "DASH_METER", "eni1", "100"}
		tblPaths, err := v2rDashMeterByEniAndClass(paths)
		if err != nil {
			t.Fatalf("v2rDashMeterByEniAndClass failed: %v", err)
		}
		if len(tblPaths) != 1 {
			t.Fatalf("expected 1 tablePath, got %d", len(tblPaths))
		}
		if tblPaths[0].dbName != "DPU_COUNTERS_DB" {
			t.Errorf("expected dbName DPU_COUNTERS_DB, got %v", tblPaths[0].dbName)
		}
		if tblPaths[0].tableName != "COUNTERS" {
			t.Errorf("expected tableName COUNTERS, got %v", tblPaths[0].tableName)
		}
		if tblPaths[0].jsonTableKey != "100" {
			t.Errorf("expected jsonTableKey '100', got %v", tblPaths[0].jsonTableKey)
		}
	})

	t.Run("Invalid_ENI", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "DASH_METER", "bad_eni", "100"}
		_, err := v2rDashMeterByEniAndClass(paths)
		if err == nil {
			t.Errorf("expected error for invalid ENI name")
		}
	})

	t.Run("NoMatching_Class", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "DASH_METER", "eni1", "999"}
		_, err := v2rDashMeterByEniAndClass(paths)
		if err == nil {
			t.Errorf("expected error when no matching meter class")
		}
	})
}

func TestV2rDashMeterByEni(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()

	// Save and restore the ENI maps
	origEniNameMap := countersEniNameMap
	origEniOidNameMap := countersEniOidNameMap
	defer func() {
		countersEniNameMap = origEniNameMap
		countersEniOidNameMap = origEniOidNameMap
	}()

	countersEniNameMap = map[string]string{
		"eni1": "oid:0xENI1",
		"eni2": "oid:0xENI2",
	}
	countersEniOidNameMap = map[string]string{
		"oid:0xENI1": "eni1",
		"oid:0xENI2": "eni2",
	}

	ns := ""
	rclient := Target2RedisDb[ns]["DPU_COUNTERS_DB"]
	if rclient == nil {
		t.Skip("DPU_COUNTERS_DB Redis client not available")
	}

	// Insert multiple DASH_METER keys
	key1 := `COUNTERS:{"eni_id":"oid:0xENI1","meter_class":"100","switch_id":"oid:0xSW1"}`
	key2 := `COUNTERS:{"eni_id":"oid:0xENI1","meter_class":"200","switch_id":"oid:0xSW1"}`
	key3 := `COUNTERS:{"eni_id":"oid:0xENI2","meter_class":"100","switch_id":"oid:0xSW1"}`
	// Also insert a non-JSON COUNTERS key that should be skipped
	key4 := "COUNTERS:oid:0x1000000000039"
	rclient.HSet(context.Background(), key1, "bytes", "1000")
	rclient.HSet(context.Background(), key2, "bytes", "2000")
	rclient.HSet(context.Background(), key3, "bytes", "3000")
	rclient.HSet(context.Background(), key4, "SAI_PORT_STAT_IF_IN_OCTETS", "999")
	defer func() {
		rclient.Del(context.Background(), key1, key2, key3, key4)
	}()

	t.Run("Specific_ENI_AllClasses", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "DASH_METER", "eni1"}
		tblPaths, err := v2rDashMeterByEni(paths)
		if err != nil {
			t.Fatalf("v2rDashMeterByEni failed: %v", err)
		}
		if len(tblPaths) != 2 {
			t.Fatalf("expected 2 tablePaths for eni1, got %d", len(tblPaths))
		}
		classFound := map[string]bool{}
		for _, tp := range tblPaths {
			classFound[tp.jsonTableKey] = true
			if tp.dbName != "DPU_COUNTERS_DB" {
				t.Errorf("expected dbName DPU_COUNTERS_DB, got %v", tp.dbName)
			}
		}
		if !classFound["100"] || !classFound["200"] {
			t.Errorf("expected classes 100 and 200, got %v", classFound)
		}
	})

	t.Run("Wildcard_AllENIs_AllClasses", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "DASH_METER", "*"}
		tblPaths, err := v2rDashMeterByEni(paths)
		if err != nil {
			t.Fatalf("v2rDashMeterByEni failed: %v", err)
		}
		if len(tblPaths) != 3 {
			t.Fatalf("expected 3 tablePaths for all ENIs, got %d", len(tblPaths))
		}
		keyFound := map[string]bool{}
		for _, tp := range tblPaths {
			keyFound[tp.jsonTableKey] = true
		}
		// Wildcard uses "eniName/class" format
		if !keyFound["eni1/100"] || !keyFound["eni1/200"] || !keyFound["eni2/100"] {
			t.Errorf("expected eni1/100, eni1/200, eni2/100 in results, got %v", keyFound)
		}
	})

	t.Run("Invalid_ENI", func(t *testing.T) {
		paths := []string{"DPU_COUNTERS_DB", "DASH_METER", "bad_eni"}
		_, err := v2rDashMeterByEni(paths)
		if err == nil {
			t.Errorf("expected error for invalid ENI name")
		}
	})
}

func TestGetDpuCountersMap(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()

	ns := ""
	rclient := Target2RedisDb[ns]["DPU_COUNTERS_DB"]
	if rclient == nil {
		t.Skip("DPU_COUNTERS_DB Redis client not available")
	}

	rclient.HSet(context.Background(), "COUNTERS_ENI_NAME_MAP", "eni1", "oid:0xENI1")
	defer rclient.Del(context.Background(), "COUNTERS_ENI_NAME_MAP")

	result, err := GetDpuCountersMap("COUNTERS_ENI_NAME_MAP")
	if err != nil {
		t.Fatalf("GetDpuCountersMap failed: %v", err)
	}
	if result["eni1"] != "oid:0xENI1" {
		t.Errorf("expected eni1->oid:0xENI1, got %v", result)
	}
}

func TestGetCountersMapForDb(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()

	ns := ""

	t.Run("COUNTERS_DB", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["COUNTERS_DB"]
		rclient.HSet(context.Background(), "COUNTERS_PORT_NAME_MAP", "Ethernet0", "oid:0x100")
		defer rclient.Del(context.Background(), "COUNTERS_PORT_NAME_MAP")

		result, err := GetCountersMapForDb("COUNTERS_DB", "COUNTERS_PORT_NAME_MAP")
		if err != nil {
			t.Fatalf("GetCountersMapForDb failed: %v", err)
		}
		if result["Ethernet0"] != "oid:0x100" {
			t.Errorf("expected Ethernet0->oid:0x100, got %v", result)
		}
	})

	t.Run("DPU_COUNTERS_DB", func(t *testing.T) {
		rclient := Target2RedisDb[ns]["DPU_COUNTERS_DB"]
		if rclient == nil {
			t.Skip("DPU_COUNTERS_DB Redis client not available")
		}
		rclient.HSet(context.Background(), "TEST_MAP", "key1", "val1")
		defer rclient.Del(context.Background(), "TEST_MAP")

		result, err := GetCountersMapForDb("DPU_COUNTERS_DB", "TEST_MAP")
		if err != nil {
			t.Fatalf("GetCountersMapForDb failed: %v", err)
		}
		if result["key1"] != "val1" {
			t.Errorf("expected key1->val1, got %v", result)
		}
	})
}

func TestInitCountersEniNameMap(t *testing.T) {
	origFn := getDpuCountersMapFn
	defer func() { getDpuCountersMapFn = origFn }()

	t.Run("Success", func(t *testing.T) {
		os.Setenv("UNIT_TEST", "1")
		ClearMappings()
		os.Unsetenv("UNIT_TEST")

		getDpuCountersMapFn = func(tableName string) (map[string]string, error) {
			switch tableName {
			case "COUNTERS_ENI_NAME_MAP":
				return map[string]string{"eni1": "oid:0xENI1"}, nil
			case "COUNTERS_ENI_OID_NAME_MAP":
				return map[string]string{"oid:0xENI1": "eni1"}, nil
			default:
				return nil, fmt.Errorf("unexpected table %s", tableName)
			}
		}

		err := initCountersEniNameMap()
		if err != nil {
			t.Fatalf("initCountersEniNameMap failed: %v", err)
		}
		if countersEniNameMap["eni1"] != "oid:0xENI1" {
			t.Errorf("expected eni1->oid:0xENI1, got %v", countersEniNameMap)
		}
		if countersEniOidNameMap["oid:0xENI1"] != "eni1" {
			t.Errorf("expected oid:0xENI1->eni1, got %v", countersEniOidNameMap)
		}
	})

	t.Run("Error_NameMap", func(t *testing.T) {
		os.Setenv("UNIT_TEST", "1")
		ClearMappings()
		os.Unsetenv("UNIT_TEST")

		getDpuCountersMapFn = func(tableName string) (map[string]string, error) {
			return nil, fmt.Errorf("redis error")
		}

		err := initCountersEniNameMap()
		if err == nil {
			t.Errorf("expected error when COUNTERS_ENI_NAME_MAP fails")
		}
	})
}

func TestGetDashMeterKeys(t *testing.T) {
	cleanup := setupTestTarget2RedisDb(t)
	defer cleanup()

	ns := ""
	rclient := Target2RedisDb[ns]["DPU_COUNTERS_DB"]
	if rclient == nil {
		t.Skip("DPU_COUNTERS_DB Redis client not available")
	}

	// Insert test keys
	key1 := `COUNTERS:{"eni_id":"oid:0xENI1","meter_class":"100","switch_id":"oid:0xSW1"}`
	key2 := `COUNTERS:{"eni_id":"oid:0xENI2","meter_class":"200","switch_id":"oid:0xSW1"}`
	// Non-JSON key that should be skipped
	key3 := "COUNTERS:oid:0x1000000000039"
	rclient.HSet(context.Background(), key1, "bytes", "1000")
	rclient.HSet(context.Background(), key2, "bytes", "2000")
	rclient.HSet(context.Background(), key3, "SAI_PORT_STAT_IF_IN_OCTETS", "999")
	defer func() {
		rclient.Del(context.Background(), key1, key2, key3)
	}()

	t.Run("All_Keys", func(t *testing.T) {
		results, err := getDashMeterKeys(rclient, ":", "", "")
		if err != nil {
			t.Fatalf("getDashMeterKeys failed: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results (non-JSON key skipped), got %d", len(results))
		}
	})

	t.Run("Filter_By_ENI", func(t *testing.T) {
		results, err := getDashMeterKeys(rclient, ":", "oid:0xENI1", "")
		if err != nil {
			t.Fatalf("getDashMeterKeys failed: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result for ENI1, got %d", len(results))
		}
		if results[0].eniOid != "oid:0xENI1" {
			t.Errorf("expected eniOid oid:0xENI1, got %v", results[0].eniOid)
		}
	})

	t.Run("Filter_By_ENI_And_Class", func(t *testing.T) {
		results, err := getDashMeterKeys(rclient, ":", "oid:0xENI1", "100")
		if err != nil {
			t.Fatalf("getDashMeterKeys failed: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].meterClass != "100" {
			t.Errorf("expected meterClass 100, got %v", results[0].meterClass)
		}
	})

	t.Run("No_Match", func(t *testing.T) {
		results, err := getDashMeterKeys(rclient, ":", "oid:0xNONEXISTENT", "")
		if err != nil {
			t.Fatalf("getDashMeterKeys failed: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})
}

func TestParsePathDpuCountersDb(t *testing.T) {
	// Save and restore the ENI maps
	origEniNameMap := countersEniNameMap
	origEniOidNameMap := countersEniOidNameMap
	origFn := getDpuCountersMapFn
	defer func() {
		countersEniNameMap = origEniNameMap
		countersEniOidNameMap = origEniOidNameMap
		getDpuCountersMapFn = origFn
	}()

	getDpuCountersMapFn = func(tableName string) (map[string]string, error) {
		switch tableName {
		case "COUNTERS_ENI_NAME_MAP":
			return map[string]string{"eni1": "oid:0xENI1"}, nil
		case "COUNTERS_ENI_OID_NAME_MAP":
			return map[string]string{"oid:0xENI1": "eni1"}, nil
		default:
			return nil, fmt.Errorf("unexpected table %s", tableName)
		}
	}

	os.Setenv("UNIT_TEST", "1")
	ClearMappings()
	os.Unsetenv("UNIT_TEST")

	t.Run("DPU_COUNTERS_DB_ENI_VirtualPath", func(t *testing.T) {
		pathG2S := make(map[*gnmipb.Path][]tablePath)
		prefix := &gnmipb.Path{Target: "DPU_COUNTERS_DB"}
		path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "ENI"}, {Name: "eni1"}}}
		err := parsePath(prefix, path, &pathG2S)
		if err != nil {
			t.Fatalf("parsePath failed: %v", err)
		}
		for _, tblPaths := range pathG2S {
			if !tblPaths[0].isVirtualPath {
				t.Errorf("expected isVirtualPath=true for COUNTERS/ENI/eni1")
			}
			if tblPaths[0].tableKey != "oid:0xENI1" {
				t.Errorf("expected tableKey oid:0xENI1, got %v", tblPaths[0].tableKey)
			}
			if tblPaths[0].dbName != "DPU_COUNTERS_DB" {
				t.Errorf("expected dbName DPU_COUNTERS_DB, got %v", tblPaths[0].dbName)
			}
		}
	})
}

func TestClearMappingsIncludesEniMaps(t *testing.T) {
	// Ensure maps are initialized (may be nil if a prior test's init failed)
	if countersEniNameMap == nil {
		countersEniNameMap = make(map[string]string)
	}
	if countersEniOidNameMap == nil {
		countersEniOidNameMap = make(map[string]string)
	}

	// Populate ENI maps
	countersEniNameMap["test_eni"] = "oid:0xTEST"
	countersEniOidNameMap["oid:0xTEST"] = "test_eni"

	os.Setenv("UNIT_TEST", "1")
	ClearMappings()
	os.Unsetenv("UNIT_TEST")

	if len(countersEniNameMap) != 0 {
		t.Errorf("expected countersEniNameMap to be cleared, got %v", countersEniNameMap)
	}
	if len(countersEniOidNameMap) != 0 {
		t.Errorf("expected countersEniOidNameMap to be cleared, got %v", countersEniOidNameMap)
	}
}
