package client

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	redis "github.com/redis/go-redis/v9"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func TestDynamicCounters(t *testing.T) {
	for _, kind := range []string{"SID", "ACL_RULE"} {
		for _, mode := range []string{"poll", "sample", "on_change"} {
			for _, specific := range []bool{false, true} {
				for _, updatesOnly := range []bool{false, true} {
					if mode == "poll" && updatesOnly {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/specific=%t/updatesOnly=%t", kind, mode, specific, updatesOnly), func(t *testing.T) {
						sdcfg.Init()
						if err := sdcfg.DbInit(); err != nil {
							t.Fatal(err)
						}
						mr := miniredis.RunT(t)
						rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
						defer rdb.Close()
						old := Target2RedisDb
						Target2RedisDb = map[string]map[string]*redis.Client{"": {"COUNTERS_DB": rdb, "CONFIG_DB": rdb}}
						defer func() { Target2RedisDb = old }()
						name, mapName := "fcbb:bbbb:1::/48", "COUNTERS_SRV6_NAME_MAP"
						if kind == "ACL_RULE" {
							name, mapName = "DATAACL:RULE_1", "ACL_COUNTER_RULE_MAP"
						}
						path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: kind + "*"}}}
						if specific {
							path.Elem[1].Name = kind + ":" + name
						}
						ordinary := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS_PORT_NAME_MAP"}}}
						mr.HSet("COUNTERS_PORT_NAME_MAP", "Ethernet0", "oid:0xport")
						client, err := NewDbClient([]*gnmipb.Path{path, ordinary}, &gnmipb.Path{Target: "COUNTERS_DB"})
						if err != nil {
							t.Fatal(err)
						}
						c := client.(*DbClient)
						original := fmt.Sprintf("%#v", c.pathG2S[path])
						ctx, cancel := context.WithCancel(context.Background())
						c.SetSubscriptionContext(ctx)
						q := queue.NewPriorityQueue(16, false)
						signal := make(chan struct{}, 1)
						var wg sync.WaitGroup
						wg.Add(1)
						if mode == "poll" {
							go c.PollRun(q, signal, &wg, nil)
						} else {
							submode := gnmipb.SubscriptionMode_ON_CHANGE
							if mode == "sample" {
								submode = gnmipb.SubscriptionMode_SAMPLE
							}
							go c.StreamRun(q, signal, &wg, &gnmipb.SubscriptionList{UpdatesOnly: updatesOnly, Subscription: []*gnmipb.Subscription{{Path: path, Mode: submode, SampleInterval: uint64(MinSampleInterval)}, {Path: ordinary, Mode: gnmipb.SubscriptionMode_SAMPLE, SampleInterval: uint64(MinSampleInterval)}}})
						}
						defer func() { cancel(); close(signal); wg.Wait(); q.Dispose() }()
						next := func() Value {
							t.Helper()
							deadline := time.Now().Add(5 * time.Second)
							for q.Empty() && time.Now().Before(deadline) {
								time.Sleep(time.Millisecond)
							}
							if q.Empty() {
								t.Fatal("no subscription response")
							}
							items, err := q.Get(1)
							if err != nil {
								t.Fatal(err)
							}
							return items[0].(Value)
						}
						if mode == "poll" {
							signal <- struct{}{}
						}
						ordinaryUpdates := 0
						dynamicInitial := 0
						for {
							v := next()
							if v.GetPath() == ordinary {
								ordinaryUpdates++
							}
							if v.GetPath() == path {
								dynamicInitial++
							}
							if v.GetSyncResponse() {
								break
							}
						}
						if ordinaryUpdates != 1 {
							t.Fatal("mixed ordinary path not read")
						}
						if mode != "poll" && ((updatesOnly && dynamicInitial != 0) || (!updatesOnly && dynamicInitial != 1)) {
							t.Fatalf("initial dynamic updates=%d", dynamicInitial)
						}
						for _, oid := range []string{"oid:0x1", "oid:0x2"} {
							mr.HSet("COUNTERS:"+oid, "SAI_COUNTER_STAT_PACKETS", oid)
							mr.HSet(mapName, name, oid)
							if mode == "poll" {
								signal <- struct{}{}
							}
							if mode == "on_change" {
								mr.Publish("__keyspace@2__:"+mapName, "hset")
							}
							for {
								value := next()
								if value.GetPath() == ordinary {
									continue
								}
								if value.GetFatal() != "" {
									t.Fatal(value.GetFatal())
								}
								if len(value.GetDelete()) != 0 {
									t.Fatal("OID replacement generated false delete")
								}
								if strings.Contains(string(value.GetVal().GetJsonIetfVal()), oid) {
									break
								}
							}
							if mode == "poll" {
								for {
									if next().GetSyncResponse() {
										break
									}
								}
							}
						}
						// Rebind with identical values, then update only the new OID. The
						// worker must switch targets even if it suppresses the identical snapshot.
						if mode == "on_change" {
							for i := 0; i < 1000; i++ {
								mr.Publish("__keyspace@2__:COUNTERS:unrelated", "hset")
							}
						}
						mr.HSet("COUNTERS:oid:0x3", "SAI_COUNTER_STAT_PACKETS", "oid:0x2")
						mr.HSet(mapName, name, "oid:0x3")
						if mode == "on_change" {
							mr.Publish("__keyspace@2__:"+mapName, "hset")
						}
						mr.HSet("COUNTERS:oid:0x3", "SAI_COUNTER_STAT_PACKETS", "after-rebind")
						if mode == "on_change" {
							mr.Publish("__keyspace@2__:COUNTERS:oid:0x3", "hset")
						}
						if mode == "poll" {
							signal <- struct{}{}
						}
						for {
							v := next()
							if len(v.GetDelete()) > 0 {
								t.Fatal("same-value OID rebind generated delete")
							}
							if strings.Contains(string(v.GetVal().GetJsonIetfVal()), "after-rebind") {
								break
							}
						}
						if mode == "poll" {
							for {
								if next().GetSyncResponse() {
									break
								}
							}
						}
						// A missing counter hash is a deletion even while its name map remains.
						mr.Del("COUNTERS:oid:0x3")
						if mode == "on_change" {
							mr.Publish("__keyspace@2__:COUNTERS:oid:0x3", "del")
						}
						if mode == "poll" {
							signal <- struct{}{}
						}
						for {
							if len(next().GetDelete()) > 0 {
								break
							}
						}
						if mode == "poll" {
							for {
								if next().GetSyncResponse() {
									break
								}
							}
						}
						mr.HSet("COUNTERS:oid:0x3", "SAI_COUNTER_STAT_PACKETS", "restored")
						if mode == "on_change" {
							mr.Publish("__keyspace@2__:COUNTERS:oid:0x3", "hset")
						}
						if mode == "poll" {
							signal <- struct{}{}
						}
						for {
							if strings.Contains(string(next().GetVal().GetJsonIetfVal()), "restored") {
								break
							}
						}
						if mode == "poll" {
							for {
								if next().GetSyncResponse() {
									break
								}
							}
						}
						mr.HDel(mapName, name)
						if mode == "poll" {
							signal <- struct{}{}
						}
						if mode == "on_change" {
							mr.Publish("__keyspace@2__:"+mapName, "hdel")
						}
						for {
							v := next()
							if len(v.GetDelete()) > 0 {
								if v.GetDelete()[0].GetElem()[1].GetName() != kind+":"+name {
									t.Fatal("delete used OID or wildcard instead of logical key")
								}
								break
							}
						}
						if fmt.Sprintf("%#v", c.pathG2S[path]) != original {
							t.Fatal("subscription mutated initial shared path map")
						}
					})
				}
			}
		}
	}
}

func TestACLNewQueryRefreshAndError(t *testing.T) {
	sdcfg.Init()
	if err := sdcfg.DbInit(); err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	old := Target2RedisDb
	Target2RedisDb = map[string]map[string]*redis.Client{"": {"COUNTERS_DB": rdb, "CONFIG_DB": rdb}}
	defer func() { Target2RedisDb = old }()
	prefix := &gnmipb.Path{Target: "COUNTERS_DB"}
	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "ACL_RULE*"}}}
	query := func() ([]tablePath, error) {
		result := make(map[*gnmipb.Path][]tablePath)
		err := parsePath(prefix, path, &result)
		return result[path], err
	}
	if p, err := query(); err != nil || len(p) != 0 {
		t.Fatalf("empty lookup %v %v", p, err)
	}
	mr.HSet("ACL_COUNTER_RULE_MAP", "DATAACL:RULE_1", "oid:0x1")
	if p, err := query(); err != nil || len(p) != 1 || p[0].jsonTableKey != "DATAACL:RULE_1" {
		t.Fatalf("populated lookup %v %v", p, err)
	}
	mr.Server().SetPreHook(func(p *server.Peer, cmd string, args ...string) bool {
		if strings.EqualFold(cmd, "hgetall") && len(args) > 0 && args[0] == "ACL_COUNTER_RULE_MAP" {
			p.WriteError("ERR ACL map unavailable")
			return true
		}
		return false
	})
	if p, err := query(); err == nil || len(p) != 0 {
		t.Fatalf("read error silently fell back %v %v", p, err)
	}
	mr.Server().SetPreHook(nil)
	if p, err := query(); err != nil || len(p) != 1 {
		t.Fatalf("retry %v %v", p, err)
	}
}

func TestDynamicCounterReadFailureAndStop(t *testing.T) {
	for _, phase := range []string{"map", "counter", "subscribe"} {
		for _, blocked := range []bool{false, true} {
			if phase == "subscribe" && !blocked {
				continue
			}
			t.Run(fmt.Sprintf("%s/blocked=%t", phase, blocked), func(t *testing.T) {
				sdcfg.Init()
				if err := sdcfg.DbInit(); err != nil {
					t.Fatal(err)
				}
				mr := miniredis.RunT(t)
				mr.HSet("COUNTERS_SRV6_NAME_MAP", "fcbb:bbbb:1::/48", "oid:0x1")
				mr.HSet("COUNTERS:oid:0x1", "SAI_COUNTER_STAT_PACKETS", "10")
				rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ReadTimeout: time.Minute})
				defer rdb.Close()
				old := Target2RedisDb
				Target2RedisDb = map[string]map[string]*redis.Client{"": {"COUNTERS_DB": rdb}}
				defer func() { Target2RedisDb = old }()
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				mr.Server().SetPreHook(func(peer *server.Peer, cmd string, args ...string) bool {
					cmd = strings.ToLower(cmd)
					match := cmd == "hgetall" && len(args) > 0 && ((phase == "map" && args[0] == "COUNTERS_SRV6_NAME_MAP") || (phase == "counter" && args[0] == "COUNTERS:oid:0x1"))
					match = match || (phase == "subscribe" && cmd == "psubscribe")
					if !match {
						return false
					}
					once.Do(func() { close(entered) })
					if blocked {
						<-release
					} else {
						peer.WriteError("ERR injected read failure")
					}
					return true
				})
				defer close(release)
				path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "SID:*"}}}
				c := &DbClient{prefix: &gnmipb.Path{Target: "COUNTERS_DB"}, pathG2S: map[*gnmipb.Path][]tablePath{path: nil}}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				c.SetSubscriptionContext(ctx)
				q := queue.NewPriorityQueue(16, false)
				defer q.Dispose()
				signal := make(chan struct{}, 1)
				defer close(signal)
				var wg sync.WaitGroup
				wg.Add(1)
				if phase == "subscribe" {
					go c.StreamRun(q, signal, &wg, &gnmipb.SubscriptionList{Subscription: []*gnmipb.Subscription{{Path: path, Mode: gnmipb.SubscriptionMode_ON_CHANGE}}})
				} else {
					go c.PollRun(q, signal, &wg, nil)
					signal <- struct{}{}
				}
				done := make(chan struct{})
				go func() { wg.Wait(); close(done) }()
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("read not reached")
				}
				if blocked {
					cancel()
				}
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("subscription did not exit on error/cancellation")
				}
				fatal := false
				for !q.Empty() {
					items, _ := q.Get(1)
					v := items[0].(Value)
					if len(v.GetDelete()) > 0 {
						t.Fatal("read failure emitted delete")
					}
					fatal = fatal || v.GetFatal() != ""
				}
				if !blocked && !fatal {
					t.Fatal("read error not propagated as fatal")
				}
				mr.Server().SetPreHook(nil)
				if err := rdb.Ping(context.Background()).Err(); err != nil {
					t.Fatalf("shared Redis client was closed: %v", err)
				}
			})
		}
	}
}
