// Package client provides a generic access layer for data available in system
package client

import (
	"context"
	"flag"
	"fmt"
	"reflect"
	"runtime"
	"strings"
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
	DELETE     int = 0
	REPLACE    int = 1
	UPDATE     int = 2
	maxWorkers     = 2 // max workers for parallel processing of Get/Subscribe requests
)

var useParallelProcessing = flag.Bool("use_parallel_processing", false, "use parallel processing for GET/SUBSCRIBE requests")
var translProcessGetFunc = transutil.TranslProcessGet
var isSubscribeSupportedFunc = translib.IsSubscribeSupported

type TranslClient struct {
	prefix *gnmipb.Path
	/* GNMI Path to REST URL Mapping */
	path2URI map[*gnmipb.Path]string
	channel  chan struct{}
	q        *queue.PriorityQueue
	superSub *superSubscription

	synced     sync.WaitGroup  // Control when to send gNMI sync_response
	w          *sync.WaitGroup // wait for all sub go routines to finish
	mu         sync.RWMutex    // Mutex for data protection among routines for transl_client
	ctx        context.Context //Contains Auth info and request info
	wakeChan   chan bool       // wakeChan is used to wake up the client to notify it is the new primary client.
	extensions []*gnmi_extpb.Extension

	version  *translib.Version // Client version; populated by parseVersion()
	encoding gnmipb.Encoding
}
type pathFromGetReq struct {
	path *gnmipb.Path
	uri  string
	c    *TranslClient
}

type pathFromSubReq struct {
	path string
	ts   *translSubscriber
}

func findMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func joinErrorsWithNewline(errs []error) error {
	//Length of 'err' validation is done before func call.
	var errStrings []string
	for _, err := range errs {
		if err != nil {
			errStrings = append(errStrings, err.Error())
		}
	}
	if len(errStrings) == 0 {
		return nil // Return actual nil, not an empty error object
	}
	// Using "\n" matches the visual output of errors.Join
	return fmt.Errorf("%s", strings.Join(errStrings, "\n"))
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
	client.wakeChan = make(chan bool, 1)

	if err != nil {
		return nil, err
	} else {
		return &client, nil
	}
}

func (c *TranslClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	rc, ctx := common_utils.GetContext(c.ctx)
	c.ctx = ctx
	var errs []error
	var values []*spb.Value
	ts := time.Now()

	version := getBundleVersion(c.extensions)
	if version != nil {
		rc.BundleVersion = version
	}
	// Iterate through all GNMI paths in parallel. The max number of workers is equal to
	// the number of Openconfig modules in a root query.
	numPaths := len(c.path2URI)
	pathChan := make(chan pathFromGetReq, numPaths)
	valueChan := make(chan *spb.Value, numPaths)
	errChan := make(chan error, numPaths)
	workerWg := &sync.WaitGroup{}
	readerWg := &sync.WaitGroup{}

	numWorkers := 1
	if *useParallelProcessing {
		numWorkers = findMin(numPaths, maxWorkers)
	}

	// Spawn workers to process the paths in the GET request.
	for i := 0; i < numWorkers; i++ {
		workerWg.Add(1)
		go processGetWorker(pathChan, valueChan, errChan, workerWg)
	}

	// Send the paths in the GET request to the workers through the channel.
	for gnmiPath, URIPath := range c.path2URI {
		pathChan <- pathFromGetReq{path: gnmiPath, uri: URIPath, c: c}
	}
	close(pathChan)

	// Start goroutines to read the response values and errors from the workers.
	readerWg.Add(2)
	go func() {
		defer readerWg.Done()
		for value := range valueChan {
			values = append(values, value)
		}
	}()
	go func() {
		defer readerWg.Done()
		for err := range errChan {
			errs = append(errs, err)
		}
	}()

	// Wait for all goroutines to finish.
	workerWg.Wait()
	close(valueChan)
	close(errChan)
	readerWg.Wait()

	if len(errs) != 0 {
		return nil, joinErrorsWithNewline(errs)
	}

	/* The values structure at the end is returned and then updates in notitications as
	specified in the proto file in the server.go */

	log.V(6).Infof("TranslClient : Getting #%v", values)
	log.V(4).Infof("TranslClient :Get done, total time taken: %v ms", int64(time.Since(ts)/time.Millisecond))

	return values, nil
}

// processGetWorker is a worker function to remove paths from a GET request off of the channel and process them.
func processGetWorker(pathChan <-chan pathFromGetReq, valueChan chan<- *spb.Value, errChan chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	for path := range pathChan {
		log.Infof("getWorker processing path: %v", path)
		val, resp, err := translProcessGetFunc(path.uri, nil, path.c.ctx, path.c.encoding)
		if err != nil {
			errChan <- err
			return
		}

		var valueTree ygot.ValidatedGoStruct
		if resp != nil {
			valueTree = resp.ValueTree
		}
		v, err := buildValueForGet(path.c.prefix, path.path, path.c.encoding, val, valueTree)
		if err != nil {
			errChan <- err
			return
		}

		valueChan <- v
	}
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
	interval  int
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

func tickerCleanup(tickers map[int]*time.Ticker) {
	for _, ticker := range tickers {
		ticker.Stop()
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
	intervalToTickerInfoMap := make(map[int][]*ticker_info)

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

	subSupport, err := isSubscribeSupportedFunc(req)
	if err != nil {
		enqueFatalMsgTranslib(c, fmt.Sprintf("Subscribe operation failed with error =%v", err.Error()))
		return
	}

	numSubWorkers := 1
	if *useParallelProcessing {
		numSubWorkers = findMin(len(subscribe.Subscription), maxWorkers)
	}

	var onChangeSubsString []string
	// Spawn routines to process the Sample subscriptions
	wg := &sync.WaitGroup{}
	subChan := make(chan pathFromSubReq, len(subscribe.Subscription))
	for i := 0; i < numSubWorkers; i++ {
		wg.Add(1)
		go processSubWorker(subChan, wg)
	}

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
				close(subChan)
				return
			}
		case gnmipb.SubscriptionMode_SAMPLE:
			subscribe_mode = gnmipb.SubscriptionMode_SAMPLE
		default:
			enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid Subscription Mode %d", sub.Mode))
			close(subChan)
			return
		}

		if pInfo.MinInterval <= 0 { // should not happen
			pInfo.MinInterval = translib.MinSubscribeInterval
		}

		if hb := sub.HeartbeatInterval; hb > 0 && hb < uint64(pInfo.MinInterval)*uint64(time.Second) {
			enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid Heartbeat Interval %ds, minimum interval is %ds",
				sub.HeartbeatInterval/uint64(time.Second), subSupport[i].MinInterval))
			close(subChan)
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
				close(subChan)
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
			// Add the path to the channel to be processed in parallel
			subChan <- pathFromSubReq{path: pathStr, ts: &ts}

			addTimer(intervalToTickerInfoMap, interval, sub, pathStr, false)
			//Heartbeat intervals are valid for SAMPLE in the case suppress_redundant is specified
			if sub.SuppressRedundant && sub.HeartbeatInterval > 0 {
				addTimer(intervalToTickerInfoMap, int(sub.HeartbeatInterval), sub, pathStr, true)
			}
		} else if subscribe_mode == gnmipb.SubscriptionMode_ON_CHANGE {
			onChangeSubsString = append(onChangeSubsString, pathStr)
			if sub.HeartbeatInterval > 0 {
				addTimer(intervalToTickerInfoMap, int(sub.HeartbeatInterval), sub, pathStr, true)
			}
		}
		log.V(6).Infof("End Sub: %v", sub)
	}
	close(subChan)

	if len(onChangeSubsString) > 0 {
		ts := translSubscriber{
			client:     c,
			session:    ss,
			filterMsgs: subscribe.UpdatesOnly,
			sampleWg:   wg,
		}
		ts.doOnChange(onChangeSubsString)
	} else {
		// If at least one ON_CHANGE subscription was present, then
		// ts.doOnChange() would have sent the sync message.
		// Explicitly send one here if all are SAMPLE subscriptions.
		wg.Wait()
		enqueueSyncMessage(c)
	}

	// Add the subscription to a SuperSubscrption.
	c.addClientToSuperSubscription(subscribe)
	defer c.leaveSuperSubscription()

	// Get tickers and cases to do samples.
	err = c.superSub.populateTickers(intervalToTickerInfoMap)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to fetch shared tickers from Super Subscription: %v", err)
		log.V(0).Info(errMsg)
		enqueFatalMsgTranslib(c, errMsg)
		return
	}
	cases, caseIndexToIntervalMap := buildSelectCases(intervalToTickerInfoMap, c.channel)

	// Non-primary clients do not need to listen for ticks unless they're woken up by the Super Subscription.
	for !c.isPrimary() {
		select {
		case <-c.wakeChan:
			log.V(2).Infof("Secondary client (%p) waking up: isPrimary=%v, tickers=%v", c, c.isPrimary(), intervalToTickerInfoMap)
			continue
		case <-c.channel:
			log.V(2).Infof("Secondary client (%p) received close signal", c)
			return
		}
	}

	for {
		chosen, _, ok := reflect.Select(cases)
		if !ok || chosen >= len(caseIndexToIntervalMap) || c.q == nil || c.q.Disposed() {
			log.V(2).Infof("TranslClient (%p) exiting StreamRun because an exit signal was received!", c)
			return
		}

		// Start goroutines to process the Sample.
		ticks := intervalToTickerInfoMap[caseIndexToIntervalMap[chosen]]
		tickSubChan := make(chan pathFromSubReq, len(ticks))
		numTickWorkers := 1
		if *useParallelProcessing {
			numTickWorkers = findMin(len(ticks), maxWorkers)
		}
		for i := 0; i < numTickWorkers; i++ {
			wg.Add(1)
			go processSubWorker(tickSubChan, wg)
		}
		for _, tick := range ticks {
			log.V(6).Infof("tick, heartbeat: %t, path: %s\n", tick.heartbeat, c.path2URI[tick.sub.Path])
			ts := translSubscriber{
				client:      c,
				session:     ss,
				sampleCache: sampleCache[tick.pathStr],
				filterDups:  (!tick.heartbeat && tick.sub.SuppressRedundant),
				sampleWg:    wg,
			}
			tickSubChan <- pathFromSubReq{path: tick.pathStr, ts: &ts}
		}
		close(tickSubChan)
		wg.Wait()
	}
}

func processSubWorker(subChan <-chan pathFromSubReq, wg *sync.WaitGroup) {
	defer wg.Done()
	for sub := range subChan {
		sub.ts.doSample(sub.path)
	}
}

// addTimer adds a new ticker_info with the given interval, sub, and heartbeat to the intervalToTickerInfoMap.
func addTimer(intervalToTickerInfoMap map[int][]*ticker_info, interval int, sub *gnmipb.Subscription, pathStr string, heartbeat bool) {
	if tickers, ok := intervalToTickerInfoMap[interval]; ok && tickers != nil {
		tickers = append(tickers, &ticker_info{
			sub:       sub,
			pathStr:   pathStr,
			interval:  interval,
			heartbeat: heartbeat,
		})
		intervalToTickerInfoMap[interval] = tickers
	} else {
		intervalToTickerInfoMap[interval] = []*ticker_info{{
			sub:       sub,
			pathStr:   pathStr,
			interval:  interval,
			heartbeat: heartbeat,
		}}
	}
}

// buildSelectCases takes in the intervalToTickerInfoMap and a close channel and returns the cases and caseIndexToIntervalMap that include
// those ticker channels.
// - cases is a slice of SelectCase used to call select across all the ticker and close channels.
// - caseIndexToIntervalMap is a map from ticker index within the cases slice to the corresponding interval. It can be used to
// associate an interval with the chosen case during the select operation.
func buildSelectCases(intervalToTickerInfoMap map[int][]*ticker_info, closeChan <-chan struct{}) ([]reflect.SelectCase, map[int]int) {
	cases := make([]reflect.SelectCase, 0)
	caseIndexToIntervalMap := make(map[int]int)

	for interval, tickers := range intervalToTickerInfoMap {
		if len(tickers) < 1 {
			continue
		}
		ticker := tickers[0]
		if ticker == nil {
			log.V(0).Infof("Failed to build select case for interval %v because ticker is nil!", interval)
			continue
		}
		caseIndexToIntervalMap[len(cases)] = interval
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ticker.t.C)})
	}
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(closeChan)})
	return cases, caseIndexToIntervalMap
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
		// Spawn worker threads to process the subscription
		numPaths := len(c.path2URI)
		wg := &sync.WaitGroup{}
		subChan := make(chan pathFromSubReq, numPaths)
		numWorkers := 1
		if *useParallelProcessing {
			numWorkers = findMin(numPaths, maxWorkers)
		}
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go processSubWorker(subChan, wg)
		}
		for _, gnmiPath := range c.path2URI {
			if synced || !subscribe.UpdatesOnly {
				ts := translSubscriber{client: c}
				subChan <- pathFromSubReq{path: gnmiPath, ts: &ts}
			}
		}
		close(subChan)
		wg.Wait()

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
	// Spawn worker threads to process the subscription
	numPaths := len(c.path2URI)
	wg := &sync.WaitGroup{}
	subChan := make(chan pathFromSubReq, numPaths)
	numWorkers := 1
	if *useParallelProcessing {
		numWorkers = findMin(numPaths, maxWorkers)
	}
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go processSubWorker(subChan, wg)
	}
	for _, gnmiPath := range c.path2URI {
		ts := translSubscriber{client: c}
		subChan <- pathFromSubReq{path: gnmiPath, ts: &ts}
	}
	close(subChan)
	wg.Wait()
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

// setPrefixTarget fills prefix taregt for given Notification objects.
func setPrefixTarget(notifs []*gnmipb.Notification, target string) {
	for _, n := range notifs {
		if n.Prefix == nil {
			n.Prefix = &gnmipb.Path{Target: target}
		} else {
			n.Prefix.Target = target
		}
	}
}

// Creates a spb.Value out of data from the translib according to the requested encoding.
func buildValue(prefix *gnmipb.Path, path *gnmipb.Path, enc gnmipb.Encoding,
	typedVal *gnmipb.TypedValue, valueTree ygot.ValidatedGoStruct) (*spb.Value, error) {

	switch enc {
	case gnmipb.Encoding_JSON, gnmipb.Encoding_JSON_IETF:
		if typedVal == nil {
			return nil, nil
		}
		return &spb.Value{
			Prefix:       prefix,
			Path:         path,
			Timestamp:    time.Now().UnixNano(),
			SyncResponse: false,
			Val:          typedVal,
		}, nil

	case gnmipb.Encoding_PROTO:
		if valueTree == nil {
			return nil, nil
		}

		fullPath := transutil.GnmiTranslFullPath(prefix, path)
		removeLastPathElem(fullPath)
		notifications, err := ygot.TogNMINotifications(
			valueTree,
			time.Now().UnixNano(),
			ygot.GNMINotificationsConfig{
				UsePathElem:    true,
				PathElemPrefix: fullPath.GetElem(),
			})
		if err != nil {
			return nil, fmt.Errorf("Cannot convert OC Struct to Notifications: %s", err)
		}
		if len(notifications) != 1 {
			return nil, fmt.Errorf("YGOT returned wrong number of notifications")
		}
		if len(prefix.Target) != 0 {
			// Copy target from reqest.. ygot.TogNMINotifications does not fill it.
			setPrefixTarget(notifications, prefix.Target)
		}
		return &spb.Value{
			Notification: notifications[0],
		}, nil
	default:
		return nil, fmt.Errorf("Unsupported Encoding %s", enc)
	}
}

// buildValueForGet generates a spb.Value for GetRequest.
// Besides the same function as buildValue, it generates spb.Value for nil value as well.
// Return: spb.Value is guaranteed not nil when error is nil.
func buildValueForGet(prefix *gnmipb.Path, path *gnmipb.Path, enc gnmipb.Encoding,
	typedVal *gnmipb.TypedValue, valueTree ygot.ValidatedGoStruct) (*spb.Value, error) {

	if spv, err := buildValue(prefix, path, enc, typedVal, valueTree); err != nil || spv != nil {
		return spv, err
	}

	// spv is nil. Server needs to generate Notification for GetRequest.
	switch enc {
	case gnmipb.Encoding_JSON, gnmipb.Encoding_JSON_IETF:
		return &spb.Value{
			Prefix:       prefix,
			Path:         path,
			Timestamp:    time.Now().UnixNano(),
			SyncResponse: false,
			Val:          &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte("{}")}},
		}, nil

	case gnmipb.Encoding_PROTO:
		// The Notification has no update and its prefix is the full path.
		fullPath := transutil.GnmiTranslFullPath(prefix, path)
		return &spb.Value{
			Notification: &gnmipb.Notification{Prefix: fullPath, Timestamp: time.Now().UnixNano()},
		}, nil

	default:
		return nil, fmt.Errorf("Unsupported Encoding %s", enc)
	}
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

func SetUseParallelProcessing(val bool) {
	*useParallelProcessing = val
}

type TranslClientOption interface {
	IsTranslClientOption()
}

type TranslWildcardOption struct{}

func (t TranslWildcardOption) IsTranslClientOption() {}
