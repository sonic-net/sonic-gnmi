package common_utils

import (
	"context"
	log "github.com/golang/glog"
	"testing"
	"time"
)

func TestCounter(t *testing.T) {
	log.V(2).Info("TestCounter")
	// InitCounters should set them all to zero.
	for i := 0; i < len(globalCounters); i++ {
		globalCounters[i] = 7
	}
	InitCounters()
	for i, v := range globalCounters {
		if v != 0 {
			t.Fatalf("Counter %d has non-zero value %v", i, v)
		}
	}
	refCounts := []struct {
		ctr  CounterType
		name string
		val  uint64
	}{
		{
			ctr:  GNOI_FACTORY_RESET,
			name: "GNOI Factory Reset",
			val:  9,
		},
	}
	// Check counter increment and string name
	for _, r := range refCounts {
		t.Run(r.name, func(t *testing.T) {
			for i := 0; uint64(i) < r.val; i++ {
				IncCounter(r.ctr)
			}
			if r.val != globalCounters[r.ctr] {
				t.Fatalf("Counter %v is %v, expected %v", r.ctr, globalCounters[r.ctr], r.val)
			}
			if r.ctr.String() != r.name {
				t.Fatalf("Counter %v is %v, expected %v", r.ctr, r.ctr.String(), r.name)
			}
		})
	}
	// Check restore from shared mem.
	for i := 0; i < len(globalCounters); i++ {
		globalCounters[i] = 77
	}
	GetMemCounters(&globalCounters)
	for _, r := range refCounts {
		t.Run(r.name+"_restore", func(t *testing.T) {
			if r.val != globalCounters[r.ctr] {
				t.Fatalf("Counter %v is %v, expected %v", r.ctr, globalCounters[r.ctr], r.val)
			}
		})
	}
}
func TestParamNoKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	val, sts := ReqParam(ctx, "foo")
	if sts != false {
		t.Fatalf("ReqParam didn't return false for unknown key")
	}
	if val != nil {
		t.Fatalf("ReqParam didn't return nil for unknown key")
	}
}
