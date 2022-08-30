package client

/*
#cgo CFLAGS: -g -Wall -I./sonic-swss-common/common -Wformat -Werror=format-security -fPIE 
#cgo LDFLAGS: -L/usr/lib -lpthread -lboost_thread -lboost_system -lzmq -lboost_serialization -luuid -lswsscommon
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include "events_wrap.h"
*/
import "C"

import (
    "strconv"
    "fmt"
    "reflect"
    "sync"
    "time"
    "unsafe"

    "github.com/go-redis/redis"

    spb "github.com/sonic-net/sonic-gnmi/proto"
    sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
    "github.com/Workiva/go-datastructures/queue"
    log "github.com/golang/glog"
    gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

const SUBSCRIBER_TIMEOUT = (2 * 1000)  // 2 seconds
const EVENT_BUFFSZ = 4096

const LATENCY_LIST_SIZE = 10  // Size of list of latencies.
const PQ_MAX_SIZE = 10240     // Max cnt of pending events in PQ.

// STATS counters
const MISSED = "COUNTERS_EVENTS:missed_internal"
const DROPPED = "COUNTERS_EVENTS:missed_by_slow_receiver"
const LATENCY = "COUNTERS_EVENTS:latency_in_ms"

var STATS_CUMULATIVE_KEYS = [...]string {MISSED, DROPPED}
var STATS_ABSOLUTE_KEYS = [...]string {LATENCY}

const STATS_FIELD_NAME = "value"

const EVENTD_PUBLISHER_SOURCE = "{\"sonic-events-eventd"

// Path parameter
const PARAM_HEARTBEAT = "heartbeat"

type EventClient struct {

    prefix      *gnmipb.Path
    path        *gnmipb.Path

    q           *queue.PriorityQueue
    channel     chan struct{}

    wg          *sync.WaitGroup // wait for all sub go routines to finish

    subs_handle unsafe.Pointer

    stopped     int

    // Stats counter
    counters    map[string]uint64

    last_latencies  [LATENCY_LIST_SIZE]uint64
    last_latency_index  int
    last_latency_full   bool

    last_errors uint64
}

func set_heartbeat(val int) {
    s := fmt.Sprintf("{\"HEARTBEAT_INTERVAL\":%d}", val)
    rc := C.event_set_global_options(C.CString(s));
    if rc != 0  {
        log.V(4).Infof("Failed to set heartbeat val=%d rc=%d", val, rc)
    }
}

func NewEventClient(paths []*gnmipb.Path, prefix *gnmipb.Path, logLevel int) (Client, error) {
    var evtc EventClient
    evtc.prefix = prefix
    for _, path := range paths {
        // Only one path is expected. Take the last if many
        evtc.path = path
    }

    for _, e := range evtc.path.GetElem() {
        keys := e.GetKey()
        for k, v := range keys {
            if (k == PARAM_HEARTBEAT) {
                if val, err := strconv.Atoi(v); err == nil {
                    log.V(7).Infof("evtc.heartbeat_interval is set to %d", val)
                    set_heartbeat(val)
                }
            }
        }
    }

    C.swssSetLogPriority(C.int(logLevel))

    /* Init subscriber with cache use and defined time out */
    evtc.subs_handle = C.events_init_subscriber_wrap(true, C.int(SUBSCRIBER_TIMEOUT))
    evtc.stopped = 0

    /* Init list & counters */
    evtc.counters = make(map[string]uint64)

    for _, key := range STATS_CUMULATIVE_KEYS {
        evtc.counters[key] = 0
    }

    for _, key := range STATS_ABSOLUTE_KEYS {
        evtc.counters[key] = 0
    }

    for i := 0; i < len(evtc.last_latencies); i++ {
        evtc.last_latencies[i] = 0
    }
    evtc.last_latency_index = 0
    evtc.last_errors = 0
    evtc.last_latency_full = false

    log.V(7).Infof("NewEventClient constructed. logLevel=%d", logLevel)

    return &evtc, nil
}


func compute_latency(evtc *EventClient) {
    if evtc.last_latency_full {
        var total uint64 = 0
        var cnt uint64 = 0

        for _, v := range evtc.last_latencies {
            if v > 0 {
                total += v
                cnt += 1
            }
        }
        if (cnt > 0) {
            evtc.counters[LATENCY] = (uint64) (total/cnt/1000/1000)
        }
    }
}

func update_stats(evtc *EventClient) {
    /* Wait for any update */
    db_counters := make(map[string]uint64)
    var wr_counters *map[string]uint64 = nil
    var rclient *redis.Client

    /*
     * This loop pauses until at least one non zero counter.
     * This helps add some initial pause before accessing DB
     * for existing values.
     */
    for evtc.stopped == 0 {
        var val uint64

        compute_latency(evtc)
        for _, val = range evtc.counters {
            if val != 0 {
                break
            }
        }
        if val != 0 {
            break
        }
        time.Sleep(time.Second)
    }
    
    /* Populate counters from DB for cumulative counters. */
    if evtc.stopped == 0 {
        ns := sdcfg.GetDbDefaultNamespace()

        rclient = redis.NewClient(&redis.Options{
            Network:    "tcp",
            Addr:       sdcfg.GetDbTcpAddr("COUNTERS_DB", ns),
            Password:   "", // no password set,
            DB:         sdcfg.GetDbId("COUNTERS_DB", ns),
            DialTimeout:0,
        })


        // Init current values for cumulative keys and clear for absolute 
        for _, key := range STATS_CUMULATIVE_KEYS {
            fv, err := rclient.HGetAll(key).Result()
            if err != nil {
                number, errC := strconv.ParseUint(fv[STATS_FIELD_NAME], 10, 64)
                if errC == nil {
                    db_counters[key] = number
                }
            }
        }
        for _, key := range STATS_ABSOLUTE_KEYS {
            db_counters[key] = 0
        }
    }

    /* Main running loop that updates DB */
    for evtc.stopped == 0 {
        tmp_counters := make(map[string]uint64)

        // compute latency
        compute_latency(evtc)

        for key, val := range evtc.counters {
            tmp_counters[key] = val + db_counters[key]
        }
        tmp_counters[DROPPED] += evtc.last_errors

        if (wr_counters == nil) || !reflect.DeepEqual(tmp_counters, *wr_counters) {
            for key, val := range tmp_counters {
                sval := strconv.FormatUint(val, 10)
                ret, err := rclient.HSet(key, STATS_FIELD_NAME, sval).Result()
                if !ret {
                    log.V(3).Infof("EventClient failed to update COUNTERS key:%s val:%v err:%v",
                        key, sval, err)
                }
            }
            wr_counters = &tmp_counters
        }
        time.Sleep(time.Second)
    }
}


// String returns the target the client is querying.
func (evtc *EventClient) String() string {
    return fmt.Sprintf("EventClient Prefix %v", evtc.prefix.GetTarget())
}


func get_events(evtc *EventClient) {
    str_ptr := C.malloc(C.sizeof_char * C.size_t(EVENT_BUFFSZ)) 
    defer C.free(unsafe.Pointer(str_ptr))

    evt_ptr := &C.event_receive_op_C_t{}
    evt_ptr = (*C.event_receive_op_C_t)(C.malloc(C.size_t(unsafe.Sizeof(C.event_receive_op_C_t{}))))
    defer C.free(unsafe.Pointer(evt_ptr))

    evt_ptr.event_str = (*C.char)(str_ptr)
    evt_ptr.event_sz = C.uint32_t(EVENT_BUFFSZ)

    for {

        rc := C.event_receive_wrap(evtc.subs_handle, evt_ptr)
        log.V(7).Infof("C.event_receive_wrap rc=%d evt:%s", rc, (*C.char)(str_ptr))

        if rc == 0 {
            var cnt uint64
            cnt = (uint64)(evt_ptr.missed_cnt)
            evtc.counters[MISSED] += cnt
            qlen := evtc.q.Len()
            estr := C.GoString((*C.char)(evt_ptr.event_str))

            if (qlen < PQ_MAX_SIZE) {
                evtTv := &gnmipb.TypedValue {
                    Value: &gnmipb.TypedValue_StringVal {
                        StringVal: estr,
                    }}
                var ts int64
                ts = (int64)(evt_ptr.publish_epoch_ms)
                if err := send_event(evtc, evtTv, ts); err != nil {
                    return
                }
            } else {
                evtc.counters[DROPPED] += 1
            }
        }
        if evtc.stopped == 1 {
            log.V(1).Infof("%v stop channel closed, exiting get_events routine", evtc)
            C.events_deinit_subscriber_wrap(evtc.subs_handle)
            evtc.subs_handle = nil
            return
        }
        // TODO: Record missed count in stats table.
        // intVar, err := strconv.Atoi(C.GoString((*C.char)(c_mptr)))
    }
}


func send_event(evtc *EventClient, tv *gnmipb.TypedValue,
        timestamp int64) error {
    spbv := &spb.Value{
        Prefix:    evtc.prefix,
        Path:    evtc.path,
        Timestamp: timestamp,
        Val:  tv,
    }

    if err := evtc.q.Put(Value{spbv}); err != nil {
        log.V(3).Infof("Queue error:  %v", err)
        return err
    }
    return nil
}

func (evtc *EventClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {

    evtc.wg = wg
    defer evtc.wg.Done()

    evtc.q = q
    evtc.channel = stop

    go get_events(evtc)
    go update_stats(evtc)

    for {
        select {
        case <-evtc.channel:
            evtc.stopped = 1
            log.V(3).Infof("Channel closed by client")
            return
        }
    }
}


func (evtc *EventClient) Get(wg *sync.WaitGroup) ([]*spb.Value, error) {
    return nil, nil
}

func (evtc *EventClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    return
}

func (evtc *EventClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    return
}


func (evtc *EventClient) Close() error {
    return nil
}

func  (evtc *EventClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
    return nil
}
func (evtc *EventClient) Capabilities() []gnmipb.ModelData {
    return nil
}

func (c *EventClient) SentOne(val *Value) {
    var udiff uint64

    diff := time.Now().UnixNano() - val.GetTimestamp()
    udiff = (uint64)(diff)

    c.last_latencies[c.last_latency_index] = udiff
    c.last_latency_index += 1
    if c.last_latency_index >= len(c.last_latencies) {
        c.last_latency_index = 0
        c.last_latency_full = true
    }
}

func (c *EventClient) FailedSend() {
    c.last_errors += 1
}


// cgo LDFLAGS: -L/sonic/target/files/bullseye -lxswsscommon -lpthread -lboost_thread -lboost_system -lzmq -lboost_serialization -luuid -lxxeventxx -Wl,-rpath,/sonic/target/files/bullseye

