package client

// Tests for suppress_redundant handling in SAMPLE-mode stream subscriptions
// (issue #762). These drive DbClient and MixedDbClient directly against a
// miniredis instance with a mocked interval ticker.

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/alicebob/miniredis/v2"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/redis/go-redis/v9"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
)

const testSampleNs = "sample-test-ns"

// mockIntervalTicker installs a manually driven interval ticker and returns
// the tick channel and a restore function.
func mockIntervalTicker() (chan time.Time, func()) {
	tickCh := make(chan time.Time)
	NeedMock = true
	orig := GetIntervalTicker()
	SetIntervalTicker(func(interval time.Duration) <-chan time.Time {
		return tickCh
	})
	return tickCh, func() {
		SetIntervalTicker(orig)
		NeedMock = false
	}
}

// getQueueValue reads one spb.Value from the queue with a timeout.
func getQueueValue(t *testing.T, pq *queue.PriorityQueue) *spb.Value {
	t.Helper()
	ch := make(chan *spb.Value, 1)
	go func() {
		items, err := pq.Get(1)
		if err != nil || len(items) == 0 {
			ch <- nil
			return
		}
		ch <- items[0].(Value).Value
	}()
	select {
	case v := <-ch:
		if v == nil {
			t.Fatal("failed to read value from queue")
		}
		return v
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for queue value")
	}
	return nil
}

// assertJsonValue asserts the value carries the expected JSON payload.
func assertJsonValue(t *testing.T, v *spb.Value, want string) {
	t.Helper()
	if v.GetSyncResponse() {
		t.Fatalf("expected data update, got sync_response")
	}
	got := v.GetVal().GetJsonIetfVal()
	var j1, j2 interface{}
	if err := json.Unmarshal(got, &j1); err != nil {
		t.Fatalf("invalid JSON in update %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &j2); err != nil {
		t.Fatalf("invalid JSON in expectation %q: %v", want, err)
	}
	g, _ := json.Marshal(j1)
	w, _ := json.Marshal(j2)
	if string(g) != string(w) {
		t.Fatalf("unexpected update payload: got %s, want %s", g, w)
	}
}

func assertSyncResponse(t *testing.T, v *spb.Value) {
	t.Helper()
	if !v.GetSyncResponse() {
		t.Fatalf("expected sync_response, got %v", v)
	}
}

// TestDbClientSampleSuppressRedundantTableKey covers the DbClient SAMPLE
// table-key path (dbTableKeySubscribe): with suppress_redundant set, the
// payload is cleared after every send so an unchanged interval produces an
// empty update.
func TestDbClientSampleSuppressRedundantTableKey(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.HSet("COUNTERS:oid:0x1", "SAI_PORT_STAT_PFC_7_RX_PKTS", "2")

	defer saveAndResetTarget2RedisDb()()
	Target2RedisDb[testSampleNs] = map[string]*redis.Client{
		"COUNTERS_DB": redis.NewClient(&redis.Options{Addr: mr.Addr()}),
	}

	tickCh, restoreTicker := mockIntervalTicker()
	defer restoreTicker()

	p := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet0"}}}
	c := DbClient{
		prefix: &gnmipb.Path{Target: "COUNTERS_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{
			p: {{
				dbNamespace: testSampleNs,
				dbName:      "COUNTERS_DB",
				tableName:   "COUNTERS",
				tableKey:    "oid:0x1",
				delimitor:   ":",
			}},
		},
	}

	pq := queue.NewPriorityQueue(10, false)
	stop := make(chan struct{})
	var w sync.WaitGroup
	w.Add(1)
	go c.StreamRun(pq, stop, &w, &gnmipb.SubscriptionList{
		Mode: gnmipb.SubscriptionList_STREAM,
		Subscription: []*gnmipb.Subscription{{
			Path:              p,
			Mode:              gnmipb.SubscriptionMode_SAMPLE,
			SampleInterval:    uint64(time.Second),
			SuppressRedundant: true,
		}},
	})

	assertJsonValue(t, getQueueValue(t, pq), `{"SAI_PORT_STAT_PFC_7_RX_PKTS":"2"}`)
	assertSyncResponse(t, getQueueValue(t, pq))

	// No change: with suppress_redundant the interval produces an empty update.
	tickCh <- time.Now()
	assertJsonValue(t, getQueueValue(t, pq), `{}`)
	tickCh <- time.Now()
	assertJsonValue(t, getQueueValue(t, pq), `{}`)

	close(stop)
	w.Wait()
}

// TestDbClientSampleSuppressRedundantField covers the DbClient SAMPLE
// field-across-tables path (dbFieldMultiSubscribe): a changed leaf is sent
// alone, and an unchanged interval produces an empty update.
func TestDbClientSampleSuppressRedundantField(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.HSet("COUNTERS:oid:0x1", "SAI_PORT_STAT_PFC_7_RX_PKTS", "2")
	mr.HSet("COUNTERS:oid:0x2", "SAI_PORT_STAT_PFC_7_RX_PKTS", "3")

	defer saveAndResetTarget2RedisDb()()
	Target2RedisDb[testSampleNs] = map[string]*redis.Client{
		"COUNTERS_DB": redis.NewClient(&redis.Options{Addr: mr.Addr()}),
	}

	tickCh, restoreTicker := mockIntervalTicker()
	defer restoreTicker()

	p := &gnmipb.Path{Elem: []*gnmipb.PathElem{
		{Name: "COUNTERS"}, {Name: "Ethernet*"}, {Name: "SAI_PORT_STAT_PFC_7_RX_PKTS"},
	}}
	c := DbClient{
		prefix: &gnmipb.Path{Target: "COUNTERS_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{
			p: {
				{
					dbNamespace:  testSampleNs,
					dbName:       "COUNTERS_DB",
					tableName:    "COUNTERS",
					tableKey:     "oid:0x1",
					delimitor:    ":",
					field:        "SAI_PORT_STAT_PFC_7_RX_PKTS",
					jsonTableKey: "Ethernet0",
					jsonField:    "SAI_PORT_STAT_PFC_7_RX_PKTS",
				},
				{
					dbNamespace:  testSampleNs,
					dbName:       "COUNTERS_DB",
					tableName:    "COUNTERS",
					tableKey:     "oid:0x2",
					delimitor:    ":",
					field:        "SAI_PORT_STAT_PFC_7_RX_PKTS",
					jsonTableKey: "Ethernet4",
					jsonField:    "SAI_PORT_STAT_PFC_7_RX_PKTS",
				},
			},
		},
	}

	pq := queue.NewPriorityQueue(10, false)
	stop := make(chan struct{})
	var w sync.WaitGroup
	w.Add(1)
	go c.StreamRun(pq, stop, &w, &gnmipb.SubscriptionList{
		Mode: gnmipb.SubscriptionList_STREAM,
		Subscription: []*gnmipb.Subscription{{
			Path:              p,
			Mode:              gnmipb.SubscriptionMode_SAMPLE,
			SampleInterval:    uint64(time.Second),
			SuppressRedundant: true,
		}},
	})

	assertJsonValue(t, getQueueValue(t, pq),
		`{"Ethernet0":{"SAI_PORT_STAT_PFC_7_RX_PKTS":"2"},"Ethernet4":{"SAI_PORT_STAT_PFC_7_RX_PKTS":"3"}}`)
	assertSyncResponse(t, getQueueValue(t, pq))

	// Change one leaf: only that leaf is reported on the next interval.
	mr.HSet("COUNTERS:oid:0x1", "SAI_PORT_STAT_PFC_7_RX_PKTS", "7")
	tickCh <- time.Now()
	assertJsonValue(t, getQueueValue(t, pq), `{"Ethernet0":{"SAI_PORT_STAT_PFC_7_RX_PKTS":"7"}}`)

	// No change: empty update.
	tickCh <- time.Now()
	assertJsonValue(t, getQueueValue(t, pq), `{}`)

	close(stop)
	w.Wait()
}

// TestMixedDbClientSampleSuppressRedundant covers the MixedDbClient SAMPLE
// table path (streamSampleSubscription -> dbTableKeySubscribe) with
// suppress_redundant set.
func TestMixedDbClientSampleSuppressRedundant(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.HSet("VLAN|Vlan100", "vlanid", "100")

	origRedisDbMap := RedisDbMap
	RedisDbMap = map[string]*redis.Client{
		"sample-test:CONFIG_DB": redis.NewClient(&redis.Options{Addr: mr.Addr()}),
	}
	defer func() { RedisDbMap = origRedisDbMap }()

	tickCh, restoreTicker := mockIntervalTicker()
	defer restoreTicker()

	dbkey := swsscommon.NewSonicDBKey()
	defer swsscommon.DeleteSonicDBKey(dbkey)
	c := MixedDbClient{
		prefix:   &gnmipb.Path{Target: "CONFIG_DB"},
		target:   "CONFIG_DB",
		encoding: gnmipb.Encoding_JSON_IETF,
		dbkey:    dbkey,
		mapkey:   "sample-test",
	}

	p := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "VLAN"}}}
	pq := queue.NewPriorityQueue(10, false)
	stop := make(chan struct{})
	var w sync.WaitGroup
	w.Add(1)
	go c.StreamRun(pq, stop, &w, &gnmipb.SubscriptionList{
		Mode: gnmipb.SubscriptionList_STREAM,
		Subscription: []*gnmipb.Subscription{{
			Path:              p,
			Mode:              gnmipb.SubscriptionMode_SAMPLE,
			SampleInterval:    uint64(time.Second),
			SuppressRedundant: true,
		}},
	})

	assertJsonValue(t, getQueueValue(t, pq), `{"Vlan100":{"vlanid":"100"}}`)
	assertSyncResponse(t, getQueueValue(t, pq))

	// No change: with suppress_redundant the interval produces an empty update.
	tickCh <- time.Now()
	assertJsonValue(t, getQueueValue(t, pq), `{}`)

	close(stop)
	w.Wait()
}
