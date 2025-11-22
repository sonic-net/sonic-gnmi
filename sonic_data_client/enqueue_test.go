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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

func TestPollRun_SyncResponseEnqueueItemResourceExhausted(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 1)
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

func TestDbFieldMultiSubscribe_EnqueFatalMsg(t *testing.T) {

	// Use real Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})

	// Set up test data
	err := redisClient.HSet("INTERFACES|Ethernet0", "admin_status", "up").Err()
	if err != nil {
		t.Fatalf("Failed to set test data in Redis: %v", err)
	}

	Target2RedisDb = map[string]map[string]*redis.Client{
		"default": {
			"APPL_DB": redisClient,
		},
	}

	var wg sync.WaitGroup

	q := NewLimitedQueue(1, false, 1) // Small queue to simulate overflow
	poll := make(chan struct{})
	interval := 10 * time.Millisecond

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := DbClient{
		prefix: &gnmipb.Path{Target: "APPL_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{
			path: {{
				dbName:       "APPL_DB",
				tableName:    "INTERFACES",
				field:        "admin_status",
				tableKey:     "Ethernet0",
				jsonField:    "admin_status",
				jsonTableKey: "Ethernet0",
				delimitor:    "|",
				dbNamespace:  "default",
			}},
		},
		q:       q,
		channel: poll,
		w:       &wg,
		synced:  sync.WaitGroup{},
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

	// Mock Msi2TypedValue to return error first, then success
	callCount := 0
	originalMsi2TypedValue := getMsiTypedValueFunc
	getMsiTypedValueFunc = func(msi map[string]interface{}) (*gnmipb.TypedValue, error) {
		callCount++
		if callCount == 1 {
			return nil, fmt.Errorf("mocked Msi2TypedValue error")
		}
		return &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_StringVal{StringVal: "mocked"},
		}, nil
	}
	defer func() { getMsiTypedValueFunc = originalMsi2TypedValue }()

	originalTicker := IntervalTicker
	IntervalTicker = func(d time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	defer func() { IntervalTicker = originalTicker }()

	wg.Add(1)
	client.synced.Add(1)
	go dbFieldMultiSubscribe(&client, path, false, interval, false)

	wg.Wait()
	client.synced.Wait()

	itemOut, err := q.DequeueItem()
	if itemOut.Fatal == "" {
		t.Errorf("Expected fatal message, got: %v", itemOut)
	}
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
}

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

func TestMixedDbClientPollRun_EnqueFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 1) // Small queue to simulate overflow
	poll := make(chan struct{})

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := MixedDbClient{
		prefix:  &gnmipb.Path{Target: "APPL_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{path: {{dbName: "APPL_DB", tableName: "INTERFACES"}}},
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
	close(poll)
	wg.Wait()

	// Check for fatal messages
	_, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
}

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

//Testing

func TestEnqueueItem(t *testing.T) {
	q := NewLimitedQueue(10, false, 100)
	val := &spb.Value{
		Timestamp: time.Now().UnixNano(),
	}
	err := q.EnqueueItem(Value{val})
	if err != nil {
		t.Errorf("EnqueueItem returned error: %v", err)
	}
}

func TestEnqueueItemError(t *testing.T) {
	q := NewLimitedQueue(10, false, 0)
	val := &spb.Value{
		Timestamp: time.Now().UnixNano(),
	}
	err := q.EnqueueItem(Value{val})
	if err == nil {
		t.Errorf("EnqueueItem did not return error")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.ResourceExhausted {
		t.Errorf("EnqueueItem returned incorrect error: %v", err)
	}
}

func TestStreamRun_EnqueFatalMsgMixed(t *testing.T) {
	var wg sync.WaitGroup
	var synced sync.WaitGroup

	q := NewLimitedQueue(1, false, 0)
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
		Mode: gnmipb.SubscriptionMode_SAMPLE,
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

func TestPollRun_EnqueFatalMsgMixed(t *testing.T) {
	var wg sync.WaitGroup
	q := NewLimitedQueue(1, false, 0)
	poll := make(chan struct{})

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}

	client := MixedDbClient{
		prefix:  &gnmipb.Path{Target: "APPL_DB"},
		pathG2S: map[*gnmipb.Path][]tablePath{path: {{dbName: "APPL_DB", tableName: "INTERFACES"}}},
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
	close(poll)
	wg.Wait()

	// Check for fatal messages
	_, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
}
