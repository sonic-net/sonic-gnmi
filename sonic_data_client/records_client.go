package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
)

// How often to re-check the priority queue while applying backpressure.
const recordsBackpressureTick = 10 * time.Millisecond

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
	pq_max  int // same default as EventClient (PQ_DEF_SIZE)
	stalls  uint64
	matched uint64 // records that passed Matcher and were enqueued
	sent    uint64 // Notifications successfully written by Client.send
	failed  uint64 // stream.Send failures reported via FailedSend
	// subMatched[i] is matches attributed to c.subs[i] (same length as subs).
	subMatched []uint64

	channel chan struct{}
	wg      *sync.WaitGroup

	cancel    context.CancelFunc
	runDone   chan struct{} // closed when StreamRun exits
	closeOnce sync.Once
}

// NewRecordsClient builds a RECORDS client for the given subscription paths.
func NewRecordsClient(paths []*gnmipb.Path, prefix *gnmipb.Path, logLevel int) (Client, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("RECORDS: at least one path is required")
	}
	c := &RecordsClient{
		prefix: prefix,
		parser: NewRecordsParser(RecordsLocation()),
		pq_max: PQ_DEF_SIZE,
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
		// Fallback path for control events / unknown match index.
		c.path = path
	}
	c.matcher = NewRecordsMatcher(matchSubs)
	tailer, err := newRecordsTailer(c.subs)
	if err != nil {
		return nil, err
	}
	c.tailer = tailer
	c.subMatched = make([]uint64, len(c.subs))
	log.V(2).Infof("NewRecordsClient prefix=%v path=%v subs=%d logLevel=%d", prefix, c.path, len(c.subs), logLevel)
	c.logSubscriptions("NewRecordsClient")
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
		Filter:    sub.filter,
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

const (
	recordsEventReplayStart = "replay_start"
	recordsEventLive        = "live"
	recordsEventGap         = "gap"
)

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

// Stalls returns how many times StreamRun had to wait because the outbound
// priority queue was at pq_max. Live records are delayed, never dropped: the
// file still holds them while we apply backpressure.
func (c *RecordsClient) Stalls() uint64 {
	return atomic.LoadUint64(&c.stalls)
}

// Matched returns how many Records passed the Matcher and were enqueued.
func (c *RecordsClient) Matched() uint64 {
	return atomic.LoadUint64(&c.matched)
}

// Sent returns how many Notifications Client.send successfully wrote.
func (c *RecordsClient) Sent() uint64 {
	return atomic.LoadUint64(&c.sent)
}

// Failed returns how many stream.Send failures were reported via FailedSend.
func (c *RecordsClient) Failed() uint64 {
	return atomic.LoadUint64(&c.failed)
}

// SubMatched returns matches attributed to subscription i (0 if out of range).
func (c *RecordsClient) SubMatched(i int) uint64 {
	if i < 0 || i >= len(c.subMatched) {
		return 0
	}
	return atomic.LoadUint64(&c.subMatched[i])
}

func (c *RecordsClient) logSubscriptions(stage string) {
	for i, sub := range c.subs {
		fromStr := "live-only"
		if !sub.from.IsZero() {
			fromStr = sub.from.Format(time.RFC3339Nano)
		}
		ops := "all"
		if len(sub.ops) > 0 {
			ops = strings.Join(sub.ops, ",")
		}
		log.V(2).Infof("RecordsClient %s sub[%d] ns=%s db=%s table=%s key=%q ops=%s from=%s",
			stage, i, sub.namespace, sub.db, sub.table, sub.key, ops, fromStr)
	}
}

func (c *RecordsClient) logTailerFiles(stage string) {
	switch t := c.tailer.(type) {
	case *FileTailer:
		log.V(2).Infof("RecordsClient %s file=%s source=%s", stage, t.LivePath(), t.Source())
	case multiTailer:
		for i, part := range t.parts {
			if ft, ok := part.(*FileTailer); ok {
				log.V(2).Infof("RecordsClient %s file[%d]=%s source=%s", stage, i, ft.LivePath(), ft.Source())
			}
		}
	default:
		log.V(2).Infof("RecordsClient %s tailer=%T (no file paths)", stage, c.tailer)
	}
}

func (c *RecordsClient) logTailerStats(stage string) {
	switch t := c.tailer.(type) {
	case *FileTailer:
		st := t.Stats()
		log.V(2).Infof("RecordsClient %s file=%s opened=%d emitted=%d skipped=%d dropped=%d rotations=%d gaps=%d replayed=%d",
			stage, t.LivePath(), st.FilesOpened, st.LinesEmitted, st.LinesSkipped, st.LinesDropped, st.Rotations, st.Gaps, st.ReplayedFiles)
	case multiTailer:
		for i, part := range t.parts {
			ft, ok := part.(*FileTailer)
			if !ok {
				continue
			}
			st := ft.Stats()
			log.V(2).Infof("RecordsClient %s file[%d]=%s opened=%d emitted=%d skipped=%d dropped=%d rotations=%d gaps=%d replayed=%d",
				stage, i, ft.LivePath(), st.FilesOpened, st.LinesEmitted, st.LinesSkipped, st.LinesDropped, st.Rotations, st.Gaps, st.ReplayedFiles)
		}
	}
}

func (c *RecordsClient) logSessionSummary(stage string) {
	log.V(2).Infof("RecordsClient %s matched=%d stalls=%d sent=%d failed=%d",
		stage, c.Matched(), c.Stalls(), c.Sent(), c.Failed())
	for i, sub := range c.subs {
		log.V(2).Infof("RecordsClient %s sub[%d] matched=%d ns=%s db=%s table=%s key=%q",
			stage, i, c.SubMatched(i), sub.namespace, sub.db, sub.table, sub.key)
	}
	c.logTailerStats(stage)
}

// matchRecord runs the Matcher and returns which subscription index hit (-1 if unknown).
func (c *RecordsClient) matchRecord(r *Record) (ok bool, how string, subIdx int) {
	if rm, is := c.matcher.(*RecordsMatcher); is {
		return rm.matchWithIndex(r)
	}
	ok, how = c.matcher.Match(r)
	// Custom matchers have no index; with a single sub, attribute the hit to it.
	if ok && len(c.subs) == 1 {
		return ok, how, 0
	}
	return ok, how, -1
}

// waitForQueueSpace blocks until Len() < pq_max, or ctx/stop is cancelled.
// Unlike EventClient (which drops on overflow), RECORDS never drops: disk is
// the durable queue, so we stall the Tailer via the unbuffered out channel.
func (c *RecordsClient) waitForQueueSpace(ctx context.Context) error {
	if c.q == nil {
		return fmt.Errorf("RECORDS: queue not set")
	}
	max := c.pq_max
	if max <= 0 {
		max = PQ_DEF_SIZE
	}
	if c.q.Len() < max {
		return nil
	}

	atomic.AddUint64(&c.stalls, 1)
	log.V(2).Infof("RecordsClient queue full (len=%d max=%d); applying backpressure (stalls=%d)",
		c.q.Len(), max, atomic.LoadUint64(&c.stalls))

	ticker := time.NewTicker(recordsBackpressureTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.channel:
			return context.Canceled
		case <-ticker.C:
			if c.q.Len() < max {
				return nil
			}
		}
	}
}

func (c *RecordsClient) putWithBackpressure(ctx context.Context, val Value) error {
	if err := c.waitForQueueSpace(ctx); err != nil {
		return err
	}
	return c.q.Put(val)
}

// encodePath returns the gNMI path for the subscription that matched, or the
// session fallback (last subscribe path) when the index is unknown.
func (c *RecordsClient) encodePath(subIdx int) *gnmipb.Path {
	if subIdx >= 0 && subIdx < len(c.subs) && c.subs[subIdx].path != nil {
		return c.subs[subIdx].path
	}
	return c.path
}

func (c *RecordsClient) enqueueRecord(ctx context.Context, r Record, subIdx int) error {
	jv, err := marshalRecordJSON(r)
	if err != nil {
		return err
	}
	spbv := &spb.Value{
		Prefix:    c.prefix,
		Path:      c.encodePath(subIdx),
		Timestamp: r.TS.UnixNano(),
		Val: &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: jv,
			},
		},
	}
	return c.putWithBackpressure(ctx, Value{spbv})
}

// enqueueControlJSON sends an operator control event as a JSON_IETF Notification.
// These are not recorder lines; they mark replay/live phase and gaps.
func (c *RecordsClient) enqueueControlJSON(ctx context.Context, jv []byte) error {
	spbv := &spb.Value{
		Prefix:    c.prefix,
		Path:      c.path,
		Timestamp: time.Now().UnixNano(),
		Val: &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: jv,
			},
		},
	}
	return c.putWithBackpressure(ctx, Value{spbv})
}

func (c *RecordsClient) enqueueControlEvent(ctx context.Context, payload map[string]string) error {
	jv, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.enqueueControlJSON(ctx, jv)
}

func (c *RecordsClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, wg *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.wg = wg
	defer c.wg.Done()
	c.q = q
	c.channel = stop
	if c.pq_max <= 0 {
		c.pq_max = PQ_DEF_SIZE
	}

	c.runDone = make(chan struct{})
	defer close(c.runDone)

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	defer cancel()
	go func() {
		select {
		case <-c.channel:
			cancel()
		case <-ctx.Done():
		}
	}()

	from := c.earliestFrom()
	c.logSubscriptions("StreamRun")
	c.logTailerFiles("StreamRun")
	log.V(2).Infof("RecordsClient StreamRun prefix=%v earliest_from=%v subs=%d",
		c.prefix, from, len(c.subs))
	if !from.IsZero() {
		if err := c.enqueueControlEvent(ctx, map[string]string{
			"event": recordsEventReplayStart,
			"from":  from.Format(time.RFC3339Nano),
		}); err != nil {
			log.V(1).Infof("RecordsClient replay_start enqueue failed: %v", err)
			return
		}
	}

	out := make(chan RawLine)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.tailer.Run(ctx, from, out)
		close(out)
	}()

	synced := false
	liveSent := false
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
		if err := c.putWithBackpressure(ctx, Value{&spb.Value{
			Timestamp:    time.Now().UnixNano(),
			SyncResponse: true,
		}}); err != nil {
			log.V(1).Infof("RecordsClient sync enqueue failed: %v", err)
			return false
		}
		return true
	}
	sendLive := func() bool {
		if liveSent {
			return true
		}
		liveSent = true
		if err := c.enqueueControlEvent(ctx, map[string]string{
			"event": recordsEventLive,
		}); err != nil {
			log.V(1).Infof("RecordsClient live enqueue failed: %v", err)
			return false
		}
		return sendSync()
	}

	for line := range out {
		if ctx.Err() != nil {
			continue // drain so Tailer is not stuck on a send
		}
		if line.Source == RecordsSourceControl {
			switch controlEventName(line.Line) {
			case recordsEventLive:
				lives++
				if lives >= liveNeeded && !sendLive() {
					cancel()
				}
			case recordsEventGap:
				// Forward the tailer's gap body (reason, source, namespace).
				if err := c.enqueueControlJSON(ctx, []byte(line.Line)); err != nil {
					log.V(1).Infof("RecordsClient gap enqueue failed: %v", err)
					cancel()
				}
			default:
				log.V(2).Infof("RecordsClient ignoring unknown control event: %s", line.Line)
			}
			continue
		}
		// One line can carry several records (bulk sairedis ops, E re-emits).
		var recs []*Record
		if bp, isBulk := c.parser.(BulkParser); isBulk {
			recs = bp.ParseAll(line)
		} else if r, ok := c.parser.Parse(line); ok && r != nil {
			recs = []*Record{r}
		}
		for _, r := range recs {
			ok, how, subIdx := c.matchRecord(r)
			if !ok {
				continue
			}
			r.MatchedBy = how
			if err := c.enqueueRecord(ctx, *r, subIdx); err != nil {
				log.V(1).Infof("RecordsClient enqueue failed: %v", err)
				cancel()
				break
			}
			atomic.AddUint64(&c.matched, 1)
			if subIdx >= 0 && subIdx < len(c.subMatched) {
				atomic.AddUint64(&c.subMatched[subIdx], 1)
			}
		}
	}
	if err := <-errCh; err != nil && ctx.Err() == nil {
		log.V(1).Infof("RecordsClient tailer: %v", err)
	}
	if !sendLive() {
		return
	}

	c.logSessionSummary("waiting-for-stop")
	select {
	case <-c.channel:
	case <-ctx.Done():
	}
	c.logSessionSummary("stopped")
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

// Advertised via gNMI Capabilities so clients (e.g. gnmic) discover the RECORDS target.
var recordsSupportedModels = []gnmipb.ModelData{
	{
		Name:         "RECORDS",
		Organization: "SONiC",
		Version:      "0.1.0",
	},
}

func (c *RecordsClient) Capabilities() []gnmipb.ModelData {
	return recordsSupportedModels
}

// Close cancels an in-flight StreamRun (if any) and waits for it to exit.
// Safe to call more than once; a no-op if StreamRun never started.
func (c *RecordsClient) Close() error {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		if c.runDone != nil {
			<-c.runDone
		}
	})
	return nil
}

// SentOne is called by Client.send after a Notification is written to the stream.
func (c *RecordsClient) SentOne(val *Value) {
	if val == nil {
		return
	}
	atomic.AddUint64(&c.sent, 1)
}

// FailedSend is called by Client.send when stream.Send fails.
func (c *RecordsClient) FailedSend() {
	atomic.AddUint64(&c.failed, 1)
}
