package client

import (
	"fmt"
	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"sync"
	"testing"
)

var streamFunc = translib.Stream
var subscribeFunc = translib.Subscribe

func TestDoSampleStreamErrorTriggersFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	var syncGroup sync.WaitGroup

	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        1024 * 1024,
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

	subscriber.synced.Add(1)
	wg.Add(1)
	subscriber.doSample("/test/path")
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}

	spbv, ok := item.Val.(*spb.Value)
	if !ok {
		t.Fatalf("Expected *spb.Value, got %T", item.Val)
	}

	if !strings.Contains(spbv.Fatal, "Subscribe operation failed") {
		t.Errorf("Expected fatal message, got: %v", spbv.Fatal)
	}
}

func TestDoOnChangeSubscribeErrorTriggersFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	var syncGroup sync.WaitGroup

	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        1024 * 1024,
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

	subscriber.synced.Add(1)
	wg.Add(1)
	subscriber.doOnChange([]string{"/test/path"})
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}

	spbv, ok := item.Val.(*spb.Value)
	if !ok {
		t.Fatalf("Expected *spb.Value, got %T", item.Val)
	}

	if !strings.Contains(spbv.Fatal, "Subscribe operation failed") {
		t.Errorf("Expected fatal message, got: %v", spbv.Fatal)
	}
}

type MockPriorityQueue struct {
	ReturnError error
}

func (m *MockPriorityQueue) Get(n int) ([]interface{}, error) {
	return nil, m.ReturnError
}

func (m *MockPriorityQueue) Dispose() {}

func TestProcessResponsesQueueErrorTriggersFatalMsg(t *testing.T) {
	var wg sync.WaitGroup
	var syncGroup sync.WaitGroup

	mockQ := &MockPriorityQueue{
		ReturnError: fmt.Errorf("simulated queue error"),
	}

	q := &LimitedQueue{
		Q:              mockQ,
		maxSize:        1024 * 1024,
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

	subscriber.synced.Add(1)
	wg.Add(1)
	go subscriber.processResponses(mockQ)
	wg.Wait()

	item, err := q.DequeueItem()
	if err != nil {
		t.Fatalf("Expected fatal message, got error: %v", err)
	}

	spbv, ok := item.Val.(*spb.Value)
	if !ok {
		t.Fatalf("Expected *spb.Value, got %T", item.Val)
	}

	if !strings.Contains(spbv.Fatal, "Subscribe operation failed") {
		t.Errorf("Expected fatal message, got: %v", spbv.Fatal)
	}
}
