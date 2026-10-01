package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// recordsAlwaysMatcher accepts every Record (backpressure tests inject FakeTailer lines).
type recordsAlwaysMatcher struct{}

func (recordsAlwaysMatcher) Match(r *Record) (bool, string) {
	return true, "exact"
}

func recordsTestPath() *gnmipb.Path {
	return &gnmipb.Path{
		Elem: []*gnmipb.PathElem{
			{Name: "RECORDS"},
			{Name: "localhost"},
			{Name: "APPL_DB"},
			{Name: "ROUTE_TABLE"},
			{Name: "10.1.0.0"},
			{Name: "24"},
		},
	}
}

func recordsTestPrefix() *gnmipb.Path {
	return &gnmipb.Path{Target: "RECORDS"}
}

func TestNewRecordsClient(t *testing.T) {
	withRecordsNamespaces(t, []string{""})

	path := recordsTestPath()
	prefix := recordsTestPrefix()
	dc, err := NewRecordsClient([]*gnmipb.Path{path}, prefix, 0)
	if err != nil {
		t.Fatalf("NewRecordsClient: %v", err)
	}
	rc, ok := dc.(*RecordsClient)
	if !ok {
		t.Fatalf("expected *RecordsClient, got %T", dc)
	}
	if rc.prefix != prefix {
		t.Fatalf("prefix not set")
	}
	if rc.path != path {
		t.Fatalf("path not set to last subscription path")
	}
	if len(rc.subs) != 1 {
		t.Fatalf("subs = %d, want 1", len(rc.subs))
	}
	if rc.subs[0].key != "10.1.0.0/24" {
		t.Fatalf("parsed key = %q, want 10.1.0.0/24", rc.subs[0].key)
	}
	if rc.tailer == nil {
		t.Fatal("expected default Tailer")
	}
	if rc.parser == nil {
		t.Fatal("expected default Parser")
	}
	if rc.pq_max != PQ_DEF_SIZE {
		t.Fatalf("pq_max = %d, want EventClient default %d", rc.pq_max, PQ_DEF_SIZE)
	}
}

func TestFakeTailerEmitsLines(t *testing.T) {
	ft := NewSampleFakeTailer()
	out := make(chan RawLine, 1)
	ctx := context.Background()
	if err := ft.Run(ctx, time.Time{}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case line := <-out:
		if line.Source != "sairedis" {
			t.Errorf("Source = %q, want sairedis", line.Source)
		}
		if line.Seq == "" || line.Line == "" {
			t.Fatalf("empty Seq/Line: %+v", line)
		}
	default:
		t.Fatal("expected one RawLine")
	}
}

func TestPassThroughParserJSONAndRaw(t *testing.T) {
	p := PassThroughParser{}
	sample := sampleRecord()
	jv, err := marshalRecordJSON(sample)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r, ok := p.Parse(RawLine{Source: "sairedis", Seq: "x", Line: string(jv)})
	if !ok || r == nil {
		t.Fatal("Parse JSON sample failed")
	}
	if r.Op != "c" || r.DB != "ASIC_DB" || r.Source != "sairedis" {
		t.Errorf("parsed record mismatch: %+v", r)
	}
	if !r.TS.Equal(sample.TS) {
		t.Errorf("TS = %v, want %v", r.TS, sample.TS)
	}

	r2, ok := p.Parse(RawLine{Source: "swss", Seq: "swss:1:2", Line: "not-json|raw"})
	if !ok || r2 == nil {
		t.Fatal("Parse raw line failed")
	}
	if r2.Raw != "not-json|raw" || r2.Source != "swss" || r2.Seq != "swss:1:2" {
		t.Errorf("raw pass-through mismatch: %+v", r2)
	}
	if _, ok := p.Parse(RawLine{Line: ""}); ok {
		t.Error("empty line should be skipped")
	}
}

func TestSampleRecordFields(t *testing.T) {
	r := sampleRecord()
	if r.Source != "sairedis" {
		t.Errorf("Source = %q, want sairedis", r.Source)
	}
	if r.DB != "ASIC_DB" {
		t.Errorf("DB = %q, want ASIC_DB", r.DB)
	}
	if r.Op != "c" {
		t.Errorf("Op = %q, want c", r.Op)
	}
	if r.MatchedBy != "correlation:ROUTE_TABLE.dest" {
		t.Errorf("MatchedBy = %q", r.MatchedBy)
	}
	if r.Fields["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"] == "" {
		t.Errorf("missing next-hop field")
	}
}

func TestRecordsClientStreamRunEmitsSampleAndSync(t *testing.T) {
	withRecordsNamespaces(t, []string{""})
	dir := t.TempDir()
	prev := RecordsDir()
	SetRecordsDir(dir)
	t.Cleanup(func() { SetRecordsDir(prev) })
	stamp := time.Now().Format(recordsFileTSLayout)
	body := stamp + "|ROUTE_TABLE:10.1.0.0/24|SET|nexthop:10.0.0.1|ifname:Ethernet0\n" +
		stamp + "|ROUTE_TABLE:172.31.58.254/32|SET|nexthop:1.1.1.1|ifname:eth0\n"
	if err := os.WriteFile(filepath.Join(dir, "swss.rec"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	path := recordsTestPath()
	path.Elem[3].Key = map[string]string{"from": "-1h"}
	prefix := recordsTestPrefix()
	dc, err := NewRecordsClient([]*gnmipb.Path{path}, prefix, 0)
	if err != nil {
		t.Fatalf("NewRecordsClient: %v", err)
	}

	pq := queue.NewPriorityQueue(10, false)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go dc.StreamRun(pq, stop, &wg, nil)

	sawReplayStart := false
	sawLive := false
	sawRecord := false
	sawSync := false
	deadline := time.After(5 * time.Second)
	for !(sawReplayStart && sawRecord && sawLive && sawSync) {
		select {
		case <-deadline:
			t.Fatalf("timeout: replay_start=%v record=%v live=%v sync=%v",
				sawReplayStart, sawRecord, sawLive, sawSync)
		default:
		}
		items, err := pq.Get(1)
		if err != nil {
			t.Fatalf("queue Get: %v", err)
		}
		val, ok := items[0].(Value)
		if !ok {
			t.Fatalf("expected Value, got %T", items[0])
		}
		resp, err := ValToResp(val)
		if err != nil {
			t.Fatalf("ValToResp: %v", err)
		}
		if resp.GetSyncResponse() {
			sawSync = true
			continue
		}
		update := resp.GetUpdate()
		if update == nil || len(update.Update) != 1 {
			t.Fatalf("expected Notification update, got %#v", resp.Response)
		}
		jv := update.Update[0].GetVal().GetJsonIetfVal()
		var payload map[string]interface{}
		if err := json.Unmarshal(jv, &payload); err != nil {
			t.Fatalf("unmarshal JSON: %v", err)
		}
		switch payload["event"] {
		case recordsEventReplayStart:
			sawReplayStart = true
			if payload["from"] == nil || payload["from"] == "" {
				t.Error("replay_start missing from=")
			}
		case recordsEventLive:
			sawLive = true
		default:
			// Matched Record.
			if payload["source"] != "swss" || payload["key"] != "10.1.0.0/24" {
				t.Fatalf("unexpected record payload: %v", payload)
			}
			if payload["matched_by"] != "exact" {
				t.Errorf("matched_by = %v, want exact", payload["matched_by"])
			}
			sawRecord = true
		}
	}

	close(stop)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamRun did not exit after stop")
	}
}

func TestRecordsClientControlEvents(t *testing.T) {
	withRecordsNamespaces(t, []string{""})

	dc, err := NewRecordsClient([]*gnmipb.Path{recordsTestPath()}, recordsTestPrefix(), 0)
	if err != nil {
		t.Fatalf("NewRecordsClient: %v", err)
	}
	rc := dc.(*RecordsClient)
	rc.parser = PassThroughParser{}
	rc.matcher = recordsAlwaysMatcher{}

	r := sampleRecord()
	jv, err := marshalRecordJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Now().Add(-30 * time.Minute)
	rc.subs[0].from = from
	rc.tailer = &FakeTailer{Lines: []RawLine{
		{Source: RecordsSourceControl, Line: `{"event":"gap","reason":"truncate","tailer_source":"swss","namespace":"localhost"}`},
		{Source: "sairedis", Seq: r.Seq, Line: string(jv)},
		{Source: RecordsSourceControl, Line: `{"event":"live","tailer_source":"swss","namespace":"localhost"}`},
	}}

	pq := queue.NewPriorityQueue(10, false)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go dc.StreamRun(pq, stop, &wg, nil)

	want := []string{recordsEventReplayStart, recordsEventGap, "record", recordsEventLive, "sync"}
	got := make([]string, 0, len(want))
	deadline := time.After(3 * time.Second)
	for len(got) < len(want) {
		select {
		case <-deadline:
			t.Fatalf("timeout got=%v want=%v", got, want)
		default:
		}
		items, err := pq.Get(1)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		val := items[0].(Value)
		resp, err := ValToResp(val)
		if err != nil {
			t.Fatalf("ValToResp: %v", err)
		}
		if resp.GetSyncResponse() {
			got = append(got, "sync")
			continue
		}
		jv := resp.GetUpdate().Update[0].GetVal().GetJsonIetfVal()
		var payload map[string]interface{}
		if err := json.Unmarshal(jv, &payload); err != nil {
			t.Fatal(err)
		}
		if ev, ok := payload["event"].(string); ok && ev != "" {
			got = append(got, ev)
			if ev == recordsEventGap && payload["reason"] != "truncate" {
				t.Errorf("gap reason = %v", payload["reason"])
			}
			continue
		}
		got = append(got, "record")
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("seq[%d]=%q, want %q (full got=%v)", i, got[i], want[i], got)
		}
	}

	close(stop)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamRun did not exit after stop")
	}
}

func TestRecordsClientBackpressure(t *testing.T) {
	withRecordsNamespaces(t, []string{""})

	dc, err := NewRecordsClient([]*gnmipb.Path{recordsTestPath()}, recordsTestPrefix(), 0)
	if err != nil {
		t.Fatalf("NewRecordsClient: %v", err)
	}
	rc := dc.(*RecordsClient)
	rc.pq_max = 1
	rc.parser = PassThroughParser{}
	rc.matcher = recordsAlwaysMatcher{}

	const n = 5
	lines := make([]RawLine, 0, n)
	for i := 0; i < n; i++ {
		r := sampleRecord()
		r.Seq = fmt.Sprintf("sairedis:test:%d", i)
		jv, err := marshalRecordJSON(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		lines = append(lines, RawLine{Source: "sairedis", Seq: r.Seq, Line: string(jv)})
	}
	rc.tailer = &FakeTailer{Lines: lines}

	pq := queue.NewPriorityQueue(10, false)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go dc.StreamRun(pq, stop, &wg, nil)

	deadline := time.After(2 * time.Second)
	for rc.Stalls() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected at least one stall with pq_max=1 and 5 records")
		default:
			time.Sleep(recordsBackpressureTick)
		}
	}

	gotRecords := 0
	gotSync := false
	drainDeadline := time.After(3 * time.Second)
	for gotRecords < n || !gotSync {
		select {
		case <-drainDeadline:
			t.Fatalf("drain timeout: records=%d sync=%v stalls=%d", gotRecords, gotSync, rc.Stalls())
		default:
		}
		items, err := pq.Get(1)
		if err != nil {
			t.Fatalf("queue Get: %v", err)
		}
		val, ok := items[0].(Value)
		if !ok {
			t.Fatalf("expected Value, got %T", items[0])
		}
		resp, err := ValToResp(val)
		if err != nil {
			t.Fatalf("ValToResp: %v", err)
		}
		if resp.GetSyncResponse() {
			gotSync = true
			continue
		}
		jv := resp.GetUpdate().Update[0].GetVal().GetJsonIetfVal()
		var payload map[string]interface{}
		if err := json.Unmarshal(jv, &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, isEvent := payload["event"]; isEvent {
			continue // control events (e.g. live) are not counted as records
		}
		gotRecords++
	}
	if gotRecords != n {
		t.Fatalf("records = %d, want %d (backpressure must not drop)", gotRecords, n)
	}
	if rc.Stalls() == 0 {
		t.Fatal("stalls still zero after drain")
	}

	close(stop)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamRun did not exit after stop")
	}
}

func TestRecordsClientStubs(t *testing.T) {
	withRecordsNamespaces(t, []string{""})

	dc, err := NewRecordsClient([]*gnmipb.Path{recordsTestPath()}, recordsTestPrefix(), 0)
	if err != nil {
		t.Fatalf("NewRecordsClient: %v", err)
	}
	rc := dc.(*RecordsClient)

	var wg sync.WaitGroup
	if _, err := rc.Get(&wg); err != nil {
		t.Errorf("Get: %v", err)
	}
	rc.PollRun(nil, nil, &wg, nil)
	rc.AppDBPollRun(nil, nil, &wg, nil)
	rc.OnceRun(nil, nil, &wg, nil)
	if err := rc.Set(nil, nil, nil); err != nil {
		t.Errorf("Set: %v", err)
	}
	caps := rc.Capabilities()
	if len(caps) != 1 || caps[0].Name != "RECORDS" {
		t.Errorf("Capabilities = %v, want [{Name:RECORDS ...}]", caps)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	rc.SentOne(nil)
	if rc.Sent() != 0 {
		t.Errorf("SentOne(nil) should not count, got %d", rc.Sent())
	}
	rc.SentOne(&Value{})
	rc.FailedSend()
	if rc.Sent() != 1 || rc.Failed() != 1 {
		t.Errorf("sent=%d failed=%d, want 1/1", rc.Sent(), rc.Failed())
	}
}

func TestRecordsClientSentFailedAndClose(t *testing.T) {
	withRecordsNamespaces(t, []string{""})

	dc, err := NewRecordsClient([]*gnmipb.Path{recordsTestPath()}, recordsTestPrefix(), 0)
	if err != nil {
		t.Fatalf("NewRecordsClient: %v", err)
	}
	rc := dc.(*RecordsClient)
	rc.parser = PassThroughParser{}
	rc.matcher = recordsAlwaysMatcher{}

	r := sampleRecord()
	jv, err := marshalRecordJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	rc.tailer = &FakeTailer{Lines: []RawLine{
		{Source: "sairedis", Seq: r.Seq, Line: string(jv)},
		{Source: RecordsSourceControl, Line: `{"event":"live","tailer_source":"sairedis","namespace":"localhost"}`},
	}}

	pq := queue.NewPriorityQueue(10, false)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go dc.StreamRun(pq, stop, &wg, nil)

	// Drain until sync so StreamRun is sitting in the final wait.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for sync")
		default:
		}
		items, err := pq.Get(1)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		val := items[0].(Value)
		resp, err := ValToResp(val)
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetSyncResponse() {
			break
		}
		rc.SentOne(&val)
	}
	if rc.Matched() != 1 {
		t.Fatalf("matched=%d, want 1", rc.Matched())
	}
	if rc.SubMatched(0) != 1 {
		t.Fatalf("sub[0] matched=%d, want 1", rc.SubMatched(0))
	}
	if rc.Sent() < 1 {
		t.Fatalf("sent=%d, want at least the record", rc.Sent())
	}
	rc.FailedSend()
	if rc.Failed() != 1 {
		t.Fatalf("failed=%d, want 1", rc.Failed())
	}

	// Close should cancel StreamRun without needing close(stop).
	closed := make(chan struct{})
	go func() {
		if err := rc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return within 2s")
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamRun did not exit after Close")
	}

	// Idempotent.
	if err := rc.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func recordsNeighPath() *gnmipb.Path {
	return &gnmipb.Path{
		Elem: []*gnmipb.PathElem{
			{Name: "RECORDS"},
			{Name: "localhost"},
			{Name: "APPL_DB"},
			{Name: "NEIGH_TABLE"},
		},
	}
}

func applRecord(table, key string) Record {
	ts, _ := time.ParseInLocation(recordTSLayout, "2026-09-26T10:15:32.123456", time.Local)
	return Record{
		Seq:    "swss:1:0",
		TS:     ts,
		Source: "swss",
		DB:     "APPL_DB",
		Table:  table,
		Key:    key,
		Op:     "SET",
		Fields: map[string]string{"ifname": "Ethernet0"},
	}
}

func TestRecordsMatcherDirectMatchPrecedesCorrelation(t *testing.T) {
	appl := Subscription{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "10.1.0.0/24"}
	asic := Subscription{DB: "ASIC_DB", Table: "SAI_OBJECT_TYPE_ROUTE_ENTRY"}
	record := &Record{
		Source: "sairedis",
		DB:     "ASIC_DB",
		Table:  "SAI_OBJECT_TYPE_ROUTE_ENTRY",
		Key:    `{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000"}`,
		Op:     "c",
	}

	for _, tt := range []struct {
		name    string
		subs    []Subscription
		wantIdx int
	}{
		{name: "APPL first", subs: []Subscription{appl, asic}, wantIdx: 1},
		{name: "ASIC first", subs: []Subscription{asic, appl}, wantIdx: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := NewRecordsMatcher(tt.subs)
			ok, how, idx := m.matchWithIndex(record)
			if !ok || idx != tt.wantIdx || how != "prefix" {
				t.Errorf("match = (%v, %q, %d), want (true, prefix, %d)",
					ok, how, idx, tt.wantIdx)
			}
		})
	}
}

// TestRecordsClientEncodePathPerSubscription checks multi-path STREAM updates echo
// the matching subscription path (not always the last path).
func TestRecordsClientEncodePathPerSubscription(t *testing.T) {
	withRecordsNamespaces(t, []string{""})

	routePath := recordsTestPath()
	neighPath := recordsNeighPath()
	dc, err := NewRecordsClient([]*gnmipb.Path{routePath, neighPath}, recordsTestPrefix(), 0)
	if err != nil {
		t.Fatalf("NewRecordsClient: %v", err)
	}
	rc := dc.(*RecordsClient)
	rc.parser = PassThroughParser{}
	// Keep the real RecordsMatcher so each record attributes to the right sub.

	route := applRecord("ROUTE_TABLE", "10.1.0.0/24")
	neigh := applRecord("NEIGH_TABLE", "Ethernet0:10.0.0.2")
	routeJV, err := marshalRecordJSON(route)
	if err != nil {
		t.Fatal(err)
	}
	neighJV, err := marshalRecordJSON(neigh)
	if err != nil {
		t.Fatal(err)
	}
	rc.tailer = &FakeTailer{Lines: []RawLine{
		{Source: "swss", Seq: route.Seq, Line: string(routeJV)},
		{Source: "swss", Seq: neigh.Seq, Line: string(neighJV)},
		{Source: RecordsSourceControl, Line: `{"event":"live","tailer_source":"swss","namespace":"localhost"}`},
	}}

	pq := queue.NewPriorityQueue(10, false)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go dc.StreamRun(pq, stop, &wg, nil)

	wantByTable := map[string]*gnmipb.Path{
		"ROUTE_TABLE": routePath,
		"NEIGH_TABLE": neighPath,
	}
	gotTables := map[string]bool{}
	gotControl := false
	deadline := time.After(3 * time.Second)
	for len(gotTables) < 2 || !gotControl {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for records and control event; records=%v control=%v",
				gotTables, gotControl)
		default:
		}
		items, err := pq.Get(1)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		val := items[0].(Value)
		resp, err := ValToResp(val)
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetSyncResponse() {
			break
		}
		upd := resp.GetUpdate()
		if upd == nil || len(upd.Update) != 1 {
			t.Fatalf("expected update, got %#v", resp.Response)
		}
		jv := upd.Update[0].GetVal().GetJsonIetfVal()
		var payload map[string]interface{}
		if err := json.Unmarshal(jv, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["event"] != nil {
			gotPath := upd.Update[0].GetPath()
			if got := fmt.Sprint(pathElemNames(gotPath)); got != "[RECORDS]" {
				t.Errorf("control event path = %s, want neutral [RECORDS]", got)
			}
			gotControl = true
			continue
		}
		table, _ := payload["table"].(string)
		want, ok := wantByTable[table]
		if !ok {
			t.Fatalf("unexpected table %q in %v", table, payload)
		}
		gotPath := upd.Update[0].GetPath()
		if fmt.Sprint(pathElemNames(gotPath)) != fmt.Sprint(pathElemNames(want)) {
			t.Errorf("table %s: path elems = %v, want %v",
				table, pathElemNames(gotPath), pathElemNames(want))
		}
		// Regression: must not always use the last subscribe path (NEIGH).
		if table == "ROUTE_TABLE" && fmt.Sprint(pathElemNames(gotPath)) == fmt.Sprint(pathElemNames(neighPath)) {
			t.Errorf("ROUTE_TABLE incorrectly encoded with last (NEIGH) path")
		}
		gotTables[table] = true
	}

	if rc.SubMatched(0) != 1 || rc.SubMatched(1) != 1 {
		t.Errorf("subMatched = [%d, %d], want [1, 1]", rc.SubMatched(0), rc.SubMatched(1))
	}

	close(stop)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamRun did not exit after stop")
	}
}
