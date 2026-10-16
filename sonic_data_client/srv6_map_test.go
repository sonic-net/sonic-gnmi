package client

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	redis "github.com/redis/go-redis/v9"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func setupSRv6Redis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	sdcfg.Init()
	if err := sdcfg.DbInit(); err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	original := Target2RedisDb
	Target2RedisDb = map[string]map[string]*redis.Client{
		"": {"COUNTERS_DB": rdb, "CONFIG_DB": rdb, "APPL_DB": rdb},
	}
	t.Cleanup(func() {
		Target2RedisDb = original
		rdb.Close()
	})
	return mr
}

func TestSRv6MapRefreshNewQuery(t *testing.T) {
	mr := setupSRv6Redis(t)
	prefix := &gnmipb.Path{Target: "COUNTERS_DB"}
	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "SID:*"}}}
	query := func() []tablePath {
		t.Helper()
		resolved := make(map[*gnmipb.Path][]tablePath)
		if err := parsePath(prefix, path, &resolved); err != nil {
			t.Fatal(err)
		}
		return resolved[path]
	}
	if paths := query(); len(paths) != 0 {
		t.Fatalf("empty map returned paths: %+v", paths)
	}
	const sid = "fcbb:bbbb:1::/48"
	for _, oid := range []string{"oid:0x11110000001eb3", "oid:0x11110000001eb4"} {
		mr.HSet("COUNTERS_SRV6_NAME_MAP", sid, oid)
		paths := query()
		if len(paths) != 1 || paths[0].tableKey != oid || paths[0].jsonTableKey != sid || !paths[0].isVirtualPath {
			t.Fatalf("new query did not resolve current SID mapping: %+v", paths)
		}
		mr.HSet("COUNTERS:"+oid, "SAI_COUNTER_STAT_PACKETS", "10", "SAI_COUNTER_STAT_BYTES", "40960")
		value, err := tableData2TypedValue(paths, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"fcbb:bbbb:1::/48":{"SAI_COUNTER_STAT_BYTES":"40960","SAI_COUNTER_STAT_PACKETS":"10"}}`
		if string(value.GetJsonIetfVal()) != want {
			t.Fatalf("counter response = %s, want %s", value.GetJsonIetfVal(), want)
		}
	}
	mr.HDel("COUNTERS_SRV6_NAME_MAP", sid)
	if paths := query(); len(paths) != 0 {
		t.Fatalf("deleted SID still resolved: %+v", paths)
	}
}

func TestSRv6MapReadErrorAndFallback(t *testing.T) {
	mr := setupSRv6Redis(t)
	original := getCountersMapFn
	readErr := errors.New("SID map read failed")
	fail := true
	getCountersMapFn = func(table string) (map[string]string, error) {
		if table == "COUNTERS_SRV6_NAME_MAP" && fail {
			return nil, readErr
		}
		return original(table)
	}
	t.Cleanup(func() { getCountersMapFn = original })
	prefix := &gnmipb.Path{Target: "COUNTERS_DB"}
	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "SID:*"}}}
	resolved := make(map[*gnmipb.Path][]tablePath)
	if err := parsePath(prefix, path, &resolved); !errors.Is(err, readErr) {
		t.Fatalf("parsePath swallowed SID read failure: %v", err)
	}
	if _, exists := resolved[path]; exists {
		t.Fatal("failed SID read fell back to a physical path")
	}
	// An unrelated physical counter path must not fetch the failed SID map.
	physical := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "oid:0x123"}}}
	if err := parsePath(prefix, physical, &resolved); err != nil {
		t.Fatal(err)
	}
	if len(resolved[physical]) != 1 || resolved[physical][0].isVirtualPath {
		t.Fatal("ordinary physical path no longer falls back")
	}
	fail = false
	mr.HSet("COUNTERS_SRV6_NAME_MAP", "fcbb:bbbb:1::/48", "oid:0x11110000001eb3")
	if err := parsePath(prefix, path, &resolved); err != nil || len(resolved[path]) != 1 || !resolved[path][0].isVirtualPath {
		t.Fatalf("query did not recover after read failure: paths=%+v err=%v", resolved[path], err)
	}
	// Missing specific SID retains the pre-existing physical fallback behavior.
	missing := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "SID:fcbb:bbbb:2::/48"}}}
	if err := parsePath(prefix, missing, &resolved); err != nil {
		t.Fatal(err)
	}
	if len(resolved[missing]) != 1 || resolved[missing][0].isVirtualPath {
		t.Fatal("missing SID no longer preserves physical fallback")
	}
}

func TestSRv6MapConcurrentLookups(t *testing.T) {
	mr := setupSRv6Redis(t)
	const sid = "fcbb:bbbb:1::/48"
	mr.HSet("COUNTERS_SRV6_NAME_MAP", sid, "oid:0x1")
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				paths, err := lookupV2R([]string{"COUNTERS_DB", "COUNTERS", "SID:*"})
				if err != nil || len(paths) != 1 || paths[0].jsonTableKey != sid || paths[0].tableKey == "" {
					t.Errorf("concurrent lookup: paths=%+v err=%v", paths, err)
					return
				}
			}
		}()
	}
	for i := 0; i < 30; i++ {
		mr.HSet("COUNTERS_SRV6_NAME_MAP", sid, fmt.Sprintf("oid:0x%x", i+1))
	}
	wg.Wait()
}
