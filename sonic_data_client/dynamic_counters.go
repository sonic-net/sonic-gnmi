package client

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/redis/go-redis/v9"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"google.golang.org/protobuf/proto"
)

// Only these virtual datasets have dynamic name maps. Keep ordinary Redis paths
// and other virtual datasets on their existing subscription implementations.
func dynamicCounterMap(prefix, path *gnmipb.Path) string {
	db, valid, _, _ := IsTargetDb(prefix.GetTarget())
	if !valid || db != "COUNTERS_DB" || prefix.GetOrigin() != "" || path.GetOrigin() != "" {
		return ""
	}
	elems := gnmiFullPath(prefix, path).GetElem()
	if len(elems) != 2 || elems[0].GetName() != "COUNTERS" || len(path.GetElem()) == 0 {
		return ""
	}
	name := elems[1].GetName()
	if name == "SID*" || strings.HasPrefix(name, "SID:") {
		return "COUNTERS_SRV6_NAME_MAP"
	}
	if name == "ACL_RULE*" || strings.HasPrefix(name, "ACL_RULE:") {
		return "ACL_COUNTER_RULE_MAP"
	}
	return ""
}

// SetSubscriptionContext is called once before starting subscription workers.
func (c *DbClient) SetSubscriptionContext(ctx context.Context) {
	c.subscriptionContext = ctx
}

type dynamicCounterReader struct {
	ctx     context.Context
	cancel  context.CancelFunc
	clients map[string]*redis.Client
	done    chan struct{}
	once    sync.Once
}

func (r *dynamicCounterReader) close() {
	r.once.Do(func() {
		r.cancel()
		for _, client := range r.clients {
			client.Close()
		}
	})
}

func (c *DbClient) prepareDynamicCounters(stop <-chan struct{}) error {
	needed := false
	for path := range c.pathG2S {
		needed = needed || dynamicCounterMap(c.prefix, path) != ""
	}
	if !needed {
		return nil
	}
	clients, err := GetRedisClientsForDb("COUNTERS_DB")
	if err != nil {
		return err
	}
	if len(clients) == 0 {
		return fmt.Errorf("COUNTERS_DB client unavailable")
	}
	parent := c.subscriptionContext
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r := &dynamicCounterReader{ctx: ctx, cancel: cancel, clients: make(map[string]*redis.Client), done: make(chan struct{})}
	for ns, client := range clients {
		opts := *client.Options()
		opts.ContextTimeoutEnabled = true
		opts.MaxRetries = -1
		r.clients[ns] = redis.NewClient(&opts)
	}
	c.dynamic = r
	go func() {
		defer close(r.done)
		select {
		case <-stop:
		case <-ctx.Done():
		}
		r.close()
	}()
	return nil
}

func (r *dynamicCounterReader) snapshot(prefix, path *gnmipb.Path) ([]tablePath, map[string]interface{}, error) {
	_, _, namespace, explicit := IsTargetDb(prefix.GetTarget())
	if explicit {
		return nil, nil, fmt.Errorf("Target having %v namespace is not supported for V2R Dataset", namespace)
	}
	names := []string{"COUNTERS_DB"}
	for _, elem := range gnmiFullPath(prefix, path).GetElem() {
		names = append(names, elem.GetName())
	}
	mapping := make(map[string]string)
	for _, client := range r.clients {
		values, err := client.HGetAll(r.ctx, dynamicCounterMap(prefix, path)).Result()
		if err != nil {
			return nil, nil, &counterMapReadError{err}
		}
		addmap(mapping, values)
	}
	var paths []tablePath
	var err error
	if dynamicCounterMap(prefix, path) == "COUNTERS_SRV6_NAME_MAP" {
		paths, err = srv6SidPaths(names, mapping)
	} else {
		paths, err = aclRulePaths(names, mapping)
	}
	var missing *counterMapEntryMissing
	if errors.As(err, &missing) {
		return nil, map[string]interface{}{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	current := make(map[string]interface{})
	for _, p := range paths {
		client := r.clients[p.dbNamespace]
		if client == nil {
			return nil, nil, fmt.Errorf("COUNTERS_DB client unavailable for namespace %q", p.dbNamespace)
		}
		values, err := client.HGetAll(r.ctx, p.tableName+p.delimitor+p.tableKey).Result()
		if err != nil {
			return nil, nil, err
		}
		if len(values) == 0 {
			continue
		}
		var key *string
		if p.jsonTableKey != "" {
			key = &p.jsonTableKey
		}
		if err := makeJSON_redis(&current, key, nil, values); err != nil {
			return nil, nil, err
		}
	}
	return paths, current, nil
}

// STREAM workers own their snapshots and pubsub connection. Subscribing before
// the first read avoids losing a map change between snapshot and registration.
// SAMPLE re-reads at its configured interval; ON_CHANGE wakes on map changes or
// relevant counter-key events, including keys introduced after stream creation.
func streamDynamicCounters(c *DbClient, path *gnmipb.Path, interval time.Duration, updateOnly bool) {
	defer c.w.Done()
	ctx := c.dynamic.ctx
	synced := false
	defer func() {
		if !synced {
			c.synced.Done()
		}
	}()
	mapName := dynamicCounterMap(c.prefix, path)
	var paths []tablePath
	var events <-chan interface{}
	var pubsub *redis.PubSub
	if interval == 0 {
		rdb := c.dynamic.clients[""]
		if rdb == nil {
			enqueueFatalMsg(c, "COUNTERS_DB client unavailable")
			return
		}
		pubsub = rdb.PSubscribe(ctx, "__keyspace@2__:"+mapName, "__keyspace@2__:COUNTERS:*")
		defer pubsub.Close()
		stopClose := context.AfterFunc(ctx, func() { pubsub.Close() })
		defer stopClose()
	}
	if pubsub != nil {
		pending := map[string]bool{"__keyspace@2__:" + mapName: true, "__keyspace@2__:COUNTERS:*": true}
		for len(pending) > 0 {
			message, err := pubsub.ReceiveTimeout(ctx, time.Second)
			if err != nil {
				enqueueFatalMsg(c, err.Error())
				return
			}
			if sub, ok := message.(*redis.Subscription); ok && sub.Kind == "psubscribe" {
				delete(pending, sub.Channel)
			}
		}
		// Unlike go-redis Channel(), this handoff never times out and drops a
		// queued map event. Backpressure can disconnect a Redis pubsub client;
		// such a receive failure terminates the RPC rather than serving stale data.
		messages := make(chan interface{})
		receiverDone := make(chan struct{})
		receiveCtx, cancelReceive := context.WithCancel(ctx)
		defer func() { cancelReceive(); pubsub.Close(); <-receiverDone }()
		go func() {
			defer close(receiverDone)
			for {
				message, err := pubsub.Receive(receiveCtx)
				if err != nil {
					message = err
				}
				select {
				case messages <- message:
				case <-receiveCtx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}()
		events = messages
	}
	var ticks <-chan time.Time
	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	previous := make(map[string]interface{})
	initial := true
	readAndSend := func() error {
		var err error
		var current map[string]interface{}
		paths, current, err = c.dynamic.snapshot(c.prefix, path)
		if err != nil {
			return err
		}
		if !initial && interval == 0 && reflect.DeepEqual(previous, current) {
			return nil
		}
		deleted := dynamicCounterDeletes(c.prefix, path, previous, current)
		if len(deleted) > 0 {
			if err := c.q.Put(Value{&spb.Value{Prefix: c.prefix, Timestamp: time.Now().UnixNano(), Delete: deleted}}); err != nil {
				return err
			}
		}
		payload := current
		if !initial && updateOnly {
			payload = make(map[string]interface{})
			for key, value := range current {
				if !reflect.DeepEqual(previous[key], value) {
					payload[key] = value
				}
			}
		}
		if !initial || !updateOnly {
			value, err := Msi2TypedValue(payload)
			if err != nil {
				return err
			}
			if err := c.q.Put(Value{&spb.Value{Prefix: c.prefix, Path: path, Timestamp: time.Now().UnixNano(), Val: value}}); err != nil {
				return err
			}
		}
		previous, initial = current, false
		return nil
	}
	if err := readAndSend(); err != nil {
		enqueueFatalMsg(c, err.Error())
		return
	}
	c.synced.Done()
	synced = true
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				enqueueFatalMsg(c, "counter notification channel closed")
				return
			}
			relevant := false
			switch message := event.(type) {
			case error:
				enqueueFatalMsg(c, message.Error())
				return
			case *redis.Subscription:
				// A reconnect acknowledgement requires a fresh snapshot.
				relevant = true
			case *redis.Message:
				relevant = message.Channel == "__keyspace@2__:"+mapName
				for _, p := range paths {
					relevant = relevant || message.Channel == "__keyspace@2__:"+p.tableName+p.delimitor+p.tableKey
				}
			}
			if !relevant {
				continue
			}
		case <-ticks:
		}
		if err := readAndSend(); err != nil {
			enqueueFatalMsg(c, err.Error())
			return
		}
	}
}

// Delete paths name logical counters, never their transient OIDs. In particular,
// rebinding one name to a new OID must not delete the logical counter.
func dynamicCounterDeletes(prefix, path *gnmipb.Path, previous, current map[string]interface{}) []*gnmipb.Path {
	if len(previous) == 0 {
		return nil
	}
	fullPath := gnmiFullPath(prefix, path)
	if !strings.HasSuffix(fullPath.Elem[len(fullPath.Elem)-1].Name, "*") {
		if len(current) == 0 {
			return []*gnmipb.Path{path}
		}
		return nil
	}
	var deleted []*gnmipb.Path
	for key := range previous {
		if _, exists := current[key]; exists {
			continue
		}
		p := proto.Clone(path).(*gnmipb.Path)
		kind := "SID:"
		if dynamicCounterMap(prefix, path) == "ACL_COUNTER_RULE_MAP" {
			kind = "ACL_RULE:"
		}
		p.Elem[len(p.Elem)-1].Name = kind + key
		deleted = append(deleted, p)
	}
	return deleted
}
