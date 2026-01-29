package gnmi

// Tests for CHASSIS_STATE_DB target
// Covers: Poll mode, Sample mode, and Get operations for DPU_STATE table

import (
	"crypto/tls"
	"encoding/json"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	"github.com/kylelemons/godebug/pretty"
	"github.com/openconfig/gnmi/client"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

// prepareChassisStateDb loads DPU_STATE test data into CHASSIS_STATE_DB (db 13)
func prepareChassisStateDb(t *testing.T, namespace string) {
	rclient := getRedisClientN(t, 13, namespace)
	defer rclient.Close()
	rclient.FlushDB()

	fileName := "../testdata/DPU_STATE.txt"
	dpuStateBytes, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	dpuStateData := loadConfig(t, "", dpuStateBytes)
	loadDB(t, rclient, dpuStateData)
}

// TestChassisStateDBPoll tests Poll mode subscription for CHASSIS_STATE_DB DPU_STATE
func TestChassisStateDBPoll(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareChassisStateDb(t, ns)

	// Load expected JSON for comparison
	fileName := "../testdata/DPU_STATE_MAP.txt"
	dpuStateBytes, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var dpuStateJson interface{}
	json.Unmarshal(dpuStateBytes, &dpuStateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "poll DPU_STATE table",
			poll: 3,
			q: client.Query{
				Target:  "CHASSIS_STATE_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"DPU_STATE"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			// Initial Subscribe returns Update+Sync, then each Poll() returns Update+Sync
			// Total: Connected + (1 initial + 3 polls) × (Update, Sync) = 1 + 8 = 9 notifications
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"CHASSIS_STATE_DB", "DPU_STATE"}, TS: time.Unix(0, 200), Val: dpuStateJson},
				client.Sync{},
				client.Update{Path: []string{"CHASSIS_STATE_DB", "DPU_STATE"}, TS: time.Unix(0, 200), Val: dpuStateJson},
				client.Sync{},
				client.Update{Path: []string{"CHASSIS_STATE_DB", "DPU_STATE"}, TS: time.Unix(0, 200), Val: dpuStateJson},
				client.Sync{},
				client.Update{Path: []string{"CHASSIS_STATE_DB", "DPU_STATE"}, TS: time.Unix(0, 200), Val: dpuStateJson},
				client.Sync{},
			},
		},
		{
			desc: "poll specific DPU_STATE key (DPU0)",
			poll: 2,
			q: client.Query{
				Target:  "CHASSIS_STATE_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"DPU_STATE", "DPU0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			// Initial Subscribe returns Update+Sync, then each Poll() returns Update+Sync
			// Total: Connected + (1 initial + 2 polls) × (Update, Sync) = 1 + 6 = 7 notifications
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"CHASSIS_STATE_DB", "DPU_STATE", "DPU0"}, TS: time.Unix(0, 200), Val: dpuStateJson.(map[string]interface{})["DPU0"]},
				client.Sync{},
				client.Update{Path: []string{"CHASSIS_STATE_DB", "DPU_STATE", "DPU0"}, TS: time.Unix(0, 200), Val: dpuStateJson.(map[string]interface{})["DPU0"]},
				client.Sync{},
				client.Update{Path: []string{"CHASSIS_STATE_DB", "DPU_STATE", "DPU0"}, TS: time.Unix(0, 200), Val: dpuStateJson.(map[string]interface{})["DPU0"]},
				client.Sync{},
			},
		},
	}

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				} else {
					gotNoti = append(gotNoti, n)
				}
				mutexGotNoti.Unlock()
				return nil
			}

			wg := new(sync.WaitGroup)
			wg.Add(1)

			go func() {
				defer wg.Done()
				if err := c.Subscribe(context.Background(), q); err != nil {
					t.Errorf("c.Subscribe(): got error %v, expected nil", err)
				}
			}()

			wg.Wait()

			for i := 0; i < tt.poll; i++ {
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()

			if len(gotNoti) == 0 {
				t.Errorf("Expected non zero length of notifications")
			}

			if diff := pretty.Compare(tt.wantNoti, gotNoti); diff != "" {
				t.Log("\n Want: \n", tt.wantNoti)
				t.Log("\n Got : \n", gotNoti)
				t.Errorf("unexpected updates:\n%s", diff)
			}

			mutexGotNoti.Unlock()

			c.Close()
		})
	}
}

// TestChassisStateDBSample tests Sample mode subscription for CHASSIS_STATE_DB DPU_STATE
func TestChassisStateDBSample(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareChassisStateDb(t, ns)

	// Load expected JSON for comparisons
	fileName := "../testdata/DPU_STATE_MAP.txt"
	dpuStateBytes, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var dpuStateJson interface{}
	json.Unmarshal(dpuStateBytes, &dpuStateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
	}{
		{
			desc: "sample DPU_STATE table",
			q: createQueryOrFail(t,
				pb.SubscriptionList_STREAM,
				"CHASSIS_STATE_DB",
				[]subscriptionQuery{
					{
						Query:          []string{"DPU_STATE"},
						SubMode:        pb.SubscriptionMode_SAMPLE,
						SampleInterval: 0,
					},
				},
				false),
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"CHASSIS_STATE_DB", "DPU_STATE"}, TS: time.Unix(0, 200), Val: dpuStateJson},
				client.Sync{},
			},
		},
	}

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				} else {
					gotNoti = append(gotNoti, n)
				}
				mutexGotNoti.Unlock()
				return nil
			}

			go func() {
				// Subscribe blocks until connection closes for streaming subscriptions
				// Error on connection close is expected, not a failure
				c.Subscribe(context.Background(), q)
			}()

			// Wait for initial sync and first sample
			time.Sleep(time.Millisecond * 1000)

			mutexGotNoti.Lock()

			if len(gotNoti) == 0 {
				t.Errorf("Expected non zero length of notifications")
			}

			if diff := pretty.Compare(tt.wantNoti, gotNoti); diff != "" {
				t.Log("\n Want: \n", tt.wantNoti)
				t.Log("\n Got : \n", gotNoti)
				t.Errorf("unexpected updates:\n%s", diff)
			}

			mutexGotNoti.Unlock()

			c.Close()
		})
	}
}

// TestChassisStateDBGet tests gNMI Get for CHASSIS_STATE_DB DPU_STATE
func TestChassisStateDBGet(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareChassisStateDb(t, ns)

	// Load expected JSON for comparison (MAP version has keys without table prefix)
	fileName := "../testdata/DPU_STATE_MAP.txt"
	dpuStateBytes, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	// Load expected JSON for single DPU0 key
	dpu0FileName := "../testdata/DPU_STATE_DPU0.txt"
	dpu0StateBytes, err := ioutil.ReadFile(dpu0FileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", dpu0FileName, err)
	}

	// Create gRPC client
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	conn, err := grpc.Dial("127.0.0.1:8081", opts...)
	if err != nil {
		t.Fatalf("Dialing to 127.0.0.1:8081 failed: %v", err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx := context.Background()

	tests := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
	}{
		{
			desc:        "Get DPU_STATE table",
			pathTarget:  "CHASSIS_STATE_DB",
			textPbPath:  `elem: <name: "DPU_STATE" >`,
			wantRetCode: codes.OK,
			wantRespVal: dpuStateBytes,
			valTest:     true,
		},
		{
			desc:        "Get specific DPU_STATE key (DPU0)",
			pathTarget:  "CHASSIS_STATE_DB",
			textPbPath:  `elem: <name: "DPU_STATE" > elem: <name: "DPU0" >`,
			wantRetCode: codes.OK,
			wantRespVal: dpu0StateBytes,
			valTest:     true,
		},
		{
			desc:        "Get non-existent DPU_STATE key",
			pathTarget:  "CHASSIS_STATE_DB",
			textPbPath:  `elem: <name: "DPU_STATE" > elem: <name: "DPU99" >`,
			wantRetCode: codes.NotFound,
			wantRespVal: nil,
			valTest:     false,
		},
	}

	for _, td := range tests {
		t.Run(td.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.valTest)
		})
	}
}
