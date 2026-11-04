package client

//This file contains dummy tests for the sake of coverage and will be removed later

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/agiledragon/gomonkey/v2"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func TestUpdateStatsReturnsWhenSocketLookupFails(t *testing.T) {
	wantErr := errors.New("local Redis socket lookup failed")
	calls := 0
	patches := gomonkey.ApplyFunc(sdcfg.GetDbSock, func(dbName, namespace string) (string, error) {
		calls++
		if dbName != "COUNTERS_DB" {
			t.Errorf("GetDbSock database = %q, want COUNTERS_DB", dbName)
		}
		return "", wantErr
	})
	defer patches.Reset()

	var wg sync.WaitGroup
	wg.Add(1)
	evtc := &EventClient{
		wg:       &wg,
		counters: map[string]uint64{MISSED: 1},
	}

	update_stats(evtc)

	if calls != 1 {
		t.Fatalf("GetDbSock called %d times, want 1", calls)
	}
}

func TestDummyEventClient(t *testing.T) {
	evtc := &EventClient{}
	evtc.last_latencies[0] = 1
	evtc.last_latencies[1] = 2
	evtc.last_latency_index = 9
	evtc.last_latency_full = true
	evtc.counters = make(map[string]uint64)
	evtc.counters["COUNTERS_EVENTS:latency_in_ms"] = 0
	compute_latency(evtc)

	// Prepare necessary arguments for each function
	var wg sync.WaitGroup
	var q *queue.PriorityQueue // Assuming queue.PriorityQueue is a valid type
	once := make(chan struct{})
	poll := make(chan struct{})
	var subscribe *gnmipb.SubscriptionList             // Assuming gnmipb.SubscriptionList is a valid type
	var deletePaths []*gnmipb.Path                     // Assuming gnmipb.Path is a valid type
	var replaceUpdates, updateUpdates []*gnmipb.Update // Assuming gnmipb.Update is a valid type

	evtc.Get(&wg)
	evtc.OnceRun(q, once, &wg, subscribe)
	evtc.PollRun(q, poll, &wg, subscribe)
	evtc.Close()
	evtc.Set(deletePaths, replaceUpdates, updateUpdates)
	evtc.Capabilities()
	evtc.last_latencies[0] = 1
	evtc.last_latencies[1] = 2
	evtc.last_latency_index = 9
	evtc.last_latency_full = true
	evtc.SentOne(&Value{
		&spb.Value{
			Timestamp: time.Now().UnixNano(),
		},
	})
	evtc.FailedSend()
	// Skip C_init_subs: the events CGO subscriber creates a background
	// thread that blocks on Trixie, causing the test binary to hang.

}

func TestNewEventClient(t *testing.T) {
	// Skip: event_set_global_options (called by Set_heartbeat) hangs on Trixie,
	// leaking a goroutine that causes the test binary to exceed its timeout.
	// TODO: re-enable once swss-common events library is fixed for Trixie.
	t.Skip("Skipping: CGO event_set_global_options blocks on Trixie")
}
