package restconfbody

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/goyang/pkg/yang"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func leaf(name string, kind yang.TypeKind) *yang.Entry {
	return &yang.Entry{Name: name, Kind: yang.LeafEntry, Type: &yang.YangType{Kind: kind}}
}

func leafref(name, path string) *yang.Entry {
	return &yang.Entry{Name: name, Kind: yang.LeafEntry, Type: &yang.YangType{Kind: yang.Yleafref, Path: path}}
}

func leafList(name string, kind yang.TypeKind) *yang.Entry {
	e := leaf(name, kind)
	e.ListAttr = &yang.ListAttr{}
	return e
}

func union(name string, kinds ...yang.TypeKind) *yang.Entry {
	e := leaf(name, yang.Yunion)
	for _, k := range kinds {
		e.Type.Type = append(e.Type.Type, &yang.YangType{Kind: k})
	}
	return e
}

func unionList(name string, kinds ...yang.TypeKind) *yang.Entry {
	e := union(name, kinds...)
	e.ListAttr = &yang.ListAttr{}
	return e
}

func node(name string, kind yang.EntryKind, children ...*yang.Entry) *yang.Entry {
	e := &yang.Entry{Name: name, Kind: kind, Dir: map[string]*yang.Entry{}}
	for _, c := range children {
		c.Parent = e
		e.Dir[c.Name] = c
	}
	return e
}

func container(name string, children ...*yang.Entry) *yang.Entry {
	return node(name, yang.DirectoryEntry, children...)
}

func list(name, key string, children ...*yang.Entry) *yang.Entry {
	e := node(name, yang.DirectoryEntry, children...)
	e.ListAttr = &yang.ListAttr{}
	e.Key = key
	return e
}

// testSchema is a reduced OpenConfig-like schema covering containers, lists
// with leafref keys, an augment-style child, leaf-lists, 64-bit and decimal64
// leaves and a choice.
func testSchema() *yang.Entry {
	return container("device",
		container("interfaces",
			list("interface", "name",
				leafref("name", "../config/name"),
				container("config",
					leaf("name", yang.Ystring),
					leaf("mtu", yang.Yuint16),
					leaf("description", yang.Ystring),
					leaf("enabled", yang.Ybool),
					leafList("vlans", yang.Yuint16),
					leafList("tags", yang.Ystring),
				),
				container("counters",
					leaf("in-octets", yang.Yuint64),
					leaf("ratio", yang.Ydecimal64),
					leafList("samples", yang.Yint64),
					union("property", yang.Ystring, yang.Ybool, yang.Yint64, yang.Yuint64, yang.Ydecimal64),
					union("ratio-or-text", yang.Ystring, yang.Ydecimal64),
					union("small-or-big", yang.Yuint8, yang.Yuint64),
					union("text-or-port", yang.Ystring, yang.Yuint16),
					unionList("properties", yang.Ystring, yang.Yint64),
				),
				container("subinterfaces",
					list("subinterface", "index",
						leafref("index", "../config/index"),
						container("config", leaf("index", yang.Yuint32)),
						container("ipv4",
							container("addresses",
								list("address", "ip",
									leafref("ip", "../config/ip"),
									container("config",
										leaf("ip", yang.Ystring),
										leaf("prefix-length", yang.Yuint8),
									),
								),
							),
						),
					),
				),
			),
		),
		container("acl",
			container("acl-sets",
				list("acl-set", "name type",
					leafref("name", "../config/name"),
					leafref("type", "../config/type"),
					container("config",
						leaf("name", yang.Ystring),
						leaf("type", yang.Yidentityref),
						leaf("description", yang.Ystring),
					),
				),
			),
		),
		container("mode",
			node("kind", yang.ChoiceEntry,
				node("simple", yang.CaseEntry, leaf("x", yang.Ystring)),
			),
		),
	)
}

func jsonVal(s string) *gnmipb.TypedValue {
	return &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(s)}}
}

func plainJSONVal(s string) *gnmipb.TypedValue {
	return &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonVal{JsonVal: []byte(s)}}
}

func mustPath(t *testing.T, s string) *gnmipb.Path {
	t.Helper()
	p, err := ygot.StringToStructuredPath(s)
	if err != nil {
		t.Fatalf("bad path %q: %v", s, err)
	}
	return p
}

func TestFromGNMI(t *testing.T) {
	root := testSchema()
	const intf = "/interfaces/interface[name=Ethernet0]"
	const acl = "/acl/acl-sets/acl-set[name=A1][type=ACL_IPV4]"

	tests := []struct {
		desc    string
		path    string
		val     *gnmipb.TypedValue
		replace bool
		want    string
	}{{
		desc: "container value is the container content",
		path: intf + "/config",
		val:  jsonVal(`{"mtu": 9100, "description": "up"}`),
		want: `{"config": {"mtu": 9100, "description": "up"}}`,
	}, {
		desc: "module-qualified path element keeps its name",
		path: intf + "/subinterfaces/subinterface[index=0]/openconfig-if-ip:ipv4",
		val:  jsonVal(`{"addresses": {"address": [{"ip": "9.9.9.9", "config": {"ip": "9.9.9.9", "prefix-length": 24}}]}}`),
		want: `{"openconfig-if-ip:ipv4": {"addresses": {"address": [{"ip": "9.9.9.9", "config": {"ip": "9.9.9.9", "prefix-length": 24}}]}}}`,
	}, {
		desc: "leaf JSON scalar",
		path: intf + "/config/mtu",
		val:  jsonVal(`9100`),
		want: `{"mtu": 9100}`,
	}, {
		desc: "leaf JSON string",
		path: intf + "/config/description",
		val:  jsonVal(`"uplink"`),
		want: `{"description": "uplink"}`,
	}, {
		desc: "leaf uint_val",
		path: intf + "/config/mtu",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_UintVal{UintVal: 9100}},
		want: `{"mtu": 9100}`,
	}, {
		desc: "leaf string_val",
		path: intf + "/config/description",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "uplink"}},
		want: `{"description": "uplink"}`,
	}, {
		desc: "leaf bool_val",
		path: intf + "/config/enabled",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_BoolVal{BoolVal: true}},
		want: `{"enabled": true}`,
	}, {
		desc: "uint64 leaf is a JSON string",
		path: intf + "/counters/in-octets",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_UintVal{UintVal: 18446744073709551615}},
		want: `{"in-octets": "18446744073709551615"}`,
	}, {
		desc: "decimal64 leaf from decimal_val",
		path: intf + "/counters/ratio",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_DecimalVal{DecimalVal: &gnmipb.Decimal64{Digits: -1234, Precision: 2}}},
		want: `{"ratio": "-12.34"}`,
	}, {
		desc: "decimal64 leaf with leading zeros",
		path: intf + "/counters/ratio",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_DecimalVal{DecimalVal: &gnmipb.Decimal64{Digits: 5, Precision: 3}}},
		want: `{"ratio": "0.005"}`,
	}, {
		desc: "json_val decimal64 leaf number is a JSON string",
		path: intf + "/counters/ratio",
		val:  plainJSONVal(`1.25`),
		want: `{"ratio": "1.25"}`,
	}, {
		desc: "json_val uint64 leaf number is a JSON string",
		path: intf + "/counters/in-octets",
		val:  plainJSONVal(`18446744073709551615`),
		want: `{"in-octets": "18446744073709551615"}`,
	}, {
		desc: "json_ietf_val 64-bit string stays a string",
		path: intf + "/counters/in-octets",
		val:  jsonVal(`"5"`),
		want: `{"in-octets": "5"}`,
	}, {
		desc: "json_val int64 leaf-list numbers are JSON strings",
		path: intf + "/counters/samples",
		val:  plainJSONVal(`[1, -2]`),
		want: `{"samples": ["1", "-2"]}`,
	}, {
		desc: "json_val uint64 leaf exponent number is a decimal string",
		path: intf + "/counters/in-octets",
		val:  plainJSONVal(`1e3`),
		want: `{"in-octets": "1000"}`,
	}, {
		desc: "json_val uint64 leaf with zero fraction is a decimal string",
		path: intf + "/counters/in-octets",
		val:  plainJSONVal(`18446744073709551615.000`),
		want: `{"in-octets": "18446744073709551615"}`,
	}, {
		desc: "json_val int64 leaf-list integer-valued numbers are decimal strings",
		path: intf + "/counters/samples",
		val:  plainJSONVal(`[-1.5e1, 2E+0, 0.0]`),
		want: `{"samples": ["-15", "2", "0"]}`,
	}, {
		desc: "json_val number for a union with 64-bit members is a JSON string",
		path: intf + "/counters/property",
		val:  plainJSONVal(`1e3`),
		want: `{"property": "1000"}`,
	}, {
		desc: "json_val fraction for a union with a decimal64 member is a JSON string",
		path: intf + "/counters/property",
		val:  plainJSONVal(`2.5`),
		want: `{"property": "2.5"}`,
	}, {
		desc: "json_val number for a union with a decimal64 member keeps its digits",
		path: intf + "/counters/ratio-or-text",
		val:  plainJSONVal(`1.50`),
		want: `{"ratio-or-text": "1.50"}`,
	}, {
		desc: "int_val for a union with 64-bit members is a JSON string",
		path: intf + "/counters/property",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_IntVal{IntVal: -7}},
		want: `{"property": "-7"}`,
	}, {
		desc: "string and bool values for a union are unchanged",
		path: intf + "/counters",
		val:  plainJSONVal(`{"property": true, "ratio-or-text": "high"}`),
		want: `{"counters": {"property": true, "ratio-or-text": "high"}}`,
	}, {
		desc: "union numbers nested in a container",
		path: intf + "/counters",
		val:  plainJSONVal(`{"property": 3, "small-or-big": 300}`),
		want: `{"counters": {"property": "3", "small-or-big": "300"}}`,
	}, {
		desc: "union number in the range of a narrow integer member stays a number",
		path: intf + "/counters/small-or-big",
		val:  plainJSONVal(`7`),
		want: `{"small-or-big": 7}`,
	}, {
		desc: "union without 64-bit members keeps JSON numbers",
		path: intf + "/counters/text-or-port",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_UintVal{UintVal: 8080}},
		want: `{"text-or-port": 8080}`,
	}, {
		desc: "union leaf-list numbers are JSON strings",
		path: intf + "/counters/properties",
		val:  plainJSONVal(`[1, "x", 2.0]`),
		want: `{"properties": ["1", "x", "2"]}`,
	}, {
		desc: "union leaflist_val numbers are JSON strings",
		path: intf + "/counters/properties",
		val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_LeaflistVal{LeaflistVal: &gnmipb.ScalarArray{Element: []*gnmipb.TypedValue{
			{Value: &gnmipb.TypedValue_IntVal{IntVal: 4}},
			{Value: &gnmipb.TypedValue_StringVal{StringVal: "y"}},
		}}}},
		want: `{"properties": ["4", "y"]}`,
	}, {
		desc: "json_val 64-bit numbers nested in a list entry",
		path: intf,
		val:  plainJSONVal(`{"config": {"mtu": 9100}, "counters": {"in-octets": 7, "ratio": 0.5, "samples": [3]}}`),
		want: `{"interface": [{"name": "Ethernet0", "config": {"mtu": 9100}, "counters": {"in-octets": "7", "ratio": "0.5", "samples": ["3"]}}]}`,
	}, {
		desc: "json_val 64-bit numbers in a whole list value, with module prefixes",
		path: "/interfaces/interface",
		val:  plainJSONVal(`[{"name": "Ethernet0", "openconfig-interfaces:counters": {"in-octets": 7}, "unknown": 1}]`),
		want: `{"interface": [{"name": "Ethernet0", "openconfig-interfaces:counters": {"in-octets": "7"}, "unknown": 1}]}`,
	}, {
		desc: "leaf-list from leaflist_val",
		path: intf + "/config/vlans",
		val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_LeaflistVal{LeaflistVal: &gnmipb.ScalarArray{Element: []*gnmipb.TypedValue{
			{Value: &gnmipb.TypedValue_UintVal{UintVal: 10}},
			{Value: &gnmipb.TypedValue_UintVal{UintVal: 20}},
		}}}},
		want: `{"vlans": [10, 20]}`,
	}, {
		desc: "leaf-list from JSON array",
		path: intf + "/config/tags",
		val:  jsonVal(`["a", "b"]`),
		want: `{"tags": ["a", "b"]}`,
	}, {
		desc: "whole list value is a JSON array",
		path: "/acl/acl-sets/acl-set",
		val:  jsonVal(`[{"name": "A1", "type": "ACL_IPV4", "config": {"name": "A1", "type": "ACL_IPV4"}}]`),
		want: `{"acl-set": [{"name": "A1", "type": "ACL_IPV4", "config": {"name": "A1", "type": "ACL_IPV4"}}]}`,
	}, {
		desc: "list entry gets its keys from the path",
		path: acl,
		val:  jsonVal(`{"config": {"description": "d"}}`),
		want: `{"acl-set": [{"name": "A1", "type": "ACL_IPV4", "config": {"description": "d"}}]}`,
	}, {
		desc: "list entry keys matching the path, identity with module prefix",
		path: acl,
		val:  jsonVal(`{"name": "A1", "type": "openconfig-acl:ACL_IPV4"}`),
		want: `{"acl-set": [{"name": "A1", "type": "openconfig-acl:ACL_IPV4"}]}`,
	}, {
		desc: "numeric list key from the path is a JSON number",
		path: intf + "/subinterfaces/subinterface[index=0]",
		val:  jsonVal(`{"config": {"index": 0}}`),
		want: `{"subinterface": [{"index": 0, "config": {"index": 0}}]}`,
	}, {
		desc: "json_val numeric key compares by value",
		path: intf + "/subinterfaces/subinterface[index=0]",
		val:  plainJSONVal(`{"index": 0.0, "config": {"index": 0}}`),
		want: `{"subinterface": [{"index": 0.0, "config": {"index": 0}}]}`,
	}, {
		desc: "json_val numeric key with exponent compares by value",
		path: intf + "/subinterfaces/subinterface[index=100]",
		val:  plainJSONVal(`{"index": 1e2}`),
		want: `{"subinterface": [{"index": 1e2}]}`,
	}, {
		desc: "update of a list entry with an empty value",
		path: acl,
		val:  jsonVal(`{}`),
		want: `{"acl-set": [{"name": "A1", "type": "ACL_IPV4"}]}`,
	}, {
		desc: "leaf through a choice and case",
		path: "/mode/x",
		val:  jsonVal(`"v"`),
		want: `{"x": "v"}`,
	}, {
		desc: "json_val container",
		path: intf + "/config",
		val:  plainJSONVal(`{"mtu": 9100}`),
		want: `{"config": {"mtu": 9100}}`,
	}, {
		desc: "json_val leaf number",
		path: intf + "/config/mtu",
		val:  plainJSONVal(`9100`),
		want: `{"mtu": 9100}`,
	}, {
		desc: "json_val leaf string",
		path: intf + "/config/description",
		val:  plainJSONVal(`"uplink"`),
		want: `{"description": "uplink"}`,
	}, {
		desc: "json_val leaf bool",
		path: intf + "/config/enabled",
		val:  plainJSONVal(`false`),
		want: `{"enabled": false}`,
	}, {
		desc: "json_val list entry",
		path: acl,
		val:  plainJSONVal(`{"config": {"description": "d"}}`),
		want: `{"acl-set": [{"name": "A1", "type": "ACL_IPV4", "config": {"description": "d"}}]}`,
	}, {
		desc: "json_val whole list",
		path: "/acl/acl-sets/acl-set",
		val:  plainJSONVal(`[{"name": "A1", "type": "ACL_IPV4"}]`),
		want: `{"acl-set": [{"name": "A1", "type": "ACL_IPV4"}]}`,
	}, {
		desc: "json_val leaf-list",
		path: intf + "/config/vlans",
		val:  plainJSONVal(`[10, 20]`),
		want: `{"vlans": [10, 20]}`,
	}, {
		desc: "JSON numbers keep their digits",
		path: intf + "/counters/in-octets",
		val:  jsonVal(`18446744073709551615.0000000001`),
		want: `{"in-octets": "18446744073709551615.0000000001"}`,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := FromGNMI(root, mustPath(t, tt.path), tt.val, tt.replace)
			if err != nil {
				t.Fatalf("FromGNMI() error = %v", err)
			}
			assertJSONEqual(t, got, tt.want)
		})
	}
}

func TestFromGNMIErrors(t *testing.T) {
	root := testSchema()
	const intf = "/interfaces/interface[name=Ethernet0]"
	const acl = "/acl/acl-sets/acl-set[name=A1][type=ACL_IPV4]"

	tests := []struct {
		desc    string
		path    string
		val     *gnmipb.TypedValue
		replace bool
		code    codes.Code
	}{{
		desc: "root path",
		path: "/",
		val:  jsonVal(`{}`),
		code: codes.Unimplemented,
	}, {
		desc: "unknown path element",
		path: intf + "/config/speed",
		val:  jsonVal(`1`),
		code: codes.InvalidArgument,
	}, {
		desc: "missing value",
		path: intf + "/config/mtu",
		val:  &gnmipb.TypedValue{},
		code: codes.InvalidArgument,
	}, {
		desc: "invalid JSON",
		path: intf + "/config",
		val:  jsonVal(`{"mtu":`),
		code: codes.InvalidArgument,
	}, {
		desc: "data after the JSON value",
		path: intf + "/config",
		val:  jsonVal(`{"mtu": 1} {}`),
		code: codes.InvalidArgument,
	}, {
		desc: "stray closing bracket after a scalar",
		path: intf + "/config/mtu",
		val:  jsonVal(`9100]`),
		code: codes.InvalidArgument,
	}, {
		desc: "stray closing brace after an object",
		path: intf + "/config",
		val:  jsonVal(`{"mtu": 9100}}`),
		code: codes.InvalidArgument,
	}, {
		desc: "stray closing bracket in json_val",
		path: intf + "/config",
		val:  plainJSONVal(`{"mtu": 9100}]`),
		code: codes.InvalidArgument,
	}, {
		desc: "decimal_val precision above the decimal64 limit",
		path: intf + "/counters/ratio",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_DecimalVal{DecimalVal: &gnmipb.Decimal64{Digits: 1, Precision: 19}}},
		code: codes.InvalidArgument,
	}, {
		desc: "decimal_val with a huge precision for a non-decimal leaf",
		path: intf + "/config/mtu",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_DecimalVal{DecimalVal: &gnmipb.Decimal64{Digits: 1, Precision: 4294967295}}},
		code: codes.InvalidArgument,
	}, {
		desc: "scalar value for a container",
		path: intf + "/config",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_UintVal{UintVal: 1}},
		code: codes.InvalidArgument,
	}, {
		desc: "JSON scalar for a container",
		path: intf + "/config",
		val:  jsonVal(`1`),
		code: codes.InvalidArgument,
	}, {
		desc: "JSON object for a whole list",
		path: "/acl/acl-sets/acl-set",
		val:  jsonVal(`{"acl-set": []}`),
		code: codes.InvalidArgument,
	}, {
		desc: "JSON array for a list entry",
		path: acl,
		val:  jsonVal(`[{"name": "A1"}]`),
		code: codes.InvalidArgument,
	}, {
		desc: "list key differs from the path key",
		path: acl,
		val:  jsonVal(`{"name": "A2"}`),
		code: codes.InvalidArgument,
	}, {
		desc: "IPv6 list key differs from the path key",
		path: intf + "/subinterfaces/subinterface[index=0]/ipv4/addresses/address[ip=150::1]",
		val:  jsonVal(`{"ip": "2001::1"}`),
		code: codes.InvalidArgument,
	}, {
		desc: "numeric key with a different value",
		path: intf + "/subinterfaces/subinterface[index=0]",
		val:  plainJSONVal(`{"index": 0.5}`),
		code: codes.InvalidArgument,
	}, {
		desc: "string key compares exactly",
		path: intf + "/subinterfaces/subinterface[index=0]/ipv4/addresses/address[ip=1.0]",
		val:  jsonVal(`{"ip": "1.00"}`),
		code: codes.InvalidArgument,
	}, {
		desc: "json_val scalar for a container",
		path: intf + "/config",
		val:  plainJSONVal(`1`),
		code: codes.InvalidArgument,
	}, {
		desc: "invalid json_val",
		path: intf + "/config/mtu",
		val:  plainJSONVal(`router1`),
		code: codes.InvalidArgument,
	}, {
		desc:    "replace of a list entry with an empty value",
		path:    acl,
		val:     jsonVal(`{}`),
		replace: true,
		code:    codes.InvalidArgument,
	}, {
		desc: "leaflist_val for a leaf",
		path: intf + "/config/mtu",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_LeaflistVal{LeaflistVal: &gnmipb.ScalarArray{}}},
		code: codes.InvalidArgument,
	}, {
		desc: "scalar for a leaf-list",
		path: intf + "/config/vlans",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_UintVal{UintVal: 10}},
		code: codes.InvalidArgument,
	}, {
		desc: "unsupported value type",
		path: intf + "/config/description",
		val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_AnyVal{}},
		code: codes.InvalidArgument,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := FromGNMI(root, mustPath(t, tt.path), tt.val, tt.replace)
			if err == nil {
				t.Fatalf("FromGNMI() = %s, want error code %v", got, tt.code)
			}
			if c := status.Code(err); c != tt.code {
				t.Fatalf("FromGNMI() error code = %v (%v), want %v", c, err, tt.code)
			}
		})
	}
}

func TestDecimalString(t *testing.T) {
	tests := []struct {
		digits    int64
		precision uint32
		want      string
		wantErr   bool
	}{
		{0, 0, "0", false},
		{12, 0, "12", false},
		{1234, 2, "12.34", false},
		{-5, 1, "-0.5", false},
		{-9223372036854775808, 0, "-9223372036854775808", false},
		{-9223372036854775808, 18, "-9.223372036854775808", false},
		{1, 18, "0.000000000000000001", false},
		{1, 19, "", true},
		{1, 4294967295, "", true},
	}
	for _, tt := range tests {
		got, err := decimalString(&gnmipb.Decimal64{Digits: tt.digits, Precision: tt.precision})
		if (err != nil) != tt.wantErr {
			t.Errorf("decimalString(%d, %d) error = %v, want error %v", tt.digits, tt.precision, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("decimalString(%d, %d) = %q, want %q", tt.digits, tt.precision, got, tt.want)
		}
	}
}

func TestIntegerString(t *testing.T) {
	tests := []struct{ in, want string }{
		{"1000", "1000"},
		{"1e3", "1000"},
		{"1E+3", "1000"},
		{"1000.0", "1000"},
		{"1.5e1", "15"},
		{"12500e-2", "125"},
		{"-9223372036854775808.00", "-9223372036854775808"},
		{"18446744073709551615e0", "18446744073709551615"},
		{"0.0", "0"},
		{"-0", "0"},
		{"007", "7"},
		{"1.5", "1.5"},
		{"15e-1", "15e-1"},
		{"1e-64", "1e-64"},
		{"1e65", "1e65"},
		{"1e999999999", "1e999999999"},
		{"18446744073709551615.0000000001", "18446744073709551615.0000000001"},
	}
	for _, tt := range tests {
		if got := integerString(tt.in); got != tt.want {
			t.Errorf("integerString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSameKeyValue(t *testing.T) {
	tests := []struct {
		kind    yang.TypeKind
		jv      interface{}
		pathVal string
		want    bool
	}{
		{yang.Yuint32, json.Number("0.0"), "0", true},
		{yang.Yuint32, json.Number("1e2"), "100", true},
		{yang.Yuint32, json.Number("1"), "2", false},
		{yang.Yint64, "-5", "-5.0", true},
		{yang.Yuint64, "18446744073709551615", "18446744073709551615.0", true},
		{yang.Ydecimal64, json.Number("1.50"), "1.5", true},
		{yang.Ydecimal64, json.Number("1.5"), "1.05", false},
		{yang.Yuint32, json.Number("1"), "x", false},
		{yang.Ystring, "1.0", "1", false},
		{yang.Ystring, json.Number("1.0"), "1", false},
		{yang.Yidentityref, "openconfig-acl:ACL_IPV4", "ACL_IPV4", true},
		{yang.Ybool, true, "true", true},
	}
	for _, tt := range tests {
		if got := sameKeyValue(tt.kind, tt.jv, tt.pathVal); got != tt.want {
			t.Errorf("sameKeyValue(%v, %#v, %q) = %v, want %v", tt.kind, tt.jv, tt.pathVal, got, tt.want)
		}
	}
}

func TestUnionNumber(t *testing.T) {
	nested := &yang.YangType{Kind: yang.Yunion, Type: []*yang.YangType{
		{Kind: yang.Ystring},
		{Kind: yang.Yunion, Type: []*yang.YangType{{Kind: yang.Yint8}, {Kind: yang.Yuint64}}},
	}}
	tests := []struct {
		in   string
		want interface{}
	}{
		{"-128", json.Number("-128")},
		{"1e2", json.Number("1e2")},
		{"200", "200"},
		{"-129", "-129"},
		{"1.5", "1.5"},
	}
	for _, tt := range tests {
		if got := number(nested, tt.in); got != tt.want {
			t.Errorf("number(nested union, %q) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestCanonicalNumber(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0", "0", true},
		{"-0.0e5", "0", true},
		{"1e3", "1000", true},
		{"-5.0e-1", "-0.5", true},
		{"0.0012500", "0.00125", true},
		{"125e-5", "0.00125", true},
		{"18446744073709551615.000", "18446744073709551615", true},
		{"1e65", "", false},
		{"1.2.3", "", false},
		{"abc", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := canonicalNumber(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("canonicalNumber(%q) = %q, %v, want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w interface{}
	dg := json.NewDecoder(strings.NewReader(string(got)))
	dg.UseNumber()
	if err := dg.Decode(&g); err != nil {
		t.Fatalf("result is not JSON: %s: %v", got, err)
	}
	dw := json.NewDecoder(strings.NewReader(want))
	dw.UseNumber()
	if err := dw.Decode(&w); err != nil {
		t.Fatalf("bad expected JSON %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %s, want %s", got, want)
	}
}
