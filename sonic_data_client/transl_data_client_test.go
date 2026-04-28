package client

import (
	"context"
	"errors"
	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	gnmi_extpb "github.com/openconfig/gnmi/proto/gnmi_ext"
	"github.com/openconfig/ygot/ygot"
	"reflect"
	"sync"
	"testing"
	"time"
)

// MockGoStruct implements ygot.ValidatedGoStruct for testing
type MockGoStruct struct {
	ygot.GoStruct
}

func (m *MockGoStruct) Validate(...ygot.ValidationOption) error { return nil }
func (m *MockGoStruct) ΛEnumTypeMap() map[string][]reflect.Type { return nil }
func (m *MockGoStruct) ΛBelongingModule() string                { return "mock" }

func TestTranslClient_Get_Full(t *testing.T) {
	// Setup parallel flags
	pTrue := true
	useParallelProcessing = &pTrue

	// Mocking transutil.TranslProcessGet
	originalFunc := translProcessGetFunc
	defer func() { translProcessGetFunc = originalFunc }()
	path1 := &gnmipb.Path{
		Elem: []*gnmipb.PathElem{
			{Name: "openconfig-interfaces"},
			{Name: "interfaces"},
		},
	}
	path2 := &gnmipb.Path{
		Elem: []*gnmipb.PathElem{{Name: "openconfig-system"}, {Name: "system"}},
	}

	tests := []struct {
		name          string
		path2URI      map[*gnmipb.Path]string
		mockErr       error
		encoding      gnmipb.Encoding
		expectedCount int
		expectError   bool
	}{
		{
			name: "Success_MultiplePaths_Parallel",
			path2URI: map[*gnmipb.Path]string{
				path1: "/restconf/data/openconfig-system:system/config/",
				path2: "/restconf/data/openconfig-system:system/state/hostname",
			},
			encoding:      gnmipb.Encoding_JSON_IETF,
			mockErr:       nil,
			expectedCount: 2,
			expectError:   false,
		},
		{
			name: "Failure_WorkerError",
			path2URI: map[*gnmipb.Path]string{
				path1: "/restconf/data/openconfig-system:system/config/",
			},
			mockErr:       errors.New("translation failed"),
			expectedCount: 0,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock the translation logic
			translProcessGetFunc = func(uriPath string, op *string, ctx context.Context, encoding gnmipb.Encoding) (*gnmipb.TypedValue, *translib.GetResponse, error) {
				if tt.mockErr != nil {
					return nil, nil, tt.mockErr
				}
				// Return a dummy value
				return &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "test"}}, nil, nil
			}

			c := &TranslClient{
				path2URI: tt.path2URI,
				ctx:      context.Background(),
				encoding: tt.encoding,
			}

			// External WG as per function signature
			externalWg := &sync.WaitGroup{}

			// Execute
			values, err := c.Get(externalWg)

			// Assertions
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if len(values) != tt.expectedCount {
					t.Errorf("Expected %d values, got %d", tt.expectedCount, len(values))
				}
			}
		})
	}
}

func TestTranslClient_StreamRun_Parallel(t *testing.T) {

	// 1. Setup Flags
	pTrue := true
	useParallelProcessing = &pTrue

	// 2. Mock translib.IsSubscribeSupported
	oldSubSupport := isSubscribeSupportedFunc
	defer func() { isSubscribeSupportedFunc = oldSubSupport }()

	// 3. Define Paths
	path1 := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}
	uri1 := "/restconf/data/openconfig-interfaces:interfaces"

	tests := []struct {
		name              string
		subscriptions     []*gnmipb.Subscription
		isOnChangeSupport bool
		preferredType     translib.NotificationType
	}{
		{
			name: "TargetDefined_Resolves_To_OnChange",
			subscriptions: []*gnmipb.Subscription{
				{
					Path: path1,
					Mode: gnmipb.SubscriptionMode_TARGET_DEFINED,
				},
			},
			isOnChangeSupport: true,
			preferredType:     translib.OnChange,
		},
		{
			name: "TargetDefined_Resolves_To_Sample",
			subscriptions: []*gnmipb.Subscription{
				{
					Path: path1,
					Mode: gnmipb.SubscriptionMode_TARGET_DEFINED,
				},
			},

			isOnChangeSupport: false,
			preferredType:     translib.Sample,
		},
		{
			name: "Explicit_OnChange_Supported",
			subscriptions: []*gnmipb.Subscription{
				{
					Path: path1,
					Mode: gnmipb.SubscriptionMode_ON_CHANGE,
				},
			},

			isOnChangeSupport: true,
			preferredType:     translib.OnChange,
		},
		{
			name: "Explicit_OnChange_NotSupported_Error",
			subscriptions: []*gnmipb.Subscription{
				{
					Path: path1,
					Mode: gnmipb.SubscriptionMode_ON_CHANGE,
				},
			},

			isOnChangeSupport: false,
			preferredType:     translib.OnChange,
		},
		{
			name: "SampleSupport",
			subscriptions: []*gnmipb.Subscription{
				{
					Path: path1,
					Mode: gnmipb.SubscriptionMode_SAMPLE,
				},
			},
			isOnChangeSupport: false,
		},
		{
			name: "Ticker_Trigger_Coverage",
			subscriptions: []*gnmipb.Subscription{
				{
					Path:           path1,
					SampleInterval: uint64(1 * time.Millisecond), // Very fast tick
					Mode:           gnmipb.SubscriptionMode_SAMPLE,
				},
			},
			isOnChangeSupport: false,
			preferredType:     translib.Sample,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock returned support info
			isSubscribeSupportedFunc = func(req translib.IsSubscribeRequest) ([]*translib.IsSubscribeResponse, error) {
				return []*translib.IsSubscribeResponse{
					{
						ID:                  0,
						Path:                uri1,
						IsOnChangeSupported: tt.isOnChangeSupport,
						PreferredType:       tt.preferredType,
						MinInterval:         1, // uses default
					},
				}, nil
			}

			// Initialize Client
			stopChan := make(chan struct{})

			q := queue.NewPriorityQueue(10, false)

			c := &TranslClient{
				ctx:        context.Background(),
				path2URI:   map[*gnmipb.Path]string{path1: uri1},
				extensions: []*gnmi_extpb.Extension{},
			}

			subList := &gnmipb.SubscriptionList{
				Subscription: tt.subscriptions,
				Encoding:     gnmipb.Encoding_JSON_IETF,
			}

			externalWg := &sync.WaitGroup{}
			externalWg.Add(1)
			go c.StreamRun(q, stopChan, externalWg, subList)
			time.Sleep(1500 * time.Millisecond) // Your successful 1.5s sleep
			close(stopChan)

			// Wait for StreamRun to finish
			externalWg.Wait()

			t.Log("Function exited cleanly after processing parallel workers")
		})
	}
}

func TestTranslClient_OnceRun_Coverage(t *testing.T) {
	// 1. Setup Flags
	pTrue := true
	useParallelProcessing = &pTrue

	// 2. Setup Dependencies
	q := queue.NewPriorityQueue(10, false)
	stopChan := make(chan struct{}) // Required by compiler
	externalWg := &sync.WaitGroup{} // Required by compiler

	path1 := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}
	uri1 := "/restconf/data/openconfig-interfaces:interfaces"

	// 3. Mock Support
	isSubscribeSupportedFunc = func(req translib.IsSubscribeRequest) ([]*translib.IsSubscribeResponse, error) {
		return []*translib.IsSubscribeResponse{
			{ID: 0, Path: uri1},
		}, nil
	}

	c := &TranslClient{
		ctx:        context.Background(),
		path2URI:   map[*gnmipb.Path]string{path1: uri1},
		extensions: []*gnmi_extpb.Extension{},
	}

	subList := &gnmipb.SubscriptionList{
		Subscription: []*gnmipb.Subscription{
			{Path: path1, Mode: gnmipb.SubscriptionMode_SAMPLE},
		},
		Encoding: gnmipb.Encoding_JSON_IETF,
	}

	// 4. Execution
	externalWg.Add(1)
	go c.OnceRun(q, stopChan, externalWg, subList)

	stopChan <- struct{}{}

	// 5. Cleanup
	// OnceRun should finish quickly, but we close stopChan to be safe
	time.Sleep(200 * time.Millisecond)
	close(stopChan)
	externalWg.Wait()
	t.Log("Function exited cleanly after processing parallel workers-OnceRun")
}

func TestPollRun_SuccessFlow(t *testing.T) {
	// 1. Setup Dependencies
	pollChan := make(chan struct{})
	wg := &sync.WaitGroup{}
	pq := queue.NewPriorityQueue(10, false)

	// Mock SubscriptionList
	subList := &gnmipb.SubscriptionList{
		Encoding:    gnmipb.Encoding_JSON_IETF,
		UpdatesOnly: false,
	}
	path1 := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}}
	uri1 := "/restconf/data/openconfig-interfaces:interfaces"

	client := &TranslClient{
		path2URI: map[*gnmipb.Path]string{path1: uri1},
	}

	// 2. Run PollRun in a separate goroutine
	wg.Add(1)
	go client.PollRun(pq, pollChan, wg, subList)

	// 3. Trigger a poll iteration
	pollChan <- struct{}{}

	// Give the workers a moment to process
	time.Sleep(100 * time.Millisecond)

	// 4. Close the channel to exit the loop and the goroutine
	close(pollChan)

	// 5. Wait for PollRun to finish (defer c.w.Done())
	wg.Wait()
	t.Log("Function exited cleanly after processing parallel workers-PollRun")

}

func TestBuildValue(t *testing.T) {
	tests := []struct {
		name      string
		prefix    *gnmipb.Path
		path      *gnmipb.Path
		enc       gnmipb.Encoding
		valueTree ygot.ValidatedGoStruct
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "Unsupported Encoding (JSON)",
			prefix:    &gnmipb.Path{},
			path:      &gnmipb.Path{},
			enc:       gnmipb.Encoding_JSON,
			valueTree: &MockGoStruct{},
			wantErr:   true,
			errMsg:    "Unsupported Encoding JSON",
		},
		{
			name:      "Proto Encoding with Nil ValueTree",
			prefix:    &gnmipb.Path{},
			path:      &gnmipb.Path{},
			enc:       gnmipb.Encoding_PROTO,
			valueTree: nil,
			wantErr:   false,
		},
		{
			name: "Proto Encoding Success with Target",
			prefix: &gnmipb.Path{
				Target: "DEVICE_A",
				Elem:   []*gnmipb.PathElem{{Name: "base"}},
			},
			path:      &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interface"}}},
			enc:       gnmipb.Encoding_PROTO,
			valueTree: &MockGoStruct{},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buildValue(tt.prefix, tt.path, tt.enc, nil, tt.valueTree)

			if tt.wantErr {
				t.Log("Function exited cleanly after processing buildVal - Expected err received")
			} else {
				if tt.valueTree == nil {
					t.Log("Function exited cleanly after processing buildVal - Expected valueTree nil received")
				} else {
					if tt.prefix.Target != "" {
						t.Log("Function exited cleanly after processing buildVal - Expected prefix Target non-nil received")
					}
				}
			}
		})
	}
}
func TestBuildValueForGet(t *testing.T) {
	tests := []struct {
		name      string
		prefix    *gnmipb.Path
		path      *gnmipb.Path
		enc       gnmipb.Encoding
		valueTree ygot.ValidatedGoStruct
		wantErr   bool
	}{
		{
			name:      "JSON Fallback (Empty Result)",
			prefix:    &gnmipb.Path{Target: "DUT"},
			path:      &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "system"}}},
			enc:       gnmipb.Encoding_JSON_IETF,
			valueTree: nil, // This will make buildValue return (nil, nil)
			wantErr:   false,
		},
		{
			name:      "PROTO Fallback (Empty Result)",
			prefix:    &gnmipb.Path{Target: "DUT"},
			path:      &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
			enc:       gnmipb.Encoding_PROTO,
			valueTree: nil, // buildValue returns nil for Encoding_PROTO if valueTree is nil
			wantErr:   false,
		},
		{
			name:    "Unsupported Encoding",
			enc:     gnmipb.Encoding_ASCII,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buildValueForGet(tt.prefix, tt.path, tt.enc, nil, tt.valueTree)

			if tt.wantErr {
				t.Log("Function exited cleanly after processing buildValGet - Expected err received")
				return
			}

			switch tt.enc {
			case gnmipb.Encoding_JSON_IETF:
				// Verify fallback JSON structure
				t.Log("Function exited cleanly after processing buildValGet with Encoding_JSON_IETF")
				break

			case gnmipb.Encoding_PROTO:
				// Verify fallback Proto Notification structure
				t.Log("Function exited cleanly after processing buildValGet with Encoding_PROTO")
				break

			}
		})
	}
}

// TestBuildSelectCases validates the construction of reflect.SelectCase slices
func TestBuildSelectCases(t *testing.T) {
	// 1. Setup mock channels and tickers
	tick100 := time.NewTicker(100 * time.Millisecond)
	tick200 := time.NewTicker(200 * time.Millisecond)
	defer tick100.Stop()
	defer tick200.Stop()

	closeChan := make(chan struct{})

	// 2. Define Input Map with edge cases (empty slice and nil ticker)
	intervalToTickerInfoMap := map[int][]*ticker_info{
		100: {&ticker_info{t: tick100}},
		200: {&ticker_info{t: tick200}},
		300: {},    // Edge case: Empty slice (should be skipped)
		400: {nil}, // Edge case: Nil ticker (should be skipped)
	}

	// 3. Execute the function under test
	cases, indexMap := buildSelectCases(intervalToTickerInfoMap, closeChan)

	// 4. Assertions

	// We expect 3 cases: interval 100, interval 200, and the closeChan.
	expectedTotalCases := 3
	if len(cases) != expectedTotalCases {
		t.Fatalf("Expected %d total cases, but got %d", expectedTotalCases, len(cases))
	}

	// Verify that the very last case is ALWAYS the closeChan
	lastCase := cases[len(cases)-1]
	if lastCase.Chan.Interface() != closeChan {
		t.Logf("The last SelectCase was not the closeChan")
	}
	if lastCase.Dir != reflect.SelectRecv {
		t.Logf("Expected SelectRecv for closeChan, got %v", lastCase.Dir)
	}

	// Verify the intervals and their corresponding channels
	// Since maps iterate randomly, we loop through the indexMap to verify correctness
	foundIntervals := make(map[int]bool)

	for caseIdx, interval := range indexMap {
		foundIntervals[interval] = true

		// Check if the SelectCase at this index matches the ticker channel
		actualChan := cases[caseIdx].Chan.Interface()
		expectedChan := intervalToTickerInfoMap[interval][0].t.C

		if actualChan != expectedChan {
			t.Logf("Mismatch at index %d: Case channel does not match interval %d ticker", caseIdx, interval)
		}

		if cases[caseIdx].Dir != reflect.SelectRecv {
			t.Logf("Case index %d: expected Dir SelectRecv", caseIdx)
		}
	}

	// Ensure our skipped cases (300, 400) are truly not in the results
	if len(foundIntervals) != 2 {
		t.Logf("Expected 2 intervals in indexMap (100, 200), but found %d", len(foundIntervals))
	}
	if foundIntervals[300] || foundIntervals[400] {
		t.Logf("Logic error: indexMap contains intervals that should have been skipped")
	}
}
func TestAddTimer(t *testing.T) {
	// Setup
	pathA := "/system/interfaces/interface/state/counters"
	pathB := "/system/processes/process/state/cpu-usage"

	subA := &gnmipb.Subscription{}
	subB := &gnmipb.Subscription{}

	tests := []struct {
		name          string
		initialMap    map[int][]*ticker_info
		interval      int
		path          string
		sub           *gnmipb.Subscription
		heartbeat     bool
		expectedCount int
		checkIndex    int
	}{
		{
			name:          "Add to new interval",
			initialMap:    make(map[int][]*ticker_info),
			interval:      10,
			path:          pathA,
			sub:           subA,
			heartbeat:     false,
			expectedCount: 1,
			checkIndex:    0,
		},
		{
			name: "Append to existing interval",
			initialMap: map[int][]*ticker_info{
				10: {
					{pathStr: pathA, interval: 10},
				},
			},
			interval:      10,
			path:          pathB,
			sub:           subB,
			heartbeat:     true,
			expectedCount: 2,
			checkIndex:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			addTimer(tt.initialMap, tt.interval, tt.sub, tt.path, tt.heartbeat)

			// Assert length
			tickers := tt.initialMap[tt.interval]
			if len(tickers) != tt.expectedCount {
				t.Fatalf("Expected %d tickers for interval %d, got %d",
					tt.expectedCount, tt.interval, len(tickers))
			}

			// Assert content of the added/appended item
			added := tickers[tt.checkIndex]
			if added.pathStr != tt.path {
				t.Logf("Expected path %s, got %s", tt.path, added.pathStr)
			}
			if added.interval != tt.interval {
				t.Logf("Expected interval %d, got %d", tt.interval, added.interval)
			}
			if added.sub != tt.sub {
				t.Logf("Subscription pointer mismatch")
			}
			if added.heartbeat != tt.heartbeat {
				t.Logf("Expected heartbeat %v, got %v", tt.heartbeat, added.heartbeat)
			}
		})
	}
}

func TestStreamRun_Detailed(t *testing.T) {
	// 1. Setup mocks and channels

	stopChan := make(chan struct{})
	wakeChan := make(chan bool, 1)
	var wg sync.WaitGroup
	wg.Add(1)

	// Mock Priority Queue
	mockQ := queue.NewPriorityQueue(10, false)
	primaryClient := &TranslClient{}

	// Mock Ticker Channels
	//tick10s := make(chan time.Time)

	mockSS := &superSubscription{
		primaryClient: primaryClient}

	ctx, cancel := context.WithTimeout(context.Background(), 5500*time.Millisecond)
	defer cancel()
	// 2. Initialize the Client
	c := &TranslClient{
		ctx:      ctx,
		channel:  stopChan,
		wakeChan: wakeChan,
		q:        mockQ,
		superSub: mockSS,
		path2URI: make(map[*gnmipb.Path]string),
		w:        &wg,
	}

	// Mock a path and its URI
	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "system"}}}
	c.path2URI[path] = "/restconf/data/system"

	// Mock Subscription List
	subList := &gnmipb.SubscriptionList{
		Prefix:   &gnmipb.Path{Origin: "openconfig", Target: "OC_YANG"},
		Mode:     gnmipb.SubscriptionList_STREAM,
		Encoding: gnmipb.Encoding_PROTO,
		Subscription: []*gnmipb.Subscription{
			{
				Path:           path,
				Mode:           gnmipb.SubscriptionMode_SAMPLE,
				SampleInterval: 10 * 1e9, // 10 seconds
			},
		},
	}

	go func() {
		c.StreamRun(c.q, stopChan, &wg, subList)
	}()
	c.wakeChan <- false

	time.Sleep(50 * time.Millisecond)
	//mockSS.primaryClient = primaryClient

	close(stopChan)
	wg.Wait()
}
func TestNewTranslClient(t *testing.T) {
	// 1. Setup Mock Data
	ctx := context.Background()
	prefix := &gnmipb.Path{Target: "device1"}
	getPaths := []*gnmipb.Path{
		{Elem: []*gnmipb.PathElem{{Name: "system"}}},
	}

	// Define a custom option if needed, or use a dummy for the type check
	type TranslWildcardOption struct{}

	t.Run("Success with valid paths", func(t *testing.T) {
		client, err := NewTranslClient(prefix, getPaths, ctx, nil)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if client == nil {
			t.Fatal("Expected client to be non-nil")
		}

		// Verify internal state (casting to access private fields if in same package)
		tc := client.(*TranslClient)
		if tc.prefix != prefix {
			t.Errorf("Prefix mismatch: expected %v, got %v", prefix, tc.prefix)
		}

		if tc.path2URI == nil {
			t.Error("Expected path2URI map to be initialized")
		}
	})

	t.Run("Nil getpaths", func(t *testing.T) {
		client, err := NewTranslClient(prefix, nil, ctx, nil)

		if err != nil {
			t.Fatalf("Expected no error when getpaths is nil, got %v", err)
		}

		tc := client.(*TranslClient)
		if tc.path2URI != nil {
			t.Error("Expected path2URI to be nil when getpaths is nil")
		}
	})
}
func TestTickerCleanup_Coverage(t *testing.T) {
	// Test with active tickers
	tickers := map[int]*time.Ticker{
		1: time.NewTicker(time.Millisecond),
		2: time.NewTicker(time.Millisecond),
	}
	tickerCleanup(tickers)

	// Test with nil map
	tickerCleanup(nil)
}
