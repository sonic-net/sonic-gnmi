package client

import (
	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	"sync"
	"testing"
)

func TestNotifyQueueExhausted(t *testing.T) {
	// Create a LimitedQueue with maxSize = 0 to simulate exhaustion
	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        0,
		queueLengthSum: 0,
	}

	client := &TranslClient{
		q: q,
	}

	// Dummy message builder that returns a non-empty message
	dummyBuilder := func(resp *translib.SubscribeResponse, ts *translSubscriber) (*gnmipb.Notification, error) {
		return &gnmipb.Notification{
			Update: []*gnmipb.Update{
				{
					Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "dummy"}}},
					Val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "test"}},
				},
			},
		}, nil
	}

	subscriber := &translSubscriber{
		client:     client,
		msgBuilder: dummyBuilder,
	}

	// Create a dummy SubscribeResponse
	resp := &translib.SubscribeResponse{}

	// Call notify
	err := subscriber.notify(resp)

	// Assert error is ResourceExhausted
	if err == nil {
		t.Errorf("Expected ResourceExhausted error, got nil")
	} else if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("Expected ResourceExhausted error, got %v", err)
	}
}

func TestProcessResponsesQueueExhausted(t *testing.T) {
	var wg sync.WaitGroup
	var syncGroup sync.WaitGroup
	stop := make(chan struct{})

	// Create a LimitedQueue with maxSize = 0 to force exhaustion
	q := &LimitedQueue{
		Q:              queue.NewPriorityQueue(1, false),
		maxSize:        0,
		queueLengthSum: 0,
	}

	client := &TranslClient{
		q:       q,
		channel: stop,
		w:       &wg,
	}

	// Dummy msgBuilder that returns a non-empty message
	dummyBuilder := func(resp *translib.SubscribeResponse, ts *translSubscriber) (*gnmipb.Notification, error) {
		return &gnmipb.Notification{
			Update: []*gnmipb.Update{
				{
					Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "dummy"}}},
					Val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "test"}},
				},
			},
		}, nil
	}

	subscriber := &translSubscriber{
		client:     client,
		msgBuilder: dummyBuilder,
		synced:     syncGroup,
	}

	// Add to synced to match the deferred Done()
	subscriber.synced.Add(1)

	// Add a SubscribeResponse to the queue
	err := q.Q.Put(&translib.SubscribeResponse{})
	if err != nil {
		t.Fatalf("Failed to put item in queue: %v", err)
	}

	// Run processResponses
	wg.Add(1)
	go subscriber.processResponses(q.Q)
	wg.Wait()

	// Success: processResponses exited cleanly
}
