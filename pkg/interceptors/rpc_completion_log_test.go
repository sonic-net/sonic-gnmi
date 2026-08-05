package interceptors

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"net"
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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type accessLogServerStream struct {
	ctx context.Context
}

type completionTestAddr struct {
	network string
	address string
}

func (a completionTestAddr) Network() string { return a.network }
func (a completionTestAddr) String() string  { return a.address }

type completionCapture struct {
	mu        sync.Mutex
	lines     []string
	delays    []time.Duration
	callbacks []func()
}

func (c *completionCapture) sink(line string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
	return nil
}

func (c *completionCapture) schedule(delay time.Duration, callback func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delays = append(c.delays, delay)
	c.callbacks = append(c.callbacks, callback)
}

func (c *completionCapture) snapshot() ([]string, []time.Duration, []func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...),
		append([]time.Duration(nil), c.delays...),
		append([]func(){}, c.callbacks...)
}

func (s *accessLogServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *accessLogServerStream) SendHeader(metadata.MD) error { return nil }
func (s *accessLogServerStream) SetTrailer(metadata.MD)       {}
func (s *accessLogServerStream) Context() context.Context     { return s.ctx }
func (s *accessLogServerStream) SendMsg(interface{}) error    { return nil }
func (s *accessLogServerStream) RecvMsg(interface{}) error    { return nil }

func captureAccessLog(t *testing.T) (lineSink, func() rpcCompletionRecord) {
	t.Helper()

	var lines []string
	sink := func(line string) error {
		lines = append(lines, line)
		return nil
	}
	record := func() rpcCompletionRecord {
		t.Helper()
		if len(lines) != 1 {
			t.Fatalf("got %d log lines, want 1: %v", len(lines), lines)
		}
		return parseAccessLog(t, lines[0])
	}
	return sink, record
}

func parseAccessLog(t *testing.T, line string) rpcCompletionRecord {
	t.Helper()

	const prefix = "RPC_COMPLETION "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("log line %q does not start with %q", line, prefix)
	}

	payload := []byte(strings.TrimPrefix(line, prefix))
	var fields map[string]interface{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	gotFields := make([]string, 0, len(fields))
	for field := range fields {
		gotFields = append(gotFields, field)
	}
	slices.Sort(gotFields)
	wantFields := []string{"auth_type", "code", "duration_ms", "method", "path", "peer", "peer_type", "principal", "suppressed", "type", "v"}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("log record fields = %v, want %v", gotFields, wantFields)
	}

	var got rpcCompletionRecord
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("cannot decode access log: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("access log version = %d, want 2", got.Version)
	}
	if got.Principal != "" || got.AuthType != "" || len(got.Path) != 0 {
		t.Fatalf("access defaults = principal %q, auth_type %q, path %v; want empty",
			got.Principal, got.AuthType, got.Path)
	}
	return got
}

func parseAccessLogSummary(t *testing.T, line string) rpcCompletionSummary {
	t.Helper()

	const prefix = "RPC_COMPLETION_SUMMARY "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("log line %q does not start with %q", line, prefix)
	}
	payload := []byte(strings.TrimPrefix(line, prefix))
	var fields map[string]interface{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("log summary is not valid JSON: %v", err)
	}
	gotFields := make([]string, 0, len(fields))
	for field := range fields {
		gotFields = append(gotFields, field)
	}
	slices.Sort(gotFields)
	wantFields := []string{"code", "method", "suppressed", "v"}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("log summary fields = %v, want %v", gotFields, wantFields)
	}

	var got rpcCompletionSummary
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("cannot decode access log summary: %v", err)
	}
	return got
}

func TestRPCLoggerEndToEnd(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() failed: %v", err)
	}
	defer listener.Close()

	logs := make(chan string, 1)
	logger := newRPCCompletionLogger(func(line string) error {
		logs <- line
		return nil
	})
	server := grpc.NewServer(
		grpc.UnaryInterceptor(logger.UnaryInterceptor()),
		grpc.StreamInterceptor(logger.StreamInterceptor()),
	)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil {
			select {
			case logs <- fmt.Sprintf("serve error: %v", serveErr):
			default:
			}
		}
	}()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("grpc.DialContext() failed: %v", err)
	}
	defer conn.Close()

	response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Health.Check() failed: %v", err)
	}
	if response.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("Health.Check() status = %v, want SERVING", response.Status)
	}

	select {
	case line := <-logs:
		got := parseAccessLog(t, line)
		if got.RPCType != "unary" || got.Method != "/grpc.health.v1.Health/Check" ||
			got.PeerType != "tcp" || got.Code != codes.OK.String() {
			t.Fatalf("access log = %+v, want successful TCP Health.Check", got)
		}
		if got.DurationMS < 0 {
			t.Fatalf("duration_ms = %d, want non-negative", got.DurationMS)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for RPC access log")
	}
}

func TestRPCLoggerRateLimitsEachMethodAndCode(t *testing.T) {
	now := time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)
	var lines []string
	logger := newRPCCompletionLoggerWithClockAndScheduler(func(line string) error {
		lines = append(lines, line)
		return nil
	}, func() time.Time { return now }, func(time.Duration, func()) {})
	interceptor := logger.UnaryInterceptor()

	call := func(method string, wantErr error) {
		t.Helper()
		_, err := interceptor(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: method},
			func(context.Context, interface{}) (interface{}, error) { return nil, wantErr })
		if err != wantErr {
			t.Fatalf("UnaryInterceptor() error = %v, want %v", err, wantErr)
		}
	}

	call("/test.Service/Get", nil)
	call("/test.Service/Get", nil)
	call("/test.Service/Get", nil)
	if len(lines) != 1 {
		t.Fatalf("same-key calls produced %d log lines, want 1: %v", len(lines), lines)
	}

	permissionDenied := status.Error(codes.PermissionDenied, "not allowed")
	call("/test.Service/Get", permissionDenied)
	call("/test.Service/Set", nil)
	if len(lines) != 3 {
		t.Fatalf("distinct-key calls produced %d log lines, want 3: %v", len(lines), lines)
	}

	now = now.Add(10 * time.Second)
	call("/test.Service/Get", nil)
	if len(lines) != 4 {
		t.Fatalf("call after interval produced %d log lines, want 4: %v", len(lines), lines)
	}
	got := parseAccessLog(t, lines[3])
	if got.Method != "/test.Service/Get" || got.Code != codes.OK.String() ||
		got.Suppressed == nil || *got.Suppressed != 2 {
		t.Fatalf("access log = %+v, want Get/OK with 2 suppressed calls", got)
	}
}

func TestRPCLoggerReportsSuppressionAfterTrafficStops(t *testing.T) {
	now := time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)
	var lines []string
	var scheduled func()
	logger := newRPCCompletionLoggerWithClockAndScheduler(func(line string) error {
		lines = append(lines, line)
		return nil
	}, func() time.Time { return now }, func(delay time.Duration, f func()) {
		if delay != 10*time.Second {
			t.Fatalf("summary delay = %v, want 10s", delay)
		}
		scheduled = f
	})
	interceptor := logger.UnaryInterceptor()
	call := func() {
		_, _ = interceptor(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: "/test.Service/Get"},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	}

	call()
	call()
	call()
	if scheduled == nil {
		t.Fatal("suppression summary was not scheduled")
	}
	now = now.Add(10 * time.Second)
	scheduled()

	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want access and summary: %v", len(lines), lines)
	}
	got := parseAccessLogSummary(t, lines[1])
	if got.Version != 1 || got.Method != "/test.Service/Get" ||
		got.Code != codes.OK.String() || got.Suppressed != 2 {
		t.Fatalf("access log summary = %+v, want Get/OK with 2 suppressed calls", got)
	}
	call()
	if len(lines) != 2 {
		t.Fatalf("call after summary produced %d log lines, want shared rate limit", len(lines))
	}
}

func TestRPCLoggerRetriesSummaryWhenAccessRecordWinsInterval(t *testing.T) {
	now := time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)
	var lines []string
	var scheduled []func()
	logger := newRPCCompletionLoggerWithClockAndScheduler(func(line string) error {
		lines = append(lines, line)
		return nil
	}, func() time.Time { return now }, func(delay time.Duration, f func()) {
		if delay != 10*time.Second {
			t.Fatalf("summary delay = %v, want 10s", delay)
		}
		scheduled = append(scheduled, f)
	})
	interceptor := logger.UnaryInterceptor()
	call := func() {
		_, _ = interceptor(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: "/test.Service/Get"},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	}

	call()
	call()
	now = now.Add(10 * time.Second)
	call()
	call()
	scheduled[0]()
	if len(scheduled) != 2 {
		t.Fatalf("scheduled callbacks = %d, want summary retry", len(scheduled))
	}
	if len(lines) != 2 {
		t.Fatalf("got %d log lines before retry, want 2: %v", len(lines), lines)
	}

	now = now.Add(10 * time.Second)
	scheduled[1]()
	if len(lines) != 3 {
		t.Fatalf("got %d log lines after retry, want 3: %v", len(lines), lines)
	}
	if got := parseAccessLogSummary(t, lines[2]); got.Suppressed != 1 {
		t.Fatalf("suppressed = %d, want 1 since last access record", got.Suppressed)
	}
}

func TestRPCLoggerRateLimitIsConcurrent(t *testing.T) {
	now := time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)
	var lines []string
	var linesMu sync.Mutex
	logger := newRPCCompletionLoggerWithClockAndScheduler(func(line string) error {
		linesMu.Lock()
		defer linesMu.Unlock()
		lines = append(lines, line)
		return nil
	}, func() time.Time { return now }, func(time.Duration, func()) {})
	interceptor := logger.UnaryInterceptor()

	const calls = 100
	var wg sync.WaitGroup
	wg.Add(calls)
	for range calls {
		go func() {
			defer wg.Done()
			_, _ = interceptor(context.Background(), nil,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Get"},
				func(context.Context, interface{}) (interface{}, error) { return nil, nil })
		}()
	}
	wg.Wait()

	if len(lines) != 1 {
		t.Fatalf("concurrent calls produced %d log lines, want 1", len(lines))
	}
	now = now.Add(10 * time.Second)
	_, _ = interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Get"},
		func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	if got := parseAccessLog(t, lines[1]); got.Suppressed == nil ||
		*got.Suppressed != calls-1 {
		t.Fatalf("suppressed = %v, want %d", got.Suppressed, calls-1)
	}
}

func TestRPCLoggerUnaryError(t *testing.T) {
	sink, capturedRecord := captureAccessLog(t)
	logger := newRPCCompletionLogger(sink)
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Set"}
	wantErr := status.Error(codes.PermissionDenied, "not allowed")

	response, err := logger.UnaryInterceptor()(context.Background(), "secret request", info,
		func(context.Context, interface{}) (interface{}, error) {
			return nil, wantErr
		})
	if err != wantErr {
		t.Fatalf("UnaryInterceptor() error = %v, want original error %v", err, wantErr)
	}
	if response != nil {
		t.Fatalf("UnaryInterceptor() response = %v, want nil", response)
	}

	got := capturedRecord()
	if got.Code != codes.PermissionDenied.String() {
		t.Fatalf("access log code = %q, want %q", got.Code, codes.PermissionDenied)
	}
	if got.PeerType != "unknown" || got.Peer != "" {
		t.Fatalf("access log peer = %q/%q, want unknown/empty", got.PeerType, got.Peer)
	}
}

func TestRPCLoggerUnaryContextErrorUsesGRPCCode(t *testing.T) {
	sink, capturedRecord := captureAccessLog(t)
	logger := newRPCCompletionLogger(sink)

	_, err := logger.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Get"},
		func(context.Context, interface{}) (interface{}, error) {
			return nil, context.DeadlineExceeded
		})
	if err != context.DeadlineExceeded {
		t.Fatalf("UnaryInterceptor() error = %v, want original context error", err)
	}
	if got := capturedRecord(); got.Code != codes.DeadlineExceeded.String() {
		t.Fatalf("access log code = %q, want %q", got.Code, codes.DeadlineExceeded)
	}
}

func TestRPCLoggerStreamError(t *testing.T) {
	sink, capturedRecord := captureAccessLog(t)
	logger := newRPCCompletionLogger(sink)
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.UnixAddr{Name: "/var/run/gnmi/gnmi.sock", Net: "unix"},
	})
	stream := &accessLogServerStream{ctx: ctx}
	info := &grpc.StreamServerInfo{FullMethod: "/gnmi.gNMI/Subscribe"}
	wantErr := status.Error(codes.Canceled, "client closed stream")

	err := logger.StreamInterceptor()(nil, stream, info,
		func(interface{}, grpc.ServerStream) error {
			return wantErr
		})
	if err != wantErr {
		t.Fatalf("StreamInterceptor() error = %v, want original error %v", err, wantErr)
	}

	got := capturedRecord()
	want := rpcCompletionRecord{
		Version:  2,
		RPCType:  "stream",
		Method:   "/gnmi.gNMI/Subscribe",
		PeerType: "unix",
		Peer:     "/var/run/gnmi/gnmi.sock",
		Code:     codes.Canceled.String(),
	}
	if got.Version != want.Version || got.RPCType != want.RPCType || got.Method != want.Method ||
		got.PeerType != want.PeerType || got.Peer != want.Peer || got.Code != want.Code {
		t.Fatalf("access log = %+v, want %+v", got, want)
	}
}

func TestRPCLoggerDoesNotPropagateLoggingPanic(t *testing.T) {
	logger := newRPCCompletionLogger(func(string) error { panic("log sink failure") })

	response, err := logger.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Get"},
		func(context.Context, interface{}) (interface{}, error) { return "response", nil })
	if err != nil || response != "response" {
		t.Fatalf("UnaryInterceptor() = %v, %v; want response, nil", response, err)
	}
}

func TestRPCLoggerRecordsInnerShortCircuit(t *testing.T) {
	sink, capturedRecord := captureAccessLog(t)
	logger := newRPCCompletionLogger(sink)
	calls := []string{}
	shortCircuit := &mockInterceptor{name: "short-circuit", calls: &calls, shouldReplace: true}
	chain := NewChain(logger, shortCircuit)

	response, err := chain.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/gnoi.os.OS/Activate"},
		func(context.Context, interface{}) (interface{}, error) {
			t.Fatal("handler called after inner interceptor short-circuited")
			return nil, nil
		})
	if err != nil {
		t.Fatalf("UnaryInterceptor() returned error: %v", err)
	}
	if response != "short-circuit response" {
		t.Fatalf("UnaryInterceptor() response = %v, want short-circuit response", response)
	}
	if got := capturedRecord(); got.Method != "/gnoi.os.OS/Activate" || got.Code != codes.OK.String() {
		t.Fatalf("access log = %+v, want short-circuited RPC with OK status", got)
	}
}

func TestDeriveRequestFields(t *testing.T) {
	getPath := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "get"}}}
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1234},
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{{
					Subject: pkix.Name{CommonName: "client-cn"},
				}},
			},
		},
	})

	got := deriveRequestFields(ctx, &gnmipb.GetRequest{Path: []*gnmipb.Path{getPath}})
	if got.peerType != "tcp" || got.address != "10.0.0.1:1234" ||
		got.principal != "client-cn" || got.authType != "tls" ||
		len(got.paths) != 1 || got.paths[0] != getPath {
		t.Fatalf("deriveRequestFields() = %+v", got)
	}

	empty := deriveRequestFields(context.Background(), nil)
	if empty.peerType != "unknown" || empty.address != "" ||
		empty.principal != "" || empty.authType != "" || len(empty.paths) != 0 {
		t.Fatalf("deriveRequestFields() defaults = %+v", empty)
	}
}

func TestRequestPathsSet(t *testing.T) {
	deletePath := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "delete"}}}
	replacePath := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "replace"}}}
	updatePath := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "update"}}}
	got := requestPaths(&gnmipb.SetRequest{
		Delete:  []*gnmipb.Path{deletePath},
		Replace: []*gnmipb.Update{{Path: replacePath}},
		Update:  []*gnmipb.Update{{Path: updatePath}},
	})
	want := []*gnmipb.Path{deletePath, replacePath, updatePath}
	if len(got) != len(want) {
		t.Fatalf("Set paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Set path[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestGNMIGetPolicyAndSummary(t *testing.T) {
	now := time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC)
	capture := &completionCapture{}
	logger := newRPCCompletionLoggerWithClockAndScheduler(
		capture.sink,
		func() time.Time { return now },
		capture.schedule,
	)
	interceptor := logger.UnaryInterceptor()
	policy := rpcLogPolicyForMethod(gnmiGetMethod)

	const calls = 100
	for range calls {
		_, _ = interceptor(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	}

	lines, delays, callbacks := capture.snapshot()
	if len(lines) != policy.burst ||
		!reflect.DeepEqual(delays, []time.Duration{policy.summaryInterval}) ||
		len(callbacks) != 1 {
		t.Fatalf("Get policy output = lines %d delays %v callbacks %d",
			len(lines), delays, len(callbacks))
	}

	now = now.Add(policy.summaryInterval)
	callbacks[0]()
	lines, _, _ = capture.snapshot()
	if len(lines) != policy.burst+1 {
		t.Fatalf("Get lines after summary = %d, want %d", len(lines), policy.burst+1)
	}
	summary := parseAccessLogSummary(t, lines[len(lines)-1])
	if summary.Method != gnmiGetMethod || summary.Code != codes.OK.String() ||
		summary.Suppressed != uint64(calls-policy.burst) {
		t.Fatalf("Get summary = %+v", summary)
	}

	refillNow := time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC)
	refillCapture := &completionCapture{}
	refillLogger := newRPCCompletionLoggerWithClockAndScheduler(
		refillCapture.sink,
		func() time.Time { return refillNow },
		refillCapture.schedule,
	)
	refillInterceptor := refillLogger.UnaryInterceptor()
	for range policy.burst {
		_, _ = refillInterceptor(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	}
	refillNow = refillNow.Add(policy.tokenInterval)
	_, _ = refillInterceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	if refillLines, _, _ := refillCapture.snapshot(); len(refillLines) != policy.burst+1 {
		t.Fatalf("Get records after token refill = %d, want %d",
			len(refillLines), policy.burst+1)
	}
}

func TestGNMISetPolicyIsUnlimited(t *testing.T) {
	capture := &completionCapture{}
	logger := newRPCCompletionLoggerWithClockAndScheduler(
		capture.sink, time.Now, capture.schedule)
	interceptor := logger.UnaryInterceptor()

	const calls = 100
	for range calls {
		_, _ = interceptor(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: gnmiSetMethod},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	}

	lines, delays, callbacks := capture.snapshot()
	if len(lines) != calls || len(delays) != 0 || len(callbacks) != 0 {
		t.Fatalf("Set policy output = lines %d delays %v callbacks %d",
			len(lines), delays, len(callbacks))
	}
}
