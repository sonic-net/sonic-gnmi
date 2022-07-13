package client

/*
#cgo CFLAGS: -g -Wall -I/sonic/src/sonic-swss-common/common -Wformat -Werror=format-security -fPIE 
#cgo LDFLAGS: -lpthread -lboost_thread -lboost_system -lzmq -lboost_serialization -luuid -lswsscommon
#include <stdlib.h>
#include "events_wrap.h"
*/
import "C"

import (
    "encoding/json"
    "fmt"
    "sync"
    "time"
    "unsafe"

    spb "github.com/Azure/sonic-telemetry/proto"
    "github.com/Workiva/go-datastructures/queue"
    log "github.com/golang/glog"
    gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

type EventClient struct {

    prefix      *gnmipb.Path
    path        *gnmipb.Path

    q           *queue.PriorityQueue
    channel     chan struct{}

    wg          *sync.WaitGroup // wait for all sub go routines to finish

    subs_handle unsafe.Pointer

    stopped     int
}

const SUBSCRIBER_TIMEOUT = (2 * 1000)  // 2 seconds
const HEARTBEAT_TIMEOUT = 2
const EVENT_BUFFSZ = 4096
const MISSED_BUFFSZ = 16

func NewEventClient(paths []*gnmipb.Path, prefix *gnmipb.Path, logLevel int) (Client, error) {
    var evtc EventClient
    evtc.prefix = prefix
    for _, path := range paths {
        // Only one path is expected. Take the last if many
        evtc.path = path
    }
    C.swssSetLogPriority(C.int(logLevel))

    /* Init subscriber with 2 seconds time out */
    subs_data := make(map[string]interface{})
    subs_data["recv_timeout"] = SUBSCRIBER_TIMEOUT
    j, err := json.Marshal(subs_data)
    if err != nil {
        log.V(3).Infof("events_init_subscriber: Failed to marshal")
        return nil, err
    }
    js := string(j)
    evtc.subs_handle = C.events_init_subscriber_wrap(C.CString(js))
    evtc.stopped = 0

    log.V(7).Infof("NewEventClient constructed. logLevel=%d", logLevel)

    return &evtc, nil
}

// String returns the target the client is querying.
func (evtc *EventClient) String() string {
    return fmt.Sprintf("EventClient Prefix %v", evtc.prefix.GetTarget())
}


func get_events(evtc *EventClient, updateChannel chan string) {
    
    evt_ptr := C.malloc(C.sizeof_char * EVENT_BUFFSZ)
    missed_ptr := C.malloc(C.sizeof_char * MISSED_BUFFSZ)

    defer C.free(unsafe.Pointer(evt_ptr))
    defer C.free(unsafe.Pointer(missed_ptr))

    c_eptr := (*C.char)(unsafe.Pointer(evt_ptr))
    c_mptr := (*C.char)(unsafe.Pointer(missed_ptr))

    for {
        rc := C.event_receive_wrap(evtc.subs_handle, c_eptr, EVENT_BUFFSZ, c_mptr, MISSED_BUFFSZ)
        log.V(7).Infof("C.event_receive_wrap rc=%d evt:%s", rc, (*C.char)(evt_ptr))

        if rc != 0 {
            updateChannel <- C.GoString((*C.char)(evt_ptr))
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


func send_event(evtc *EventClient, tv *gnmipb.TypedValue) error {
    spbv := &spb.Value{
        Prefix:    evtc.prefix,
        Path:    evtc.path,
        Timestamp: time.Now().UnixNano(),
        Val:  tv,
    }

    log.V(7).Infof("Sending spbv")
    if err := evtc.q.Put(Value{spbv}); err != nil {
        log.V(3).Infof("Queue error:  %v", err)
        return err
    }
    log.V(7).Infof("send_event done")
    return nil
}

func (evtc *EventClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    hbData := make(map[string]interface{})
    hbData["heart"] = "beat"
    hbVal, _ := json.Marshal(hbData)

    hbTv := &gnmipb.TypedValue {
        Value: &gnmipb.TypedValue_JsonIetfVal{
            JsonIetfVal: hbVal,
        }}


    evtc.wg = wg
    defer evtc.wg.Done()

    evtc.q = q
    evtc.channel = stop

    updateChannel := make(chan string)
    go get_events(evtc, updateChannel)

    for {
        select {
        case nextEvent := <-updateChannel:
            log.V(7).Infof("update received: %v", nextEvent)
            evtTv := &gnmipb.TypedValue {
                Value: &gnmipb.TypedValue_StringVal {
                    StringVal: nextEvent,
                }}
            if err := send_event(evtc, evtTv); err != nil {
                return
            }

        case <-IntervalTicker(time.Second * HEARTBEAT_TIMEOUT):
            log.V(7).Infof("Ticker received")
            if err := send_event(evtc, hbTv); err != nil {
                return
            }
        case <-evtc.channel:
            evtc.stopped = 1
            log.V(3).Infof("Channel closed by client")
            return
        }
    }
    log.V(3).Infof("Event exiting streamrun")
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

// cgo LDFLAGS: -L/sonic/target/files/bullseye -lxswsscommon -lpthread -lboost_thread -lboost_system -lzmq -lboost_serialization -luuid -lxxeventxx -Wl,-rpath,/sonic/target/files/bullseye

