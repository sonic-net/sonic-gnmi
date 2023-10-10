package client

/*
#cgo CFLAGS: -g -Wall -I../../sonic-swss-common/common -Wformat -Werror=format-security -fPIE
#cgo LDFLAGS: -L/usr/lib -lpthread -lboost_thread -lboost_system -lzmq -lboost_serialization -luuid -lswsscommon
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include "events_wrap.h"
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/go-redis/redis"

	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

const SUBSCRIBER_TIMEOUT = (2 * 1000) // 2 seconds
const EVENT_BUFFSZ = 4096

const LATENCY_LIST_SIZE = 10 // Size of list of latencies.
const PQ_DEF_SIZE = 10240    // Def size for pending events in PQ.
const PQ_MIN_SIZE = 1024     // Min size for pending events in PQ.
const PQ_MAX_SIZE = 102400   // Max size for pending events in PQ.

const HEARTBEAT_MAX = 600 // 10 mins

// STATS counters
const MISSED = "COUNTERS_EVENTS:missed_internal"
const DROPPED = "COUNTERS_EVENTS:missed_by_slow_receiver"
const LATENCY = "COUNTERS_EVENTS:latency_in_ms"

var STATS_CUMULATIVE_KEYS = [...]string{MISSED, DROPPED}
var STATS_ABSOLUTE_KEYS = [...]string{LATENCY}

const STATS_FIELD_NAME = "value"

const EVENTD_PUBLISHER_SOURCE = "{\"sonic-events-eventd"

const TEST_EVENT = "{\"sonic-host:device-test-event"

// Path parameter
const PARAM_HEARTBEAT = "heartbeat"
const PARAM_QSIZE = "qsize"
const PARAM_USE_CACHE = "usecache"

type EventClient struct {
	prefix *gnmipb.Path
	path   *gnmipb.Path

	q       *queue.PriorityQueue
	pq_max  int
	channel chan struct{}

	wg *sync.WaitGroup // wait for all sub go routines to finish

	subs_handle unsafe.Pointer

	stopped   int
	stopMutex sync.RWMutex

	// Stats counter
	counters      map[string]uint64
	countersMutex sync.RWMutex

	last_latencies     [LATENCY_LIST_SIZE]uint64
	last_latency_index int
	last_latency_full  bool

	last_errors uint64
}

func Set_heartbeat(val int) {
	s := fmt.Sprintf("{\"HEARTBEAT_INTERVAL\":%d}", val)
	rc := C.event_set_global_options(C.CString(s))
	if rc != 0 {
		log.V(4).Infof("Failed to set heartbeat val=%d rc=%d", val, rc)
	}
}

func C_init_subs(use_cache bool) unsafe.Pointer {
	return C.events_init_subscriber_wrap(C.bool(use_cache), C.int(SUBSCRIBER_TIMEOUT))
}

func NewEventClient(paths []*gnmipb.Path, prefix *gnmipb.Path, logLevel int) (Client, error) {
	var evtc EventClient
	use_cache := true
	evtc.prefix = prefix
	evtc.pq_max = PQ_DEF_SIZE
	log.V(4).Infof("Events priority Q max set default = %v", evtc.pq_max)

	for _, path := range paths {
		// Only one path is expected. Take the last if many
		evtc.path = path
	}

	for _, e := range evtc.path.GetElem() {
		keys := e.GetKey()
		for k, v := range keys {
			if k == PARAM_HEARTBEAT {
				if val, err := strconv.Atoi(v); err == nil {
					if val > HEARTBEAT_MAX {
						log.V(4).Infof("heartbeat req %v > max %v; default to max", val, HEARTBEAT_MAX)
						val = HEARTBEAT_MAX
					}
					log.V(7).Infof("evtc.heartbeat_interval is set to %d", val)
					Set_heartbeat(val)
				}
			} else if k == PARAM_QSIZE {
				if val, err := strconv.Atoi(v); err == nil {
					qval := val
					if val < PQ_MIN_SIZE {
						val = PQ_MIN_SIZE
					} else if val > PQ_MAX_SIZE {
						val = PQ_MAX_SIZE
					}
					if val != qval {
						log.V(4).Infof("Events priority Q request %v updated to nearest limit %v",
							qval, val)
					}
					evtc.pq_max = val
					log.V(7).Infof("Events priority Q max set by qsize param = %v", evtc.pq_max)
				}
			} else if k == PARAM_USE_CACHE {
				if strings.ToLower(v) == "false" {
					use_cache = false
					log.V(7).Infof("Cache use is turned off")
				}
			}
		}
	}

	C.swssSetLogPriority(C.int(logLevel))

	/* Init subscriber with cache use and defined time out */
	evtc.subs_handle = C_init_subs(use_cache)
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

		for _, v := range evtc.last_latencies {
			if v > 0 {
				total += v
			}
		}
		evtc.countersMutex.RLock()
		evtc.counters[LATENCY] = (uint64)(total / LATENCY_LIST_SIZE / 1000 / 1000)
		evtc.countersMutex.RUnlock()
	}
}

func update_stats(evtc *EventClient) {
	defer evtc.wg.Done()

	/* Wait for any update */
	db_counters := make(map[string]uint64)
	var wr_counters *map[string]uint64 = nil
	var rclient *redis.Client

	/*
	 * This loop pauses until at least one non zero counter.
	 * This helps add some initial pause before accessing DB
	 * for existing values.
	 */

	for !evtc.isStopped() {
		var val uint64

		compute_latency(evtc)

		evtc.countersMutex.Lock()
		for _, val = range evtc.counters {
			if val != 0 {
				break
			}
		}
		evtc.countersMutex.Unlock()

		if val != 0 {
			break
		}
		time.Sleep(time.Second)
	}

	/* Populate counters from DB for cumulative counters. */
	if !evtc.isStopped() {
		ns := sdcfg.GetDbDefaultNamespace()

		rclient = redis.NewClient(&redis.Options{
			Network:     "tcp",
			Addr:        sdcfg.GetDbTcpAddr("COUNTERS_DB", ns),
			Password:    "", // no password set,
			DB:          sdcfg.GetDbId("COUNTERS_DB", ns),
			DialTimeout: 0,
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
	for !evtc.isStopped() {
		tmp_counters := make(map[string]uint64)

		// compute latency
		compute_latency(evtc)

		evtc.countersMutex.Lock()
		current_counters := evtc.counters
		evtc.countersMutex.Unlock()

		for key, val := range current_counters {
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

var evt_ptr *C.event_receive_op_C_t

type Evt_rcvd struct {
	Event_str        string
	Missed_cnt       uint32
	Publish_epoch_ms int64
}

func C_recv_evt(h unsafe.Pointer) (int, Evt_rcvd) {
	var evt Evt_rcvd

	rc := (int)(C.event_receive_wrap(h, evt_ptr))
	evt.Event_str = C.GoString((*C.char)(evt_ptr.event_str))
	evt.Missed_cnt = (uint32)(evt_ptr.missed_cnt)
	evt.Publish_epoch_ms = (int64)(evt_ptr.publish_epoch_ms)

	return rc, evt
}

func C_deinit_subs(h unsafe.Pointer) {
	C.events_deinit_subscriber_wrap(h)
}

func get_events(evtc *EventClient) {
	defer evtc.wg.Done()

	str_ptr := C.malloc(C.sizeof_char * C.size_t(EVENT_BUFFSZ))
	defer C.free(unsafe.Pointer(str_ptr))

	evt_ptr = (*C.event_receive_op_C_t)(C.malloc(C.size_t(unsafe.Sizeof(C.event_receive_op_C_t{}))))
	defer C.free(unsafe.Pointer(evt_ptr))

	evt_ptr.event_str = (*C.char)(str_ptr)
	evt_ptr.event_sz = C.uint32_t(EVENT_BUFFSZ)

	for {

		rc, evt := C_recv_evt(evtc.subs_handle)

		if rc == 0 {
			evtc.countersMutex.Lock()
			current_missed_cnt := evtc.counters[MISSED]
			evtc.countersMutex.Unlock()

			evtc.countersMutex.RLock()
			evtc.counters[MISSED] = current_missed_cnt + (uint64)(evt.Missed_cnt)
			evtc.countersMutex.RUnlock()

			if !strings.HasPrefix(evt.Event_str, TEST_EVENT) {
				qlen := evtc.q.Len()

				if qlen < evtc.pq_max {
					var fvp map[string]interface{}
					json.Unmarshal([]byte(evt.Event_str), &fvp)

					jv, err := json.Marshal(fvp)

					if err == nil {
						evtTv := &gnmipb.TypedValue{
							Value: &gnmipb.TypedValue_JsonIetfVal{
								JsonIetfVal: jv,
							}}
						if err := send_event(evtc, evtTv, evt.Publish_epoch_ms); err != nil {
							break
						}
					} else {
						log.V(1).Infof("Invalid event string: %v", evt.Event_str)
					}
				} else {
					evtc.countersMutex.Lock()
					dropped_cnt := evtc.counters[DROPPED]
					evtc.countersMutex.Unlock()

					evtc.countersMutex.RLock()
					evtc.counters[DROPPED] = dropped_cnt + 1
					evtc.countersMutex.RUnlock()
				}
			}
		}
		if evtc.isStopped() {
			break
		}
		// TODO: Record missed count in stats table.
		// intVar, err := strconv.Atoi(C.GoString((*C.char)(c_mptr)))
	}
	log.V(1).Infof("%v stop channel closed or send_event err, exiting get_events routine", evtc)
	C_deinit_subs(evtc.subs_handle)
	evtc.subs_handle = nil
	// set evtc.stopped for case where send_event error and channel was not stopped
	evtc.stopMutex.RLock()
	evtc.stopped = 1
	evtc.stopMutex.RUnlock()
}

func send_event(evtc *EventClient, tv *gnmipb.TypedValue,
	timestamp int64) error {
	spbv := &spb.Value{
		Prefix:    evtc.prefix,
		Path:      evtc.path,
		Timestamp: timestamp,
		Val:       tv,
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
	evtc.wg.Add(1)
	go update_stats(evtc)
	evtc.wg.Add(1)

	for !evtc.isStopped() {
		select {
		case <-evtc.channel:
			evtc.stopMutex.RLock()
			evtc.stopped = 1
			evtc.stopMutex.RUnlock()
			log.V(3).Infof("Channel closed by client")
			return
		}
	}
}

func (evtc *EventClient) isStopped() bool {
	evtc.stopMutex.Lock()
	val := evtc.stopped
	evtc.stopMutex.Unlock()
	return val == 1
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

func (evtc *EventClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
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
