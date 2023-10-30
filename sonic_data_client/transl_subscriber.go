////////////////////////////////////////////////////////////////////////////////
//                                                                            //
//  Copyright 2021 Broadcom. The term Broadcom refers to Broadcom Inc. and/or //
//  its subsidiaries.                                                         //
//                                                                            //
//  Licensed under the Apache License, Version 2.0 (the "License");           //
//  you may not use this file except in compliance with the License.          //
//  You may obtain a copy of the License at                                   //
//                                                                            //
//     http://www.apache.org/licenses/LICENSE-2.0                             //
//                                                                            //
//  Unless required by applicable law or agreed to in writing, software       //
//  distributed under the License is distributed on an "AS IS" BASIS,         //
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.  //
//  See the License for the specific language governing permissions and       //
//  limitations under the License.                                            //
//                                                                            //
////////////////////////////////////////////////////////////////////////////////

package client

import (
	"fmt"
	"sync"
	"time"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"github.com/sonic-net/sonic-gnmi/transl_utils"
)

// translSubscriber is an extension of TranslClient to service Subscribe RPC.
type translSubscriber struct {
	client      *TranslClient
	session     *translib.SubscribeSession
	sampleCache *ygotCache      // session cache for SAMPLE; optional
	filterMsgs  bool            // Filter out messages till sync done (updates_only)
	filterDups  bool            // Filter out duplicate updates (suppress_redundant)
	stopOnSync  bool            // Stop upon sync message from translib
	synced      sync.WaitGroup  // To signal receipt of sync message from translib
	rcvdPaths   map[string]bool // Paths from received SubscribeResponse
	msgBuilder  notificationBuilder
}

// notificationBuilder creates gnmipb.Notification from a translib.SubscribeResponse
// instance. Input can be nil, indicating the end of current sample iteration.
type notificationBuilder func(
	resp *translib.SubscribeResponse, ts *translSubscriber) (*gnmipb.Notification, error)

// doSample invokes translib.Stream API to service SAMPLE, POLL and ONCE subscriptions.
// Timer for SAMPLE subscription should be managed outside.
func (ts *translSubscriber) doSample(path string) {
	if ts.sampleCache != nil {
		ts.msgBuilder = ts.sampleCache.msgBuilder // SAMPLE
		ts.rcvdPaths = make(map[string]bool)
	} else {
		ts.msgBuilder = defaultMsgBuilder // ONCE, POLL or heartbeat for ON_CHANGE
	}

	c := ts.client
	req := translib.SubscribeRequest{
		Paths:   []string{path},
		Q:       queue.NewPriorityQueue(1, false),
		Session: ts.session,
	}
	if c.version != nil {
		req.ClientVersion = *c.version
	}

	c.w.Add(1)
	ts.synced.Add(1)
	ts.stopOnSync = true
	go ts.processResponses(req.Q)

	err := translib.Stream(req)
	if err != nil {
		req.Q.Dispose()
		enqueFatalMsgTranslib(c, fmt.Sprintf("Subscribe operation failed with error = %v", err))
	}

	ts.synced.Wait()
}

// doOnChange handles the ON_CHANGE subscriptions through translib.Subscribe API.
// Returns only after initial updates and sync message are sent to the RPC queue.
func (ts *translSubscriber) doOnChange(stringPaths []string) {
	c := ts.client
	q := queue.NewPriorityQueue(1, false)

	req := translib.SubscribeRequest{
		Paths:   stringPaths,
		Q:       q,
		Stop:    c.channel,
		Session: ts.session,
	}
	if c.version != nil {
		req.ClientVersion = *c.version
	}

	c.w.Add(1)
	ts.synced.Add(1)
	ts.msgBuilder = defaultMsgBuilder
	go ts.processResponses(q)

	err := translib.Subscribe(req)
	if err != nil {
		q.Dispose()
		enqueFatalMsgTranslib(c, "Subscribe operation failed with error: "+err.Error())
	}

	ts.synced.Wait()
}

// processResponses waits for SubscribeResponse messages from translib over a
// queue, formats them as spb.Value and pushes to the RPC queue.
func (ts *translSubscriber) processResponses(q *queue.PriorityQueue) {
	c := ts.client
	var syncDone bool
	defer c.w.Done()
	defer func() {
		if !syncDone {
			ts.synced.Done()
		}
	}()
	defer recoverSubscribe(c)

	for {
		items, err := q.Get(1)
		if err == queue.ErrDisposed {
			log.V(3).Info("PriorityQueue was disposed!")
			return
		}
		if err != nil {
			enqueFatalMsgTranslib(c, fmt.Sprintf("Subscribe operation failed with error =%v", err.Error()))
			return
		}
		switch v := items[0].(type) {
		case *translib.SubscribeResponse:

			if v.IsTerminated {
				//DB Connection or other backend error
				enqueFatalMsgTranslib(c, "DB Connection Error")
				close(c.channel)
				return
			}

			if v.SyncComplete {
				if ts.stopOnSync {
					ts.notify(nil)
					log.V(6).Infof("Stopping on sync signal from translib")
					return
				}

				log.V(6).Infof("SENDING SYNC")
				enqueueSyncMessage(c)
				syncDone = true
				ts.synced.Done()
				ts.filterMsgs = false
				break
			}

			if err := ts.notify(v); err != nil {
				log.Warning(err)
				enqueFatalMsgTranslib(c, "Internal error")
				return
			}
		default:
			log.V(1).Infof("Unknown data type %T in queue", v)
		}
	}
}

func (ts *translSubscriber) notify(v *translib.SubscribeResponse) error {
	msg, err := ts.msgBuilder(v, ts)
	if err != nil {
		return err
	}

	if msg == nil || (len(msg.Update) == 0 && len(msg.Delete) == 0) {
		log.V(6).Infof("Ignore nil message")
		return nil
	}

	spbv := &spb.Value{Notification: msg}
	ts.client.q.Put(Value{spbv})
	log.V(6).Infof("Added spbv %#v", spbv)
	return nil
}

func (ts *translSubscriber) toPrefix(path string) *gnmipb.Path {
	p, _ := ygot.StringToStructuredPath(path)
	p.Target = ts.client.prefix.GetTarget()
	p.Origin = ts.client.prefix.GetOrigin()
	return p
}

func defaultMsgBuilder(v *translib.SubscribeResponse, ts *translSubscriber) (*gnmipb.Notification, error) {
	if v == nil {
		return nil, nil
	}
	if ts.filterMsgs {
		log.V(2).Infof("Msg suppressed due to updates_only")
		return nil, nil
	}

	p := ts.toPrefix(v.Path)
	n := gnmipb.Notification{
		Prefix:    p,
		Timestamp: v.Timestamp,
	}

	// Move last elem of v.Path to updates & deletes if v.Delete contains
	// an empty path (to indicate the v.Path itself was deleted).
	var extraPrefix *gnmipb.PathElem
	if strSliceContains(v.Delete, "") {
		extraPrefix = removeLastPathElem(p)
	}

	if v.Update != nil {
		var err error
		n.Update, err = ts.ygotToScalarValues(extraPrefix, v.Update)
		if err != nil {
			return nil, err
		}
	}

	for _, del := range v.Delete {
		p, err := ygot.StringToStructuredPath(del)
		if err != nil {
			return nil, err
		}
		insertFirstPathElem(p, extraPrefix)
		n.Delete = append(n.Delete, p)
	}

	return &n, nil
}

// ygotToScalarValues returns scalar encoded values for a ygot object.
// If prefixElem is provided, it will be prefixed to each value's path.
func (ts *translSubscriber) ygotToScalarValues(prefixElem *gnmipb.PathElem, obj ygot.ValidatedGoStruct) ([]*gnmipb.Update, error) {
	tmp, err := ygot.TogNMINotifications(obj, 0,
		ygot.GNMINotificationsConfig{
			UsePathElem:    true,
			PathElemPrefix: nil,
			Encoding:       ts.client.encoding,
		})
	if err != nil {
		return nil, err
	}

	updates := tmp[0].Update
	if prefixElem != nil {
		for _, u := range updates {
			insertFirstPathElem(u.Path, prefixElem)
		}
	}

	return updates, nil
}

// insertFirstPathElem inserts newElem at the beginning of path p.
func insertFirstPathElem(p *gnmipb.Path, newElem *gnmipb.PathElem) {
	if newElem != nil {
		ne := make([]*gnmipb.PathElem, 0, len(p.Elem)+1)
		ne = append(ne, newElem)
		p.Elem = append(ne, p.Elem...)
	}
}

// removeLastPathElem removes the last PathElem from the path p.
// Returns the removed element.
func removeLastPathElem(p *gnmipb.Path) *gnmipb.PathElem {
	k := len(p.Elem) - 1
	if k < 0 {
		return nil
	}
	if p.Element != nil {
		p.Element = p.Element[:k]
	}
	last := p.Elem[k]
	p.Elem = p.Elem[:k]
	return last
}

func strSliceContains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// ygotCache holds path to ygot struct mappings
type ygotCache struct {
	values  map[string]ygot.GoStruct
	pattern *gnmipb.Path // Prefix pattern for the cache keys
}

// newYgotCache creates a new ygotCache instance
func newYgotCache(pattern *gnmipb.Path) *ygotCache {
	return &ygotCache{
		values:  make(map[string]ygot.GoStruct),
		pattern: pattern,
	}
}

// msgBuilder is a notificationBuilder implementation to create a gnmipb.Notification
// message by comparing the SubscribeResponse.Update ygot struct to the cached value.
// Includes only modified or deleted leaf paths if translSubscriber.filterDups is set.
// Returns nil message if translSubscriber.filterMsgs is set or on error.
// Updates the cache with the new ygot struct (SubscribeResponse.Update).
// Special case: if SubscribeResponse is nil, calls deleteMsgBuilder to delete
// non-existing paths from the cache.
func (c *ygotCache) msgBuilder(v *translib.SubscribeResponse, ts *translSubscriber) (*gnmipb.Notification, error) {
	if v == nil {
		return c.deleteMsgBuilder(ts)
	}

	old := c.values[v.Path]
	c.values[v.Path] = v.Update
	ts.rcvdPaths[v.Path] = true
	log.V(2).Infof("%s updated; old=%p, new=%p, filterDups=%v", v.Path, old, v.Update, ts.filterDups)
	if ts.filterMsgs {
		log.V(2).Infof("Msg suppressed due to updates_only")
		return nil, nil
	}

	res, err := transl_utils.Diff(old, v.Update,
		transl_utils.DiffOptions{
			RecordAll: !ts.filterDups,
		})
	if err != nil {
		return nil, err
	}

	return &gnmipb.Notification{
		Timestamp: v.Timestamp,
		Prefix:    ts.toPrefix(v.Path),
		Update:    res.Update,
		Delete:    res.Delete,
	}, nil
}

// deleteMsgBuilder deletes the cache entries whose path does not appear in
// the translSubscriber.rcvdPaths map. Creates a gnmipb.Notification message
// for the deleted paths. Returns nil message if there are no such delete paths
// or translSubscriber.filterMsgs is set.
func (c *ygotCache) deleteMsgBuilder(ts *translSubscriber) (*gnmipb.Notification, error) {
	if ts.filterMsgs {
		log.V(2).Infof("Msg suppressed due to updates_only")
		return nil, nil
	}
	var deletePaths []*gnmipb.Path
	for path := range c.values {
		if !ts.rcvdPaths[path] {
			log.V(3).Infof("%s deleted", path)
			deletePaths = append(deletePaths, c.toDeletePath(path))
			delete(c.values, path)
		}
	}
	if len(deletePaths) == 0 {
		return nil, nil
	}
	return &gnmipb.Notification{
		Timestamp: time.Now().UnixNano(),
		Prefix:    ts.toPrefix("/"),
		Delete:    deletePaths,
	}, nil
}

func (c *ygotCache) toDeletePath(path string) *gnmipb.Path {
	p, _ := ygot.StringToStructuredPath(path)
	// p will be parent container path when subscribing to a leaf.
	// Append the leaf suffix to p if p is shorter than subscribe path.
	if n := len(p.Elem); n < len(c.pattern.Elem) {
		suffix := c.pattern.Elem[n:]
		p.Elem = append(p.Elem, suffix...)
	}
	return p
}
