package client

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

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
	if rc.tailer == nil {
		t.Fatal("expected default Tailer")
	}
	if rc.parser == nil {
		t.Fatal("expected default Parser")
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
	path := recordsTestPath()
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

	// First item: sample Record as JSON_IETF TypedValue.
	items, err := pq.Get(1)
	if err != nil {
		t.Fatalf("queue Get(record): %v", err)
	}
	val, ok := items[0].(Value)
	if !ok {
		t.Fatalf("expected Value, got %T", items[0])
	}
	resp, err := ValToResp(val)
	if err != nil {
		t.Fatalf("ValToResp(record): %v", err)
	}
	update := resp.GetUpdate()
	if update == nil {
		t.Fatalf("expected Notification update, got %#v", resp.Response)
	}
	if update.GetPrefix().GetTarget() != "RECORDS" {
		t.Errorf("Notification prefix target = %q, want RECORDS", update.GetPrefix().GetTarget())
	}
	if len(update.Update) != 1 {
		t.Fatalf("want 1 Update, got %d", len(update.Update))
	}
	jv := update.Update[0].GetVal().GetJsonIetfVal()
	if len(jv) == 0 {
		t.Fatal("TypedValue JsonIetfVal is empty")
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(jv, &payload); err != nil {
		t.Fatalf("unmarshal sample JSON: %v", err)
	}
	for _, key := range []string{"seq", "ts", "source", "db", "table", "key", "op", "fields", "matched_by"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("sample JSON missing %q", key)
		}
	}
	if payload["source"] != "sairedis" {
		t.Errorf("source = %v, want sairedis", payload["source"])
	}
	if payload["db"] != "ASIC_DB" {
		t.Errorf("db = %v, want ASIC_DB", payload["db"])
	}
	if payload["op"] != "c" {
		t.Errorf("op = %v, want c", payload["op"])
	}
	if payload["ts"] != "2026-09-26T10:15:32.123456" {
		t.Errorf("ts = %v, want 2026-09-26T10:15:32.123456", payload["ts"])
	}
	wantTS := sampleRecord().TS.UnixNano()
	if update.GetTimestamp() != wantTS {
		t.Errorf("Notification timestamp = %d, want record TS %d", update.GetTimestamp(), wantTS)
	}

	// Second item: sync_response.
	items, err = pq.Get(1)
	if err != nil {
		t.Fatalf("queue Get(sync): %v", err)
	}
	val, ok = items[0].(Value)
	if !ok {
		t.Fatalf("expected Value for sync, got %T", items[0])
	}
	resp, err = ValToResp(val)
	if err != nil {
		t.Fatalf("ValToResp(sync): %v", err)
	}
	if !resp.GetSyncResponse() {
		t.Fatalf("expected sync_response true, got %#v", resp.Response)
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
	if caps := rc.Capabilities(); caps != nil {
		t.Errorf("Capabilities = %v, want nil", caps)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	rc.SentOne(nil)
	rc.FailedSend()
}
