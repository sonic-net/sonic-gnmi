package gnmi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openconfig/gnmi/client"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

func createRecordsQuery(t *testing.T, mode pb.SubscriptionList_Mode, paths ...string) client.Query {
	t.Helper()
	return createQueryOrFail(t,
		mode,
		"RECORDS",
		[]subscriptionQuery{
			{
				Query:   paths,
				SubMode: pb.SubscriptionMode_ON_CHANGE,
			},
		},
		false)
}

func TestRecordsSubscribeStreamSample(t *testing.T) {
	dir := t.TempDir()
	prev := sdc.RecordsDir()
	sdc.SetRecordsDir(dir)
	t.Cleanup(func() { sdc.SetRecordsDir(prev) })

	s := createServer(t, 18081)
	go runServer(t, s)
	defer s.ForceStop()

	q := createRecordsQuery(t, pb.SubscriptionList_STREAM,
		"RECORDS", "localhost", "APPL_DB", "ROUTE_TABLE", "10.1.0.0", "24")
	q.Addrs = []string{"127.0.0.1:18081"}

	var (
		mu      sync.Mutex
		gotVals []interface{}
		gotSync bool
		subErr  error
		subDone = make(chan struct{})
	)

	c := client.New()
	defer c.Close()
	q.NotificationHandler = func(n client.Notification) error {
		mu.Lock()
		defer mu.Unlock()
		switch v := n.(type) {
		case client.Update:
			gotVals = append(gotVals, v.Val)
		case client.Sync:
			gotSync = true
		}
		return nil
	}

	go func() {
		subErr = c.Subscribe(context.Background(), q)
		close(subDone)
	}()

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		ready := gotSync
		mu.Unlock()
		if ready {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Fatalf("timeout waiting for sync; updates=%d sync=%v subErr=%v", len(gotVals), gotSync, subErr)
			mu.Unlock()
		case <-time.After(50 * time.Millisecond):
		}
	}

	stamp := time.Now().Format("2006-01-02.15:04:05.000000")
	line := stamp + "|ROUTE_TABLE:10.1.0.0/24|SET|nexthop:10.0.0.1|ifname:Ethernet0\n"
	if err := os.WriteFile(filepath.Join(dir, "swss.rec"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline = time.After(5 * time.Second)
	for {
		mu.Lock()
		ready := len(gotVals) >= 1
		mu.Unlock()
		if ready {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Fatalf("timeout waiting for live record; updates=%d sync=%v subErr=%v", len(gotVals), gotSync, subErr)
			mu.Unlock()
		case <-time.After(50 * time.Millisecond):
		}
	}

	c.Close()
	select {
	case <-subDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not return after Close")
	}

	mu.Lock()
	defer mu.Unlock()
	var record map[string]interface{}
	for _, v := range gotVals {
		payload, err := asJSONObject(v)
		if err != nil {
			continue
		}
		if _, isEvent := payload["event"]; isEvent {
			continue
		}
		record = payload
		break
	}
	if record == nil {
		t.Fatalf("expected at least one Record Update among %d updates", len(gotVals))
	}

	if record["source"] != "swss" {
		t.Errorf("source = %v, want swss", record["source"])
	}
	if record["db"] != "APPL_DB" {
		t.Errorf("db = %v, want APPL_DB", record["db"])
	}
	if record["key"] != "10.1.0.0/24" {
		t.Errorf("key = %v, want 10.1.0.0/24", record["key"])
	}
	if record["op"] != "SET" {
		t.Errorf("op = %v, want SET", record["op"])
	}
	if !gotSync {
		t.Error("expected sync_response")
	}
}

func TestRecordsSubscribePollUnimplemented(t *testing.T) {
	s := createServer(t, 18082)
	go runServer(t, s)
	defer s.ForceStop()

	q := createRecordsQuery(t, pb.SubscriptionList_POLL,
		"RECORDS", "localhost", "APPL_DB", "ROUTE_TABLE")
	q.Addrs = []string{"127.0.0.1:18082"}
	q.TLS = &tls.Config{InsecureSkipVerify: true}

	c := client.New()
	defer c.Close()
	err := c.Subscribe(context.Background(), q)
	if err == nil {
		t.Fatal("expected Unimplemented for POLL on RECORDS, got nil")
	}
	msg := err.Error()
	if !strings.Contains(strings.ToLower(msg), "unimplemented") &&
		!strings.Contains(msg, "RECORDS only supports STREAM") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func asJSONObject(v interface{}) (map[string]interface{}, error) {
	switch x := v.(type) {
	case map[string]interface{}:
		return x, nil
	case []byte:
		var m map[string]interface{}
		if err := json.Unmarshal(x, &m); err != nil {
			return nil, err
		}
		return m, nil
	case string:
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(x), &m); err != nil {
			return nil, err
		}
		return m, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var m map[string]interface{}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		return m, nil
	}
}
