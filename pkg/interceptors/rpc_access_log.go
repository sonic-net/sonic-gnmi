package interceptors

import (
	"context"
	"sync"
	"time"

	sharedlog "github.com/sonic-net/sonic-gnmi/pkg/logging"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/codes"
)

const (
	rpcAccessLogPrefix        = "RPC_ACCESS"
	rpcAccessLogSummaryPrefix = "RPC_ACCESS_SUMMARY"
	rpcAccessLogInterval      = 10 * time.Second
)

type scheduleFunc func(time.Duration, func())

// rpcLogger emits at most one access record or suppression summary per method
// and status-code pair during each interval.
type rpcLogger struct {
	sink     sharedlog.LineSink
	now      func() time.Time
	schedule scheduleFunc
	mu       sync.Mutex
	limits   map[rpcLogKey]*rpcLogLimit
}

type rpcLogKey struct {
	method string
	code   codes.Code
}

type rpcLogLimit struct {
	limiter    *rate.Limiter
	suppressed uint64
	scheduled  bool
}

type rpcAccessLogRecord struct {
	Version    int    `json:"v"`
	RPCType    string `json:"type"`
	Method     string `json:"method"`
	PeerType   string `json:"peer_type"`
	Peer       string `json:"peer"`
	Code       string `json:"code"`
	DurationMS int64  `json:"duration_ms"`
	Suppressed uint64 `json:"suppressed"`
}

type rpcAccessLogSummary struct {
	Version    int    `json:"v"`
	Method     string `json:"method"`
	Code       string `json:"code"`
	Suppressed uint64 `json:"suppressed"`
}

func newRPCLogger(sink sharedlog.LineSink) *rpcLogger {
	return newRPCLoggerWithClock(sink, time.Now, func(delay time.Duration, f func()) {
		time.AfterFunc(delay, f)
	})
}

func newRPCLoggerWithClock(sink sharedlog.LineSink, now func() time.Time, schedule scheduleFunc) *rpcLogger {
	return &rpcLogger{
		sink:     sink,
		now:      now,
		schedule: schedule,
		limits:   make(map[rpcLogKey]*rpcLogLimit),
	}
}

func (l *rpcLogger) log(ctx context.Context, rpcType, method string, started time.Time, err error) {
	finished := l.now()
	code := sharedlog.GRPCCode(err)
	suppressed, allowed := l.allow(method, code, finished)
	if !allowed {
		return
	}

	peerType, peerAddress := sharedlog.PeerTypeAddr(ctx)
	_ = sharedlog.WriteJSON(rpcAccessLogPrefix, rpcAccessLogRecord{
		Version:    2,
		RPCType:    rpcType,
		Method:     method,
		PeerType:   peerType,
		Peer:       peerAddress,
		Code:       code.String(),
		DurationMS: finished.Sub(started).Milliseconds(),
		Suppressed: suppressed,
	}, l.sink)
}

func (l *rpcLogger) allow(method string, code codes.Code, now time.Time) (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := rpcLogKey{method: method, code: code}
	limit, ok := l.limits[key]
	if !ok {
		limit = &rpcLogLimit{
			limiter: rate.NewLimiter(rate.Every(rpcAccessLogInterval), 1),
		}
		l.limits[key] = limit
	}
	if !limit.limiter.AllowN(now, 1) {
		limit.suppressed++
		l.scheduleSummary(key, limit)
		return 0, false
	}
	suppressed := limit.suppressed
	limit.suppressed = 0
	return suppressed, true
}

func (l *rpcLogger) scheduleSummary(key rpcLogKey, limit *rpcLogLimit) {
	if limit.scheduled {
		return
	}
	limit.scheduled = true
	l.schedule(rpcAccessLogInterval, func() { l.writeSummary(key) })
}

func (l *rpcLogger) writeSummary(key rpcLogKey) {
	l.mu.Lock()
	limit := l.limits[key]
	limit.scheduled = false
	if limit.suppressed == 0 {
		l.mu.Unlock()
		return
	}
	if !limit.limiter.AllowN(l.now(), 1) {
		l.scheduleSummary(key, limit)
		l.mu.Unlock()
		return
	}
	suppressed := limit.suppressed
	limit.suppressed = 0
	l.mu.Unlock()

	_ = sharedlog.WriteJSON(rpcAccessLogSummaryPrefix, rpcAccessLogSummary{
		Version:    1,
		Method:     key.method,
		Code:       key.code.String(),
		Suppressed: suppressed,
	}, l.sink)
}
