package client

/*
#cgo CFLAGS: -g -Wall -I/sonic/src/sonic-swss-common/common -Wformat -Werror=format-security -fPIE 
#cgo LDFLAGS: -lpthread -lboost_thread -lboost_system -lzmq -lboost_serialization -luuid -lswsscommon -Wl,-rpath,/sonic/target/files/bullseye
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

    w           *sync.WaitGroup // wait for all sub go routines to finish

    subs_handle unsafe.Pointer
}

const SUBSCRIBER_TIMEOUT = 2
const HEARTBEAT_TIMEOUT = 2
const EVENT_BUFFSZ = 4096
const MISSED_BUFFSZ = 16

func NewEventClient(paths []*gnmipb.Path, prefix *gnmipb.Path) (Client, error) {
    var evtc EventClient
    evtc.prefix = prefix
    for _, path := range paths {
        // Only one path is expected. Take the last if many
        evtc.path = path
    }
    log.Errorf("NewEventClient constructed")

    /* Init subscriber with 2 seconds time out */
    subs_data := make(map[string]interface{})
    subs_data["recv_timeout"] = SUBSCRIBER_TIMEOUT
    j, err := json.Marshal(subs_data)
    if err != nil {
        js := string(j)
        evtc.subs_handle = C.events_init_subscriber_wrap(C.CString(js))
        log.Errorf("events_init_subscriber: h=%v", evtc.subs_handle)
        return nil, err
    }
    log.Errorf("events_init_subscriber: Failed to marshal")
    return &evtc, nil
}

// String returns the target the client is querying.
func (c *EventClient) String() string {
	return fmt.Sprintf("EventClient Prefix %v", c.prefix.GetTarget())
}


func get_events(c *EventClient, updateChannel chan string) {
    
    evt_ptr := C.malloc(C.sizeof_char * EVENT_BUFFSZ)
    missed_ptr := C.malloc(C.sizeof_char * MISSED_BUFFSZ)

    defer C.free(unsafe.Pointer(evt_ptr))
    defer C.free(unsafe.Pointer(missed_ptr))

    for {
        c_eptr := (*C.char)(unsafe.Pointer(evt_ptr))
        // c_eptr := (*C.char)(evt_ptr)
        // c_mptr := (*C.char)(missed_ptr)
        // c_hptr := (unsafe.Pointer)(c.subs_handle)
        // rc := C.event_receive_wrap(c_hptr, c_eptr, EVENT_BUFFSZ, c_mptr, MISSED_BUFFSZ)
        //rc := 5
        // sz := C.int(20)
        rc := event_receive_wrap_54(c_eptr)
        C.events_init_subscriber_wrap(c_eptr) // Good

        if rc == 0 {
            updateChannel <- C.GoString((*C.char)(evt_ptr))
        }
        _, more := <-c.channel
        if !more {
            log.V(1).Infof("%v stop channel closed, exiting get_events routine", c)
            // c_hptr := (unsafe.Pointer)(c.subs_handle)
            events_deinit_subscriber_wrap(c.subs_handle)
            c.subs_handle = nil
            return
        }
    }
}


func send_event(c *EventClient, sval *string) error {
    log.Errorf("send_event calling json.Marshal")

    val, err := json.Marshal(sval)
    if err != nil {
        log.Errorf("Failed to marshall %V", err)
    }

    log.Errorf("send_event calling gnmipb.TypedValue")
    tv := &gnmipb.TypedValue {
        Value: &gnmipb.TypedValue_JsonIetfVal{
            JsonIetfVal: val,
        }}

    spbv := &spb.Value{
        Prefix:    c.prefix,
        Path:    c.path,
        Timestamp: time.Now().UnixNano(),
        Val:  tv,
    }

    log.Errorf("Sending spbv")
    log.Errorf("spbv: %v", *spbv)
    if err := c.q.Put(Value{spbv}); err != nil {
        log.Errorf("Queue error:  %v", err)
        return err
    }
    log.Errorf("send_event done")
    return nil
}

func (c *EventClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    data := make(map[string]interface{})
    data["heart"] = "beat"
    hb, err := json.Marshal(data)
    if err != nil {
        log.Errorf("StreamRun: Failed to marshal hearbet data")
        return
    }
    hstr := string(hb)

	c.w = w
    defer c.w.Done()

    c.q = q
    c.channel = stop

    updateChannel := make(chan string)
    go get_events(c, updateChannel)

    for {
        select {
        case nextEvent := <-updateChannel:
            log.Errorf("update received: %v", nextEvent)
            if err := send_event(c, &nextEvent); err != nil {
                return
            }

        case <-IntervalTicker(time.Second * HEARTBEAT_TIMEOUT):
            log.Errorf("Ticker received")
            if err := send_event(c, &hstr); err != nil {
                return
            }
        case <-c.channel:
            log.Errorf("Channel closed by client")
            return
        }
    }
    log.Errorf("Event exiting streamrun")
}


// TODO: Log data related to this session

func (c *EventClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
    return nil, nil
}

func (c *EventClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    return
}

func (c *EventClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    return
}


func (c *EventClient) Close() error {
	return nil
}

func  (c *EventClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	return nil
}
func (c *EventClient) Capabilities() []gnmipb.ModelData {
	return nil
}

// cgo LDFLAGS: -L/sonic/target/files/bullseye -lxswsscommon -lpthread -lboost_thread -lboost_system -lzmq -lboost_serialization -luuid -lxxeventxx -Wl,-rpath,/sonic/target/files/bullseye

