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
	"reflect"

	"github.com/Azure/sonic-mgmt-common/translib/ocbinds"
	"github.com/openconfig/gnmi/errlist"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
)

// DiffOptions holds the optional parameters foi Diff API.
type DiffOptions struct {
	// RecordAll indicates if all attributes of modified GoStruct
	// should be recorded - even if the values are same.
	RecordAll bool
}

// DiffResults holds updated {path, value} pairs and deleted paths
// resolved by the Diff API.
type DiffResults struct {
	Update []*gnmi.Update
	Delete []*gnmi.Path
}

// Diff compares original and modified ygot structs; returns
// the changes as a DiffResults.
// Works with translib generated ygot structs only.
func Diff(original, modified ygot.GoStruct, opts DiffOptions) (DiffResults, error) {
	var yd ygotDiff
	yd.DiffOptions = opts
	yd.errors.Separator = "\n"
	if original != nil && reflect.TypeOf(original) != reflect.TypeOf(modified) {
		return yd.DiffResults, fmt.Errorf("Type mismatch")
	}

	yd.forStruct(reflect.ValueOf(original), reflect.ValueOf(modified))
	return yd.DiffResults, yd.errors.Err()
}

// ygotDiff is a utility to compare ygot structs.
type ygotDiff struct {
	DiffOptions
	DiffResults
	prefix []*gnmi.PathElem // current prefix
	errors errlist.List
}

func (yd *ygotDiff) forStruct(v1, v2 reflect.Value) {
	if !v1.IsValid() || v1.IsNil() {
		if v2.IsValid() && !v2.IsNil() {
			yd.recordUpdates(v2.Interface())
		}
		return
	}
	if !v2.IsValid() || v2.IsNil() {
		yd.recordDeletePath(nil)
		return
	}

	v1 = v1.Elem()
	v2 = v2.Elem()
	st := v1.Type()

	for i := v1.NumField() - 1; i >= 0; i-- {
		f1 := v1.Field(i)
		f2 := v2.Field(i)
		ft := st.Field(i)

		switch ft.Type.Kind() {
		case reflect.Ptr: // leaf or container
			if ft.Type.Elem().Kind() == reflect.Struct {
				yd.pushPrefix(getElemName(&ft), nil)
				yd.forStruct(f1, f2)
				yd.popPrefix()
			} else {
				yd.forLeaf(&ft, unboxPtr(f1), unboxPtr(f2))
			}
		case reflect.Map: // list
			yd.forList(&ft, &f1, &f2)
		case reflect.Slice: // leaf-list
			yd.forLeafList(&ft, &f1, &f2)
		case reflect.Int64: // enum
			yd.forEnum(&ft, f1.Int(), f2.Int())
		case reflect.Interface: // union
			yd.forUnion(&ft, &f1, &f2)
		case reflect.Bool: // empty leaf
			yd.forLeaf(&ft, f1.Bool(), f2.Bool())
		default:
			yd.recordError("Unexpected type %v for field %s", ft.Type.Kind(), ft.Name)
		}
	}
}

// forList compares ygot lists. Both m1 and m2 are assumed
// to be map values.
func (yd *ygotDiff) forList(f *reflect.StructField, m1, m2 *reflect.Value) {
	listName := getElemName(f)
	for iter := m1.MapRange(); iter.Next(); {
		v1 := iter.Value()
		if v2 := m2.MapIndex(iter.Key()); v2.IsValid() {
			// Instance present in both m1 and m2. Compare them
			yd.pushPrefix(listName, &v2)
			yd.forStruct(v1, v2)
			yd.popPrefix()
		} else {
			// Instance deleted in m2
			suffix := newPathElem(listName, &v1)
			yd.recordDeletePath(suffix)
		}
	}
	// Look for new instances in m2 (that are not in m1)
	for iter := m2.MapRange(); iter.Next(); {
		if v1 := m1.MapIndex(iter.Key()); !v1.IsValid() {
			v2 := iter.Value()
			yd.pushPrefix(listName, &v2)
			yd.recordUpdates(v2.Interface())
			yd.popPrefix()
		}
	}
}

func (yd *ygotDiff) forUnion(f *reflect.StructField, v1, v2 *reflect.Value) {
	if v2.IsNil() {
		if !v1.IsNil() {
			yd.recordDelete(f)
		}
		return
	}
	if yd.RecordAll || v1.IsNil() {
		yd.recordUpdate(f, v2.Interface())
		return
	}

	// Union is modeled as an interface wrapping a struct pointer
	u1 := v1.Elem().Elem().Interface()
	u2 := v2.Elem().Elem().Interface()
	if u1 != u2 {
		yd.recordUpdate(f, v2.Interface())
	}
}

func (yd *ygotDiff) forEnum(f *reflect.StructField, v1, v2 int64) {
	if v2 == 0 {
		if v1 != 0 {
			yd.recordDelete(f)
		}
		return
	}
	if !yd.RecordAll && v1 == v2 {
		return
	}

	//TODO avoid directly referring to ocbinds
	enumDef, ok := ocbinds.ΛEnum[f.Type.Name()][v2]
	if !ok {
		yd.recordError("%s is not a GoEnum", f.Type.Name())
	} else {
		yd.recordUpdate(f, enumDef.Name)
	}
}

func (yd *ygotDiff) forLeaf(f *reflect.StructField, v1, v2 interface{}) {
	if v2 == nil {
		if v1 != nil {
			yd.recordDelete(f)
		}
	} else if yd.RecordAll || v1 != v2 {
		yd.recordUpdate(f, v2)
	}
}

func (yd *ygotDiff) forLeafList(f *reflect.StructField, v1, v2 *reflect.Value) {
	var len1, len2 int
	if !v1.IsNil() {
		len1 = v1.Len()
	}
	if !v2.IsNil() {
		len2 = v2.Len()
	}
	if len2 == 0 {
		if len1 != 0 {
			yd.recordDelete(f)
		}
		return
	}
	if yd.RecordAll || len1 != len2 {
		yd.recordUpdate(f, v2.Interface())
		return
	}
	for i := 0; i < len1; i++ {
		if v1.Index(i).Interface() != v2.Index(i).Interface() {
			yd.recordUpdate(f, v2.Interface())
			return
		}
	}
}

// recordUpdates records an update for of the set fields of a GoStruct.
// Object v should be a GoStruct.
func (yd *ygotDiff) recordUpdates(v interface{}) {
	s, ok := v.(ygot.GoStruct)
	if !ok {
		yd.recordError("%T is not a ygot.GoStruct", v)
		return
	}

	msg, err := ygot.TogNMINotifications(s, 0,
		ygot.GNMINotificationsConfig{UsePathElem: true})
	if err != nil {
		yd.recordError("TogNMINotifications failed; %v", err)
		return
	}

	for _, m := range msg {
		for _, u := range m.Update {
			elems := make([]*gnmi.PathElem, 0, len(yd.prefix)+len(u.Path.Elem))
			elems = append(elems, yd.prefix...)
			u.Path.Elem = append(elems, u.Path.Elem...)
		}
		yd.Update = append(yd.Update, m.Update...)
	}
}

// recordUpdate records an update for the GoStruct field f, value v.
// Uses ygot.EncodeTypedValue API to construct a gnmi.TypedValue from
// the value v.
func (yd *ygotDiff) recordUpdate(f *reflect.StructField, v interface{}) {
	tv, err := ygot.EncodeTypedValue(v, gnmi.Encoding_JSON)
	if err != nil {
		yd.recordError("EncodeTypedValue failed; %v", err)
		return
	}

	suffix := newPathElem(getElemName(f), nil)
	u := &gnmi.Update{
		Path: yd.newPath(suffix),
		Val:  tv,
	}
	yd.Update = append(yd.Update, u)
}

func (yd *ygotDiff) recordDelete(f *reflect.StructField) {
	suffix := newPathElem(getElemName(f), nil)
	yd.recordDeletePath(suffix)
}

func (yd *ygotDiff) recordDeletePath(suffix *gnmi.PathElem) {
	yd.Delete = append(yd.Delete, yd.newPath(suffix))
}

func (yd *ygotDiff) recordError(f string, args ...interface{}) {
	p, _ := ygot.PathToString(&gnmi.Path{Elem: yd.prefix})
	yd.errors.Add(fmt.Errorf(p+": "+f, args...))
}

// newPath returns a new *gnmi.Path by joining the path elements
// yd.prefix and an optional suffix.
func (yd *ygotDiff) newPath(suffix *gnmi.PathElem) *gnmi.Path {
	n := len(yd.prefix)
	if suffix != nil {
		n++
	}

	elems := make([]*gnmi.PathElem, n)
	copy(elems, yd.prefix)
	if suffix != nil {
		elems[len(yd.prefix)] = suffix
	}

	return &gnmi.Path{Elem: elems}
}

// pushPrefix appends a new path element to the prefix.
func (yd *ygotDiff) pushPrefix(name string, keyObj *reflect.Value) {
	yd.prefix = append(yd.prefix, newPathElem(name, keyObj))
}

// popPrefix removes last path element from the prefix.
func (yd *ygotDiff) popPrefix() {
	yd.prefix = yd.prefix[:len(yd.prefix)-1]
}

// getElemName returns the path element name for a ygot struct field f.
// It panic if there is no 'path' tag, which should never happen.
func getElemName(f *reflect.StructField) string {
	name, ok := f.Tag.Lookup("path")
	if !ok {
		panic(f.Name + " has no path tag")
	}
	return name
}

// newPathElem creates a new gnmi.PathElem object for the given
// node name and an optional key object.
func newPathElem(name string, keyObj *reflect.Value) *gnmi.PathElem {
	pElem := &gnmi.PathElem{Name: name}
	if keyObj != nil {
		var err error
		pElem.Key, err = ygot.PathKeyFromStruct(*keyObj)
		if err != nil {
			panic("ygot.PathKeyFromStruct failed: " + err.Error())
		}
	}
	return pElem
}

func unboxPtr(v reflect.Value) interface{} {
	if v.IsNil() {
		return nil
	}
	return v.Elem().Interface()
}
