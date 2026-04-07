package gnmi

// server_test covers gNMI get, subscribe (stream and poll) test
// Prerequisite: redis-server should be running.
import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io/ioutil"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kylelemons/godebug/pretty"
	"github.com/openconfig/gnmi/client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func TestPollMissingTableThenTableKey(t *testing.T) {
	// Test that 1) missing table 2)table + key should just send sync responses and rpc connection should be alive
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE",
			poll: 3,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
		{
			desc: "query ROUTE_TABLE:0.0.0.0/0",
			poll: 3,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
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

func TestPollMissingTableAndTableKey(t *testing.T) {
	// Test that missing table and table + key should just send sync responses and rpc connection should be alive
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE + ROUTE_TABLE:0.0.0.0/0",
			poll: 3,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}, {"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
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

func TestPollMissingTableThenAdded(t *testing.T) {
	// Test that missing table should just send sync responses and rpc connection should be alive
	// When we add data for Table, we should receive update notifications
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/LLDP_ENTRY_TABLE_UPDATE.txt"
	lldpEntryTableUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var lldpEntryTableUpdateJson interface{}
	json.Unmarshal(lldpEntryTableUpdateByte, &lldpEntryTableUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())

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
				if i == 2 { // After first 2 polls, add data
					rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
					rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")
					// Sleep just one second to allow redis data to be entered
					time.Sleep(time.Millisecond * 1000)
				}
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

func TestPollMissingKeyThenAdded(t *testing.T) {
	// Test that missing table+key should just send sync responses and rpc connection should be alive
	// When we add data for table+key, we should receive update notifications
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/ROUTE_TABLE_DEFAULT_ROUTE_UPDATE.txt"
	routeTableDefaultRouteUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var routeTableDefaultRouteUpdateJson interface{}
	json.Unmarshal(routeTableDefaultRouteUpdateByte, &routeTableDefaultRouteUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query ROUTE_TABLE/0.0.0.0/0",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())

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
				if i == 2 { // After first 2 polls, add data
					rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
					rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")
					// Sleep just one second to allow redis data to be entered
					time.Sleep(time.Millisecond * 1000)
				}
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

func TestPollMissingTableAndKeyThenAdded(t *testing.T) {
	// Test that we get not updates from missing table and table key queried but still get sync responses
	// After adding back, we will get both updates
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/ROUTE_TABLE_DEFAULT_ROUTE_UPDATE.txt"
	routeTableDefaultRouteUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var routeTableDefaultRouteUpdateJson interface{}
	json.Unmarshal(routeTableDefaultRouteUpdateByte, &routeTableDefaultRouteUpdateJson)

	fileName = "../testdata/LLDP_ENTRY_TABLE_UPDATE.txt"
	lldpEntryTableUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var lldpEntryTableUpdateJson interface{}
	json.Unmarshal(lldpEntryTableUpdateByte, &lldpEntryTableUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE + ROUTE_TABLE/0.0.0.0/0",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}, {"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())

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
				if i == 2 { // After first 2 polls, add data
					rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
					rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")
					rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
					rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")
					// Sleep just one second to allow redis data to be entered
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()

			if len(gotNoti) == 0 || len(gotNoti) != len(tt.wantNoti) {
				t.Errorf("Expected non zero length of notifications or equal notifications")
			}

			// check that the connected and sync messages are identical
			for i := 0; i < 4; i++ {
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) {
					t.Fatalf("notification %d mismatch:\n got  %#v\n want %#v", i, gotNoti[i], tt.wantNoti[i])
				}
			}

			// Check that both notifications are coming at every poll interval
			for _, pair := range [][2]int{{4, 5}, {7, 8}, {10, 11}} { // these indexes are our update notifications
				i, j := pair[0], pair[1]
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) || !reflect.DeepEqual(gotNoti[j], tt.wantNoti[j]) {
					if !reflect.DeepEqual(gotNoti[j], tt.wantNoti[i]) && !reflect.DeepEqual(gotNoti[i], tt.wantNoti[j]) {
						t.Fatalf("mismatch at indices %d/%d:\n got  (%#v, %#v)\n want (%#v, %#v)", i, j, gotNoti[i], gotNoti[j], tt.wantNoti[i], tt.wantNoti[j])
					}
				}
			}

			mutexGotNoti.Unlock()

			c.Close()
		})
	}

}

func TestPollPresentTableMissingTableKey(t *testing.T) {
	// Test that we receive update notification for table query and no data for missing key
	// After 2 polls, we will add back the missing key data to get both data in our notifications
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/ROUTE_TABLE_DEFAULT_ROUTE_UPDATE.txt"
	routeTableDefaultRouteUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var routeTableDefaultRouteUpdateJson interface{}
	json.Unmarshal(routeTableDefaultRouteUpdateByte, &routeTableDefaultRouteUpdateJson)

	fileName = "../testdata/LLDP_ENTRY_TABLE_UPDATE.txt"
	lldpEntryTableUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var lldpEntryTableUpdateJson interface{}
	json.Unmarshal(lldpEntryTableUpdateByte, &lldpEntryTableUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE + ROUTE_TABLE/0.0.0.0/0",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}, {"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")

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
				if i == 2 { // After first 2 polls, add data
					rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
					rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")
					// Sleep just one second to allow redis data to be entered
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()

			if len(gotNoti) == 0 || len(gotNoti) != len(tt.wantNoti) {
				t.Errorf("Expected non zero length of notifications or equal notifications")
			}

			// check that the connected and sync messages and table update notifications are identical
			for i := 0; i < 7; i++ {
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) {
					t.Fatalf("notification %d mismatch:\n got  %#v\n want %#v", i, gotNoti[i], tt.wantNoti[i])
				}
			}

			// Check that both notifications are coming at every poll interval
			for _, pair := range [][2]int{{7, 8}, {10, 11}, {13, 14}} { // these indexes are our update notifications
				i, j := pair[0], pair[1]
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) || !reflect.DeepEqual(gotNoti[j], tt.wantNoti[j]) {
					if !reflect.DeepEqual(gotNoti[j], tt.wantNoti[i]) && !reflect.DeepEqual(gotNoti[i], tt.wantNoti[j]) {
						t.Fatalf("mismatch at indices %d/%d:\n got  (%#v, %#v)\n want (%#v, %#v)", i, j, gotNoti[i], gotNoti[j], tt.wantNoti[i], tt.wantNoti[j])
					}
				}
			}

			mutexGotNoti.Unlock()

			c.Close()
		})
	}
}

func TestPollPresentTableKeyMissingTable(t *testing.T) {
	// Test that we receive update notification for table key query and no data for missing table
	// After 2 polls, we will add back the missing table data to get both data in our notifications
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/ROUTE_TABLE_DEFAULT_ROUTE_UPDATE.txt"
	routeTableDefaultRouteUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var routeTableDefaultRouteUpdateJson interface{}
	json.Unmarshal(routeTableDefaultRouteUpdateByte, &routeTableDefaultRouteUpdateJson)

	fileName = "../testdata/LLDP_ENTRY_TABLE_UPDATE.txt"
	lldpEntryTableUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var lldpEntryTableUpdateJson interface{}
	json.Unmarshal(lldpEntryTableUpdateByte, &lldpEntryTableUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE + ROUTE_TABLE/0.0.0.0/0",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}, {"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")

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
				if i == 2 { // After first 2 polls, add data
					rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
					rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")
					// Sleep just one second to allow redis data to be entered
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()

			if len(gotNoti) == 0 || len(gotNoti) != len(tt.wantNoti) {
				t.Errorf("Expected non zero length of notifications or equal notifications")
			}

			// check that the connected and sync messages and table key update notifications are identical
			for i := 0; i < 7; i++ {
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) {
					t.Fatalf("notification %d mismatch:\n got  %#v\n want %#v", i, gotNoti[i], tt.wantNoti[i])
				}
			}

			// Check that both notifications are coming at every poll interval
			for _, pair := range [][2]int{{7, 8}, {10, 11}, {13, 14}} { // these indexes are our update notifications
				i, j := pair[0], pair[1]
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) || !reflect.DeepEqual(gotNoti[j], tt.wantNoti[j]) {
					if !reflect.DeepEqual(gotNoti[j], tt.wantNoti[i]) && !reflect.DeepEqual(gotNoti[i], tt.wantNoti[j]) {
						t.Fatalf("mismatch at indices %d/%d:\n got  (%#v, %#v)\n want (%#v, %#v)", i, j, gotNoti[i], gotNoti[j], tt.wantNoti[i], tt.wantNoti[j])
					}
				}
			}

			mutexGotNoti.Unlock()

			c.Close()
		})
	}
}

func TestPollTableDeleted(t *testing.T) {
	// Test that we received update notifications for existing table data, then delete table, we should receive 1 delete notification
	// After delete notification, we should only see sync responses
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/LLDP_ENTRY_TABLE_UPDATE.txt"
	lldpEntryTableUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var lldpEntryTableUpdateJson interface{}
	json.Unmarshal(lldpEntryTableUpdateByte, &lldpEntryTableUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Delete{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 { // After first 2 polls, del data
					rclient.FlushDB(context.Background())
					// Sleep just one second to allow redis data to be deleted
					time.Sleep(time.Millisecond * 1000)
				}
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

func TestPollTableFieldDeleted(t *testing.T) {
	// Test that we received update notifications for existing table field data, then delete table field, we should receive 1 delete notification
	// After delete notification, we should only see sync responses
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_LOC_CHASSIS + lldp_loc_sys_name",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_LOC_CHASSIS", "lldp_loc_sys_name"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"APPL_DB", "LLDP_LOC_CHASSIS", "lldp_loc_sys_name"}, TS: time.Unix(0, 200), Val: "dummy"},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_LOC_CHASSIS", "lldp_loc_sys_name"}, TS: time.Unix(0, 200), Val: "dummy"},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_LOC_CHASSIS", "lldp_loc_sys_name"}, TS: time.Unix(0, 200), Val: "dummy"},
				client.Sync{},
				client.Delete{Path: []string{"APPL_DB", "LLDP_LOC_CHASSIS", "lldp_loc_sys_name"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "LLDP_LOC_CHASSIS", "lldp_loc_sys_name", "dummy")

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 { // After first 2 polls, del data
					rclient.FlushDB(context.Background())
					// Sleep just one second to allow redis data to be deleted
					time.Sleep(time.Millisecond * 1000)
				}
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

func TestPollTableKeyDeleted(t *testing.T) {
	// Test that we received update notifications for existing table key data, then delete table key, we should receive 1 delete notification
	// After delete notification, we should only see sync responses
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/ROUTE_TABLE_DEFAULT_ROUTE_UPDATE.txt"
	routeTableDefaultRouteUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var routeTableDefaultRouteUpdateJson interface{}
	json.Unmarshal(routeTableDefaultRouteUpdateByte, &routeTableDefaultRouteUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query ROUTE_TABLE/0.0.0.0/0",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Delete{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 { // After first 2 polls, del data
					rclient.FlushDB(context.Background())
					// Sleep just one second to allow redis data to be deleted
					time.Sleep(time.Millisecond * 1000)
				}
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

func TestPollTableAndTableKeyBothDeleted(t *testing.T) {
	// Test that we received update notifications for existing data, then delete both table and table key, we should receive 2 delete notifications
	// After delete notifications, we should only see sync responses
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/ROUTE_TABLE_DEFAULT_ROUTE_UPDATE.txt"
	routeTableDefaultRouteUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var routeTableDefaultRouteUpdateJson interface{}
	json.Unmarshal(routeTableDefaultRouteUpdateByte, &routeTableDefaultRouteUpdateJson)

	fileName = "../testdata/LLDP_ENTRY_TABLE_UPDATE.txt"
	lldpEntryTableUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var lldpEntryTableUpdateJson interface{}
	json.Unmarshal(lldpEntryTableUpdateByte, &lldpEntryTableUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE + ROUTE_TABLE/0.0.0.0/0",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}, {"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Delete{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200)},
				client.Delete{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 { // After first 2 polls, delete data
					rclient.FlushDB(context.Background())
					// Sleep just one second to allow redis data to be deleted
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()

			if len(gotNoti) == 0 || len(gotNoti) != len(tt.wantNoti) {
				t.Errorf("Expected non zero length of notifications or equal notifications")
			}

			// Check that both notifications are coming at every poll interval
			for _, pair := range [][2]int{{1, 2}, {4, 5}, {7, 8}, {10, 11}} { // these indexes are our update notifications
				i, j := pair[0], pair[1]
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) || !reflect.DeepEqual(gotNoti[j], tt.wantNoti[j]) {
					if !reflect.DeepEqual(gotNoti[j], tt.wantNoti[i]) && !reflect.DeepEqual(gotNoti[i], tt.wantNoti[j]) {
						t.Fatalf("mismatch at indices %d/%d:\n got  (%#v, %#v)\n want (%#v, %#v)", i, j, gotNoti[i], gotNoti[j], tt.wantNoti[i], tt.wantNoti[j])
					}
				}
			}

			// check that the sync messages are identical
			for i := 12; i < 15; i++ {
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) {
					t.Fatalf("notification %d mismatch:\n got  %#v\n want %#v", i, gotNoti[i], tt.wantNoti[i])
				}
			}

			mutexGotNoti.Unlock()

			c.Close()
		})
	}
}

func TestPollTableAndTableKeyTableDeleted(t *testing.T) {
	// Test that we receive update notifications for existing data, and then when we delete table we should receive delete notification
	// After delete notification, we should see sync responses and continued update for existing data
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/ROUTE_TABLE_DEFAULT_ROUTE_UPDATE.txt"
	routeTableDefaultRouteUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var routeTableDefaultRouteUpdateJson interface{}
	json.Unmarshal(routeTableDefaultRouteUpdateByte, &routeTableDefaultRouteUpdateJson)

	fileName = "../testdata/LLDP_ENTRY_TABLE_UPDATE.txt"
	lldpEntryTableUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var lldpEntryTableUpdateJson interface{}
	json.Unmarshal(lldpEntryTableUpdateByte, &lldpEntryTableUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE + ROUTE_TABLE/0.0.0.0/0",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}, {"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Delete{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200)},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 { // After first 2 polls, delete data
					rclient.Del(context.Background(), "LLDP_ENTRY_TABLE:eth0")
					// Sleep just one second to allow redis data to be deleted
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()

			if len(gotNoti) == 0 || len(gotNoti) != len(tt.wantNoti) {
				t.Errorf("Expected non zero length of notifications or equal notifications")
			}

			// Check that both notifications are coming at every poll interval
			for _, pair := range [][2]int{{1, 2}, {4, 5}, {7, 8}, {10, 11}} { // these indexes are our update notifications
				i, j := pair[0], pair[1]
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) || !reflect.DeepEqual(gotNoti[j], tt.wantNoti[j]) {
					if !reflect.DeepEqual(gotNoti[j], tt.wantNoti[i]) && !reflect.DeepEqual(gotNoti[i], tt.wantNoti[j]) {
						t.Fatalf("mismatch at indices %d/%d:\n got  (%#v, %#v)\n want (%#v, %#v)", i, j, gotNoti[i], gotNoti[j], tt.wantNoti[i], tt.wantNoti[j])
					}
				}
			}

			// check that the sync messages are identical
			for i := 13; i < 17; i++ {
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) {
					t.Fatalf("notification %d mismatch:\n got  %#v\n want %#v", i, gotNoti[i], tt.wantNoti[i])
				}
			}

			mutexGotNoti.Unlock()

			c.Close()
		})
	}
}

func TestPollTableAndTableKeyTableKeyDeleted(t *testing.T) {
	// Test that we receive update notifications for existing data, and then when we delete table key we should receive delete notification
	// After delete notification, we should see sync responses and continued update for existing data
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/ROUTE_TABLE_DEFAULT_ROUTE_UPDATE.txt"
	routeTableDefaultRouteUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var routeTableDefaultRouteUpdateJson interface{}
	json.Unmarshal(routeTableDefaultRouteUpdateByte, &routeTableDefaultRouteUpdateJson)

	fileName = "../testdata/LLDP_ENTRY_TABLE_UPDATE.txt"
	lldpEntryTableUpdateByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var lldpEntryTableUpdateJson interface{}
	json.Unmarshal(lldpEntryTableUpdateByte, &lldpEntryTableUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query LLDP_ENTRY_TABLE + ROUTE_TABLE/0.0.0.0/0",
			poll: 5,
			q: client.Query{
				Target:  "APPL_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"LLDP_ENTRY_TABLE"}, {"ROUTE_TABLE", "0.0.0.0/0"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Update{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200), Val: routeTableDefaultRouteUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Delete{Path: []string{"APPL_DB", "ROUTE_TABLE", "0.0.0.0/0"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"APPL_DB", "LLDP_ENTRY_TABLE"}, TS: time.Unix(0, 200), Val: lldpEntryTableUpdateJson},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
	rclient.HSet(context.Background(), "LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
	rclient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 { // After first 2 polls, delete data
					rclient.Del(context.Background(), "ROUTE_TABLE:0.0.0.0/0")
					// Sleep just one second to allow redis data to be deleted
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()

			if len(gotNoti) == 0 || len(gotNoti) != len(tt.wantNoti) {
				t.Errorf("Expected non zero length of notifications or equal notifications")
			}

			// Check that both notifications are coming at every poll interval
			for _, pair := range [][2]int{{1, 2}, {4, 5}, {7, 8}, {10, 11}} { // these indexes are our update notifications
				i, j := pair[0], pair[1]
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) || !reflect.DeepEqual(gotNoti[j], tt.wantNoti[j]) {
					if !reflect.DeepEqual(gotNoti[j], tt.wantNoti[i]) && !reflect.DeepEqual(gotNoti[i], tt.wantNoti[j]) {
						t.Fatalf("mismatch at indices %d/%d:\n got  (%#v, %#v)\n want (%#v, %#v)", i, j, gotNoti[i], gotNoti[j], tt.wantNoti[i], tt.wantNoti[j])
					}
				}
			}

			// check that the sync messages are identical
			for i := 13; i < 17; i++ {
				if !reflect.DeepEqual(gotNoti[i], tt.wantNoti[i]) {
					t.Fatalf("notification %d mismatch:\n got  %#v\n want %#v", i, gotNoti[i], tt.wantNoti[i])
				}
			}

			mutexGotNoti.Unlock()

			c.Close()
		})
	}
}

// ==========================================
// STATE_DB Poll Mode Tests
// ==========================================

func TestPollStateDBMissingKey(t *testing.T) {
	// Test that missing key in STATE_DB sends sync responses only, RPC stays open
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query STATE_DB NEIGH_STATE_TABLE non-existent key",
			poll: 3,
			q: client.Query{
				Target:  "STATE_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"NEIGH_STATE_TABLE", "10.0.0.57"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 6, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
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

func TestPollStateDBMissingTable(t *testing.T) {
	// Test that missing table in STATE_DB sends sync responses only, RPC stays open
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query STATE_DB NEIGH_STATE_TABLE missing table",
			poll: 3,
			q: client.Query{
				Target:  "STATE_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"NEIGH_STATE_TABLE"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 6, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
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

func TestPollStateDBMissingKeyThenAdded(t *testing.T) {
	// Test that missing key in STATE_DB sends sync, then updates appear when data is added
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	var neighStateKeyUpdateJson interface{}
	json.Unmarshal([]byte(`{"peerType":"e-BGP","state":"Established"}`), &neighStateKeyUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query STATE_DB NEIGH_STATE_TABLE key then add",
			poll: 5,
			q: client.Query{
				Target:  "STATE_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"NEIGH_STATE_TABLE", "10.0.0.57"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE", "10.0.0.57"}, TS: time.Unix(0, 200), Val: neighStateKeyUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE", "10.0.0.57"}, TS: time.Unix(0, 200), Val: neighStateKeyUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE", "10.0.0.57"}, TS: time.Unix(0, 200), Val: neighStateKeyUpdateJson},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 6, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
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
				if i == 2 {
					rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP")
					rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "state", "Established")
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()
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

func TestPollStateDBKeyDeleted(t *testing.T) {
	// Test that STATE_DB key deletion sends proper delete notification
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	var neighStateKeyUpdateJson interface{}
	json.Unmarshal([]byte(`{"peerType":"e-BGP","state":"Established"}`), &neighStateKeyUpdateJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query STATE_DB NEIGH_STATE_TABLE key then delete",
			poll: 5,
			q: client.Query{
				Target:  "STATE_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"NEIGH_STATE_TABLE", "10.0.0.57"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE", "10.0.0.57"}, TS: time.Unix(0, 200), Val: neighStateKeyUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE", "10.0.0.57"}, TS: time.Unix(0, 200), Val: neighStateKeyUpdateJson},
				client.Sync{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE", "10.0.0.57"}, TS: time.Unix(0, 200), Val: neighStateKeyUpdateJson},
				client.Sync{},
				client.Delete{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE", "10.0.0.57"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 6, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP")
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "state", "Established")
	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 {
					rclient.Del(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57")
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()
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

func TestPollStateDBTableDeleted(t *testing.T) {
	// Test that STATE_DB table deletion sends proper delete notification
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	fileName := "../testdata/NEIGH_STATE_TABLE_MAP.txt"
	neighStateTableMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var neighStateTableJson interface{}
	json.Unmarshal(neighStateTableMapByte, &neighStateTableJson)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query STATE_DB NEIGH_STATE_TABLE then delete all",
			poll: 5,
			q: client.Query{
				Target:  "STATE_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"NEIGH_STATE_TABLE"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200), Val: neighStateTableJson},
				client.Sync{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200), Val: neighStateTableJson},
				client.Sync{},
				client.Update{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200), Val: neighStateTableJson},
				client.Sync{},
				client.Delete{Path: []string{"STATE_DB", "NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Sync{},
				client.Sync{},
			},
		},
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 6, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())

	loadFileName := "../testdata/NEIGH_STATE_TABLE.txt"
	neighStateTableByte, err := ioutil.ReadFile(loadFileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", loadFileName, err)
	}
	neighData := loadConfig(t, "", neighStateTableByte)
	loadDB(t, rclient, neighData)

	var mutexGotNoti sync.Mutex

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 {
					rclient.FlushDB(context.Background())
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()
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

// ==========================================
// COUNTERS_DB Virtual Path Poll Mode Tests
// ==========================================

func TestPollCountersDBVirtualPathMissingKey(t *testing.T) {
	// Test that COUNTERS_DB virtual path with port in name map but no counter data sends sync only
	// Port name map has Ethernet68 -> oid:xxx but COUNTERS:oid:xxx doesn't exist
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	rclient := getRedisClientN(t, 2, ns)
	defer rclient.Close()

	// Delete counter data for Ethernet68 so it's "missing"
	rclient.Del(context.Background(), "COUNTERS:oid:0x1000000000039")

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query COUNTERS/Ethernet68 with missing counter data",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
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

func TestPollCountersDBNonVirtualMissingTable(t *testing.T) {
	// Test that missing non-virtual COUNTERS_DB table sends sync responses only
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 2, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query COUNTERS_DB COUNTERS_PORT_NAME_MAP missing",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
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

func TestPollCountersDBNonVirtualMissingThenAdded(t *testing.T) {
	// Test that missing non-virtual COUNTERS_DB table sends sync, then updates when data appears
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 2, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query COUNTERS_DB COUNTERS_PORT_NAME_MAP missing then added",
			poll: 5,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: map[string]interface{}{"Ethernet1": "oid:0x1000000000001"}},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: map[string]interface{}{"Ethernet1": "oid:0x1000000000001"}},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: map[string]interface{}{"Ethernet1": "oid:0x1000000000001"}},
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
				if i == 2 {
					rclient.HSet(context.Background(), "COUNTERS_PORT_NAME_MAP", "Ethernet1", "oid:0x1000000000001")
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()
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

func TestPollCountersDBKeyDeleted(t *testing.T) {
	// Test that COUNTERS_DB non-virtual table deletion sends delete notification
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 2, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())
	rclient.HSet(context.Background(), "COUNTERS_PORT_NAME_MAP", "Ethernet1", "oid:0x1000000000001")

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query COUNTERS_DB COUNTERS_PORT_NAME_MAP then delete",
			poll: 5,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: map[string]interface{}{"Ethernet1": "oid:0x1000000000001"}},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: map[string]interface{}{"Ethernet1": "oid:0x1000000000001"}},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: map[string]interface{}{"Ethernet1": "oid:0x1000000000001"}},
				client.Sync{},
				client.Delete{Path: []string{"COUNTERS_DB", "COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Sync{},
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
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 {
					rclient.FlushDB(context.Background())
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()
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

func TestPollCountersDBVirtualPathKeyDeleted(t *testing.T) {
	// Test that COUNTERS_DB virtual path data deletion sends delete notification
	// Port name map + counter data exist, then counter data is deleted
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	rclient := getRedisClientN(t, 2, ns)
	defer rclient.Close()

	fileName := "../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernet68Json interface{}
	json.Unmarshal(countersEthernet68Byte, &countersEthernet68Json)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query COUNTERS/Ethernet68 then delete counter data",
			poll: 5,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68Json},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68Json},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68Json},
				client.Sync{},
				client.Delete{Path: []string{"COUNTERS_DB", "COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200)},
				client.Sync{},
				client.Sync{},
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
				switch nn := n.(type) {
				case client.Connected, client.Sync:
					gotNoti = append(gotNoti, nn)
				case client.Delete:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				case client.Update:
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				default:
					t.Errorf("Unexpected Client Notification: %v", nn)
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
				if i == 2 {
					// Delete only the counter data, keep the port name map
					rclient.Del(context.Background(), "COUNTERS:oid:0x1000000000039")
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()
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

// ==========================================
// OTHERS (NonDbClient) Poll Mode Tests
// ==========================================

func TestPollOthersBasic(t *testing.T) {
	// Test that OTHERS target poll mode works - basic regression test
	// OTHERS uses NonDbClient with getter functions (e.g. /proc data), not Redis
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	tests := []struct {
		desc string
		q    client.Query
		poll int
	}{
		{
			desc: "poll OTHERS proc/uptime",
			poll: 3,
			q: client.Query{
				Target:  "OTHERS",
				Type:    client.Poll,
				Queries: []client.Path{{"proc", "uptime"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
		},
		{
			desc: "poll OTHERS proc/meminfo",
			poll: 3,
			q: client.Query{
				Target:  "OTHERS",
				Type:    client.Poll,
				Queries: []client.Path{{"proc", "meminfo"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
		},
		{
			desc: "poll OTHERS proc/loadavg",
			poll: 3,
			q: client.Query{
				Target:  "OTHERS",
				Type:    client.Poll,
				Queries: []client.Path{{"proc", "loadavg"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
		},
		{
			desc: "poll OTHERS proc/vmstat",
			poll: 3,
			q: client.Query{
				Target:  "OTHERS",
				Type:    client.Poll,
				Queries: []client.Path{{"proc", "vmstat"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
		},
		{
			desc: "poll OTHERS proc/stat",
			poll: 3,
			q: client.Query{
				Target:  "OTHERS",
				Type:    client.Poll,
				Queries: []client.Path{{"proc", "stat"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
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
			var gotSyncs int
			var gotUpdates int

			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				gotNoti = append(gotNoti, n)
				switch n.(type) {
				case client.Sync:
					gotSyncs++
				case client.Update:
					gotUpdates++
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
			// OTHERS should return Connected + (Update + Sync) for initial + each poll
			// Verify we got syncs and updates (data may vary by system)
			if gotSyncs < tt.poll {
				t.Errorf("Expected at least %d sync responses, got %d", tt.poll, gotSyncs)
			}
			if gotUpdates < 1 {
				t.Errorf("Expected at least 1 update, got %d", gotUpdates)
			}
			mutexGotNoti.Unlock()

			c.Close()
		})
	}
}

func TestPollCountersDBWildcardEthernetMissingThenPartialAdded(t *testing.T) {
	// Test that COUNTERS/Ethernet* with port name map but no counter data sends sync only,
	// then when counter data for one port (Ethernet68) is added, updates are sent
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	rclient := getRedisClientN(t, 2, ns)
	defer rclient.Close()

	// Delete all counter data but keep name maps
	rclient.Del(context.Background(), "COUNTERS:oid:0x1000000000039")
	rclient.Del(context.Background(), "COUNTERS:oid:0x1000000000003")
	rclient.Del(context.Background(), "COUNTERS:oid:0x1500000000092a")
	rclient.Del(context.Background(), "COUNTERS:oid:0x1500000000091c")

	fileName := "../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernet68Fields interface{}
	json.Unmarshal(countersEthernet68Byte, &countersEthernet68Fields)
	wildcardResult := map[string]interface{}{
		"Ethernet68/1": countersEthernet68Fields,
	}

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		poll     int
	}{
		{
			desc: "query COUNTERS/Ethernet* no data then add Ethernet68",
			poll: 5,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Sync{},
				client.Sync{},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: wildcardResult},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: wildcardResult},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_DB", "COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: wildcardResult},
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
				if i == 2 {
					// Add counter data for just Ethernet68 (oid:0x1000000000039)
					mpi_counter := loadConfig(t, "COUNTERS:oid:0x1000000000039", countersEthernet68Byte)
					loadDB(t, rclient, mpi_counter)
					time.Sleep(time.Millisecond * 1000)
				}
				err := c.Poll()
				if err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			mutexGotNoti.Lock()
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
