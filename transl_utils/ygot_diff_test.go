////////////////////////////////////////////////////////////////////////////////
//                                                                            //
//  Copyright 2021 Broadcom. The term Broadcom refers to Broadcom Inc. and/or //
//  its subsidiaries.                                                         //
//                                                                            //
//  Licensed under the Apache License, Version 2.0 (the "License");           //
//  you may not use this file except in compliance with the License.          //
//  You may obtain a copy of the License at                                   //
//                                                                            //
//     http://www.apache.org/licenses/LICENSE-2.0                             //
//                                                                            //
//  Unless required by applicable law or agreed to in writing, software       //
//  distributed under the License is distributed on an "AS IS" BASIS,         //
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.  //
//  See the License for the specific language governing permissions and       //
//  limitations under the License.                                            //
//                                                                            //
////////////////////////////////////////////////////////////////////////////////

package transl_utils

import (
	"fmt"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/Azure/sonic-mgmt-common/translib/ocbinds"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
)

// diffData holds ygot diff info (updates and deletes) and
// provides methods to compare them.
type diffData struct {
	updates map[string]string
	deletes map[string]bool
}

func (dd *diffData) Update(p string, v interface{}) {
	if dd.updates == nil {
		dd.updates = make(map[string]string)
	}
	dd.updates[p] = fmt.Sprintf("%v", v)
}

func (dd *diffData) Updates(prefix string, data map[string]interface{}) {
	for p, v := range data {
		dd.Update(pathJoin(prefix, p), v)
	}
}

func (dd *diffData) Delete(p string) {
	if dd.deletes == nil {
		dd.deletes = make(map[string]bool)
	}
	dd.deletes[p] = true
}

func (dd *diffData) Deletes(prefix string, paths ...string) {
	for _, p := range paths {
		dd.Delete(pathJoin(prefix, p))
	}
}

func (dd *diffData) YgotDiff(t *testing.T, s1, s2 ygot.GoStruct, opts DiffOptions) {
	yd, err := Diff(s1, s2, opts)
	if err != nil {
		t.Fatalf("Diff failed; %v", err)
	}
	for _, u := range yd.Update {
		p, _ := ygot.PathToString(u.Path)
		dd.Update(p, tvToStr(u.Val))
	}
	for _, d := range yd.Delete {
		p, _ := ygot.PathToString(d)
		dd.Delete(p)
	}
}

func (dd *diffData) Compare(t *testing.T, exp *diffData) {
	for p, v := range dd.updates {
		if u, ok := exp.updates[p]; !ok {
			t.Errorf("Update <\"%s\", %v> not expected", p, v)
		} else if v != u {
			t.Errorf("Update <\"%s\", %v> does not match %v", p, v, u)
		}
	}
	for p, v := range exp.updates {
		if _, ok := dd.updates[p]; !ok {
			t.Errorf("Update <\"%s\", %v> not found", p, v)
		}
	}
	for p := range dd.deletes {
		if _, ok := exp.deletes[p]; !ok {
			t.Errorf("Delete \"%s\" not expected", p)
		}
	}
	for p := range exp.deletes {
		if _, ok := dd.deletes[p]; !ok {
			t.Errorf("Delete \"%s\" not found", p)
		}
	}
}

func pathJoin(p, s string) string {
	return strings.TrimSuffix(p, "/") + "/" + strings.TrimPrefix(s, "/")
}

func tvToStr(tv *gnmi.TypedValue) string {
	switch x := tv.Value.(type) {
	case *gnmi.TypedValue_StringVal:
		return x.StringVal
	case *gnmi.TypedValue_LeaflistVal:
		var values []string
		for _, v := range x.LeaflistVal.Element {
			values = append(values, tvToStr(v))
		}
		return strings.Join(values, ",")
	}

	// stringify TypedValue into "field: value" format
	// and extract the value part..
	s := fmt.Sprintf("%v", tv)
	t := strings.SplitN(s, ":", 2)
	return strings.TrimSpace(t[1])
}

func loadJSON(t *testing.T, v string) ygot.GoStruct {
	root := &ocbinds.Device{}
	err := ocbinds.Unmarshal([]byte(v), root)
	if err != nil {
		if f, err := ioutil.TempFile("", "payload-*"); err == nil {
			f.Write([]byte(v))
			f.Close()
			t.Log("Faulty payload saved at ", f.Name())
		}
		t.Fatalf("ocbinds.Unmarshal failed; err=%v", err)
	}
	return root
}

func TestAcl_nodiff(t *testing.T) {
	a1 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"}
		}
	]}}}`)
	a2 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"}
		}
	]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{})
	dif.Compare(t, &exp)
}

func TestAcl_add1acl(t *testing.T) {
	a1 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"}
		}
	]}}}`)
	a2 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"}
		},
		{"name": "ACL2", "type": "ACL_IPV6",
			"config":{"name": "ACL2", "type": "ACL_IPV6", "description": "foo/bar"},
			"state":{"name": "ACL2", "type": "ACL_IPV6"}
		}
	]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL2][type=ACL_IPV6]",
		map[string]interface{}{
			"name":               "ACL2",
			"type":               "ACL_IPV6",
			"config/name":        "ACL2",
			"config/type":        "ACL_IPV6",
			"config/description": "foo/bar",
			"state/name":         "ACL2",
			"state/type":         "ACL_IPV6",
		})
	dif.Compare(t, &exp)
}

func TestAcl_del1acl(t *testing.T) {
	a1 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"}
		},
		{"name": "ACL2", "type": "ACL_IPV6",
			"config":{"name": "ACL2", "type": "ACL_IPV6", "description": "foo/bar"},
			"state":{"name": "ACL2", "type": "ACL_IPV6"}
		}
	]}}}`)
	a2 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"}
		}
	]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{})
	exp.Delete("/acl/acl-sets/acl-set[name=ACL2][type=ACL_IPV6]")
	dif.Compare(t, &exp)
}

func TestAcl_mod1del1(t *testing.T) {
	a1 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"},
			"state":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"}
		},
		{"name": "ACL2", "type": "ACL_IPV6",
			"config":{"name": "ACL2", "type": "ACL_IPV6", "description": "foo/bar"}
		}
	]}}}`)
	a2 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "foo/bar"},
			"state":{"name": "ACL1", "type": "ACL_IPV4"}
		}
	]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{})
	exp.Update("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/config/description", "foo/bar")
	exp.Delete("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/state/description")
	exp.Delete("/acl/acl-sets/acl-set[name=ACL2][type=ACL_IPV6]")
	dif.Compare(t, &exp)
}

func TestAcl_add1rule(t *testing.T) {
	a1 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"}
		}
	]}}}`)
	a2 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"},
			"acl-entries": { "acl-entry":[
				{"sequence-id": 101,
					"config": {"sequence-id":101, "description": "rule101"},
					"state":  {"sequence-id":101},
					"actions":{"config":{"forwarding-action": "ACCEPT"}},
					"ipv4": {
						"config":{"protocol":"IP_TCP", "source-address": "101.0.0.0/8"},
						"state":{"protocol":"IP_TCP", "source-address": "101.0.0.0/8"}
					}
				}
			]}
		}
	]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/acl-entries/acl-entry[sequence-id=101]",
		map[string]interface{}{
			"sequence-id":                      101,
			"config/sequence-id":               101,
			"config/description":               "rule101",
			"state/sequence-id":                101,
			"actions/config/forwarding-action": "ACCEPT",
			"ipv4/config/protocol":             "IP_TCP",
			"ipv4/config/source-address":       "101.0.0.0/8",
			"ipv4/state/protocol":              "IP_TCP",
			"ipv4/state/source-address":        "101.0.0.0/8",
		})
	dif.Compare(t, &exp)
}

func TestAcl_modrules(t *testing.T) {
	a1 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4"},
			"acl-entries": { "acl-entry":[
				{"sequence-id": 101,
					"config": {"sequence-id": 101, "description": "rule101"},
					"actions":{"config":{"forwarding-action": "ACCEPT"}},
					"ipv4": {
						"config":{"protocol": "IP_TCP", "source-address": "101.0.0.0/8"},
						"state": {"protocol": "IP_TCP", "source-address": "101.0.0.0/8"}
					}
				},
				{"sequence-id": 102,
					"config": {"sequence-id": 102},
					"ipv4":   {"config":{"protocol": "IP_UDP", "source-address": "102.0.0.0/8"}}
				},
				{"sequence-id": 103,
					"config": {"sequence-id": 103},
					"ipv4":   {"config":{"source-address": "103.0.0.0/8"}}
				},
				{"sequence-id": 104,
					"config": {"sequence-id": 104}
				},
				{"sequence-id": 105,
					"config": {"sequence-id": 105},
					"ipv4":   {"config":{"protocol": 95, "source-address": "105.0.0.0/8"}}
				},
				{"sequence-id": 106,
					"config": {"sequence-id": 106},
					"ipv4":   {"config":{"protocol": 96, "source-address": "106.0.0.0/8"}}
				},
				{"sequence-id": 107,
					"config": {"sequence-id": 107},
					"ipv4":   {"config":{"protocol": "IP_GRE", "source-address": "107.0.0.0/8"}}
				},
				{"sequence-id": 108,
					"config": {"sequence-id": 108},
					"ipv4":   {"config":{"protocol": 98, "source-address": "108.0.0.0/8"}}
				}
			]}
		}
	]}}}`)
	a2 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "Hello, world!"},
			"acl-entries": { "acl-entry":[
				{"sequence-id": 101,
					"config": {"sequence-id": 101},
					"state":  {"sequence-id": 101},
					"actions":{"config":{"forwarding-action": "REJECT"}},
					"ipv4":   {"config":{"protocol": "IP_UDP", "source-address": "101.0.0.0/8"}}
				},
				{"sequence-id": 102,
					"config": {"sequence-id": 102},
					"ipv4":   {"config":{"source-address": "102.0.0.0/8"}}
				},
				{"sequence-id": 103,
					"config": {"sequence-id": 103},
					"ipv4":   {"config":{"protocol": "IP_TCP", "source-address": "103.0.0.0/8"}}
				},
				{"sequence-id": 105,
					"config": {"sequence-id": 105},
					"ipv4":   {"config":{"protocol": "IP_GRE", "source-address": "105.0.0.0/8"}}
				},
				{"sequence-id": 106,
					"config": {"sequence-id": 106},
					"ipv4":   {"config":{"protocol": 66, "source-address": "106.0.0.0/8"}}
				},
				{"sequence-id": 107,
					"config": {"sequence-id": 107},
					"ipv4":   {"config":{"protocol": 97, "source-address": "107.0.0.0/8"}}
				},
				{"sequence-id": 108,
					"config": {"sequence-id": 108},
					"ipv4":   {"config":{"source-address": "108.0.0.0/8"}}
				},
				{"sequence-id": 300,
					"config": {"sequence-id": 300, "description": "rule300"}
				}
			]}
		}
	]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]",
		map[string]interface{}{
			"config/description": "Hello, world!",
		})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/acl-entries",
		map[string]interface{}{
			"acl-entry[sequence-id=101]/state/sequence-id":                101,
			"acl-entry[sequence-id=101]/actions/config/forwarding-action": "REJECT",
			"acl-entry[sequence-id=101]/ipv4/config/protocol":             "IP_UDP",
			"acl-entry[sequence-id=103]/ipv4/config/protocol":             "IP_TCP",
			"acl-entry[sequence-id=105]/ipv4/config/protocol":             "IP_GRE",
			"acl-entry[sequence-id=106]/ipv4/config/protocol":             66,
			"acl-entry[sequence-id=107]/ipv4/config/protocol":             97,
			"acl-entry[sequence-id=300]/sequence-id":                      300,
			"acl-entry[sequence-id=300]/config/sequence-id":               300,
			"acl-entry[sequence-id=300]/config/description":               "rule300",
		})
	exp.Deletes("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/acl-entries",
		"acl-entry[sequence-id=101]/config/description",
		"acl-entry[sequence-id=101]/ipv4/state",
		"acl-entry[sequence-id=102]/ipv4/config/protocol",
		"acl-entry[sequence-id=104]",
		"acl-entry[sequence-id=108]/ipv4/config/protocol",
	)
	dif.Compare(t, &exp)
}

func TestAcl_RecordAll(t *testing.T) {
	a1 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4", "description": "???"},
			"state": {"name": "ACL1", "type": "ACL_IPV4"},
			"acl-entries": { "acl-entry":[
				{"sequence-id": 101,
					"config": {"sequence-id": 101, "description": "rule101"},
					"actions":{"config":{"forwarding-action": "ACCEPT"}},
					"ipv4":   {"config":{"protocol": "IP_TCP", "source-address": "101.0.0.0/8"}}
				},
				{"sequence-id": 102,
					"config": {"sequence-id": 102},
					"actions":{"config":{"forwarding-action": "REJECT"}},
					"ipv4":   {"config":{"protocol": "IP_UDP", "source-address": "102.0.0.0/8"}}
				},
				{"sequence-id": 103,
					"config": {"sequence-id": 103}
				},
				{"sequence-id": 104,
					"config": {"sequence-id": 104, "description": "rule104"},
					"actions":{"config":{"forwarding-action": "ACCEPT"}},
					"ipv4":   {"config":{"protocol": "IP_TCP", "source-address": "101.0.0.0/8"}}
				}
			]}
		}
	]}}}`)
	a2 := loadJSON(t, `
	{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4",
			"config":{"name": "ACL1", "type": "ACL_IPV4"},
			"acl-entries": { "acl-entry":[
				{"sequence-id": 101,
					"config": {"sequence-id": 101, "description": "rule101"},
					"actions":{"config":{"forwarding-action": "ACCEPT"}},
					"ipv4":   {"config":{"protocol": "IP_TCP", "source-address": "101.0.0.0/8"}}
				},
				{"sequence-id": 102,
					"config": {"sequence-id": 102},
					"actions":{"config":{"forwarding-action": "DROP"}},
					"ipv4":   {"config":{"protocol": "IP_GRE", "source-address": "2.2.2.2/32"}}
				},
				{"sequence-id": 103,
					"config": {"sequence-id": 103, "description": "rule103"},
					"actions":{"config":{"forwarding-action": "REJECT"}},
					"ipv4":   {"config":{"protocol": "IP_UDP", "source-address": "103.0.0.0/8"}}
				},
				{"sequence-id": 104,
					"config": {"sequence-id": 104},
					"actions":{"config":{}},
					"ipv4":   {"config":{}}

				},
				{"sequence-id": 300,
					"config": {"sequence-id": 300, "description": "rule300"},
					"actions":{"config":{}},
					"ipv4":   {"config":{}}
				}
			]}
		}
	]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{RecordAll: true})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]",
		map[string]interface{}{
			"name":        "ACL1",
			"type":        "ACL_IPV4",
			"config/name": "ACL1",
			"config/type": "ACL_IPV4",
		})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/acl-entries",
		map[string]interface{}{
			"acl-entry[sequence-id=101]/sequence-id":                      101,
			"acl-entry[sequence-id=101]/config/sequence-id":               101,
			"acl-entry[sequence-id=101]/config/description":               "rule101",
			"acl-entry[sequence-id=101]/actions/config/forwarding-action": "ACCEPT",
			"acl-entry[sequence-id=101]/ipv4/config/protocol":             "IP_TCP",
			"acl-entry[sequence-id=101]/ipv4/config/source-address":       "101.0.0.0/8",
			"acl-entry[sequence-id=102]/sequence-id":                      102,
			"acl-entry[sequence-id=102]/config/sequence-id":               102,
			"acl-entry[sequence-id=102]/actions/config/forwarding-action": "DROP",
			"acl-entry[sequence-id=102]/ipv4/config/protocol":             "IP_GRE",
			"acl-entry[sequence-id=102]/ipv4/config/source-address":       "2.2.2.2/32",
			"acl-entry[sequence-id=103]/sequence-id":                      103,
			"acl-entry[sequence-id=103]/config/sequence-id":               103,
			"acl-entry[sequence-id=103]/config/description":               "rule103",
			"acl-entry[sequence-id=103]/actions/config/forwarding-action": "REJECT",
			"acl-entry[sequence-id=103]/ipv4/config/protocol":             "IP_UDP",
			"acl-entry[sequence-id=103]/ipv4/config/source-address":       "103.0.0.0/8",
			"acl-entry[sequence-id=104]/sequence-id":                      104,
			"acl-entry[sequence-id=104]/config/sequence-id":               104,
			"acl-entry[sequence-id=300]/sequence-id":                      300,
			"acl-entry[sequence-id=300]/config/sequence-id":               300,
			"acl-entry[sequence-id=300]/config/description":               "rule300",
		})
	exp.Deletes("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]",
		"config/description",
		"state",
	)
	exp.Deletes("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/acl-entries",
		"acl-entry[sequence-id=104]/config/description",
		"acl-entry[sequence-id=104]/actions/config/forwarding-action",
		"acl-entry[sequence-id=104]/ipv4/config/protocol",
		"acl-entry[sequence-id=104]/ipv4/config/source-address",
	)
	dif.Compare(t, &exp)
}

func TestAcl_LeafList(t *testing.T) {
	a1 := loadJSON(t, `{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4", "acl-entries": {"acl-entry": [
		{"sequence-id": 1, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 2, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 3, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 4, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 5, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 6, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 7, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 8, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 9}
		]}}]}}}`)
	a2 := loadJSON(t, `{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4", "acl-entries": {"acl-entry": [
		{"sequence-id": 1, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_RST"]}}},
		{"sequence-id": 2, "transport": {"config": {"tcp-flags": ["TCP_FIN", "TCP_SYN"]}}},
		{"sequence-id": 3, "transport": {"config": {"tcp-flags": ["TCP_SYN"]}}},
		{"sequence-id": 4, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN", "TCP_ACK"]}}},
		{"sequence-id": 5, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 6, "transport": {"config": {"tcp-flags": []}}},
		{"sequence-id": 7, "transport": {"config": {}}},
		{"sequence-id": 8},
		{"sequence-id": 9, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id":10, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}}
		]}}]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/acl-entries",
		map[string]interface{}{
			"acl-entry[sequence-id=1]/transport/config/tcp-flags":  "TCP_SYN,TCP_RST",
			"acl-entry[sequence-id=2]/transport/config/tcp-flags":  "TCP_FIN,TCP_SYN",
			"acl-entry[sequence-id=3]/transport/config/tcp-flags":  "TCP_SYN",
			"acl-entry[sequence-id=4]/transport/config/tcp-flags":  "TCP_SYN,TCP_FIN,TCP_ACK",
			"acl-entry[sequence-id=9]/transport/config/tcp-flags":  "TCP_SYN,TCP_FIN",
			"acl-entry[sequence-id=10]/sequence-id":                10,
			"acl-entry[sequence-id=10]/transport/config/tcp-flags": "TCP_SYN,TCP_FIN",
		})
	exp.Deletes("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/acl-entries",
		"acl-entry[sequence-id=6]/transport/config/tcp-flags",
		"acl-entry[sequence-id=7]/transport/config/tcp-flags",
		"acl-entry[sequence-id=8]/transport",
	)
	dif.Compare(t, &exp)
}

func TestAcl_LeafList_RecordAll(t *testing.T) {
	a1 := loadJSON(t, `{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4", "acl-entries": {"acl-entry": [
		{"sequence-id": 1, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 2, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 3, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 4, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}},
		{"sequence-id": 5, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}}
		]}}]}}}`)
	a2 := loadJSON(t, `{"acl":{"acl-sets":{"acl-set":[
		{"name": "ACL1", "type": "ACL_IPV4", "acl-entries": {"acl-entry": [
		{"sequence-id": 1, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_RST"]}}},
		{"sequence-id": 2, "transport": {"config": {"tcp-flags": ["TCP_FIN", "TCP_SYN"]}}},
		{"sequence-id": 3, "transport": {"config": {"tcp-flags": ["TCP_SYN"]}}},
		{"sequence-id": 4, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN", "TCP_ACK"]}}},
		{"sequence-id": 5, "transport": {"config": {"tcp-flags": ["TCP_SYN", "TCP_FIN"]}}}
		]}}]}}}`)

	var dif, exp diffData
	dif.YgotDiff(t, a1, a2, DiffOptions{RecordAll: true})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]",
		map[string]interface{}{
			"name": "ACL1",
			"type": "ACL_IPV4",
		})
	exp.Updates("/acl/acl-sets/acl-set[name=ACL1][type=ACL_IPV4]/acl-entries",
		map[string]interface{}{
			"acl-entry[sequence-id=1]/sequence-id":                1,
			"acl-entry[sequence-id=1]/transport/config/tcp-flags": "TCP_SYN,TCP_RST",
			"acl-entry[sequence-id=2]/sequence-id":                2,
			"acl-entry[sequence-id=2]/transport/config/tcp-flags": "TCP_FIN,TCP_SYN",
			"acl-entry[sequence-id=3]/sequence-id":                3,
			"acl-entry[sequence-id=3]/transport/config/tcp-flags": "TCP_SYN",
			"acl-entry[sequence-id=4]/sequence-id":                4,
			"acl-entry[sequence-id=4]/transport/config/tcp-flags": "TCP_SYN,TCP_FIN,TCP_ACK",
			"acl-entry[sequence-id=5]/sequence-id":                5,
			"acl-entry[sequence-id=5]/transport/config/tcp-flags": "TCP_SYN,TCP_FIN",
		})
	dif.Compare(t, &exp)
}
