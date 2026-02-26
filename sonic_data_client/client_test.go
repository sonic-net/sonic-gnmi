package client

import (
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
