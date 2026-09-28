package client

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
)

// RecordsClient is a STREAM-only client for the RECORDS target.
// Day-1 stub: FakeTailer + PassThroughParser emit one sample Record then sync.
type RecordsClient struct {
	prefix *gnmipb.Path
	path   *gnmipb.Path

	tailer Tailer
	parser Parser

	q       *queue.PriorityQueue
	channel chan struct{}
	wg      *sync.WaitGroup
}

// NewRecordsClient builds a RECORDS client for the given subscription paths.
func NewRecordsClient(paths []*gnmipb.Path, prefix *gnmipb.Path, logLevel int) (Client, error) {
	c := &RecordsClient{
		prefix: prefix,
		tailer: NewSampleFakeTailer(),
		parser: PassThroughParser{},
	}
	for _, path := range paths {
		// Only one path is expected; take the last if many (EVENTS precedent).
		c.path = path
	}
	log.V(2).Infof("NewRecordsClient prefix=%v path=%v logLevel=%d", prefix, c.path, logLevel)
	return c, nil
}

// sampleRecord returns the example Record from the RECORDS design doc.
func sampleRecord() Record {
	ts, _ := time.ParseInLocation(recordTSLayout, "2026-09-26T10:15:32.123456", time.Local)
	return Record{
		Seq:    "sairedis:1180422:9932711",
		TS:     ts,
		Source: "sairedis",
		DB:     "ASIC_DB",
		Table:  "SAI_OBJECT_TYPE_ROUTE_ENTRY",
		Key:    `{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000022"}`,
		Op:     "c",
		Fields: map[string]string{
			"SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID": "oid:0x5000000000a3c",
		},
		Status:    "",
		MatchedBy: "correlation:ROUTE_TABLE.dest",
	}
}

func marshalRecordJSON(r Record) ([]byte, error) {
	// Encode ts as the design's local fractional form rather than RFC3339.
	payload := struct {
		Seq       string            `json:"seq"`
		TS        string            `json:"ts"`
		Source    string            `json:"source"`
		DB        string            `json:"db"`
		Table     string            `json:"table"`
		Key       string            `json:"key"`
		Op        string            `json:"op"`
		Fields    map[string]string `json:"fields"`
		Status    string            `json:"status"`
		MatchedBy string            `json:"matched_by,omitempty"`
	}{
		Seq:       r.Seq,
		TS:        r.TS.Format(recordTSLayout),
		Source:    r.Source,
		DB:        r.DB,
		Table:     r.Table,
		Key:       r.Key,
		Op:        r.Op,
		Fields:    r.Fields,
		Status:    r.Status,
		MatchedBy: r.MatchedBy,
	}
	return json.Marshal(payload)
}

func (c *RecordsClient) enqueueRecord(r Record) error {
	jv, err := marshalRecordJSON(r)
	if err != nil {
		return err
	}
	spbv := &spb.Value{
		Prefix:    c.prefix,
		Path:      c.path,
		Timestamp: r.TS.UnixNano(),
		Val: &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: jv,
			},
		},
	}
	return c.q.Put(Value{spbv})
}

func (c *RecordsClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.wg = wg
	defer c.wg.Done()
	c.q = q
	c.channel = stop

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-c.channel:
			cancel()
		case <-ctx.Done():
		}
	}()

	out := make(chan RawLine)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.tailer.Run(ctx, time.Time{}, out)
		close(out)
	}()

	for line := range out {
		if ctx.Err() != nil {
			continue // drain so Tailer is not stuck on a send
		}
		r, ok := c.parser.Parse(line)
		if !ok || r == nil {
			continue
		}
		if err := c.enqueueRecord(*r); err != nil {
			log.V(1).Infof("RecordsClient enqueue failed: %v", err)
			cancel()
			continue
		}
	}
	if err := <-errCh; err != nil && ctx.Err() == nil {
		log.V(1).Infof("RecordsClient tailer: %v", err)
	}

	if err := c.q.Put(Value{&spb.Value{
		Timestamp:    time.Now().UnixNano(),
		SyncResponse: true,
	}}); err != nil {
		log.V(1).Infof("RecordsClient sync enqueue failed: %v", err)
		return
	}

	log.V(2).Infof("RecordsClient emitted records via Tailer/Parser; waiting for stop")
	<-c.channel
	log.V(2).Infof("RecordsClient stop received")
}

func (c *RecordsClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
}

func (c *RecordsClient) AppDBPollRun(q *queue.PriorityQueue, poll chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
}

func (c *RecordsClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
}

func (c *RecordsClient) Get(wg *sync.WaitGroup) ([]*spb.Value, error) {
	return nil, nil
}

func (c *RecordsClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	return nil
}

func (c *RecordsClient) Capabilities() []gnmipb.ModelData {
	return nil
}

func (c *RecordsClient) Close() error {
	return nil
}

func (c *RecordsClient) SentOne(val *Value) {
}

func (c *RecordsClient) FailedSend() {
}
