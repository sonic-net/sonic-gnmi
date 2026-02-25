package client

import (
	"fmt"
	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"sync"
	"sync/atomic"
	"time"
)

var superSubs = superSubscriptions{
	mu:   &sync.Mutex{},
	subs: map[*superSubscription]bool{},
}

type superSubscriptions struct {
	mu   *sync.Mutex
	subs map[*superSubscription]bool
}

// superSubscription is used to deduplicate subscriptions. Stream Subscriptions
// become part of a superSubscription and whenever a Sample is processed, the
// response is sent to all clients that are part of the superSubscription.
type superSubscription struct {
	mu               *sync.RWMutex
	clients          map[*TranslClient]struct{}
	request          *gnmipb.SubscriptionList
	primaryClient    *TranslClient
	tickers          map[int]*time.Ticker // map of interval duration (nanoseconds) to ticker.
	sharedUpdates    atomic.Uint64
	exclusiveUpdates atomic.Uint64
}

// ------------- Super Subscription Functions -------------
// createSuperSubscription takes a SubscriptionList and returns a new
// superSubscription for that SubscriptionList. This function expects the
// caller to already hold superSubs.mu before calling createSuperSubscription.
func createSuperSubscription(subscription *gnmipb.SubscriptionList) *superSubscription {
	if subscription == nil {
		return nil
	}
	newSuperSub := &superSubscription{
		mu:               &sync.RWMutex{},
		clients:          map[*TranslClient]struct{}{},
		request:          subscription,
		primaryClient:    nil,
		tickers:          map[int]*time.Ticker{},
		sharedUpdates:    atomic.Uint64{},
		exclusiveUpdates: atomic.Uint64{},
	}
	if _, ok := superSubs.subs[newSuperSub]; ok {
		// This should never happen.
		log.V(0).Infof("Super Subscription (%p) for %v already exists but a new has been created!", newSuperSub, subscription)
	}
	superSubs.subs[newSuperSub] = true
	return newSuperSub
}

// findSuperSubscription takes a SubscriptionList and tries to find an
// existing superSubscription for that SubscriptionList. If one is found,
// the superSubscription is returned. Else, nil is returned. This function
// expects the caller to already hold superSubs.mu before calling findSuperSubscription.
func findSuperSubscription(subscription *gnmipb.SubscriptionList) *superSubscription {
	if subscription == nil {
		return nil
	}
	for sub, _ := range superSubs.subs {
		if sub.request == nil {
			continue
		}
		if proto.Equal(sub.request, subscription) {
			return sub
		}
	}
	return nil
}

// deleteSuperSub removes superSub from the superSubs map.
// If the superSub is removed from the TranslClient, there
// should be no remaining references to the superSub. This
// function expects the caller to already hold superSubs.mu
// before calling deleteSuperSubscription.
func deleteSuperSubscription(superSub *superSubscription) {
	if superSub == nil {
		log.V(0).Info("deleteSuperSubscription called on a nil Super Subscription!")
		return
	}
	tickerCleanup(superSub.tickers)
	delete(superSubs.subs, superSub)
}

// ------------- Super Subscription Methods -------------
// sendNotifications takes a value and adds it to the notification
// queue for each subscription in the superSubscription.
func (ss *superSubscription) sendNotifications(v *spb.Value) {
	if v == nil {
		return
	}
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	for client, _ := range ss.clients {
		value := proto.Clone(v).(*spb.Value)
		client.q.Put(Value{value})
	}
}

// populateTickers populates the ticker_info objects in the intervalToTickerInfoMap with the
// shared tickers. If tickers don't exist yet, they are created.
func (ss *superSubscription) populateTickers(intervalToTickerInfoMap map[int][]*ticker_info) error {
	if intervalToTickerInfoMap == nil {
		return fmt.Errorf("Invalid intervalToTickerInfoMap passed in: %v", intervalToTickerInfoMap)
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if len(ss.tickers) == 0 {
		// Create the tickers.
		for interval, tInfos := range intervalToTickerInfoMap {
			ticker := time.NewTicker(time.Duration(interval) * time.Nanosecond)
			ss.tickers[interval] = ticker
			for _, tInfo := range tInfos {
				tInfo.t = ticker
			}
		}
		return nil
	}
	// Use the existing tickers.
	if len(ss.tickers) != len(intervalToTickerInfoMap) {
		return fmt.Errorf("Length of intervalToTickerInfoMap does not match length of existing tickers for Super Subscription! existing tickers=%v, intervalToTickerInfoMap=%v", ss.tickers, intervalToTickerInfoMap)
	}
	for interval, tInfos := range intervalToTickerInfoMap {
		ticker, ok := ss.tickers[interval]
		if !ok {
			return fmt.Errorf("Interval in intervalToTickerInfoMap not found in existing tickers for Super Subscription! interval=%v", interval)
		}
		for _, tInfo := range tInfos {
			tInfo.t = ticker
		}
	}
	return nil
}
func (ss *superSubscription) String() string {
	return fmt.Sprintf("[{%p} NumClients=%d, SharedUpdates=%d, ExclusiveUpdates=%d, Request=%v]", ss, len(ss.clients), ss.sharedUpdates.Load(), ss.exclusiveUpdates.Load(), ss.request)
}

// ------------- TranslClient Methods -------------
// isPrimary returns true if the client is the primary client of its superSubscription.
func (c *TranslClient) isPrimary() bool {
	if c == nil || c.superSub == nil {
		return false
	}
	c.superSub.mu.RLock()
	defer c.superSub.mu.RUnlock()
	return c.superSub.primaryClient == c
}

// leaveSuperSubscription removes the client from the superSubscription.
// If there are no remaining clients in the superSubscription, it is deleted.
func (c *TranslClient) leaveSuperSubscription() {
	if c == nil || c.superSub == nil {
		return
	}
	superSubs.mu.Lock()
	defer superSubs.mu.Unlock()
	c.superSub.mu.Lock()
	defer c.superSub.mu.Unlock()
	delete(c.superSub.clients, c)
	if len(c.superSub.clients) == 0 {
		deleteSuperSubscription(c.superSub)
		log.V(2).Infof("SuperSubscription (%s) closing!", c.superSub)
	} else if c.superSub.primaryClient == c {
		// Set a new primary client.
		for client := range c.superSub.clients {
			c.superSub.primaryClient = client
			client.wakeChan <- true
			break
		}
		log.V(2).Infof("SuperSubscription (%s): %p is now the primary client", c.superSub, c.superSub.primaryClient)
	}
}

// addClientToSuperSubscription adds a client to a superSubscription.
func (c *TranslClient) addClientToSuperSubscription(subscription *gnmipb.SubscriptionList) {
	if c == nil || subscription == nil {
		return
	}
	superSubs.mu.Lock()
	defer superSubs.mu.Unlock()
	superSub := findSuperSubscription(subscription)
	if superSub == nil {
		superSub = createSuperSubscription(subscription)
	}
	superSub.mu.Lock()
	defer superSub.mu.Unlock()
	c.superSub = superSub
	superSub.clients[c] = struct{}{}
	if superSub.primaryClient == nil {
		superSub.primaryClient = c
	}
	log.V(2).Infof("SuperSubscription (%s): added new client=%p", superSub, c)
}
