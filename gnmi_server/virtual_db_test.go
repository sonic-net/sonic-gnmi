package gnmi

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/alicebob/miniredis/v2"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	redis "github.com/redis/go-redis/v9"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func TestVirtualDbSyncOnce(t *testing.T) {
	t.Setenv("UNIT_TEST", "1")
	tests := []struct {
		desc     string
		initFunc func() error
	}{
		{"countersPortNameMap", sdc.InitCountersPortNameMap},
		{"countersQueueNameMap", sdc.InitCountersQueueNameMap},
		{"countersPGNameMap", sdc.InitCountersPGNameMap},
		{"countersFabricPortNameMap", sdc.InitCountersFabricPortNameMap},
		{"countersSidMap", nil},
		{"countersAclRuleMap", sdc.InitCountersAclRuleMap},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			sdc.ClearMappings()
			if tt.desc == "countersSidMap" {
				sdcfg.Init()
				mr := miniredis.RunT(t)
				rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				defer rdb.Close()
				clients := gomonkey.ApplyFuncReturn(sdc.GetRedisClientsForDb, map[string]*redis.Client{"": rdb}, nil)
				defer clients.Reset()
			}

			var callCount int64
			mock := gomonkey.ApplyFunc(sdc.GetCountersMap, func(tableName string) (map[string]string, error) {
				if tt.desc == "countersSidMap" {
					if tableName != "COUNTERS_SRV6_NAME_MAP" {
						return map[string]string{}, nil
					}
					if atomic.AddInt64(&callCount, 1) == 1 {
						return map[string]string{}, nil
					}
					return map[string]string{"fcbb:bbbb:1::/48": "oid:0x11110000001eb3"}, nil
				}
				atomic.AddInt64(&callCount, 1)
				return map[string]string{"test_key": "test_oid"}, nil
			})
			if tt.desc == "countersFabricPortNameMap" {
				mock = gomonkey.ApplyFunc(sdc.GetFabricCountersMap, func(tableName string) (map[string]string, error) {
					atomic.AddInt64(&callCount, 1)
					return map[string]string{"test_key": "test_oid"}, nil
				})
			}
			defer mock.Reset()

			for i := 0; i < 10; i++ {
				if tt.desc == "countersSidMap" {
					paths, err := sdc.PopulateTablePaths(&gnmipb.Path{Target: "COUNTERS_DB"},
						&gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "SID:*"}}})
					if err != nil || (i == 0 && len(paths) != 0) || (i > 0 && (len(paths) != 1 ||
						!strings.Contains(fmt.Sprint(paths), `key="oid:0x11110000001eb3"`))) {
						t.Fatalf("SID query %d: paths=%v err=%v", i, paths, err)
					}
				} else {
					tt.initFunc()
				}
			}
			want := int64(1)
			if tt.desc == "countersSidMap" {
				want = 10 // SID mappings are refreshed for each new query.
			}
			if c := atomic.LoadInt64(&callCount); c != want {
				t.Errorf("expected underlying fetch called %d times, got %d", want, c)
			}
		})
	}

	t.Run("aliasMap", func(t *testing.T) {
		sdc.ClearMappings()

		var callCount int64
		mock := gomonkey.ApplyFunc(sdc.GetAliasMap, func() (map[string]string, map[string]string, map[string]string, error) {
			atomic.AddInt64(&callCount, 1)
			return map[string]string{"Eth1": "Ethernet0"},
				map[string]string{"Ethernet0": "Eth1"},
				map[string]string{"Ethernet0": ""},
				nil
		})
		defer mock.Reset()

		for i := 0; i < 10; i++ {
			sdc.AliasToPortNameMap()
		}
		if c := atomic.LoadInt64(&callCount); c != 1 {
			t.Errorf("expected underlying fetch called once, got %d", c)
		}
	})
}

func TestVirtualDbSyncOnceError(t *testing.T) {
	t.Setenv("UNIT_TEST", "1")
	tests := []struct {
		desc     string
		initFunc func() error
	}{
		{"countersPortNameMap", sdc.InitCountersPortNameMap},
		{"countersQueueNameMap", sdc.InitCountersQueueNameMap},
		{"countersPGNameMap", sdc.InitCountersPGNameMap},
		{"countersFabricPortNameMap", sdc.InitCountersFabricPortNameMap},
		{"countersSidMap", nil},
		{"countersAclRuleMap", sdc.InitCountersAclRuleMap},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			sdc.ClearMappings()
			if tt.desc == "countersSidMap" {
				sdcfg.Init()
				mr := miniredis.RunT(t)
				rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				defer rdb.Close()
				clients := gomonkey.ApplyFuncReturn(sdc.GetRedisClientsForDb, map[string]*redis.Client{"": rdb}, nil)
				defer clients.Reset()
			}

			mockErr := fmt.Errorf("mock redis error")
			mock := gomonkey.ApplyFunc(sdc.GetCountersMap, func(tableName string) (map[string]string, error) {
				if tt.desc == "countersSidMap" && tableName != "COUNTERS_SRV6_NAME_MAP" {
					return map[string]string{}, nil
				}
				return nil, mockErr
			})
			if tt.desc == "countersFabricPortNameMap" {
				mock = gomonkey.ApplyFunc(sdc.GetFabricCountersMap, func(tableName string) (map[string]string, error) {
					return nil, mockErr
				})
			}
			defer mock.Reset()

			var err error
			if tt.desc == "countersSidMap" {
				_, err = sdc.PopulateTablePaths(&gnmipb.Path{Target: "COUNTERS_DB"},
					&gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "SID:*"}}})
				if !errors.Is(err, mockErr) {
					t.Fatalf("SID lookup lost read error: %v", err)
				}
			} else {
				err = tt.initFunc()
			}
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}

	t.Run("aliasMap", func(t *testing.T) {
		sdc.ClearMappings()

		mockErr := fmt.Errorf("mock redis error")
		mock := gomonkey.ApplyFunc(sdc.GetAliasMap, func() (map[string]string, map[string]string, map[string]string, error) {
			return nil, nil, nil, mockErr
		})
		defer mock.Reset()

		// AliasToPortNameMap calls initAliasMap internally
		sdc.AliasToPortNameMap()
	})
}

func TestVirtualDbSyncOnceConcurrent(t *testing.T) {
	t.Setenv("UNIT_TEST", "1")
	tests := []struct {
		desc     string
		initFunc func() error
	}{
		{"countersPortNameMap", sdc.InitCountersPortNameMap},
		{"countersQueueNameMap", sdc.InitCountersQueueNameMap},
		{"countersPGNameMap", sdc.InitCountersPGNameMap},
		{"countersFabricPortNameMap", sdc.InitCountersFabricPortNameMap},
		{"countersSidMap", nil},
		{"countersAclRuleMap", sdc.InitCountersAclRuleMap},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			sdc.ClearMappings()
			if tt.desc == "countersSidMap" {
				sdcfg.Init()
				mr := miniredis.RunT(t)
				mr.HSet("COUNTERS_DEBUG_NAME_SWITCH_STAT_MAP", "test", "oid:0x1")
				rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				defer rdb.Close()
				clients := gomonkey.ApplyFuncReturn(sdc.GetRedisClientsForDb, map[string]*redis.Client{"": rdb}, nil)
				defer clients.Reset()
				t.Setenv("UNIT_TEST", "") // Do not reset unrelated global maps during concurrent queries.
			}

			var callCount int64
			mock := gomonkey.ApplyFunc(sdc.GetCountersMap, func(tableName string) (map[string]string, error) {
				if tt.desc == "countersSidMap" {
					if tableName != "COUNTERS_SRV6_NAME_MAP" {
						return map[string]string{}, nil
					}
					atomic.AddInt64(&callCount, 1)
					return map[string]string{"fcbb:bbbb:1::/48": "oid:0x11110000001eb3"}, nil
				}
				atomic.AddInt64(&callCount, 1)
				return map[string]string{"test_key": "test_oid"}, nil
			})
			if tt.desc == "countersFabricPortNameMap" {
				mock = gomonkey.ApplyFunc(sdc.GetFabricCountersMap, func(tableName string) (map[string]string, error) {
					atomic.AddInt64(&callCount, 1)
					return map[string]string{"test_key": "test_oid"}, nil
				})
			}
			defer mock.Reset()

			results := make([][]sdc.TablePath, 100)
			if tt.desc == "countersSidMap" {
				// Initialize DB configuration and non-SID maps before launching workers.
				_, err := sdc.PopulateTablePaths(&gnmipb.Path{Target: "COUNTERS_DB"},
					&gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "SID:*"}}})
				if err != nil {
					t.Fatal(err)
				}
				atomic.StoreInt64(&callCount, 0)
			}
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if tt.desc == "countersSidMap" {
						paths, err := sdc.PopulateTablePaths(&gnmipb.Path{Target: "COUNTERS_DB"},
							&gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "SID:*"}}})
						if err != nil || len(paths) != 1 || !strings.Contains(fmt.Sprint(paths), `key="oid:0x11110000001eb3"`) {
							t.Errorf("SID lookup: paths=%v err=%v", paths, err)
							return
						}
						results[i] = paths
					} else {
						tt.initFunc()
					}
				}(i)
			}
			wg.Wait()

			want := int64(1)
			if tt.desc == "countersSidMap" {
				want = 100
				if !t.Failed() {
					results[0][0] = sdc.CreateTablePath("COUNTERS_DB", "COUNTERS", ":", "local-change")
					for _, paths := range results[1:] {
						if !strings.Contains(fmt.Sprint(paths), `key="oid:0x11110000001eb3"`) {
							t.Error("SID lookup results share mutable storage")
						}
					}
				}
			}
			if c := atomic.LoadInt64(&callCount); c != want {
				t.Errorf("expected underlying fetch called %d times across 100 goroutines, got %d", want, c)
			}
		})
	}

	t.Run("aliasMap", func(t *testing.T) {
		sdc.ClearMappings()

		var callCount int64
		mock := gomonkey.ApplyFunc(sdc.GetAliasMap, func() (map[string]string, map[string]string, map[string]string, error) {
			atomic.AddInt64(&callCount, 1)
			return map[string]string{"Eth1": "Ethernet0"},
				map[string]string{"Ethernet0": "Eth1"},
				map[string]string{"Ethernet0": ""},
				nil
		})
		defer mock.Reset()

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sdc.AliasToPortNameMap()
			}()
		}
		wg.Wait()

		if c := atomic.LoadInt64(&callCount); c != 1 {
			t.Errorf("expected underlying fetch called once across 100 goroutines, got %d", c)
		}
	})
}
