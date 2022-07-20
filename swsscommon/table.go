package swsscommon

// #cgo LDFLAGS: -lcswsscommon -lswsscommon -lstdc++
// #include <capi/table.h>
// #include <stdlib.h>
import "C"

import (
    "log"
    "unsafe"
)

type Table struct {
    ptr   unsafe.Pointer
    table string
}


func NewTable(db DBConnector, tableName string) Table {
    tableNameC := C.CString(tableName)
    defer C.free(unsafe.Pointer(tableNameC))

    pt := C.table_new(C.db_connector_t2(db.ptr), tableNameC)
    return Table{ptr: unsafe.Pointer(pt), table: tableName}
}

func (pt Table) Delete() {
    C.table_delete(C.table_t(pt.ptr))
}

func (pt Table) SetBuffered(buffered bool) {
    C.table_set_buffered(C.table_t(pt.ptr), C._Bool(buffered))
}

func (pt Table) Set(key string, values map[string]string, op string, prefix string) {
    log.Printf(
        "trace: swss: %s %s:%s %s",
        op,
        pt.table,
        key,
        values,
    )

    keyC := C.CString(key)
    defer C.free(unsafe.Pointer(keyC))
    opC := C.CString(op)
    defer C.free(unsafe.Pointer(opC))
    prefixC := C.CString(prefix)
    defer C.free(unsafe.Pointer(prefixC))

    count := len(values)
    tuplePtr := (*C.field_value_tuple_t)(C.malloc(C.size_t(C.sizeof_field_value_tuple_t * count)))
    defer C.free(unsafe.Pointer(tuplePtr))
    // Get a Go slice to the C array - this doesn't allocate anything
    tuples := (*[(1 << 28) - 1]C.field_value_tuple_t)(unsafe.Pointer(tuplePtr))[:count:count]

    idx := 0
    for k, v := range values {
        kC := C.CString(k)
        defer C.free(unsafe.Pointer(kC))
        vC := C.CString(v)
        defer C.free(unsafe.Pointer(vC))
        tuples[idx] = C.field_value_tuple_t{
            field: (*C.char)(kC),
            value: (*C.char)(vC),
        }
        idx = idx + 1
    }

    C.table_set(C.table_t(pt.ptr), keyC, tuplePtr, C.size_t(count), opC, prefixC)
}

func (pt Table) Del(key string, op string, prefix string) {
    log.Printf(
        "trace: swss: %s %s:%s",
        op,
        pt.table,
        key,
    )

    keyC := C.CString(key)
    defer C.free(unsafe.Pointer(keyC))
    opC := C.CString(op)
    defer C.free(unsafe.Pointer(opC))
    prefixC := C.CString(prefix)
    defer C.free(unsafe.Pointer(prefixC))

    C.table_del(C.table_t(pt.ptr), keyC, opC, prefixC)
}

func (pt Table) Flush() {
    C.table_flush(C.table_t(pt.ptr))
}
