// Package client provides a generic access layer for data available in system
package client

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	gnmi_extpb "github.com/openconfig/gnmi/proto/gnmi_ext"
	"github.com/openconfig/ygot/ygot"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	transutil "github.com/sonic-net/sonic-gnmi/transl_utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	DELETE  int = 0
	REPLACE int = 1
	UPDATE  int = 2
)

type TranslClient struct {
	prefix *gnmipb.Path
	/* GNMI Path to REST URL Mapping */
	path2URI map[*gnmipb.Path]string
	channel  chan struct{}
	q        *queue.PriorityQueue

	synced     sync.WaitGroup  // Control when to send gNMI sync_response
	w          *sync.WaitGroup // wait for all sub go routines to finish
	mu         sync.RWMutex    // Mutex for data protection among routines for transl_client
	ctx        context.Context //Contains Auth info and request info
	extensions []*gnmi_extpb.Extension

	version  *translib.Version // Client version; populated by parseVersion()
	encoding gnmipb.Encoding
}

func NewTranslClient(prefix *gnmipb.Path, getpaths []*gnmipb.Path, ctx context.Context, extensions []*gnmi_extpb.Extension, opts ...TranslClientOption) (Client, error) {
	var client TranslClient
	var err error
	client.ctx = ctx
	client.prefix = prefix
	client.extensions = extensions

	if getpaths != nil {
		var addWildcardKeys bool
		for _, o := range opts {
			if _, ok := o.(TranslWildcardOption); ok {
				addWildcardKeys = true
			}
		}

		client.path2URI = make(map[*gnmipb.Path]string)
		/* Populate GNMI path to REST URL map. */
		err = transutil.PopulateClientPaths(prefix, getpaths, &client.path2URI, addWildcardKeys)
	}

	if err != nil {
		return nil, err
	} else {
		return &client, nil
	}
}

func (c *TranslClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	rc, ctx := common_utils.GetContext(c.ctx)
	c.ctx = ctx
	var values []*spb.Value
	ts := time.Now()

	version := getBundleVersion(c.extensions)
	if version != nil {
		rc.BundleVersion = version
	}
	/* Iterate through all GNMI paths. */
	for gnmiPath, URIPath := range c.path2URI {
		/* Fill values for each GNMI path. */
		val, err := transutil.TranslProcessGet(URIPath, nil, c.ctx)

		if err != nil {
			return nil, err
		}

		/* Value of each path is added to spb value structure. */
		values = append(values, &spb.Value{
			Prefix:    c.prefix,
			Path:      gnmiPath,
			Timestamp: ts.UnixNano(),
			Val:       val,
		})
	}

	/* The values structure at the end is returned and then updates in notitications as
	specified in the proto file in the server.go */

	log.V(6).Infof("TranslClient : Getting #%v", values)
	log.V(4).Infof("TranslClient :Get done, total time taken: %v ms", int64(time.Since(ts)/time.Millisecond))

	return values, nil
}

func (c *TranslClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	rc, ctx := common_utils.GetContext(c.ctx)
	c.ctx = ctx
	version := getBundleVersion(c.extensions)
	if version != nil {
		rc.BundleVersion = version
	}

	if (len(delete) + len(replace) + len(update)) > 1 {
		return transutil.TranslProcessBulk(delete, replace, update, c.prefix, c.ctx)
	} else {
		if len(delete) == 1 {
			return transutil.TranslProcessDelete(c.prefix, delete[0], c.ctx)
		}
		if len(replace) == 1 {
			return transutil.TranslProcessReplace(c.prefix, replace[0], c.ctx)
		}
		if len(update) == 1 {
			return transutil.TranslProcessUpdate(c.prefix, update[0], c.ctx)
		}
	}
	return nil
}

func enqueFatalMsgTranslib(c *TranslClient, msg string) {
	c.q.Put(Value{
		&spb.Value{
			Timestamp: time.Now().UnixNano(),
			Fatal:     msg,
		},
	})
}

func enqueueSyncMessage(c *TranslClient) {
	m := &spb.Value{
		Timestamp:    time.Now().UnixNano(),
		SyncResponse: true,
	}
	c.q.Put(Value{m})
}

// recoverSubscribe recovers from possible panics during subscribe handling.
// It pushes a fatal message to the RPC handler's queue, which forces the server to
// close the RPC with an error status. Should always be used as a deferred function.
func recoverSubscribe(c *TranslClient) {
	if r := recover(); r != nil {
		buff := make([]byte, 1<<12)
		buff = buff[:runtime.Stack(buff, false)]
		log.Error(string(buff))

		err := status.Errorf(codes.Internal, "%v", r)
		enqueFatalMsgTranslib(c, fmt.Sprintf("Subscribe operation failed with error =%v", err.Error()))
	}
}

type ticker_info struct {
	t         *time.Ticker
	sub       *gnmipb.Subscription
	pathStr   string
	heartbeat bool
}

func getTranslNotificationType(mode gnmipb.SubscriptionMode) translib.NotificationType {
	switch mode {
	case gnmipb.SubscriptionMode_ON_CHANGE:
		return translib.OnChange
	case gnmipb.SubscriptionMode_SAMPLE:
		return translib.Sample
	default:
		return translib.TargetDefined
	}
}

func tickerCleanup(ticker_map map[int][]*ticker_info, c *TranslClient) {
	for _, v := range ticker_map {
		for _, ti := range v {
			ti.t.Stop()
		}
	}
}

func (c *TranslClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w

	defer c.w.Done()
	defer recoverSubscribe(c)

	c.q = q
	c.channel = stop
	c.encoding = subscribe.Encoding

	if err := c.parseVersion(); err != nil {
		enqueFatalMsgTranslib(c, err.Error())
		return
	}

	ticker_map := make(map[int][]*ticker_info)

	defer tickerCleanup(ticker_map, c)
	var cases []reflect.SelectCase
	cases_map := make(map[int]int)
	var subscribe_mode gnmipb.SubscriptionMode
	translPaths := make([]translib.IsSubscribePath, len(subscribe.Subscription))
	sampleCache := make(map[string]*ygotCache)

	for i, sub := range subscribe.Subscription {
		translPaths[i].ID = uint32(i)
		translPaths[i].Path = c.path2URI[sub.Path]
		translPaths[i].Mode = getTranslNotificationType(sub.Mode)
	}

	rc, _ := common_utils.GetContext(c.ctx)
	ss := translib.NewSubscribeSession()
	defer ss.Close()

	req := translib.IsSubscribeRequest{
		Paths:   translPaths,
		Session: ss,
		User:    translib.UserRoles{Name: rc.Auth.User, Roles: rc.Auth.Roles},
	}
	if c.version != nil {
		req.ClientVersion = *c.version
	}

	subSupport, err := translib.IsSubscribeSupported(req)
	if err != nil {
		enqueFatalMsgTranslib(c, fmt.Sprintf("Subscribe operation failed with error =%v", err.Error()))
		return
	}

	var onChangeSubsString []string

	for i, pInfo := range subSupport {
		sub := subscribe.Subscription[pInfo.ID]
		log.V(6).Infof("Start Sub: %v", sub)
		pathStr := pInfo.Path

		switch sub.Mode {
		case gnmipb.SubscriptionMode_TARGET_DEFINED:
			if pInfo.IsOnChangeSupported && pInfo.PreferredType == translib.OnChange {
				subscribe_mode = gnmipb.SubscriptionMode_ON_CHANGE
			} else {
				subscribe_mode = gnmipb.SubscriptionMode_SAMPLE
			}
		case gnmipb.SubscriptionMode_ON_CHANGE:
			if pInfo.IsOnChangeSupported {
				subscribe_mode = gnmipb.SubscriptionMode_ON_CHANGE
			} else {
				enqueFatalMsgTranslib(c, fmt.Sprintf("ON_CHANGE Streaming mode invalid for %v", pathStr))
				return
			}
		case gnmipb.SubscriptionMode_SAMPLE:
			subscribe_mode = gnmipb.SubscriptionMode_SAMPLE
		default:
			enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid Subscription Mode %d", sub.Mode))
			return
		}

		if pInfo.MinInterval <= 0 { // should not happen
			pInfo.MinInterval = translib.MinSubscribeInterval
		}

		if hb := sub.HeartbeatInterval; hb > 0 && hb < uint64(pInfo.MinInterval)*uint64(time.Second) {
			enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid Heartbeat Interval %ds, minimum interval is %ds",
				sub.HeartbeatInterval/uint64(time.Second), subSupport[i].MinInterval))
			return
		}

		log.V(6).Infof("subscribe_mode %v for path %s", subscribe_mode, pathStr)
		if subscribe_mode == gnmipb.SubscriptionMode_SAMPLE {
			interval := int(sub.SampleInterval)
			minInterval := pInfo.MinInterval * int(time.Second)
			if interval == 0 {
				interval = minInterval
			} else if interval < minInterval {
				enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid SampleInterval %ds, minimum interval is %ds", interval/int(time.Second), pInfo.MinInterval))
				return
			}

			reqPath, _ := ygot.StringToStructuredPath(pathStr)
			yCache := newYgotCache(reqPath)
			sampleCache[pathStr] = yCache
			ts := translSubscriber{
				client:      c,
				session:     ss,
				sampleCache: yCache,
				filterMsgs:  subscribe.UpdatesOnly,
			}

			// Force ignore init updates for subpaths to prevent duplicates.
			// But performs duplicate gets though -- needs optimization.
			if pInfo.IsSubPath {
				ts.filterMsgs = true
			}

			// do initial sync & build the cache
			ts.doSample(pathStr)

			addTimer(c, ticker_map, &cases, cases_map, interval, sub, pathStr, false)
			//Heartbeat intervals are valid for SAMPLE in the case suppress_redundant is specified
			if sub.SuppressRedundant && sub.HeartbeatInterval > 0 {
				addTimer(c, ticker_map, &cases, cases_map, int(sub.HeartbeatInterval), sub, pathStr, true)
			}
		} else if subscribe_mode == gnmipb.SubscriptionMode_ON_CHANGE {
			onChangeSubsString = append(onChangeSubsString, pathStr)
			if sub.HeartbeatInterval > 0 {
				addTimer(c, ticker_map, &cases, cases_map, int(sub.HeartbeatInterval), sub, pathStr, true)
			}
		}
		log.V(6).Infof("End Sub: %v", sub)
	}

	if len(onChangeSubsString) > 0 {
		ts := translSubscriber{
			client:     c,
			session:    ss,
			filterMsgs: subscribe.UpdatesOnly,
		}
		ts.doOnChange(onChangeSubsString)
	} else {
		// If at least one ON_CHANGE subscription was present, then
		// ts.doOnChange() would have sent the sync message.
		// Explicitly send one here if all are SAMPLE subscriptions.
		enqueueSyncMessage(c)
	}

	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c.channel)})

	for {
		chosen, _, ok := reflect.Select(cases)
		if !ok {
			return
		}

		for _, tick := range ticker_map[cases_map[chosen]] {
			log.V(6).Infof("tick, heartbeat: %t, path: %s\n", tick.heartbeat, c.path2URI[tick.sub.Path])
			ts := translSubscriber{
				client:      c,
				session:     ss,
				sampleCache: sampleCache[tick.pathStr],
				filterDups:  (!tick.heartbeat && tick.sub.SuppressRedundant),
			}
			ts.doSample(tick.pathStr)
		}
	}
}

func addTimer(c *TranslClient, ticker_map map[int][]*ticker_info, cases *[]reflect.SelectCase, cases_map map[int]int, interval int, sub *gnmipb.Subscription, pathStr string, heartbeat bool) {
	//Reuse ticker for same sample intervals, otherwise create a new one.
	if ticker_map[interval] == nil {
		ticker_map[interval] = make([]*ticker_info, 1, 1)
		ticker_map[interval][0] = &ticker_info{
			t:         time.NewTicker(time.Duration(interval) * time.Nanosecond),
			sub:       sub,
			pathStr:   pathStr,
			heartbeat: heartbeat,
		}
		cases_map[len(*cases)] = interval
		*cases = append(*cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ticker_map[interval][0].t.C)})
	} else {
		ticker_map[interval] = append(ticker_map[interval], &ticker_info{
			t:         ticker_map[interval][0].t,
			sub:       sub,
			pathStr:   pathStr,
			heartbeat: heartbeat,
		})
	}
}

func (c *TranslClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w
	defer c.w.Done()
	defer recoverSubscribe(c)
	c.q = q
	c.channel = poll
	c.encoding = subscribe.Encoding

	if err := c.parseVersion(); err != nil {
		enqueFatalMsgTranslib(c, err.Error())
		return
	}

	synced := false
	for {
		_, more := <-c.channel
		if !more {
			log.V(1).Infof("%v poll channel closed, exiting pollDb routine", c)
			enqueFatalMsgTranslib(c, "")
			return
		}

		t1 := time.Now()
		for _, gnmiPath := range c.path2URI {
			if synced || !subscribe.UpdatesOnly {
				ts := translSubscriber{client: c}
				ts.doSample(gnmiPath)
			}
		}

		enqueueSyncMessage(c)
		synced = true
		log.V(4).Infof("Sync done, poll time taken: %v ms", int64(time.Since(t1)/time.Millisecond))
	}
}

func (c *TranslClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w
	defer c.w.Done()
	defer recoverSubscribe(c)

	c.q = q
	c.channel = once
	c.encoding = subscribe.Encoding

	if err := c.parseVersion(); err != nil {
		enqueFatalMsgTranslib(c, err.Error())
		return
	}

	_, more := <-c.channel
	if !more {
		log.V(1).Infof("%v once channel closed, exiting onceDb routine", c)
		enqueFatalMsgTranslib(c, "")
		return
	}

	t1 := time.Now()
	for _, gnmiPath := range c.path2URI {
		ts := translSubscriber{client: c}
		ts.doSample(gnmiPath)
	}

	enqueueSyncMessage(c)
	log.V(4).Infof("Sync done, once time taken: %v ms", int64(time.Since(t1)/time.Millisecond))

}

func (c *TranslClient) Capabilities() []gnmipb.ModelData {
	rc, ctx := common_utils.GetContext(c.ctx)
	c.ctx = ctx
	version := getBundleVersion(c.extensions)
	if version != nil {
		rc.BundleVersion = version
	}
	/* Fetch the supported models. */
	supportedModels := transutil.GetModels()
	return supportedModels
}

func (c *TranslClient) Close() error {
	return nil
}
func (c *TranslClient) SentOne(val *Value) {
}

func (c *TranslClient) FailedSend() {
}

func getBundleVersion(extensions []*gnmi_extpb.Extension) *string {
	for _, e := range extensions {
		switch v := e.Ext.(type) {
		case *gnmi_extpb.Extension_RegisteredExt:
			if v.RegisteredExt.Id == spb.BUNDLE_VERSION_EXT {
				var bv spb.BundleVersion
				proto.Unmarshal(v.RegisteredExt.Msg, &bv)
				return &bv.Version
			}

		}
	}
	return nil
}

func (c *TranslClient) parseVersion() error {
	bv := getBundleVersion(c.extensions)
	if bv == nil {
		return nil
	}
	v, err := translib.NewVersion(*bv)
	if err != nil {
		c.version = &v
		return nil
	}
	log.V(4).Infof("Failed to parse version \"%s\"; err=%v", *bv, err)
	return fmt.Errorf("Invalid bundle version: %v", *bv)
}

type TranslClientOption interface {
	IsTranslClientOption()
}

type TranslWildcardOption struct{}

func (t TranslWildcardOption) IsTranslClientOption() {}
