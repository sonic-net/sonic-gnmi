package interceptors

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	rpcCompletionLogPrefix       = "RPC_COMPLETION"
	rpcCompletionSummaryPrefix   = "RPC_COMPLETION_SUMMARY"
	rpcCompletionDefaultInterval = 10 * time.Second
	gnmiGetMethod                = "/gnmi.gNMI/Get"
	gnmiSetMethod                = "/gnmi.gNMI/Set"
)

type scheduleFunc func(time.Duration, func())
type lineSink func(string) error

type rpcLogKey struct {
	method string
	code   codes.Code
}

type rpcLogLimit struct {
	limiter    *rate.Limiter
	suppressed uint64
	scheduled  bool
}

type rpcLogPolicy struct {
	tokenInterval   time.Duration
	summaryInterval time.Duration
	burst           int
	unlimited       bool
}

var defaultRPCLogPolicy = rpcLogPolicy{
	tokenInterval:   rpcCompletionDefaultInterval,
	summaryInterval: rpcCompletionDefaultInterval,
	burst:           1,
}

var rpcLogPolicies = map[string]rpcLogPolicy{
	gnmiGetMethod: {
		tokenInterval:   time.Minute,
		summaryInterval: time.Hour,
		burst:           60,
	},
	gnmiSetMethod: {
		unlimited: true,
	},
}

type rpcCompletionRecord struct {
	Version    int      `json:"v"`
	RPCType    string   `json:"type"`
	Method     string   `json:"method"`
	PeerType   string   `json:"peer_type"`
	Peer       string   `json:"peer"`
	Principal  string   `json:"principal"`
	AuthType   string   `json:"auth_type"`
	Path       []string `json:"path"`
	Code       string   `json:"code"`
	DurationMS int64    `json:"duration_ms"`
	Suppressed uint64   `json:"suppressed"`
}

type rpcCompletionSummary struct {
	Version    int    `json:"v"`
	Method     string `json:"method"`
	Code       string `json:"code"`
	Suppressed uint64 `json:"suppressed"`
}

type requestFields struct {
	peerType  string
	address   string
	principal string
	authType  string
	paths     []string
}

// rpcCompletionLogger owns the server RPC completion lifecycle and rate policy.
type rpcCompletionLogger struct {
	sink     lineSink
	now      func() time.Time
	schedule scheduleFunc

	mu     sync.Mutex
	limits map[rpcLogKey]*rpcLogLimit
}

func newRPCCompletionLogger(sink lineSink) *rpcCompletionLogger {
	return newRPCCompletionLoggerWithClockAndScheduler(sink, time.Now, func(delay time.Duration, f func()) {
		time.AfterFunc(delay, f)
	})
}

func newRPCCompletionLoggerWithClockAndScheduler(
	sink lineSink,
	now func() time.Time,
	schedule scheduleFunc,
) *rpcCompletionLogger {
	logger := &rpcCompletionLogger{
		sink:     sink,
		now:      now,
		schedule: schedule,
		limits:   make(map[rpcLogKey]*rpcLogLimit),
	}
	return logger
}

func (l *rpcCompletionLogger) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response interface{}, err error) {
		started := l.now()
		defer l.log(ctx, req, "unary", info.FullMethod, started, &err)
		return handler(ctx, req)
	}
}

func (l *rpcCompletionLogger) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		started := l.now()
		defer l.log(stream.Context(), nil, "stream", info.FullMethod, started, &err)
		return handler(srv, stream)
	}
}

func (l *rpcCompletionLogger) log(
	ctx context.Context,
	req interface{},
	rpcType string,
	method string,
	started time.Time,
	rpcErr *error,
) {
	if panicValue := recover(); panicValue != nil {
		panic(panicValue)
	}

	finished := l.now()
	code := grpcCode(*rpcErr)
	suppressed, allowed := l.allow(method, code, finished)
	if !allowed {
		return
	}

	fields := deriveRequestFields(ctx, req)
	record := rpcCompletionRecord{
		Version:    2,
		RPCType:    rpcType,
		Method:     method,
		PeerType:   fields.peerType,
		Peer:       fields.address,
		Principal:  fields.principal,
		AuthType:   fields.authType,
		Path:       fields.paths,
		Code:       code.String(),
		DurationMS: finished.Sub(started).Milliseconds(),
		Suppressed: suppressed,
	}

	if data, err := json.Marshal(record); err == nil {
		func() {
			defer func() { _ = recover() }()
			_ = l.sink(rpcCompletionLogPrefix + " " + string(data))
		}()
	}
}

func (l *rpcCompletionLogger) allow(method string, code codes.Code, now time.Time) (uint64, bool) {
	policy := rpcLogPolicyForMethod(method)
	if policy.unlimited {
		return 0, true
	}

	l.mu.Lock()
	key := rpcLogKey{method: method, code: code}
	limit, ok := l.limits[key]
	if !ok {
		limit = &rpcLogLimit{
			limiter: rate.NewLimiter(rate.Every(policy.tokenInterval), policy.burst),
		}
		l.limits[key] = limit
	}
	if !limit.limiter.AllowN(now, 1) {
		limit.suppressed++
		shouldSchedule := markSummaryScheduled(limit)
		l.mu.Unlock()
		if shouldSchedule {
			l.scheduleSummary(key)
		}
		return 0, false
	}
	suppressed := limit.suppressed
	limit.suppressed = 0
	l.mu.Unlock()
	return suppressed, true
}

func markSummaryScheduled(limit *rpcLogLimit) bool {
	if limit.scheduled {
		return false
	}
	limit.scheduled = true
	return true
}

func (l *rpcCompletionLogger) scheduleSummary(key rpcLogKey) {
	l.schedule(rpcLogPolicyForMethod(key.method).summaryInterval, func() { l.writeSummary(key) })
}

func rpcLogPolicyForMethod(method string) rpcLogPolicy {
	if policy, ok := rpcLogPolicies[method]; ok {
		return policy
	}
	return defaultRPCLogPolicy
}

func (l *rpcCompletionLogger) writeSummary(key rpcLogKey) {
	l.mu.Lock()
	limit := l.limits[key]
	limit.scheduled = false
	if limit.suppressed == 0 {
		l.mu.Unlock()
		return
	}
	if !limit.limiter.AllowN(l.now(), 1) {
		shouldSchedule := markSummaryScheduled(limit)
		l.mu.Unlock()
		if shouldSchedule {
			l.scheduleSummary(key)
		}
		return
	}
	suppressed := limit.suppressed
	limit.suppressed = 0
	l.mu.Unlock()

	summary := rpcCompletionSummary{
		Version:    1,
		Method:     key.method,
		Code:       key.code.String(),
		Suppressed: suppressed,
	}
	if data, err := json.Marshal(summary); err == nil {
		func() {
			defer func() { _ = recover() }()
			_ = l.sink(rpcCompletionSummaryPrefix + " " + string(data))
		}()
	}
}

func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	if rpcStatus, ok := status.FromError(err); ok {
		return rpcStatus.Code()
	}
	return status.FromContextError(err).Code()
}

func deriveRequestFields(ctx context.Context, req interface{}) requestFields {
	fields := requestFields{
		peerType: "unknown",
		paths:    requestPaths(req),
	}
	requestPeer, ok := peer.FromContext(ctx)
	if !ok {
		return fields
	}

	fields.peerType, fields.address = peerAddress(requestPeer)
	fields.principal = verifiedCertificateCommonName(requestPeer)
	fields.authType = peerAuthType(requestPeer)
	return fields
}

func peerAddress(requestPeer *peer.Peer) (string, string) {
	peerType := "unknown"
	peerAddress := ""
	if requestPeer.Addr != nil {
		peerAddress = requestPeer.Addr.String()
		switch network := requestPeer.Addr.Network(); {
		case strings.HasPrefix(network, "tcp"):
			peerType = "tcp"
		case strings.HasPrefix(network, "unix"):
			peerType = "unix"
		}
	}
	return peerType, peerAddress
}

func requestPaths(req interface{}) []string {
	paths := []string{}
	switch request := req.(type) {
	case *gnmipb.GetRequest:
		for _, path := range request.GetPath() {
			paths = append(paths, formatRequestPath(path))
		}
	case *gnmipb.SetRequest:
		for _, path := range request.GetDelete() {
			paths = append(paths, formatRequestPath(path))
		}
		for _, update := range request.GetReplace() {
			paths = append(paths, formatRequestPath(update.GetPath()))
		}
		for _, update := range request.GetUpdate() {
			paths = append(paths, formatRequestPath(update.GetPath()))
		}
	}
	return paths
}

func formatRequestPath(path *gnmipb.Path) string {
	if path == nil {
		return "<invalid>"
	}

	schemaPath := &gnmipb.Path{Elem: make([]*gnmipb.PathElem, 0, len(path.GetElem()))}
	for _, elem := range path.GetElem() {
		if elem == nil {
			return "<invalid>"
		}
		schemaPath.Elem = append(schemaPath.Elem, &gnmipb.PathElem{Name: elem.GetName()})
	}

	formatted, err := ygot.PathToSchemaPath(schemaPath)
	if err != nil {
		return "<invalid>"
	}
	return formatted
}

func verifiedCertificateCommonName(requestPeer *peer.Peer) string {
	tlsInfo, ok := requestPeer.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return ""
	}
	return tlsInfo.State.VerifiedChains[0][0].Subject.CommonName
}

func peerAuthType(requestPeer *peer.Peer) string {
	if requestPeer.AuthInfo == nil {
		return ""
	}
	return requestPeer.AuthInfo.AuthType()
}
