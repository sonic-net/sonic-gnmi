package interceptors

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type auditLogCapture struct {
	mu    sync.Mutex
	lines []string
}

type auditScheduleCapture struct {
	mu        sync.Mutex
	delays    []time.Duration
	callbacks []func()
}

func (c *auditLogCapture) sink(line string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
	return nil
}

func (c *auditLogCapture) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

func (c *auditScheduleCapture) schedule(delay time.Duration, callback func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delays = append(c.delays, delay)
	c.callbacks = append(c.callbacks, callback)
}

func (c *auditScheduleCapture) snapshot() ([]time.Duration, []func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...), append([]func(){}, c.callbacks...)
}

func decodeAuditRecord(t *testing.T, line string) gnmiAuditRecord {
	t.Helper()

	const prefix = gnmiAuditPrefix + " "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("audit line %q does not start with %q", line, prefix)
	}
	payload := []byte(strings.TrimPrefix(line, prefix))
	var fields map[string]interface{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("audit line is not valid JSON: %v", err)
	}
	gotFields := make([]string, 0, len(fields))
	for field := range fields {
		gotFields = append(gotFields, field)
	}
	slices.Sort(gotFields)
	wantFields := []string{
		"auth_result", "code", "method", "path", "peer_address",
		"peer_type", "principal", "request_id", "time", "v",
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("audit fields = %v, want %v", gotFields, wantFields)
	}

	var record gnmiAuditRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatalf("cannot decode audit record: %v", err)
	}
	return record
}

func decodeAuditSummary(t *testing.T, line string) gnmiAuditSummary {
	t.Helper()

	const prefix = gnmiAuditSummaryPrefix + " "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("summary line %q does not start with %q", line, prefix)
	}
	payload := []byte(strings.TrimPrefix(line, prefix))
	var fields map[string]interface{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("summary line is not valid JSON: %v", err)
	}
	gotFields := make([]string, 0, len(fields))
	for field := range fields {
		gotFields = append(gotFields, field)
	}
	slices.Sort(gotFields)
	wantFields := []string{"class", "interval_seconds", "method", "suppressed", "v"}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("summary fields = %v, want %v", gotFields, wantFields)
	}

	var summary gnmiAuditSummary
	if err := json.Unmarshal(payload, &summary); err != nil {
		t.Fatalf("cannot decode audit summary: %v", err)
	}
	return summary
}

func TestGNMIAuditGetCompletion(t *testing.T) {
	now := time.Date(2026, time.August, 4, 18, 30, 0, 123, time.UTC)
	capture := &auditLogCapture{}
	logger := newGNMIAuditLoggerWithClock(capture.sink, func() time.Time { return now })
	ctx := auditTLSContext("client-cn", "192.0.2.10:50051")
	req := &gnmipb.GetRequest{
		Prefix: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
		Path: []*gnmipb.Path{{Elem: []*gnmipb.PathElem{
			{Name: "interface", Key: map[string]string{"name": "SECRET_PORT"}},
			{Name: "state"},
		}}},
	}

	response, err := logger.UnaryInterceptor()(ctx, req,
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) {
			if lines := capture.snapshot(); len(lines) != 0 {
				t.Fatalf("audit emitted before completion: %v", lines)
			}
			return "response", nil
		})
	if err != nil || response != "response" {
		t.Fatalf("UnaryInterceptor() = %v, %v; want response, nil", response, err)
	}

	lines := capture.snapshot()
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d, want 1: %v", len(lines), lines)
	}
	if strings.Contains(lines[0], "SECRET_PORT") {
		t.Fatalf("audit leaked path key value: %s", lines[0])
	}
	got := decodeAuditRecord(t, lines[0])
	if got.Version != 1 || got.RequestID != "GNMI-AUDIT-1" ||
		got.Principal != "client-cn" || got.AuthResult != "not_evaluated" ||
		got.PeerType != "tcp" || got.PeerAddress != "192.0.2.10:50051" ||
		got.Method != "gnmi.get" ||
		!reflect.DeepEqual(got.Path, []string{"/interfaces/interface/state"}) ||
		got.Time != now.Format(time.RFC3339Nano) || got.Code != codes.OK.String() {
		t.Fatalf("audit record = %+v", got)
	}
}

func TestGNMIAuditSetDeniedRedactsValues(t *testing.T) {
	now := time.Date(2026, time.August, 4, 18, 31, 0, 0, time.UTC)
	capture := &auditLogCapture{}
	logger := newGNMIAuditLoggerWithClock(capture.sink, func() time.Time { return now })
	req := &gnmipb.SetRequest{
		Prefix: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
		Delete: []*gnmipb.Path{{Elem: []*gnmipb.PathElem{
			{Name: "interface", Key: map[string]string{"name": "SECRET_DELETE"}},
			{Name: "config"}, {Name: "description"},
		}}},
		Replace: []*gnmipb.Update{{
			Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "replace"}}},
			Val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "SECRET_REPLACE"}},
		}},
		Update: []*gnmipb.Update{{
			Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "update"}}},
			Val:  &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "SECRET_UPDATE"}},
		}},
	}
	wantErr := status.Error(codes.PermissionDenied, "denied")

	_, err := logger.UnaryInterceptor()(context.Background(), req,
		&grpc.UnaryServerInfo{FullMethod: gnmiSetMethod},
		func(context.Context, interface{}) (interface{}, error) { return nil, wantErr })
	if err != wantErr {
		t.Fatalf("UnaryInterceptor() error = %v, want original error", err)
	}

	lines := capture.snapshot()
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d, want 1: %v", len(lines), lines)
	}
	for _, secret := range []string{"SECRET_DELETE", "SECRET_REPLACE", "SECRET_UPDATE"} {
		if strings.Contains(lines[0], secret) {
			t.Fatalf("audit leaked %q: %s", secret, lines[0])
		}
	}
	got := decodeAuditRecord(t, lines[0])
	wantPaths := []string{
		"/interfaces/interface/config/description",
		"/interfaces/replace",
		"/interfaces/update",
	}
	if got.Method != "gnmi.set" || got.AuthResult != "denied" ||
		got.Code != codes.PermissionDenied.String() || !reflect.DeepEqual(got.Path, wantPaths) {
		t.Fatalf("audit record = %+v, want paths %v", got, wantPaths)
	}
}

func TestGNMIAuditMethodScope(t *testing.T) {
	capture := &auditLogCapture{}
	logger := newGNMIAuditLogger(capture.sink)

	response, err := logger.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/gnoi.system.System/Time"},
		func(context.Context, interface{}) (interface{}, error) { return "response", nil })
	if err != nil || response != "response" {
		t.Fatalf("non-audit unary call = %v, %v", response, err)
	}

	stream := &accessLogServerStream{ctx: context.Background()}
	err = logger.StreamInterceptor()(nil, stream,
		&grpc.StreamServerInfo{FullMethod: "/gnmi.gNMI/Subscribe"},
		func(interface{}, grpc.ServerStream) error { return nil })
	if err != nil {
		t.Fatalf("stream call failed: %v", err)
	}
	if lines := capture.snapshot(); len(lines) != 0 {
		t.Fatalf("out-of-scope calls emitted audit records: %v", lines)
	}
}

func TestGNMIAuditPanicIsRecordedAndRepropagated(t *testing.T) {
	capture := &auditLogCapture{}
	logger := newGNMIAuditLogger(capture.sink)
	var recovered interface{}

	func() {
		defer func() { recovered = recover() }()
		_, _ = logger.UnaryInterceptor()(context.Background(), &gnmipb.GetRequest{},
			&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
			func(context.Context, interface{}) (interface{}, error) {
				panic("SECRET_PANIC")
			})
	}()

	if recovered != "SECRET_PANIC" {
		t.Fatalf("recovered panic = %v, want original panic", recovered)
	}
	lines := capture.snapshot()
	if len(lines) != 1 {
		t.Fatalf("panic audit lines = %d, want 1: %v", len(lines), lines)
	}
	if strings.Contains(lines[0], "SECRET_PANIC") {
		t.Fatalf("audit leaked panic value: %s", lines[0])
	}
	if got := decodeAuditRecord(t, lines[0]); got.Code != codes.Unknown.String() {
		t.Fatalf("panic code = %q, want Unknown", got.Code)
	}
}

func TestGNMIAuditGetRateLimitIsConcurrent(t *testing.T) {
	now := time.Date(2026, time.August, 4, 19, 0, 0, 0, time.UTC)
	capture := &auditLogCapture{}
	schedules := &auditScheduleCapture{}
	logger := newGNMIAuditLoggerWithClockAndScheduler(
		capture.sink,
		func() time.Time { return now },
		schedules.schedule,
	)
	interceptor := logger.UnaryInterceptor()

	const calls = 100
	var wg sync.WaitGroup
	wg.Add(calls)
	for range calls {
		go func() {
			defer wg.Done()
			_, _ = interceptor(context.Background(), &gnmipb.GetRequest{},
				&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
				func(context.Context, interface{}) (interface{}, error) { return nil, nil })
		}()
	}
	wg.Wait()

	lines := capture.snapshot()
	if len(lines) != gnmiGetAuditBurst {
		t.Fatalf("concurrent audit lines = %d, want burst %d", len(lines), gnmiGetAuditBurst)
	}
	ids := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		record := decodeAuditRecord(t, line)
		ids[record.RequestID] = struct{}{}
	}
	if len(ids) != len(lines) {
		t.Fatalf("unique request IDs = %d, want %d", len(ids), len(lines))
	}

	delays, callbacks := schedules.snapshot()
	if !reflect.DeepEqual(delays, []time.Duration{time.Hour}) || len(callbacks) != 1 {
		t.Fatalf("scheduled summaries = %v/%d, want [1h]/1", delays, len(callbacks))
	}
	callbacks[0]()

	lines = capture.snapshot()
	if len(lines) != gnmiGetAuditBurst+1 {
		t.Fatalf("lines after summary = %d, want %d", len(lines), gnmiGetAuditBurst+1)
	}
	summary := decodeAuditSummary(t, lines[len(lines)-1])
	if summary.Version != 1 || summary.Method != "gnmi.get" ||
		summary.Class != "ok" || summary.Suppressed != calls-gnmiGetAuditBurst ||
		summary.IntervalSeconds != 3600 {
		t.Fatalf("summary = %+v", summary)
	}

	_, _ = interceptor(context.Background(), &gnmipb.GetRequest{},
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	delays, callbacks = schedules.snapshot()
	if len(delays) != 2 || len(callbacks) != 2 {
		t.Fatalf("rearmed summaries = %v/%d, want 2/2", delays, len(callbacks))
	}
	callbacks[1]()
	lines = capture.snapshot()
	if summary = decodeAuditSummary(t, lines[len(lines)-1]); summary.Suppressed != 1 {
		t.Fatalf("rearmed summary = %+v, want one suppressed Get", summary)
	}
}

func TestGNMIAuditSetIsUnsampledAndConcurrent(t *testing.T) {
	capture := &auditLogCapture{}
	schedules := &auditScheduleCapture{}
	logger := newGNMIAuditLoggerWithClockAndScheduler(
		capture.sink,
		time.Now,
		schedules.schedule,
	)
	interceptor := logger.UnaryInterceptor()

	const calls = 100
	var wg sync.WaitGroup
	wg.Add(calls)
	for range calls {
		go func() {
			defer wg.Done()
			_, _ = interceptor(context.Background(), &gnmipb.SetRequest{},
				&grpc.UnaryServerInfo{FullMethod: gnmiSetMethod},
				func(context.Context, interface{}) (interface{}, error) { return nil, nil })
		}()
	}
	wg.Wait()

	lines := capture.snapshot()
	if len(lines) != calls {
		t.Fatalf("concurrent Set audit lines = %d, want %d", len(lines), calls)
	}
	if delays, callbacks := schedules.snapshot(); len(delays) != 0 || len(callbacks) != 0 {
		t.Fatalf("Set scheduled summaries: %v/%d", delays, len(callbacks))
	}
	for _, line := range lines {
		if record := decodeAuditRecord(t, line); record.Method != "gnmi.set" {
			t.Fatalf("Set audit method = %q", record.Method)
		}
	}
}

func TestGNMIAuditGetOutcomeBucketsAreIndependentAndBounded(t *testing.T) {
	now := time.Date(2026, time.August, 4, 19, 0, 0, 0, time.UTC)
	capture := &auditLogCapture{}
	schedules := &auditScheduleCapture{}
	logger := newGNMIAuditLoggerWithClockAndScheduler(
		capture.sink,
		func() time.Time { return now },
		schedules.schedule,
	)
	if len(logger.limits) != int(gnmiAuditClassCount) {
		t.Fatalf("limiter buckets = %d, want %d", len(logger.limits), gnmiAuditClassCount)
	}

	errorsByClass := []error{
		nil,
		status.Error(codes.PermissionDenied, "denied"),
		status.Error(codes.Internal, "failed"),
	}
	for _, rpcErr := range errorsByClass {
		for i := 0; i <= gnmiGetAuditBurst; i++ {
			_, _ = logger.UnaryInterceptor()(context.Background(), &gnmipb.GetRequest{},
				&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
				func(context.Context, interface{}) (interface{}, error) { return nil, rpcErr })
		}
	}

	if lines := capture.snapshot(); len(lines) != gnmiGetAuditBurst*int(gnmiAuditClassCount) {
		t.Fatalf("individual records = %d, want %d", len(lines),
			gnmiGetAuditBurst*int(gnmiAuditClassCount))
	}
	delays, callbacks := schedules.snapshot()
	if len(callbacks) != int(gnmiAuditClassCount) {
		t.Fatalf("summary callbacks = %d, want %d", len(callbacks), gnmiAuditClassCount)
	}
	for _, delay := range delays {
		if delay != time.Hour {
			t.Fatalf("summary delay = %v, want 1h", delay)
		}
	}
	for _, callback := range callbacks {
		callback()
	}

	lines := capture.snapshot()
	summaries := make(map[string]gnmiAuditSummary)
	for _, line := range lines[gnmiGetAuditBurst*int(gnmiAuditClassCount):] {
		summary := decodeAuditSummary(t, line)
		summaries[summary.Class] = summary
	}
	for _, class := range []string{"ok", "denied", "error"} {
		if summary, ok := summaries[class]; !ok || summary.Suppressed != 1 {
			t.Fatalf("summary[%q] = %+v, found=%v", class, summary, ok)
		}
	}
}

func TestGNMIAuditGetLimiterRefillsOneTokenPerMinute(t *testing.T) {
	now := time.Date(2026, time.August, 4, 19, 0, 0, 0, time.UTC)
	capture := &auditLogCapture{}
	schedules := &auditScheduleCapture{}
	logger := newGNMIAuditLoggerWithClockAndScheduler(
		capture.sink,
		func() time.Time { return now },
		schedules.schedule,
	)
	interceptor := logger.UnaryInterceptor()
	call := func() {
		_, _ = interceptor(context.Background(), &gnmipb.GetRequest{},
			&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	}

	for i := 0; i < gnmiGetAuditBurst; i++ {
		call()
	}
	call()
	if got := len(capture.snapshot()); got != gnmiGetAuditBurst {
		t.Fatalf("records before refill = %d, want %d", got, gnmiGetAuditBurst)
	}
	now = now.Add(time.Minute)
	call()
	if got := len(capture.snapshot()); got != gnmiGetAuditBurst+1 {
		t.Fatalf("records after refill = %d, want %d", got, gnmiGetAuditBurst+1)
	}
}

func TestGNMIAuditAndRPCAccessShareSink(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	sink := func(line string) error {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, line)
		return nil
	}
	chain := NewChain(newRPCLogger(sink), newGNMIAuditLogger(sink))

	_, err := chain.UnaryInterceptor()(context.Background(), &gnmipb.GetRequest{},
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	if err != nil {
		t.Fatalf("chained call failed: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("shared sink lines = %d, want 2: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], gnmiAuditPrefix+" ") ||
		!strings.HasPrefix(lines[1], rpcAccessLogPrefix+" ") {
		t.Fatalf("completion log order = %v, want GNMI_AUDIT then RPC_ACCESS", lines)
	}
}

func TestGNMIAuditWriterPanicDoesNotChangeRPCResult(t *testing.T) {
	logger := newGNMIAuditLogger(func(string) error { panic("sink failure") })
	response, err := logger.UnaryInterceptor()(context.Background(), &gnmipb.GetRequest{},
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) { return "response", nil })
	if err != nil || response != "response" {
		t.Fatalf("UnaryInterceptor() = %v, %v; want response, nil", response, err)
	}
}

func TestGNMIAuditPathLimitAndLegacyElements(t *testing.T) {
	paths := make([]*gnmipb.Path, 0, gnmiAuditPathLimit+1)
	for i := 0; i <= gnmiAuditPathLimit; i++ {
		paths = append(paths, &gnmipb.Path{
			Element: []string{string(rune('a'+i)) + "[name=SECRET]"},
		})
	}
	got := auditPaths(nil, paths)
	if len(got) != gnmiAuditPathLimit+1 || got[gnmiAuditPathLimit] != "<truncated>" {
		t.Fatalf("limited paths = %v", got)
	}
	if got[0] != "/a" {
		t.Fatalf("legacy element path = %q, want /a", got[0])
	}
	for _, path := range got {
		if strings.Contains(path, "SECRET") {
			t.Fatalf("legacy element leaked predicate: %q", path)
		}
	}
}

func TestCertificatePrincipalPrecedence(t *testing.T) {
	uri, err := url.Parse("spiffe://example/client")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		cert *x509.Certificate
		want string
	}{
		{name: "CN", cert: &x509.Certificate{
			Subject: pkix.Name{CommonName: "client-cn"}, URIs: []*url.URL{uri},
		}, want: "client-cn"},
		{name: "URI", cert: &x509.Certificate{URIs: []*url.URL{uri}}, want: uri.String()},
		{name: "DNS", cert: &x509.Certificate{DNSNames: []string{"client.example"}}, want: "client.example"},
		{name: "email", cert: &x509.Certificate{EmailAddresses: []string{"client@example.com"}}, want: "client@example.com"},
		{name: "IP", cert: &x509.Certificate{IPAddresses: []net.IP{net.ParseIP("192.0.2.20")}}, want: "192.0.2.20"},
		{name: "empty", cert: &x509.Certificate{}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := certificatePrincipal(tc.cert); got != tc.want {
				t.Fatalf("certificatePrincipal() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGNMIAuditIgnoresUnverifiedCertificate(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: "spoofed"}}},
			},
		},
	})
	record := newGNMIAuditLogger(func(string) error { return nil }).
		newRecord(ctx, &gnmipb.GetRequest{}, gnmiGetMethod)
	if record.Principal != "unknown" {
		t.Fatalf("principal = %q, want unknown for unverified certificate", record.Principal)
	}
}

func TestGNMIAuditUnixPeerIsLocal(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.UnixAddr{Name: "/run/gnmi.sock", Net: "unix"},
	})
	capture := &auditLogCapture{}
	logger := newGNMIAuditLogger(capture.sink)
	_, _ = logger.UnaryInterceptor()(ctx, &gnmipb.GetRequest{},
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) {
			return nil, status.Error(codes.PermissionDenied, "denied")
		})
	got := decodeAuditRecord(t, capture.snapshot()[0])
	if got.Principal != "local" || got.AuthResult != "local" ||
		got.PeerType != "unix" || got.PeerAddress != "/run/gnmi.sock" {
		t.Fatalf("unix audit record = %+v", got)
	}
}

func auditTLSContext(commonName, address string) context.Context {
	host, port, _ := net.SplitHostPort(address)
	portNumber := 0
	for _, digit := range port {
		portNumber = portNumber*10 + int(digit-'0')
	}
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: commonName}}
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP(host), Port: portNumber},
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}},
		},
	})
}
