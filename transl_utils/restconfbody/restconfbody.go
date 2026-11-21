// Package restconfbody converts the value of a gNMI Set update or replace into
// the RESTCONF request body that translib parses.
//
// A gNMI Update carries the value of the node at its path, for example
// {"mtu": 9100} for .../config or 9100 for .../config/mtu. Translib parses a
// RESTCONF body, which is a JSON object keyed by the target node name, for
// example {"config": {"mtu": 9100}} and {"mtu": 9100}.
package restconfbody

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/goyang/pkg/yang"
	"github.com/openconfig/ygot/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxLeafrefDepth bounds leafref chains so that a cyclic schema cannot loop.
const maxLeafrefDepth = 16

// maxUnionDepth bounds the nesting of unions within unions.
const maxUnionDepth = 16

// FromGNMI returns the translib body for setting val at path.
//
// root is the schema entry of the device root. path is the full path of the
// update, including the request prefix. replace marks a gNMI replace.
func FromGNMI(root *yang.Entry, path *gnmipb.Path, val *gnmipb.TypedValue, replace bool) ([]byte, error) {
	elems := path.GetElem()
	if len(elems) == 0 {
		return nil, status.Error(codes.Unimplemented, "set on the root path is not supported")
	}
	schema, err := targetSchema(root, elems)
	if err != nil {
		return nil, err
	}
	last := elems[len(elems)-1]

	v, err := decodeValue(schema, val)
	if err != nil {
		return nil, err
	}

	var body interface{}
	switch {
	case schema.IsList() && len(last.GetKey()) > 0:
		entry, err := listEntry(schema, last, v, replace)
		if err != nil {
			return nil, err
		}
		body = []interface{}{entry}
	case schema.IsList() || schema.IsLeafList():
		if _, ok := v.([]interface{}); !ok {
			return nil, invalidArg("value of %s must be a JSON array", schema.Name)
		}
		body = v
	case schema.IsContainer():
		if _, ok := v.(map[string]interface{}); !ok {
			return nil, invalidArg("value of %s must be a JSON object", schema.Name)
		}
		body = v
	default:
		body = v
	}

	return json.Marshal(map[string]interface{}{last.GetName(): body})
}

// targetSchema returns the schema entry of the node addressed by elems.
func targetSchema(root *yang.Entry, elems []*gnmipb.PathElem) (*yang.Entry, error) {
	e := root
	for _, elem := range elems {
		name := localName(elem.GetName())
		child := findChild(e, name)
		if child == nil {
			return nil, invalidArg("unknown path element %q", elem.GetName())
		}
		e = child
	}
	return e, nil
}

// findChild returns the data child of e named name, looking through choice and
// case nodes, which do not appear in data paths.
func findChild(e *yang.Entry, name string) *yang.Entry {
	if c, ok := e.Dir[name]; ok && !c.IsChoice() && !c.IsCase() {
		return c
	}
	for _, c := range e.Dir {
		if c.IsChoice() || c.IsCase() {
			if found := findChild(c, name); found != nil {
				return found
			}
		}
	}
	return nil
}

// listEntry returns the JSON object of one list entry. Keys in the object must
// match the path keys. Keys missing from the object are added from the path.
func listEntry(list *yang.Entry, elem *gnmipb.PathElem, v interface{}, replace bool) (map[string]interface{}, error) {
	obj, ok := v.(map[string]interface{})
	if !ok {
		return nil, invalidArg("value of list entry %s must be a JSON object", list.Name)
	}
	if replace && len(obj) == 0 {
		return nil, invalidArg("replace of list entry %s with an empty value is invalid; use delete", list.Name)
	}

	for key, pathVal := range elem.GetKey() {
		keyLeaf := findChild(list, key)
		if keyLeaf == nil {
			return nil, invalidArg("%s is not a key of list %s", key, list.Name)
		}
		leaf, err := resolveLeafref(keyLeaf)
		if err != nil {
			return nil, err
		}
		if member, found := findMember(obj, key); found {
			if !sameKeyValue(leaf.Type.Kind, obj[member], pathVal) {
				return nil, invalidArg("key %s=%v in the value of %s does not match the path key %q", key, obj[member], list.Name, pathVal)
			}
			continue
		}
		kv, err := keyJSON(leaf, pathVal)
		if err != nil {
			return nil, err
		}
		obj[key] = kv
	}
	return obj, nil
}

// findMember returns the member of obj whose name, without a module prefix,
// is name.
func findMember(obj map[string]interface{}, name string) (string, bool) {
	for m := range obj {
		if localName(m) == name {
			return m, true
		}
	}
	return "", false
}

// sameKeyValue reports whether a key value from JSON equals the path key
// value. Identity values compare without their module prefix, and values of
// integer and decimal64 keys compare as numbers, so 0.0 equals 0.
func sameKeyValue(kind yang.TypeKind, jv interface{}, pathVal string) bool {
	switch v := jv.(type) {
	case string:
		switch {
		case kind == yang.Yidentityref:
			return localName(v) == localName(pathVal)
		case isNumberKind(kind):
			return sameNumber(v, pathVal)
		}
		return v == pathVal
	case json.Number:
		if isNumberKind(kind) {
			return sameNumber(v.String(), pathVal)
		}
		return v.String() == pathVal
	case bool:
		return strconv.FormatBool(v) == pathVal
	}
	return false
}

// keyJSON returns the JSON value of a list key given as a path string. leaf
// is the key leaf with leafrefs resolved.
func keyJSON(leaf *yang.Entry, pathVal string) (interface{}, error) {
	switch leaf.Type.Kind {
	case yang.Yint8, yang.Yint16, yang.Yint32, yang.Yuint8, yang.Yuint16, yang.Yuint32:
		if _, err := strconv.ParseFloat(pathVal, 64); err != nil {
			return nil, invalidArg("key %s value %q is not a number", leaf.Name, pathVal)
		}
		return json.Number(pathVal), nil
	case yang.Ybool:
		b, err := strconv.ParseBool(pathVal)
		if err != nil {
			return nil, invalidArg("key %s value %q is not a boolean", leaf.Name, pathVal)
		}
		return b, nil
	}
	return pathVal, nil
}

// decodeValue returns the JSON value of val for the node schema.
func decodeValue(schema *yang.Entry, val *gnmipb.TypedValue) (interface{}, error) {
	switch tv := val.GetValue().(type) {
	case nil:
		return nil, invalidArg("missing value for %s", schema.Name)
	case *gnmipb.TypedValue_JsonIetfVal:
		return decodeSchemaJSON(schema, tv.JsonIetfVal)
	case *gnmipb.TypedValue_JsonVal:
		return decodeSchemaJSON(schema, tv.JsonVal)
	}

	if !schema.IsLeaf() && !schema.IsLeafList() {
		return nil, invalidArg("%T is valid only for a leaf or leaf-list, and %s is neither", val.GetValue(), schema.Name)
	}
	leaf, err := resolveLeafref(schema)
	if err != nil {
		return nil, err
	}
	if lv, ok := val.GetValue().(*gnmipb.TypedValue_LeaflistVal); ok {
		if !schema.IsLeafList() {
			return nil, invalidArg("leaflist_val is valid only for a leaf-list, and %s is a leaf", schema.Name)
		}
		out := make([]interface{}, 0, len(lv.LeaflistVal.GetElement()))
		for _, el := range lv.LeaflistVal.GetElement() {
			jv, err := scalarJSON(leaf.Type, el)
			if err != nil {
				return nil, err
			}
			out = append(out, jv)
		}
		return out, nil
	}
	if schema.IsLeafList() {
		return nil, invalidArg("value of leaf-list %s must be a leaflist_val or a JSON array", schema.Name)
	}
	return scalarJSON(leaf.Type, val)
}

// decodeJSON parses one JSON value and keeps numbers in their original form.
func decodeJSON(b []byte) (interface{}, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v interface{}
	if err := d.Decode(&v); err != nil {
		return nil, invalidArg("invalid JSON value: %v", err)
	}
	// Decoder.More reports false before a stray ']' or '}', so check for the
	// end of input instead.
	if _, err := d.Token(); err != io.EOF {
		return nil, invalidArg("invalid JSON value: data after the first value")
	}
	return v, nil
}

// decodeSchemaJSON parses a JSON value of the node schema and converts its
// numbers to the translib form.
func decodeSchemaJSON(schema *yang.Entry, b []byte) (interface{}, error) {
	v, err := decodeJSON(b)
	if err != nil {
		return nil, err
	}
	return schemaNumbers(schema, v), nil
}

// schemaNumbers returns v with the JSON numbers of 64-bit integer and
// decimal64 leaves and leaf-lists changed to strings, which translib requires
// (RFC 7951 section 6.1). json_val follows RFC 7159, where these values are
// numbers. Members that are not in the schema are left for translib to reject.
func schemaNumbers(schema *yang.Entry, v interface{}) interface{} {
	switch {
	case schema.IsLeaf():
		return leafNumber(schema, v)
	case schema.IsLeafList():
		if arr, ok := v.([]interface{}); ok {
			for i := range arr {
				arr[i] = leafNumber(schema, arr[i])
			}
		}
		return v
	}
	switch val := v.(type) {
	case []interface{}:
		if schema.IsList() {
			for i := range val {
				val[i] = schemaNumbers(schema, val[i])
			}
		}
	case map[string]interface{}:
		for m, mv := range val {
			if child := findChild(schema, localName(m)); child != nil {
				val[m] = schemaNumbers(child, mv)
			}
		}
	}
	return v
}

func leafNumber(leaf *yang.Entry, v interface{}) interface{} {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	typed, err := resolveLeafref(leaf)
	if err != nil {
		return v
	}
	return number(typed.Type, n.String())
}

// scalarJSON returns the JSON value of a scalar TypedValue for a leaf of
// type t. Values of 64-bit integer and decimal64 leaves are JSON strings
// (RFC 7951 section 6.1); other numbers are JSON numbers.
func scalarJSON(t *yang.YangType, val *gnmipb.TypedValue) (interface{}, error) {
	switch v := val.GetValue().(type) {
	case *gnmipb.TypedValue_StringVal:
		return v.StringVal, nil
	case *gnmipb.TypedValue_AsciiVal:
		return v.AsciiVal, nil
	case *gnmipb.TypedValue_BoolVal:
		return v.BoolVal, nil
	case *gnmipb.TypedValue_IntVal:
		return number(t, strconv.FormatInt(v.IntVal, 10)), nil
	case *gnmipb.TypedValue_UintVal:
		return number(t, strconv.FormatUint(v.UintVal, 10)), nil
	case *gnmipb.TypedValue_FloatVal:
		return number(t, strconv.FormatFloat(float64(v.FloatVal), 'f', -1, 32)), nil
	case *gnmipb.TypedValue_DecimalVal:
		str, err := decimalString(v.DecimalVal)
		if err != nil {
			return nil, err
		}
		return number(t, str), nil
	case *gnmipb.TypedValue_BytesVal:
		return base64.StdEncoding.EncodeToString(v.BytesVal), nil
	}
	return nil, invalidArg("unsupported value type %T", val.GetValue())
}

// number returns the JSON value of the number s for a leaf of type t.
func number(t *yang.YangType, s string) interface{} {
	switch t.Kind {
	case yang.Yint64, yang.Yuint64:
		return integerString(s)
	case yang.Ydecimal64:
		return s
	case yang.Yunion:
		return unionNumber(t, s)
	}
	return json.Number(s)
}

// unionNumber returns the JSON value of the number s for a union. ygot takes
// a JSON number only for an 8 to 32-bit integer member and a JSON string for
// an int64, uint64 or decimal64 member, so s stays a number if such an integer
// member can hold it, and is otherwise a string for a 64-bit member.
func unionNumber(t *yang.YangType, s string) interface{} {
	var int64Member, decimalMember bool
	for _, m := range unionMembers(t, 0) {
		switch m.Kind {
		case yang.Yint8, yang.Yint16, yang.Yint32, yang.Yuint8, yang.Yuint16, yang.Yuint32:
			if fitsInteger(m.Kind, s) {
				return json.Number(s)
			}
		case yang.Yint64, yang.Yuint64:
			int64Member = true
		case yang.Ydecimal64:
			decimalMember = true
		}
	}
	switch {
	case int64Member:
		return integerString(s)
	case decimalMember:
		return s
	}
	return json.Number(s)
}

// unionMembers returns the member types of the union t, with the members of
// nested unions in place of the nested union.
func unionMembers(t *yang.YangType, depth int) []*yang.YangType {
	var out []*yang.YangType
	for _, m := range t.Type {
		if m.Kind == yang.Yunion && depth < maxUnionDepth {
			out = append(out, unionMembers(m, depth+1)...)
		} else {
			out = append(out, m)
		}
	}
	return out
}

// fitsInteger reports whether the number s is an integer in the range of the
// 8 to 32-bit integer kind.
func fitsInteger(kind yang.TypeKind, s string) bool {
	n, ok := canonicalNumber(s)
	if !ok || strings.Contains(n, ".") {
		return false
	}
	var err error
	switch kind {
	case yang.Yint8:
		_, err = strconv.ParseInt(n, 10, 8)
	case yang.Yint16:
		_, err = strconv.ParseInt(n, 10, 16)
	case yang.Yint32:
		_, err = strconv.ParseInt(n, 10, 32)
	case yang.Yuint8:
		_, err = strconv.ParseUint(n, 10, 8)
	case yang.Yuint16:
		_, err = strconv.ParseUint(n, 10, 16)
	case yang.Yuint32:
		_, err = strconv.ParseUint(n, 10, 32)
	default:
		return false
	}
	return err == nil
}

// integerString returns the JSON number s, such as 1e3 or 1000.0, as a
// decimal integer string, because ygot parses 64-bit integers with
// strconv.ParseInt and ParseUint. A number with a fraction is returned
// unchanged so that translib rejects it.
func integerString(s string) string {
	n, ok := canonicalNumber(s)
	if !ok || strings.Contains(n, ".") {
		return s
	}
	return n
}

func isNumberKind(kind yang.TypeKind) bool {
	switch kind {
	case yang.Yint8, yang.Yint16, yang.Yint32, yang.Yint64,
		yang.Yuint8, yang.Yuint16, yang.Yuint32, yang.Yuint64, yang.Ydecimal64:
		return true
	}
	return false
}

// sameNumber reports whether the numbers a and b are equal. Strings that are
// not numbers compare exactly.
func sameNumber(a, b string) bool {
	ca, okA := canonicalNumber(a)
	cb, okB := canonicalNumber(b)
	if okA && okB {
		return ca == cb
	}
	return a == b
}

// maxNumberExponent bounds the exponent that canonicalNumber expands. Values
// of 64-bit integer and decimal64 leaves need far smaller exponents.
const maxNumberExponent = 64

// canonicalNumber returns the JSON number s as [-]digits[.digits] without an
// exponent or redundant zeros, for example "1000" for 1e3 and "-0.5" for
// -5.0e-1. The digits are kept exactly. ok is false if s is not a number or
// its exponent is larger than maxNumberExponent.
func canonicalNumber(s string) (string, bool) {
	sign, mant := "", s
	if strings.HasPrefix(mant, "-") {
		sign, mant = "-", mant[1:]
	}
	exp := 0
	if i := strings.IndexAny(mant, "eE"); i >= 0 {
		e, err := strconv.Atoi(strings.TrimPrefix(mant[i+1:], "+"))
		if err != nil || e > maxNumberExponent || e < -maxNumberExponent {
			return "", false
		}
		exp, mant = e, mant[:i]
	}
	intPart, frac := mant, ""
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		intPart, frac = mant[:i], mant[i+1:]
	}
	digits := intPart + frac
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return "", false
	}
	point := len(intPart) + exp
	if point < 0 {
		digits = strings.Repeat("0", -point) + digits
		point = 0
	}
	if point > len(digits) {
		digits += strings.Repeat("0", point-len(digits))
	}
	n := strings.TrimLeft(digits[:point], "0")
	if n == "" {
		n = "0"
	}
	if f := strings.TrimRight(digits[point:], "0"); f != "" {
		n += "." + f
	}
	if n == "0" {
		return n, true
	}
	return sign + n, true
}

// maxFractionDigits is the largest number of fraction digits of a YANG
// decimal64 value (RFC 7950 section 9.3.4).
const maxFractionDigits = 18

// decimalString formats a gNMI Decimal64 as a decimal string. The precision
// comes from the client, so it is checked before it sizes any allocation.
func decimalString(d *gnmipb.Decimal64) (string, error) {
	if d.GetPrecision() > maxFractionDigits {
		return "", invalidArg("decimal_val precision %d is more than %d", d.GetPrecision(), maxFractionDigits)
	}
	digits := d.GetDigits()
	sign := ""
	u := uint64(digits)
	if digits < 0 {
		sign = "-"
		u = uint64(-(digits + 1)) + 1
	}
	s := strconv.FormatUint(u, 10)
	p := int(d.GetPrecision())
	if p == 0 {
		return sign + s, nil
	}
	if len(s) <= p {
		s = strings.Repeat("0", p-len(s)+1) + s
	}
	return sign + s[:len(s)-p] + "." + s[len(s)-p:], nil
}

// resolveLeafref follows leafrefs from e to the leaf that defines the type.
func resolveLeafref(e *yang.Entry) (*yang.Entry, error) {
	for i := 0; e.Type != nil && e.Type.Kind == yang.Yleafref; i++ {
		if i == maxLeafrefDepth {
			return nil, status.Errorf(codes.Internal, "leafref chain from %s is too long", e.Name)
		}
		ref, err := util.FindLeafRefSchema(e, e.Type.Path)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "cannot resolve leafref of %s: %v", e.Name, err)
		}
		e = ref
	}
	if e.Type == nil {
		return nil, status.Errorf(codes.Internal, "schema node %s has no type", e.Name)
	}
	return e, nil
}

func localName(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func invalidArg(format string, args ...interface{}) error {
	return status.Error(codes.InvalidArgument, fmt.Sprintf(format, args...))
}
