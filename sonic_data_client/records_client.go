package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
)

// RecordsClient is a STREAM-only client for the RECORDS target.
// It tails swss.rec and sairedis.rec and keeps lines RecordsMatcher accepts.
type RecordsClient struct {
	prefix *gnmipb.Path
	path   *gnmipb.Path
	subs   []recordsSubscription

	tailer  Tailer
	parser  Parser
	matcher Matcher

	q       *queue.PriorityQueue
	channel chan struct{}
	wg      *sync.WaitGroup
}

// NewRecordsClient builds a RECORDS client for the given subscription paths.
func NewRecordsClient(paths []*gnmipb.Path, prefix *gnmipb.Path, logLevel int) (Client, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("RECORDS: at least one path is required")
	}
	c := &RecordsClient{
		prefix: prefix,
		parser: NewRecordsParser(RecordsLocation()),
	}
	var matchSubs []Subscription
	for _, path := range paths {
		sub, err := parseRecordsPath(path)
		if err != nil {
			return nil, err
		}
		if err := validateRecordsNamespace(sub.namespace); err != nil {
			return nil, err
		}
		c.subs = append(c.subs, *sub)
		matchSubs = append(matchSubs, subscriptionForMatch(*sub))
		// Keep last path for encoding (EVENTS precedent).
		c.path = path
	}
	c.matcher = NewRecordsMatcher(matchSubs)
	tailer, err := newRecordsTailer(c.subs)
	if err != nil {
		return nil, err
	}
	c.tailer = tailer
	log.V(2).Infof("NewRecordsClient prefix=%v path=%v subs=%d logLevel=%d", prefix, c.path, len(c.subs), logLevel)
	return c, nil
}

// subscriptionForMatch converts a parsed path into the WS3 matcher shape.
// ASIC_DB paths use table ASIC_STATE and put SAI_OBJECT_TYPE_X[:entry] in the key.
func subscriptionForMatch(sub recordsSubscription) Subscription {
	table, key := sub.table, sub.key
	if sub.db == recordsDBAsic && sub.key != "" {
		typ, obj, hasObj := strings.Cut(sub.key, ":")
		table = typ
		if hasObj {
			key = obj
		} else {
			key = ""
		}
	}
	return Subscription{
		Namespace: sub.namespace,
		DB:        sub.db,
		Table:     table,
		Key:       key,
		Ops:       sub.ops,
	}
}

// newRecordsTailer opens swss and sairedis tailers for each subscribed namespace.
// Both sources are read so APPL_DB subscriptions can include correlated SAI lines.
func newRecordsTailer(subs []recordsSubscription) (Tailer, error) {
	seen := map[string]bool{}
	var parts []Tailer
	for _, sub := range subs {
		for _, source := range []string{RecordsSourceSwss, RecordsSourceSairedis} {
			id := sub.namespace + "\x00" + source
			if seen[id] {
				continue
			}
			seen[id] = true
			ft, err := NewFileTailer(RecordsDir(), sub.namespace, source)
			if err != nil {
				return nil, err
			}
			parts = append(parts, ft)
		}
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return multiTailer{parts: parts}, nil
}

type multiTailer struct {
	parts []Tailer
}

func (m multiTailer) Run(ctx context.Context, from time.Time, out chan<- RawLine) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(m.parts))
	for _, part := range m.parts {
		wg.Add(1)
		go func(part Tailer) {
			defer wg.Done()
			errCh <- part.Run(ctx, from, out)
		}(part)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil && ctx.Err() == nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func controlEventName(line string) string {
	var m struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return ""
	}
	return m.Event
}

// earliestFrom returns the oldest non-zero from= across subscriptions.
// Zero means live-only (no historical replay).
func (c *RecordsClient) earliestFrom() time.Time {
	var earliest time.Time
	for _, sub := range c.subs {
		if sub.from.IsZero() {
			continue
		}
		if earliest.IsZero() || sub.from.Before(earliest) {
			earliest = sub.from
		}
	}
	return earliest
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
		errCh <- c.tailer.Run(ctx, c.earliestFrom(), out)
		close(out)
	}()

	synced := false
	lives := 0
	liveNeeded := 1
	if m, ok := c.tailer.(multiTailer); ok && len(m.parts) > 0 {
		liveNeeded = len(m.parts)
	}
	sendSync := func() bool {
		if synced {
			return true
		}
		synced = true
		if err := c.q.Put(Value{&spb.Value{
			Timestamp:    time.Now().UnixNano(),
			SyncResponse: true,
		}}); err != nil {
			log.V(1).Infof("RecordsClient sync enqueue failed: %v", err)
			return false
		}
		return true
	}

	for line := range out {
		if ctx.Err() != nil {
			continue // drain so Tailer is not stuck on a send
		}
		if line.Source == RecordsSourceControl {
			if controlEventName(line.Line) == "live" {
				lives++
				if lives >= liveNeeded && !sendSync() {
					cancel()
				}
			}
			continue
		}
		r, ok := c.parser.Parse(line)
		if !ok || r == nil {
			continue
		}
		ok, how := c.matcher.Match(r)
		if !ok {
			continue
		}
		r.MatchedBy = how
		if err := c.enqueueRecord(*r); err != nil {
			log.V(1).Infof("RecordsClient enqueue failed: %v", err)
			cancel()
			continue
		}
	}
	if err := <-errCh; err != nil && ctx.Err() == nil {
		log.V(1).Infof("RecordsClient tailer: %v", err)
	}
	if !sendSync() {
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
