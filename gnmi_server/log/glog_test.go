package log

import (
	"flag"
	"testing"
	"time"

	log "github.com/golang/glog"
)

func TestLogFirstN(t *testing.T) {
	flag.Set("logfirstn", "3")
	flag.Set("logresettime", "3s")

	numLogs := 5
	for i := 0; i < numLogs; i += 1 {
		log.V(0).Info("Test Info")
	}
	if log.GetLogCount("Test Info") != numLogs {
		t.Errorf("log.GetLogCount(\"Test Info\") = %v, want %v", log.GetLogCount("Test Info"), numLogs)
	}

	time.Sleep(3 * time.Second)
	log.V(0).Info("Test Info")
	if log.GetLogCount("Test Info") != 1 {
		t.Errorf("log.GetLogCount(\"Test Info\") = %v, want %v", log.GetLogCount("Test Info"), 1)
	}
}
