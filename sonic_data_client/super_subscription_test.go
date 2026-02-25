package client

import (
	"github.com/Workiva/go-datastructures/queue"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"sync"
	"testing"
	"time"
)

var (
	SAMPLE_SUB = &pb.SubscriptionList{
		Prefix:   &pb.Path{Origin: "openconfig", Target: "OC_YANG"},
		Mode:     pb.SubscriptionList_STREAM,
		Encoding: pb.Encoding_PROTO,
		Subscription: []*pb.Subscription{
			{
				Path:           &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
				Mode:           pb.SubscriptionMode_SAMPLE,
				SampleInterval: 2000000000,
			},
			{
				Path:           &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
				Mode:           pb.SubscriptionMode_SAMPLE,
				SampleInterval: 30000000000,
			},
		},
	}
	ONCHANGE_SUB = &pb.SubscriptionList{
		Prefix:   &pb.Path{Origin: "openconfig", Target: "OC_YANG"},
		Mode:     pb.SubscriptionList_STREAM,
		Encoding: pb.Encoding_PROTO,
		Subscription: []*pb.Subscription{
			{
				Path: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
				Mode: pb.SubscriptionMode_ON_CHANGE,
			},
		},
	}
	MIXED_SUB = &pb.SubscriptionList{
		Prefix:   &pb.Path{Origin: "openconfig", Target: "OC_YANG"},
		Mode:     pb.SubscriptionList_STREAM,
		Encoding: pb.Encoding_PROTO,
		Subscription: []*pb.Subscription{
			{
				Path: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
				Mode: pb.SubscriptionMode_ON_CHANGE,
			},
			{
				Path:           &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
				Mode:           pb.SubscriptionMode_SAMPLE,
				SampleInterval: 30000000000,
			},
			{
				Path: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
				Mode: pb.SubscriptionMode_TARGET_DEFINED,
			},
		},
	}
	TD_SUB = &pb.SubscriptionList{
		Prefix:   &pb.Path{Origin: "openconfig", Target: "OC_YANG"},
		Mode:     pb.SubscriptionList_STREAM,
		Encoding: pb.Encoding_PROTO,
		Subscription: []*pb.Subscription{
			{
				Path: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
				Mode: pb.SubscriptionMode_TARGET_DEFINED,
			},
			{
				Path:           &pb.Path{Elem: []*pb.PathElem{{Name: "lacp"}}},
				Mode:           pb.SubscriptionMode_TARGET_DEFINED,
				SampleInterval: 4000000000,
			},
		},
	}
)

func clearSuperSubscriptions() {
	superSubs.mu.Lock()
	defer superSubs.mu.Unlock()
	//clear(superSubs.subs)
	for k := range superSubs.subs {
		delete(superSubs.subs, k)
	}
}
func TestCreateSuperSubscription(t *testing.T) {
	tests := []struct {
		name         string
		subscription *pb.SubscriptionList
		expectNil    bool
	}{
		{
			name:         "CreateSuperSubscription",
			subscription: SAMPLE_SUB,
			expectNil:    false,
		},
		{
			name:         "CreateNilSuperSubscription",
			subscription: nil,
			expectNil:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Cleanup(clearSuperSubscriptions)
			if superSub := createSuperSubscription(test.subscription); (superSub == nil) != test.expectNil {
				t.Errorf("createSuperSubscription returned invalid SuperSubscription: expectNil=%v, got=%v", test.expectNil, superSub)
			}
			numSuperSubs := len(superSubs.subs)
			if test.expectNil && numSuperSubs != 0 {
				t.Errorf("createSuperSubscription incorrectly added a SuperSubscription to the map: len(superSubs)=%v", numSuperSubs)
			} else if !test.expectNil && numSuperSubs != 1 {
				t.Errorf("createSuperSubscription did not add a SuperSubscription to the map: len(superSubs)=%v", numSuperSubs)
			}
		})
	}
}
func TestFindSuperSubscription(t *testing.T) {
	t.Cleanup(clearSuperSubscriptions)
	createSuperSubscription(SAMPLE_SUB)
	createSuperSubscription(MIXED_SUB)
	createSuperSubscription(TD_SUB)
	tests := []struct {
		name         string
		subscription *pb.SubscriptionList
		expectedOk   bool
	}{
		{
			name:         "FindExistingSuperSubscription",
			subscription: SAMPLE_SUB,
			expectedOk:   true,
		},
		{
			name:         "FindNonExistingSuperSubscription",
			subscription: ONCHANGE_SUB,
			expectedOk:   false,
		},
		{
			name:         "FindNilSuperSubscription",
			subscription: nil,
			expectedOk:   false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			superSub := findSuperSubscription(test.subscription)
			if test.expectedOk && superSub == nil {
				t.Errorf("findSuperSubscription incorrectly returned a nil superSubscription: want=%v, got=%v", test.expectedOk, superSub)
			} else if !test.expectedOk && superSub != nil {
				t.Errorf("findSuperSubscription incorrectly returned a non-nil superSubscription")
			}
		})
	}
}
func TestDeleteSuperSubscription(t *testing.T) {
	t.Cleanup(clearSuperSubscriptions)
	tests := []struct {
		name     string
		superSub *superSubscription
	}{
		{
			name:     "DeleteExistingSuperSubscription",
			superSub: createSuperSubscription(SAMPLE_SUB),
		},
		{
			name:     "DeleteNonExistingSuperSubscription",
			superSub: &superSubscription{},
		},
		{
			name:     "DeleteNilSuperSubscription",
			superSub: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deleteSuperSubscription(test.superSub)
			if test.superSub == nil {
				return
			}
			if _, ok := superSubs.subs[test.superSub]; ok {
				t.Errorf("deleteSuperSubscription did not remove %v from the superSubs map: %v", test.superSub, superSubs.subs)
			}
		})
	}
}
func TestSendNotifications(t *testing.T) {
	t.Cleanup(clearSuperSubscriptions)
	clients := []*TranslClient{}
	superSub := createSuperSubscription(SAMPLE_SUB)
	for i := 0; i < 5; i++ {
		newClient := &TranslClient{
			q: queue.NewPriorityQueue(1, false),
		}
		clients = append(clients, newClient)
		newClient.addClientToSuperSubscription(SAMPLE_SUB)
	}
	superSub.sendNotifications(&spb.Value{})
	for i := 0; i < 5; i++ {
		if _, err := clients[i].q.Get(1); err != nil {
			t.Errorf("Error receiving notifications: %v", err)
		}
	}
	// Nil value passed into sendNotifications should be a no-op
	superSub.sendNotifications(nil)
	for i := 0; i < 5; i++ {
		if !clients[i].q.Empty() {
			t.Errorf("A notification was incorrectly written to the queue: length=%v", clients[i].q.Len())
		}
	}
}
func TestPopulateTickers(t *testing.T) {
	tests := []struct {
		name        string
		superSub    *superSubscription
		ticker_map  map[int][]*ticker_info
		expectErr   bool
		expectedLen int
	}{
		{
			name: "CreateNewTicker",
			superSub: &superSubscription{
				mu:      &sync.RWMutex{},
				tickers: map[int]*time.Ticker{},
			},
			ticker_map: map[int][]*ticker_info{
				10000: []*ticker_info{&ticker_info{interval: 10000}, &ticker_info{interval: 10000}},
				30000: []*ticker_info{&ticker_info{interval: 30000}},
			},
			expectErr:   false,
			expectedLen: 2,
		},
		{
			name: "UseExistingTickers",
			superSub: &superSubscription{
				mu: &sync.RWMutex{},
				tickers: map[int]*time.Ticker{
					10000: time.NewTicker(10000 * time.Nanosecond),
					30000: time.NewTicker(30000 * time.Nanosecond),
				},
			},
			ticker_map: map[int][]*ticker_info{
				10000: []*ticker_info{&ticker_info{interval: 10000}, &ticker_info{interval: 10000}},
				30000: []*ticker_info{&ticker_info{interval: 30000}},
			},
			expectErr:   false,
			expectedLen: 2,
		},
		{
			name: "LengthOfTickerMapsDontMatch",
			superSub: &superSubscription{
				mu: &sync.RWMutex{},
				tickers: map[int]*time.Ticker{
					10000: time.NewTicker(10000 * time.Nanosecond),
				},
			},
			ticker_map: map[int][]*ticker_info{
				10000: []*ticker_info{&ticker_info{interval: 10000}, &ticker_info{interval: 10000}},
				30000: []*ticker_info{&ticker_info{interval: 30000}},
			},
			expectErr: true,
		},
		{
			name: "TickerMapKeysDontMatch",
			superSub: &superSubscription{
				mu: &sync.RWMutex{},
				tickers: map[int]*time.Ticker{
					10000: time.NewTicker(10000 * time.Nanosecond),
					20000: time.NewTicker(20000 * time.Nanosecond),
				},
			},
			ticker_map: map[int][]*ticker_info{
				10000: []*ticker_info{&ticker_info{interval: 10000}, &ticker_info{interval: 10000}},
				30000: []*ticker_info{&ticker_info{interval: 30000}},
			},
			expectErr: true,
		},
		{
			name: "NilTickerMap",
			superSub: &superSubscription{
				mu:      &sync.RWMutex{},
				tickers: map[int]*time.Ticker{},
			},
			ticker_map: nil,
			expectErr:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Save pointers to the original tickers to ensure they were not overwritten.
			origTickers := map[int]*time.Ticker{}
			for interval, ticker := range test.superSub.tickers {
				origTickers[interval] = ticker
			}
			err := test.superSub.populateTickers(test.ticker_map)
			if err != nil {
				if !test.expectErr {
					t.Errorf("Unexpected error returned from populateTickers: %v", err)
				}
				return
			} else if test.expectErr {
				t.Fatalf("Test expected error but got %v", err)
			}
			for interval, tInfos := range test.ticker_map {
				for _, tInfo := range tInfos {
					if tInfo.t == nil {
						t.Errorf("Nil ticker in ticker_map")
					}
					origTicker, ok := origTickers[interval]
					if ok && origTicker != tInfo.t {
						t.Errorf("Original ticker was overwritten: original=%p, new=%p", origTicker, tInfo.t)
					}
				}
			}
			if len(test.superSub.tickers) != test.expectedLen {
				t.Errorf("Unexpected number of tickers in the Super Subscription: want=%v, got=%v", test.expectedLen, len(test.superSub.tickers))
			}
		})
	}
}
func TestIsPrimary(t *testing.T) {
	t.Cleanup(clearSuperSubscriptions)
	client1 := &TranslClient{q: queue.NewPriorityQueue(1, false)}
	client2 := &TranslClient{q: queue.NewPriorityQueue(1, false)}
	client1.addClientToSuperSubscription(SAMPLE_SUB)
	client2.addClientToSuperSubscription(SAMPLE_SUB)
	if !client1.isPrimary() {
		t.Errorf("Expected client1 to be the primary client but it is not")
	}
	if client2.isPrimary() {
		t.Errorf("Expected client2 to not be the primary client but it is")
	}
	client1.superSub.primaryClient = nil
	if client1.isPrimary() {
		t.Errorf("The primaryClient is nil but client1.isPrimary() returned true")
	}
	client1.superSub = nil
	if client1.isPrimary() {
		t.Errorf("The Super Subscription is nil but client1.isPrimary() returned true")
	}
}
func TestLeaveSuperSubscription(t *testing.T) {
	t.Cleanup(clearSuperSubscriptions)
	client1 := &TranslClient{q: queue.NewPriorityQueue(1, false), wakeChan: make(chan bool, 1)}
	client2 := &TranslClient{q: queue.NewPriorityQueue(1, false), wakeChan: make(chan bool, 1)}
	client1.addClientToSuperSubscription(SAMPLE_SUB)
	client2.addClientToSuperSubscription(SAMPLE_SUB)
	superSub := client1.superSub
	client1.leaveSuperSubscription()
	if _, ok := superSubs.subs[superSub]; !ok {
		t.Errorf("Super Subscription deleted from the global map: %v", superSubs.subs)
	}
	if len(superSub.clients) != 1 {
		t.Errorf("Super Subscription clients not updated correctly: want: length=1, got: length=%v", len(superSub.clients))
	}
	if superSub.primaryClient != client2 {
		t.Errorf("Super Subscription primary client not set correctly: want=%v, got=%v", client2, superSub.primaryClient)
	}
	client2.leaveSuperSubscription()
	if _, ok := superSubs.subs[superSub]; ok {
		t.Errorf("Super Subscription not deleted from the global map: %v", superSubs.subs)
	}
	if len(superSub.clients) != 0 {
		t.Errorf("Super Subscription clients not updated correctly: want: length=0, got: length=%v", len(superSub.clients))
	}
	client1.superSub = nil
	client1.leaveSuperSubscription()
}
func TestAddClientToSuperSubscription(t *testing.T) {
	t.Cleanup(clearSuperSubscriptions)
	client1 := &TranslClient{q: queue.NewPriorityQueue(1, false)}
	client2 := &TranslClient{q: queue.NewPriorityQueue(1, false)}
	client3 := &TranslClient{q: queue.NewPriorityQueue(1, false)}
	client1.addClientToSuperSubscription(SAMPLE_SUB)
	client2.addClientToSuperSubscription(SAMPLE_SUB)
	superSub := client1.superSub
	if client1.superSub == nil || len(superSub.clients) != 2 {
		t.Fatalf("superSub not created correctly: %v", client1.superSub)
	}
	if superSub.primaryClient != client1 {
		t.Errorf("superSub primary client not set correctly: want=%v, got=%v", client1, superSub.primaryClient)
	}
	if client2.superSub == nil {
		t.Errorf("superSub not created correctly: %v", client2.superSub)
	}
	if client2.superSub.primaryClient != client1 {
		t.Errorf("superSub primary client not set correctly: want=%v, got=%v", client1, client2.superSub.primaryClient)
	}
	client3.addClientToSuperSubscription(nil)
	if client3.superSub != nil {
		t.Errorf("superSub created incorrectly: %v", client3.superSub)
	}
}
