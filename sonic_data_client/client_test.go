package client

import (
	"testing"
	"os"
	"reflect"
	"io/ioutil"
	"encoding/json"

	"github.com/jipanyang/gnxi/utils/xpath"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
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
	_, err := NewJsonClient(testFile)
	if err == nil {
		t.Errorf("Should fail without checkpoint")
	}

	text := "{"
	err = ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	_, err = NewJsonClient(testFile)
	if err == nil {
		t.Errorf("Should fail with invalid checkpoint")
	}
}

func TestJsonAdd(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	client, err := NewJsonClient(testFile)
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string {
		[]string {
			"DASH_QOS",
		},
		[]string {
			"DASH_QOS",
			"qos_02",
		},
		[]string {
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string {
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
		[]string {
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"0",
		},
	}
	value_list := []string {
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
}

func TestJsonAddNegative(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	client, err := NewJsonClient(testFile)
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string {
		[]string {
			"DASH_QOS",
		},
		[]string {
			"DASH_QOS",
			"qos_02",
		},
		[]string {
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string {
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
		[]string {
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"0",
		},
		[]string {
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"abc",
		},
		[]string {
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"100",
		},
	}
	value_list := []string {
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

func TestJsonRemove(t *testing.T) {
	text := "{}"
	err := ioutil.WriteFile(testFile, []byte(text), 0644)
	if err != nil {
		t.Errorf("Fail to create test file")
	}
	client, err := NewJsonClient(testFile)
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string {
		[]string {
			"DASH_QOS",
		},
		[]string {
			"DASH_QOS",
			"qos_02",
		},
		[]string {
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string {
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
		[]string {
			"DASH_VNET",
			"vnet002",
			"address_spaces",
			"0",
		},
	}
	value_list := []string {
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
	client, err := NewJsonClient(testFile)
	if err != nil {
		t.Errorf("Create client fail: %v", err)
	}
	path_list := [][]string {
		[]string {
			"DASH_QOS",
		},
		[]string {
			"DASH_VNET",
			"vnet001",
			"address_spaces",
		},
	}
	value_list := []string {
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

	remove_list := [][]string {
		[]string {
			"DASH_QOS",
			"qos_02",
		},
		[]string {
			"DASH_QOS",
			"qos_03",
			"bw",
		},
		[]string {
			"DASH_VNET",
			"vnet001",
			"address_spaces",
			"abc",
		},
		[]string {
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

func TestParseOrigin(t *testing.T) {
	var test_paths []*gnmipb.Path
	var err error

	_, err = ParseOrigin("test", test_paths)
	if err != nil {
		t.Errorf("ParseOrigin failed for empty path: %v", err)
	}

	test_origin := "sonic-test"
	path, err := xpath.ToGNMIPath(test_origin + ":CONFIG_DB/VLAN")
	test_paths = append(test_paths, path)
	origin, err := ParseOrigin("", test_paths)
	if err != nil {
		t.Errorf("ParseOrigin failed to get origin: %v", err)
	}
	if origin != test_origin {
		t.Errorf("ParseOrigin return wrong origin: %v", origin)
	}
	origin, err = ParseOrigin("sonic-invalid", test_paths)
	if err == nil {
		t.Errorf("ParseOrigin should fail for conflict")
	}
}

func TestParseTarget(t *testing.T) {
	var test_paths []*gnmipb.Path
	var err error

	_, err = ParseTarget("test", test_paths)
	if err != nil {
		t.Errorf("ParseTarget failed for empty path: %v", err)
	}

	test_target := "TEST_DB"
	path, err := xpath.ToGNMIPath("sonic-db:" + test_target + "/VLAN")
	test_paths = append(test_paths, path)
	target, err := ParseTarget("", test_paths)
	if err != nil {
		t.Errorf("ParseTarget failed to get target: %v", err)
	}
	if target != test_target {
		t.Errorf("ParseTarget return wrong target: %v", target)
	}
	target, err = ParseTarget("INVALID_DB", test_paths)
	if err == nil {
		t.Errorf("ParseTarget should fail for conflict")
	}
}
