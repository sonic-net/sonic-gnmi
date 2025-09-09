package client

import (
	"context"
	"fmt"
	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
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
