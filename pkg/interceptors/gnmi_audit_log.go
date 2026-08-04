package interceptors

import (
	"context"
	"crypto/x509"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	sharedlog "github.com/sonic-net/sonic-gnmi/pkg/logging"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

const (
	gnmiAuditPrefix          = "GNMI_AUDIT"
	gnmiAuditSummaryPrefix   = "GNMI_AUDIT_SUMMARY"
	gnmiAuditVersion         = 1
	gnmiAuditPathLimit       = 32
	gnmiGetMethod            = "/gnmi.gNMI/Get"
	gnmiSetMethod            = "/gnmi.gNMI/Set"
	gnmiGetAuditRate         = 60
	gnmiGetAuditBurst        = 60
	gnmiAuditSummaryInterval = time.Hour
)

const (
	gnmiAuditClassOK gnmiAuditClass = iota
	gnmiAuditClassDenied
	gnmiAuditClassError
	gnmiAuditClassCount
)

type gnmiAuditClass uint8

type gnmiAuditRecord struct {
	Version     int      `json:"v"`
	RequestID   string   `json:"request_id"`
	Principal   string   `json:"principal"`
	AuthResult  string   `json:"auth_result"`
	PeerType    string   `json:"peer_type"`
	PeerAddress string   `json:"peer_address"`
	Method      string   `json:"method"`
	Path        []string `json:"path"`
	Time        string   `json:"time"`
	Code        string   `json:"code"`
}

type gnmiAuditSummary struct {
	Version         int    `json:"v"`
	Method          string `json:"method"`
	Class           string `json:"class"`
	Suppressed      uint64 `json:"suppressed"`
	IntervalSeconds int64  `json:"interval_seconds"`
}

type gnmiAuditLimit struct {
	limiter    *rate.Limiter
	suppressed uint64
	scheduled  bool
}

type gnmiAuditLogger struct {
	sink     sharedlog.LineSink
	now      func() time.Time
	schedule scheduleFunc
	counter  uint64
	mu       sync.Mutex
	limits   [gnmiAuditClassCount]*gnmiAuditLimit
}

func newGNMIAuditLogger(sink sharedlog.LineSink) *gnmiAuditLogger {
	return newGNMIAuditLoggerWithClockAndScheduler(sink, time.Now, func(delay time.Duration, f func()) {
		time.AfterFunc(delay, f)
	})
}

func newGNMIAuditLoggerWithClock(sink sharedlog.LineSink, now func() time.Time) *gnmiAuditLogger {
	return newGNMIAuditLoggerWithClockAndScheduler(sink, now, func(delay time.Duration, f func()) {
		time.AfterFunc(delay, f)
	})
}

func newGNMIAuditLoggerWithClockAndScheduler(
	sink sharedlog.LineSink,
	now func() time.Time,
	schedule scheduleFunc,
) *gnmiAuditLogger {
	logger := &gnmiAuditLogger{sink: sink, now: now, schedule: schedule}
	for class := gnmiAuditClass(0); class < gnmiAuditClassCount; class++ {
		logger.limits[class] = &gnmiAuditLimit{
			limiter: rate.NewLimiter(rate.Every(time.Hour/gnmiGetAuditRate), gnmiGetAuditBurst),
		}
	}
	return logger
}

func (l *gnmiAuditLogger) newRecord(ctx context.Context, req interface{}, method string) gnmiAuditRecord {
	peerType, peerAddress := sharedlog.PeerTypeAddr(ctx)
	principal := transportPrincipal(ctx)
	switch {
	case peerType == "unix":
		principal = "local"
	case principal == "":
		principal = "unknown"
	}

	return gnmiAuditRecord{
		Version:     gnmiAuditVersion,
		RequestID:   fmt.Sprintf("GNMI-AUDIT-%d", atomic.AddUint64(&l.counter, 1)),
		Principal:   principal,
		AuthResult:  "not_evaluated",
		PeerType:    peerType,
		PeerAddress: peerAddress,
		Method:      auditMethod(method),
		Path:        auditRequestPaths(req),
	}
}

func (l *gnmiAuditLogger) finish(record *gnmiAuditRecord, rpcErr *error) {
	if panicValue := recover(); panicValue != nil {
		record.Time = l.now().UTC().Format(time.RFC3339Nano)
		record.Code = codes.Unknown.String()
		l.setAuthResult(record, codes.Unknown)
		l.emit(*record)
		panic(panicValue)
	}

	code := sharedlog.GRPCCode(errorValue(rpcErr))
	record.Time = l.now().UTC().Format(time.RFC3339Nano)
	record.Code = code.String()
	l.setAuthResult(record, code)
	l.emit(*record)
}

func (l *gnmiAuditLogger) setAuthResult(record *gnmiAuditRecord, code codes.Code) {
	if record.PeerType == "unix" {
		record.AuthResult = "local"
		return
	}
	if code == codes.Unauthenticated || code == codes.PermissionDenied {
		record.AuthResult = "denied"
	}
}

func (l *gnmiAuditLogger) emit(record gnmiAuditRecord) {
	if record.Method == "gnmi.get" && !l.allowGet(gnmiAuditOutcomeClass(record.Code)) {
		return
	}
	_ = sharedlog.WriteJSON(gnmiAuditPrefix, record, l.sink)
}

func (l *gnmiAuditLogger) allowGet(class gnmiAuditClass) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	limit := l.limits[class]
	if limit.limiter.AllowN(l.now(), 1) {
		return true
	}

	limit.suppressed++
	if !limit.scheduled {
		limit.scheduled = true
		l.schedule(gnmiAuditSummaryInterval, func() { l.writeSummary(class) })
	}
	return false
}

func (l *gnmiAuditLogger) writeSummary(class gnmiAuditClass) {
	l.mu.Lock()
	limit := l.limits[class]
	suppressed := limit.suppressed
	limit.suppressed = 0
	limit.scheduled = false
	l.mu.Unlock()

	if suppressed == 0 {
		return
	}
	_ = sharedlog.WriteJSON(gnmiAuditSummaryPrefix, gnmiAuditSummary{
		Version:         gnmiAuditVersion,
		Method:          "gnmi.get",
		Class:           class.String(),
		Suppressed:      suppressed,
		IntervalSeconds: int64(gnmiAuditSummaryInterval / time.Second),
	}, l.sink)
}

func gnmiAuditOutcomeClass(code string) gnmiAuditClass {
	switch code {
	case codes.OK.String():
		return gnmiAuditClassOK
	case codes.Unauthenticated.String(), codes.PermissionDenied.String():
		return gnmiAuditClassDenied
	default:
		return gnmiAuditClassError
	}
}

func (c gnmiAuditClass) String() string {
	switch c {
	case gnmiAuditClassOK:
		return "ok"
	case gnmiAuditClassDenied:
		return "denied"
	default:
		return "error"
	}
}

func (l *gnmiAuditLogger) unary(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (response interface{}, err error) {
	if info.FullMethod != gnmiGetMethod && info.FullMethod != gnmiSetMethod {
		return handler(ctx, req)
	}

	record := l.newRecord(ctx, req, info.FullMethod)
	defer l.finish(&record, &err)
	return handler(ctx, req)
}

func auditMethod(method string) string {
	if method == gnmiSetMethod {
		return "gnmi.set"
	}
	return "gnmi.get"
}

func auditRequestPaths(req interface{}) []string {
	switch request := req.(type) {
	case *gnmipb.GetRequest:
		return auditPaths(request.GetPrefix(), request.GetPath())
	case *gnmipb.SetRequest:
		paths := make([]*gnmipb.Path, 0,
			len(request.GetDelete())+len(request.GetReplace())+len(request.GetUpdate()))
		paths = append(paths, request.GetDelete()...)
		for _, update := range request.GetReplace() {
			paths = append(paths, update.GetPath())
		}
		for _, update := range request.GetUpdate() {
			paths = append(paths, update.GetPath())
		}
		return auditPaths(request.GetPrefix(), paths)
	default:
		return []string{"/"}
	}
}

func auditPaths(prefix *gnmipb.Path, paths []*gnmipb.Path) []string {
	if len(paths) == 0 {
		return []string{redactAuditPath(prefix, nil)}
	}

	result := make([]string, 0, min(len(paths), gnmiAuditPathLimit))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		redacted := redactAuditPath(prefix, path)
		if _, ok := seen[redacted]; ok {
			continue
		}
		seen[redacted] = struct{}{}
		if len(result) == gnmiAuditPathLimit {
			return append(result, "<truncated>")
		}
		result = append(result, redacted)
	}
	return result
}

func redactAuditPath(prefix, path *gnmipb.Path) string {
	names := append(pathNames(prefix), pathNames(path)...)
	if len(names) == 0 {
		return "/"
	}

	target := strings.Split(prefix.GetTarget(), "/")[0]
	origin := prefix.GetOrigin()
	if origin == "" {
		origin = path.GetOrigin()
	}
	if strings.HasSuffix(target, "_DB") {
		return "/" + names[0]
	}
	if origin == "sonic-db" {
		if len(names) >= 3 {
			return "/" + names[0] + "/" + names[2]
		}
		return "/" + names[0]
	}
	return "/" + strings.Join(names, "/")
}

func pathNames(path *gnmipb.Path) []string {
	if path == nil {
		return nil
	}
	if len(path.GetElem()) != 0 {
		names := make([]string, 0, len(path.GetElem()))
		for _, elem := range path.GetElem() {
			names = append(names, elem.GetName())
		}
		return names
	}
	names := make([]string, 0, len(path.GetElement()))
	for _, element := range path.GetElement() {
		if predicate := strings.IndexByte(element, '['); predicate >= 0 {
			element = element[:predicate]
		}
		names = append(names, element)
	}
	return names
}

func transportPrincipal(ctx context.Context) string {
	requestPeer, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	tlsInfo, ok := requestPeer.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return ""
	}
	return certificatePrincipal(tlsInfo.State.VerifiedChains[0][0])
}

func certificatePrincipal(cert *x509.Certificate) string {
	switch {
	case cert == nil:
		return ""
	case cert.Subject.CommonName != "":
		return cert.Subject.CommonName
	case len(cert.URIs) != 0:
		return cert.URIs[0].String()
	case len(cert.DNSNames) != 0:
		return cert.DNSNames[0]
	case len(cert.EmailAddresses) != 0:
		return cert.EmailAddresses[0]
	case len(cert.IPAddresses) != 0:
		return cert.IPAddresses[0].String()
	default:
		return ""
	}
}

func errorValue(err *error) error {
	if err == nil {
		return nil
	}
	return *err
}
