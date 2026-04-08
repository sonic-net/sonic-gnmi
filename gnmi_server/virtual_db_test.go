package gnmi

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

func TestVirtualDbSyncOnce(t *testing.T) {
	tests := []struct {
		desc     string
		initFunc func() error
	}{
		{"countersPortNameMap", sdc.InitCountersPortNameMap},
		{"countersQueueNameMap", sdc.InitCountersQueueNameMap},
		{"countersPGNameMap", sdc.InitCountersPGNameMap},
		{"countersFabricPortNameMap", sdc.InitCountersFabricPortNameMap},
		{"countersSidMap", sdc.InitCountersSidMap},
		{"countersAclRuleMap", sdc.InitCountersAclRuleMap},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			sdc.ClearMappings()

			var callCount int64
			mock := gomonkey.ApplyFunc(sdc.GetCountersMap, func(tableName string) (map[string]string, error) {
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
				tt.initFunc()
			}
			if c := atomic.LoadInt64(&callCount); c != 1 {
				t.Errorf("expected underlying fetch called once, got %d", c)
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

func TestVirtualDbSyncOnceConcurrent(t *testing.T) {
	tests := []struct {
		desc     string
		initFunc func() error
	}{
		{"countersPortNameMap", sdc.InitCountersPortNameMap},
		{"countersQueueNameMap", sdc.InitCountersQueueNameMap},
		{"countersPGNameMap", sdc.InitCountersPGNameMap},
		{"countersFabricPortNameMap", sdc.InitCountersFabricPortNameMap},
		{"countersSidMap", sdc.InitCountersSidMap},
		{"countersAclRuleMap", sdc.InitCountersAclRuleMap},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			sdc.ClearMappings()

			var callCount int64
			mock := gomonkey.ApplyFunc(sdc.GetCountersMap, func(tableName string) (map[string]string, error) {
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

			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					tt.initFunc()
				}()
			}
			wg.Wait()

			if c := atomic.LoadInt64(&callCount); c != 1 {
				t.Errorf("expected underlying fetch called once across 100 goroutines, got %d", c)
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
