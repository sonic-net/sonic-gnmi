package client

import (
	"context"
	"fmt"
	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"strings"
	"sync"
	"testing"
)

var streamFunc = translib.Stream
var subscribeFunc = translib.Subscribe
var isSubscribeSupported = translib.IsSubscribeSupported

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

	// Override subscribeFunc to simulate failure
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

func TestStreamRunSubscribeFatalMsg(t *testing.T) {
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

//passes

func TestStreamRunInvalidSubscriptionModeFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        100,
		queueLengthSum: 0,
	}

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}
	client := &TranslClient{
		path2URI: map[*gnmipb.Path]string{
			path: "/interfaces",
		},
		ctx: context.Background(),
	}

	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path: path,
				Mode: 999, // Invalid mode
			},
		},
	}

	isSubscribeSupported = func(req translib.IsSubscribeRequest) ([]*translib.IsSubscribeResponse, error) {
		return []*translib.IsSubscribeResponse{
			{
				ID:                  0,
				Path:                "/interfaces",
				MinInterval:         10,
				IsOnChangeSupported: true,
				PreferredType:       translib.Sample,
			},
		}, nil
	}
	defer func() { isSubscribeSupported = translib.IsSubscribeSupported }()

	wg.Add(1)
	go client.StreamRun(q, make(chan struct{}), &wg, subscribe)
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(item.Fatal, "Invalid Subscription Mode") {
		t.Errorf("Expected fatal message for invalid mode, got: %v", item.Fatal)
	}
}

func TestStreamRunInvalidHeartbeatIntervalFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        100,
		queueLengthSum: 0,
	}

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}
	client := &TranslClient{
		path2URI: map[*gnmipb.Path]string{
			path: "/interfaces",
		},
		ctx: context.Background(),
	}

	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path:              path,
				Mode:              gnmipb.SubscriptionMode_SAMPLE,
				HeartbeatInterval: 1, // Too low heartbeat
			},
		},
	}

	isSubscribeSupported = func(req translib.IsSubscribeRequest) ([]*translib.IsSubscribeResponse, error) {
		return []*translib.IsSubscribeResponse{
			{
				ID:                  0,
				Path:                "/interfaces",
				MinInterval:         10,
				IsOnChangeSupported: true,
				PreferredType:       translib.Sample,
			},
		}, nil
	}
	defer func() { isSubscribeSupported = translib.IsSubscribeSupported }()

	wg.Add(1)
	go client.StreamRun(q, make(chan struct{}), &wg, subscribe)
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(item.Fatal, "Invalid Heartbeat Interval") {
		t.Errorf("Expected fatal message for heartbeat, got: %v", item.Fatal)
	}
}

func TestStreamRunInvalidSampleIntervalFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        100,
		queueLengthSum: 0,
	}

	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}
	client := &TranslClient{
		path2URI: map[*gnmipb.Path]string{
			path: "/interfaces",
		},
		ctx: context.Background(),
	}

	subscribe := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{
				Path:              path,
				Mode:              gnmipb.SubscriptionMode_SAMPLE,
				SampleInterval:    2, // SampleInterval lower than MinInterval
				HeartbeatInterval: 10,
			},
		},
	}

	isSubscribeSupported = func(req translib.IsSubscribeRequest) ([]*translib.IsSubscribeResponse, error) {
		return []*translib.IsSubscribeResponse{
			{
				ID:                  0,
				Path:                "/interfaces",
				MinInterval:         10,
				IsOnChangeSupported: true,
				PreferredType:       translib.Sample,
			},
		}, nil
	}

	defer func() { isSubscribeSupported = translib.IsSubscribeSupported }()

	wg.Add(1)
	go client.StreamRun(q, make(chan struct{}), &wg, subscribe)
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}
	if !strings.Contains(item.Fatal, "Invalid SampleInterval") {
		t.Errorf("Expected fatal message for sample interval, got: %v", item.Fatal)
	}
}

func TestPollRunCloseFatalMsg(t *testing.T) {
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
