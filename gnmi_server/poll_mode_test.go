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
