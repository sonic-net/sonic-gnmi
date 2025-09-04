package client

import (
	"context"
	"fmt"
	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	"github.com/go-redis/redis"
	"github.com/google/gnxi/utils/xpath"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"github.com/sonic-net/sonic-gnmi/swsscommon"

	"strings"
	"sync"
	"testing"
	"time"
)

var streamFunc = translib.Stream
var subscribeFunc = translib.Subscribe
var isSubscribeSupported = translib.IsSubscribeSupported
var getAppDbTypedValueFunc = AppDBTableData2TypedValue
var getTypedValueFunc = tableData2TypedValue

// var getTypedValueFuncMixed = tableData2TypedValueMixed
var getMsiTypedValueFunc = Msi2TypedValue
var targetToRedisDb = Target2RedisDb
var redisDbMap = RedisDbMap
var getTableData2Msi = TableData2Msi

type RedisClient interface {
	PSubscribe(pattern string) *redis.PubSub
}

func TestForceEnqueueItemWithNotification(t *testing.T) {
	q := NewLimitedQueue(1, false, 100)

	notification := &gnmipb.Notification{
		Timestamp: time.Now().UnixNano(),
		Update: []*gnmipb.Update{
			{
				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_StringVal{StringVal: "up"},
				},
			},
		},
	}

	item := Value{
		&spb.Value{
			Notification: notification,
		},
	}

	initialSize := q.queueLengthSum

	err := q.ForceEnqueueItem(item)
	if err != nil {
		t.Fatalf("ForceEnqueueItem failed: %v", err)
	}

	// Check that queueLengthSum increased
	if q.queueLengthSum <= initialSize {
		t.Errorf("Expected queueLengthSum to increase, got %d (initial %d)", q.queueLengthSum, initialSize)
	}

	// Check that item is in queue
	dequeuedItem, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Failed to dequeue item: %v", err)
	}
	if dequeuedItem.Notification == nil {
		t.Errorf("Expected Notification in dequeued item, got nil")
	}
}

// DB Client
func TestDbClientSubscriptionModeFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 100)
	stop := make(chan struct{}, 1)

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := DbClient{
		prefix:  &gnmipb.Path{Target: "APPL_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{path: {{dbName: "APPL_DB", tableName: "INTERFACES"}}},
		q:       q,
		channel: stop,
	}

	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path: path,
				Mode: 999, // Invalid mode
			},
		},
	}

	stop <- struct{}{}
	wg.Add(1)
	go client.StreamRun(q, stop, &wg, subscribe)
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(item.Fatal, "unsupported subscription mode") {
		t.Errorf("Expected fatal message for unsupported mode, got: %v", item.Fatal)
	}
}

// START NEW DB TESTS BLOCK

//	func NewDummyValue() Value {
//		return Value{
//			Notification: &spb.Value{
//				Timestamp: time.Now().UnixNano(),
//				Val: &gnmipb.TypedValue{
//					Value: &gnmipb.TypedValue_StringVal{
//						StringVal: "dummy",
//					},
//				},
//			},
//		}
//	}
//
// NEW NEED TO FIND OUT WHY TIMING OUT
// func TestAppDBPollRun_EnqueueItemResourceExhausted(t *testing.T) {
// 	var wg sync.WaitGroup
// 	q := NewLimitedQueue(1, false, 1) // Small queue to simulate overflow
// 	poll := make(chan struct{})

// 	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

// 	client := DbClient{
// 		prefix:  &gnmipb.Path{Target: "APPL_DB"},
// 		pathG2S: map[*gnmipb.Path][]tablePath{path: {{dbName: "APPL_DB", tableName: "INTERFACES"}}},
// 		q:       q,
// 		channel: poll,
// 	}

// 	notification := &gnmipb.Notification{
// 		Timestamp: time.Now().UnixNano(),
// 		Update: []*gnmipb.Update{
// 			{
// 				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
// 				Val: &gnmipb.TypedValue{
// 					Value: &gnmipb.TypedValue_StringVal{StringVal: "up"},
// 				},
// 			},
// 		},
// 	}

// 	item := Value{
// 		&spb.Value{
// 			Notification: notification,
// 		},
// 	}
// 	_ = q.EnqueueItem(item)

// 	// Replace AppDBTableData2TypedValue with a mock
// 	originalFunc := getAppDbTypedValueFunc
// 	getAppDbTypedValueFunc = func(tblPaths []tablePath, op *string) (*gnmipb.TypedValue, error, bool) {
// 		return &gnmipb.TypedValue{
// 			Value: &gnmipb.TypedValue_StringVal{StringVal: "mocked"},
// 		}, nil, true
// 	}
// 	defer func() { getAppDbTypedValueFunc = originalFunc }()

// 	client.q = q
// 	client.channel = poll

// 	wg.Add(1)
// 	go client.AppDBPollRun(q, poll, &wg, nil)

// 	// Trigger the poll
// 	poll <- struct{}{}
// 	wg.Wait()

// 	// Check for fatal message
// 	itemOut, err := q.DequeueItem()
// 	if err != nil {
// 		t.Fatalf("Expected fatal message, got error: %v", err)
// 	}
// 	if itemOut.Fatal == "" {
// 		t.Errorf("Expected fatal message, got: %v", itemOut)
// 	}
// 	if !strings.Contains(itemOut.Fatal, "Subscribe output queue exhausted") {
// 		t.Errorf("Unexpected fatal message: %v", itemOut.Fatal)
// 	}
// }

// SUCCESS
func TestPollRun_EnqueueItemResourceExhausted(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 1)
	poll := make(chan struct{}, 1)

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := DbClient{
		prefix:  &gnmipb.Path{Target: "APPL_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{path: {{dbName: "APPL_DB", tableName: "INTERFACES"}}},
		q:       q,
		channel: poll,
	}

	notification := &gnmipb.Notification{
		Timestamp: time.Now().UnixNano(),
		Update: []*gnmipb.Update{
			{
				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_StringVal{StringVal: "up"},
				},
			},
		},
	}

	item := Value{
		&spb.Value{
			Notification: notification,
		},
	}

	_ = q.EnqueueItem(item)

	poll <- struct{}{}
	wg.Add(1)
	go client.PollRun(q, poll, &wg, nil)
	wg.Wait()

	deq_item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(deq_item.Fatal, "Subscribe output queue exhausted") {
		t.Errorf("Expected fatal message for ResourceExhausted, got: %v", deq_item.Fatal)
	}
}

// NEW
func TestPollRun_SyncResponseEnqueueItemResourceExhausted(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 1) // Small queue to simulate overflow
	poll := make(chan struct{})

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := DbClient{
		prefix:  &gnmipb.Path{Target: "APPL_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{path: {{dbName: "APPL_DB", tableName: "INTERFACES"}}},
	}

	// Fill the queue to simulate ResourceExhausted
	notification := &gnmipb.Notification{
		Timestamp: time.Now().UnixNano(),
		Update: []*gnmipb.Update{
			{
				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_StringVal{StringVal: "up"},
				},
			},
		},
	}
	item := Value{
		&spb.Value{
			Notification: notification,
		},
	}
	_ = q.EnqueueItem(item)

	// Mock tableData2TypedValue to return a valid update
	originalFunc := getTypedValueFunc
	getTypedValueFunc = func(tblPaths []tablePath, op *string) (*gnmipb.TypedValue, error) {
		return &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_StringVal{StringVal: "mocked"},
		}, nil
	}
	defer func() { getTypedValueFunc = originalFunc }()

	client.q = q
	client.channel = poll

	wg.Add(1)
	go client.PollRun(q, poll, &wg, nil)

	// Trigger the poll
	poll <- struct{}{}
	wg.Wait()

	// Check for fatal message
	itemOut, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if itemOut.Fatal == "" {
		t.Errorf("Expected fatal message, got: %v", itemOut)
	}
	if !strings.Contains(itemOut.Fatal, "Subscribe output queue exhausted") {
		t.Errorf("Unexpected fatal message: %v", itemOut.Fatal)
	}
}

// SUCCESS
func TestDbFieldSubscribe_EnqueueItemResourceExhausted(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 1)
	stop := make(chan struct{}, 1)

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := DbClient{
		prefix: &gnmipb.Path{Target: "APPL_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{path: {{
			dbName:       "APPL_DB",
			tableName:    "INTERFACES",
			field:        "admin_status",
			jsonField:    "admin_status",
			jsonTableKey: "INTERFACES",
		}}},
		q:       q,
		channel: stop,
		synced:  sync.WaitGroup{},
		w:       &wg,
	}

	notification := &gnmipb.Notification{
		Timestamp: time.Now().UnixNano(),
		Update: []*gnmipb.Update{
			{
				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_StringVal{StringVal: "up"},
				},
			},
		},
	}

	item := Value{
		&spb.Value{
			Notification: notification,
		},
	}

	_ = q.EnqueueItem(item)

	wg.Add(1)
	client.synced.Add(1)
	go dbFieldSubscribe(&client, path, false, time.Millisecond*10)
	time.Sleep(20 * time.Millisecond)
	stop <- struct{}{}
	wg.Wait()
	client.synced.Wait()

	deq_item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(deq_item.Fatal, "Subscribe output queue exhausted") {
		t.Errorf("Expected fatal message for ResourceExhausted, got: %v", deq_item.Fatal)
	}
}

// NEW
// func TestDbFieldMultiSubscribe_EnqueFatalMsg(t *testing.T) {
// 	// Mock Redis client
// 	type MockRedisClient struct{}

// 	func (m *MockRedisClient) HGet(key, field string) *redis.StringCmd {
// 		return redis.NewStringResult("up", nil)
// 	}

// 	// Initialize Target2RedisDb to avoid nil dereference
// 	Target2RedisDb = map[string]map[string]*redis.Client{
// 		"default": {
// 			"APPL_DB": &MockRedisClient{}, // This line must use '&', not '&amp;'
// 		},
// 	}

// 	var wg sync.WaitGroup
// 	var synced sync.WaitGroup

// 	q := NewLimitedQueue(1, false, 1) // Small queue to simulate overflow
// 	poll := make(chan struct{})
// 	interval := 10 * time.Millisecond

// 	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

// 	client := DbClient{
// 		prefix: &gnmipb.Path{Target: "APPL_DB"},
// 		pathG2S: map[*gnmipb.Path][]tablePath{
// 			path: {{
// 				dbName:       "APPL_DB",
// 				tableName:    "INTERFACES",
// 				field:        "admin_status",
// 				tableKey:     "Ethernet0",
// 				jsonField:    "admin_status",
// 				jsonTableKey: "Ethernet0",
// 				delimitor:    "|",
// 				dbNamespace:  "default",
// 			}},
// 		},
// 		q:       q,
// 		channel: poll,
// 		w:       &wg,
// 		synced:  synced,
// 	}

// 	// Fill the queue to simulate ResourceExhausted
// 	notification := &gnmipb.Notification{
// 		Timestamp: time.Now().UnixNano(),
// 		Update: []*gnmipb.Update{
// 			{
// 				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
// 				Val: &gnmipb.TypedValue{
// 					Value: &gnmipb.TypedValue_StringVal{StringVal: "up"},
// 				},
// 			},
// 		},
// 	}

// 	item := Value{
// 		&spb.Value{
// 			Notification: notification,
// 		},
// 	}
// 	_ = q.EnqueueItem(item)

// 	// Mock Msi2TypedValue to return error first, then success
// 	callCount := 0
// 	originalMsi2TypedValue := getMsiTypedValueFunc
// 	getMsiTypedValueFunc = func(msi map[string]interface{}) (*gnmipb.TypedValue, error) {
// 		callCount++
// 		if callCount == 1 {
// 			return nil, fmt.Errorf("mocked Msi2TypedValue error")
// 		}
// 		return &gnmipb.TypedValue{
// 			Value: &gnmipb.TypedValue_StringVal{StringVal: "mocked"},
// 		}, nil
// 	}
// 	defer func() { getMsiTypedValueFunc = originalMsi2TypedValue }()

// 	// Mock ticker to fire immediately
// 	originalTicker := IntervalTicker
// 	IntervalTicker = func(d time.Duration) <-chan time.Time {
// 		ch := make(chan time.Time, 1)
// 		ch <- time.Now()
// 		return ch
// 	}
// 	defer func() { IntervalTicker = originalTicker }()

// 	wg.Add(1)
// 	synced.Add(1)
// 	go dbFieldMultiSubscribe(&client, path, false, interval, false)

// 	// Wait for goroutine to finish
// 	wg.Wait()

// 	// Check for both fatal messages
// 	foundFatal := 0
// 	for i := 0; i < 2; i++ {
// 		itemOut, err := q.DequeueItem()
// 		if err != nil {
// 			t.Fatalf("Expected fatal message, got error: %v", err)
// 		}
// 		if itemOut.Fatal != "" {
// 			foundFatal++
// 		}
// 	}
// 	if foundFatal != 2 {
// 		t.Errorf("Expected 2 fatal messages, got %d", foundFatal)
// 	}
// }

// NEW
// type MockRedisClient struct {
// 	PSubscribeFunc func(string) *redis.PubSub
// 	HGetFunc       func(string, string) *redis.StringCmd
// 	HGetAllFunc    func(string) *redis.StringStringMapCmd
// 	KeysFunc       func(string) *redis.StringSliceCmd
// }

// func (m *MockRedisClient) PSubscribe(pattern string) *redis.PubSub {
// 	if m.PSubscribeFunc != nil {
// 		return m.PSubscribeFunc(pattern)
// 	}
// 	return &redis.PubSub{}
// }

// func (m *MockRedisClient) HGet(key, field string) *redis.StringCmd {
// 	if m.HGetFunc != nil {
// 		return m.HGetFunc(key, field)
// 	}
// 	return redis.NewStringResult("mocked-value", nil)
// }

// func (m *MockRedisClient) HGetAll(key string) *redis.StringStringMapCmd {
// 	if m.HGetAllFunc != nil {
// 		return m.HGetAllFunc(key)
// 	}
// 	return redis.NewStringStringMapResult(map[string]string{"admin_status": "up"}, nil)
// }

// func (m *MockRedisClient) Keys(pattern string) *redis.StringSliceCmd {
// 	if m.KeysFunc != nil {
// 		return m.KeysFunc(pattern)
// 	}
// 	return redis.NewStringSliceResult([]string{"INTERFACES|Ethernet0"}, nil)
// }

// type MockPubSub struct {
// 	ReceiveTimeoutFunc func(time.Duration) (interface{}, error)
// 	CloseFunc          func() error
// }

// func (m *MockPubSub) ReceiveTimeout(d time.Duration) (interface{}, error) {
// 	if m.ReceiveTimeoutFunc != nil {
// 		return m.ReceiveTimeoutFunc(d)
// 	}
// 	return &redis.Subscription{
// 		Kind:    "psubscribe",
// 		Channel: "__keyspace@0__:INTERFACES|Ethernet0",
// 	}, nil
// }

// func (m *MockPubSub) Close() error {
// 	if m.CloseFunc != nil {
// 		return m.CloseFunc()
// 	}
// 	return nil
// }

// func TestDbTableKeySubscribe_EnqueFatalMsg(t *testing.T) {
// 	var wg sync.WaitGroup
// 	var synced sync.WaitGroup

// 	q := NewLimitedQueue(1, false, 1)
// 	poll := make(chan struct{})
// 	interval := 10 * time.Millisecond

// 	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

// 	client := DbClient{
// 		prefix: &gnmipb.Path{Target: "APPL_DB"},
// 		pathG2S: map[*gnmipb.Path][]tablePath{
// 			path: {{
// 				dbName:       "APPL_DB",
// 				tableName:    "INTERFACES",
// 				tableKey:     "Ethernet0",
// 				jsonField:    "admin_status",
// 				jsonTableKey: "Ethernet0",
// 				field:        "admin_status",
// 				delimitor:    "|",
// 				dbNamespace:  "default",
// 			}},
// 		},
// 		q:       q,
// 		channel: poll,
// 		w:       &wg,
// 		synced:  synced,
// 	}

// 	dummyRedis := redis.NewClient(&redis.Options{
// 		Addr: "localhost:9999", // unreachable port to simulate failure
// 	})

// 	originalTarget2RedisDb := targetToRedisDb
// 	targetToRedisDb = map[string]map[string]*redis.Client{
// 		"default": {
// 			"APPL_DB": dummyRedis,
// 		},
// 	}
// 	defer func() { targetToRedisDb = originalTarget2RedisDb }()

// 	wg.Add(1)
// 	synced.Add(1)
// 	go dbTableKeySubscribe(&client, path, interval, false)
// 	wg.Wait()

// 	itemOut, err := q.DequeueItem()
// 	if err != nil {
// 		t.Fatalf("Expected fatal message, got error: %v", err)
// 	}
// 	if itemOut.Fatal == "" {
// 		t.Errorf("Expected fatal message, got: %v", itemOut)
// 	}
// 	if !strings.Contains(itemOut.Fatal, "psubscribe") {
// 		t.Errorf("Unexpected fatal message: %v", itemOut.Fatal)
// 	}
// }

// NEW
func TestSendEvent_EnqueFatalMsg(t *testing.T) {
	// Case 1: ResourceExhausted
	q := NewLimitedQueue(1, false, 1)
	eventClient := &EventClient{
		prefix: &gnmipb.Path{Target: "EVENT_DB"},
		path:   &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "event"}}},
		q:      q,
	}

	// Fill the queue
	notification := &gnmipb.Notification{
		Timestamp: time.Now().UnixNano(),
		Update: []*gnmipb.Update{
			{
				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "event"}}},
				Val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "up"}},
			},
		},
	}

	item := Value{
		&spb.Value{
			Notification: notification,
		},
	}

	_ = q.EnqueueItem(item)

	tv := &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "test_event"}}
	err := send_event(eventClient, tv, time.Now().UnixNano())
	if err == nil {
		t.Fatalf("Expected ResourceExhausted error, got nil")
	}
	deqItem, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if deqItem.Fatal == "" {
		t.Errorf("Expected fatal message, got: %v", deqItem)
	}

	// Case 2: Simulate internal error by manually enqueuing fatal message
	q = NewLimitedQueue(1, false, 1000)
	eventClient.q = q

	// notification2 := &gnmipb.Notification{
	// 	Timestamp: time.Now().UnixNano(),
	// 	Fatal:     "Internal error",
	// }

	item2 := Value{
		&spb.Value{
			Timestamp: time.Now().UnixNano(),
			Fatal:     "Internal error",
		},
	}

	// Simulate internal error
	q.ForceEnqueueItem(item2)

	deqItem, err = q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if deqItem.Fatal != "Internal error" {
		t.Errorf("Expected 'Internal error' fatal message, got: %v", deqItem.Fatal)
	}
}

// NEW
func TestMixedDbClientPollRun_EnqueFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 1) // Small queue to simulate overflow
	poll := make(chan struct{}, 1)

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := &MixedDbClient{
		prefix:  &gnmipb.Path{Target: "APPL_DB"},
		paths:   []*gnmipb.Path{path},
		q:       q,
		channel: poll,
		mapkey:  "default",
	}

	// Fill the queue to simulate ResourceExhausted
	notification := &gnmipb.Notification{
		Timestamp: time.Now().UnixNano(),
		Update: []*gnmipb.Update{
			{
				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
				Val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "up"}},
			},
		},
	}

	item := Value{
		&spb.Value{
			Notification: notification,
		},
	}

	_ = q.EnqueueItem(item)

	// Inject a Redis client that will cause a logic error (e.g., missing RedisDbMap entry)
	RedisDbMap = map[string]*redis.Client{
		// Leave out "default:APPL_DB" to simulate missing Redis client
	}

	wg.Add(1)
	go client.PollRun(q, poll, &wg, nil)

	// Trigger the poll
	poll <- struct{}{}
	wg.Wait()

	// Check for fatal messages
	foundFatal := 0
	for i := 0; i < 2; i++ {
		itemOut, err := q.DequeueItem()
		if err != nil {
			t.Fatalf("Expected fatal message, got error: %v", err)
		}
		if itemOut.Fatal != "" {
			foundFatal++
		}
	}
	if foundFatal != 2 {
		t.Errorf("Expected 2 fatal messages, got %d", foundFatal)
	}
}

// NEW
func TestStreamRun_EnqueFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	var synced sync.WaitGroup

	q := NewLimitedQueue(1, false, 1)
	stop := make(chan struct{})

	client := &MixedDbClient{
		prefix:  &gnmipb.Path{Target: "APPL_DB"},
		paths:   []*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}},
		q:       q,
		channel: stop,
		w:       &wg,
		synced:  synced,
	}

	notification := &gnmipb.Notification{
		Timestamp: time.Now().UnixNano(),
		Update: []*gnmipb.Update{
			{
				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
				Val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "up"}},
			},
		},
	}

	item := Value{
		&spb.Value{
			Notification: notification,
		},
	}
	// Fill the queue to simulate ResourceExhausted
	_ = q.EnqueueItem(item)

	// Create a subscription with unsupported mode
	sub := &gnmipb.Subscription{
		Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
		Mode: gnmipb.SubscriptionMode_TARGET_DEFINED, // unsupported
	}
	subList := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{sub},
	}

	wg.Add(1)
	synced.Add(1)
	go client.StreamRun(q, stop, &wg, subList)
	wg.Wait()

	itemOut, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if itemOut.Fatal == "" {
		t.Errorf("Expected fatal message, got: %v", itemOut)
	}
}

// NEW
// func TestDbFieldSubscribe_EnqueFatalMsg(t *testing.T) {
// 	var wg sync.WaitGroup
// 	var synced sync.WaitGroup

// 	q := NewLimitedQueue(1, false, 1)
// 	stop := make(chan struct{})

// 	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

// 	client := &MixedDbClient{
// 		prefix:  &gnmipb.Path{Target: "APPL_DB"},
// 		q:       q,
// 		channel: stop,
// 		w:       &wg,
// 		synced:  synced,
// 		mapkey:  "default",
// 	}

// 	notification := &gnmipb.Notification{
// 		Timestamp: time.Now().UnixNano(),
// 		Update: []*gnmipb.Update{
// 			{
// 				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
// 				Val: &gnmipb.TypedValue{
// 					Value: &gnmipb.TypedValue_StringVal{StringVal: "up"},
// 				},
// 			},
// 		},
// 	}

// 	item := Value{
// 		&spb.Value{
// 			Notification: notification,
// 		},
// 	}

// 	_ = q.EnqueueItem(item)

// 	// Mock RedisDbMap to simulate missing DB
// 	originalRedisDbMap := redisDbMap
// 	redisDbMap = map[string]RedisClient{}
// 	defer func() { redisDbMap = originalRedisDbMap }()

// 	// Mock getDbtablePath to return valid path
// 	client.getDbtablePath = func(p *gnmipb.Path, op *string) ([]tablePath, error) {
// 		return []tablePath{{
// 			dbName:       "APPL_DB",
// 			tableName:    "INTERFACES",
// 			tableKey:     "Ethernet0",
// 			field:        "admin_status",
// 			jsonField:    "admin_status",
// 			jsonTableKey: "Ethernet0",
// 			delimitor:    "|",
// 			dbNamespace:  "default",
// 		}}, nil
// 	}

// 	wg.Add(1)
// 	synced.Add(1)
// 	go client.dbFieldSubscribe(path, false, 10*time.Millisecond)
// 	wg.Wait()

// 	itemOut, err := q.DequeueItem()
// 	if err != nil {
// 		t.Fatalf("Expected fatal message, got error: %v", err)
// 	}
// 	if itemOut.Fatal == "" {
// 		t.Errorf("Expected fatal message, got: %v", itemOut)
// 	}
// }

// // NEW
// func TestDbTableKeySubscribe_EnqueFatalMsg(t *testing.T) {
// 	var wg sync.WaitGroup
// 	var synced sync.WaitGroup

// 	q := NewLimitedQueue(1, false, 1)
// 	stop := make(chan struct{})

// 	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

// 	client := &MixedDbClient{
// 		prefix:  &gnmipb.Path{Target: "APPL_DB"},
// 		q:       q,
// 		channel: stop,
// 		w:       &wg,
// 		synced:  synced,
// 		mapkey:  "default",
// 	}

// 	notification := &gnmipb.Notification{
// 		Timestamp: time.Now().UnixNano(),
// 		Update: []*gnmipb.Update{
// 			{
// 				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
// 				Val: &gnmipb.TypedValue{
// 					Value: &gnmipb.TypedValue_StringVal{StringVal: "up"},
// 				},
// 			},
// 		},
// 	}

// 	item := Value{
// 		&spb.Value{
// 			Notification: notification,
// 		},
// 	}
// 	_ = q.EnqueueItem(item)

// 	// Mock getDbtablePath to return valid path
// 	client.getDbtablePath = func(p *gnmipb.Path, op *string) ([]tablePath, error) {
// 		return []tablePath{{
// 			dbName:       "APPL_DB",
// 			tableName:    "INTERFACES",
// 			tableKey:     "Ethernet0",
// 			field:        "admin_status",
// 			jsonField:    "admin_status",
// 			jsonTableKey: "Ethernet0",
// 			delimitor:    "|",
// 			dbNamespace:  "default",
// 		}}, nil
// 	}

// 	// Mock RedisDbMap with failing pubsub
// 	originalRedisDbMap := redisDbMap
// 	redisDbMap = map[string]RedisClient{
// 		"default:APPL_DB": &MockRedisClient{
// 			PSubscribeFunc: func(pattern string) *MockPubSub {
// 				return &MockPubSub{
// 					ReceiveTimeoutFunc: func(d time.Duration) (interface{}, error) {
// 						return nil, fmt.Errorf("mock psubscribe failure")
// 					},
// 					CloseFunc: func() error { return nil },
// 				}
// 			},
// 		},
// 	}
// 	defer func() { redisDbMap = originalRedisDbMap }()

// 	// Mock tableData2Msi to return error
// 	originalFunc := client.getTableData2Msi
// 	originalFunc = func(tp *tablePath, useKey bool, op *string, msi *map[string]interface{}) error {
// 		return fmt.Errorf("mock tableData2Msi error")
// 	}
// 	defer func() { client.getTableData2Msi = originalFunc }()

// 	// Mock msi2TypedValue to return error
// 	originalMsi2TypedValue := client.getMsiTypedValueFunc
// 	originalMsi2TypedValue = func(msi map[string]interface{}) (*gnmipb.TypedValue, error) {
// 		return nil, fmt.Errorf("mock msi2TypedValue error")
// 	}
// 	defer func() { client.getMsiTypedValueFunc = originalMsi2TypedValue }()

// 	wg.Add(1)
// 	synced.Add(1)
// 	go client.dbTableKeySubscribe(path, 10*time.Millisecond, false)
// 	wg.Wait()

// 	// Check for fatal message
// 	itemOut, err := q.DequeueItem()
// 	if err != nil {
// 		t.Fatalf("Expected fatal message, got error: %v", err)
// 	}
// 	if itemOut.Fatal == "" {
// 		t.Errorf("Expected fatal message, got: %v", itemOut)
// 	}
// }

// END NEW DB TESTS BLOCK

// Mixed DB Client
func TestMixedDbClientSubscriptionModeFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 100)
	stop := make(chan struct{}, 1)

	// Create a dummy path
	path, _ := xpath.ToGNMIPath("/abc/dummy")

	client := MixedDbClient{
		paths:   []*gnmipb.Path{path},
		dbkey:   swsscommon.NewSonicDBKey(),
		q:       q,
		channel: stop,
	}
	defer swsscommon.DeleteSonicDBKey(client.dbkey)

	// Create a subscription with an invalid mode
	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path: path,
				Mode: 999, // Invalid mode to trigger enqueFatalMsg
			},
		},
	}

	stop <- struct{}{} // simulate stop signal
	wg.Add(1)
	go client.StreamRun(q, stop, &wg, subscribe)
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(item.Fatal, "unsupported subscription mode") {
		t.Errorf("Expected fatal message for unsupported mode, got: %v", item.Fatal)
	}
}

// Non DB Client
func TestNonDbClientSubscriptionModeFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 100)
	stop := make(chan struct{}, 1)

	dummyGetter := func() ([]byte, error) {
		return nil, nil
	}

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := NonDbClient{
		prefix:      &gnmipb.Path{},
		path2Getter: map[*gnmipb.Path]dataGetFunc{path: dummyGetter},
		q:           q,
		channel:     stop,
	}

	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path: path,
				Mode: gnmipb.SubscriptionMode_ON_CHANGE, // Invalid for NonDbClient
			},
		},
	}

	stop <- struct{}{}
	wg.Add(1)
	go client.StreamRun(q, stop, &wg, subscribe)
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(item.Fatal, "Unsupported subscription mode") {
		t.Errorf("Expected fatal message for unsupported mode, got: %v", item.Fatal)
	}
}

func TestNonDbClientSampleIntervalFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 100)
	stop := make(chan struct{}, 1)

	dummyGetter := func() ([]byte, error) {
		return nil, nil
	}

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := NonDbClient{
		prefix:      &gnmipb.Path{},
		path2Getter: map[*gnmipb.Path]dataGetFunc{path: dummyGetter},
		q:           q,
		channel:     stop,
	}

	// Use a sample interval less than MinSampleInterval
	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path:           path,
				Mode:           gnmipb.SubscriptionMode_SAMPLE,
				SampleInterval: uint64(MinSampleInterval.Nanoseconds() - 1), // Invalid interval
			},
		},
	}

	stop <- struct{}{}
	wg.Add(1)
	go client.StreamRun(q, stop, &wg, subscribe)
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(item.Fatal, "invalid interval") {
		t.Errorf("Expected fatal message for invalid sample interval, got: %v", item.Fatal)
	}
}

func TestNonDbClientValidSubscription(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 1000)
	stop := make(chan struct{})

	// Dummy getter function
	dummyGetter := func() ([]byte, error) {
		return []byte("dummy data"), nil
	}

	// Valid path
	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := NonDbClient{
		prefix:      &gnmipb.Path{},
		path2Getter: map[*gnmipb.Path]dataGetFunc{path: dummyGetter},
		channel:     stop,
	}

	// Valid subscription
	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path:           path,
				Mode:           gnmipb.SubscriptionMode_SAMPLE,
				SampleInterval: uint64(1e9), // 1 second
			},
		},
	}

	wg.Add(1)
	go client.StreamRun(q, stop, &wg, subscribe)

	// Let the StreamRun start and enqueue sync response
	time.Sleep(100 * time.Millisecond)
	stop <- struct{}{}
	wg.Wait()

	_, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected sync response item, got error: %v", err)
	}
}

// Translib Data Client
func TestDoSampleFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	var syncGroup sync.WaitGroup

	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        100,
		queueLengthSum: 0,
	}

	client := &TranslClient{
		q:       q,
		channel: make(chan struct{}),
		w:       &wg,
	}

	subscriber := &translSubscriber{
		client:      client,
		synced:      syncGroup,
		sampleCache: nil,
	}

	// Simulate translib.Stream failure by overriding it
	streamFunc = func(req translib.SubscribeRequest) error {
		return fmt.Errorf("simulated stream failure")
	}
	defer func() { streamFunc = translib.Stream }() // Reset after test

	wg.Add(1)
	subscriber.doSample("/test/path")

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}

	spbv := &item
	if spbv == nil {
		t.Fatalf("Expected *spb.Value, got %T", &item)
	}

	if !strings.Contains(spbv.Fatal, "Subscribe operation failed") {
		t.Errorf("Expected fatal message, got: %v", spbv.Fatal)
	}
}

func TestDoOnChangeFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	var syncGroup sync.WaitGroup

	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        100,
		queueLengthSum: 0,
	}

	client := &TranslClient{
		q:       q,
		channel: make(chan struct{}),
		w:       &wg,
	}

	subscriber := &translSubscriber{
		client: client,
		synced: syncGroup,
	}

	// Simulate translib.Stream failure by overriding it
	subscribeFunc = func(req translib.SubscribeRequest) error {
		return fmt.Errorf("simulated subscribe failure")
	}
	defer func() { subscribeFunc = translib.Subscribe }() // Reset after test

	wg.Add(1)
	subscriber.doOnChange([]string{"/test/path"})

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}

	spbv := &item
	if spbv == nil {
		t.Fatalf("Expected *spb.Value, got %T", &item)
	}

	if !strings.Contains(spbv.Fatal, "Subscribe operation failed") {
		t.Errorf("Expected fatal message, got: %v", spbv.Fatal)
	}
}

func TestSubscribeFatalMsg(t *testing.T) {
	var wg sync.WaitGroup

	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        100,
		queueLengthSum: 0,
	}

	dummyPath := &gnmipb.Path{}
	client := &TranslClient{
		path2URI: map[*gnmipb.Path]string{
			dummyPath: "/test/path",
		},
		ctx: context.Background(),
	}

	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path: dummyPath,
				Mode: gnmipb.SubscriptionMode_SAMPLE,
			},
		},
		Encoding: gnmipb.Encoding_JSON,
	}

	stop := make(chan struct{})
	wg.Add(1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
			}
		}()
		client.StreamRun(q, stop, &wg, subscribe)
	}()

	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}

	if !strings.Contains(item.Fatal, "Subscribe operation failed") {
		t.Errorf("Expected fatal message, got: %v", item.Fatal)
	}
}

func TestPollCloseFatalMsg(t *testing.T) {
	var wg sync.WaitGroup

	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        100,
		queueLengthSum: 0,
	}

	dummyPath := &gnmipb.Path{}
	client := &TranslClient{
		path2URI: map[*gnmipb.Path]string{
			dummyPath: "/test/path",
		},
		ctx: context.Background(),
	}

	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path: dummyPath,
				Mode: gnmipb.SubscriptionMode_SAMPLE,
			},
		},
		Encoding: gnmipb.Encoding_JSON,
	}

	poll := make(chan struct{})
	wg.Add(1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
			}
		}()
		client.PollRun(q, poll, &wg, subscribe)
	}()

	// Simulate poll trigger
	close(poll)
	wg.Wait()

	_, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
}

func TestRecoverFatalMsg(t *testing.T) {
	// Create a mock LimitedQueue
	mockQueue := NewLimitedQueue(1, false, 100)
	// Create a mock TranslClient
	client := &TranslClient{
		q: mockQueue,
	}

	// Function that panics and defers recoverSubscribe
	func() {
		defer recoverSubscribe(client)
		panic("test panic")
	}()

	item, err := mockQueue.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(item.Fatal, "Subscribe operation failed with error =rpc error: code = Internal desc = test panic") {
		t.Errorf("Unexpected fatal message: %v", item.Fatal)
	}

}
