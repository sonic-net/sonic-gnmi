package swsscommon

// #cgo LDFLAGS: -lcswsscommon -lswsscommon -lstdc++
// #include <capi/producerstatetable.h>
// #include <stdlib.h>
import "C"

import (
    "log"
    "unsafe"
)

type ProducerStateTable struct {
    ptr   unsafe.Pointer
    table string
}


func NewProducerStateTable(db DBConnector, tableName string) ProducerStateTable {
    tableNameC := C.CString(tableName)
    defer C.free(unsafe.Pointer(tableNameC))

    pt := C.producer_state_table_new(C.db_connector_t2(db.ptr), tableNameC)
    return ProducerStateTable{ptr: unsafe.Pointer(pt), table: tableName}
}

func (pt ProducerStateTable) Delete() {
    C.producer_state_table_delete(C.producer_state_table_t(pt.ptr))
}

func (pt ProducerStateTable) SetBuffered(buffered bool) {
    C.producer_state_table_set_buffered(C.producer_state_table_t(pt.ptr), C._Bool(buffered))
}

func (pt ProducerStateTable) Set(key string, values map[string]string, op string, prefix string) {
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

    C.producer_state_table_set(C.producer_state_table_t(pt.ptr), keyC, tuplePtr, C.size_t(count), opC, prefixC)
}

func (pt ProducerStateTable) Del(key string, op string, prefix string) {
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

    C.producer_state_table_del(C.producer_state_table_t(pt.ptr), keyC, opC, prefixC)
}

func (pt ProducerStateTable) Flush() {
    C.producer_state_table_flush(C.producer_state_table_t(pt.ptr))
}
