package interceptors

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type accessLogRecord struct {
	Version    int    `json:"v"`
	RPCType    string `json:"type"`
	Method     string `json:"method"`
	PeerType   string `json:"peer_type"`
	Peer       string `json:"peer"`
	Code       string `json:"code"`
	DurationMS int64  `json:"duration_ms"`
}

type accessLogServerStream struct {
	ctx context.Context
}

func (s *accessLogServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *accessLogServerStream) SendHeader(metadata.MD) error { return nil }
func (s *accessLogServerStream) SetTrailer(metadata.MD)       {}
func (s *accessLogServerStream) Context() context.Context     { return s.ctx }
func (s *accessLogServerStream) SendMsg(interface{}) error    { return nil }
func (s *accessLogServerStream) RecvMsg(interface{}) error    { return nil }

func captureAccessLog(t *testing.T) (func(string, ...interface{}), func() accessLogRecord) {
	t.Helper()

	var lines []string
	logf := func(format string, args ...interface{}) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	record := func() accessLogRecord {
		t.Helper()
		if len(lines) != 1 {
			t.Fatalf("got %d log lines, want 1: %v", len(lines), lines)
		}
		return parseAccessLog(t, lines[0])
	}
	return logf, record
}

func parseAccessLog(t *testing.T, line string) accessLogRecord {
	t.Helper()

	const prefix = "RPC_ACCESS "
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
	wantFields := []string{"code", "duration_ms", "method", "peer", "peer_type", "type", "v"}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("log record fields = %v, want %v", gotFields, wantFields)
	}

	var got accessLogRecord
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("cannot decode access log: %v", err)
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
	logger := newRPCLogger(func(format string, args ...interface{}) {
		logs <- fmt.Sprintf(format, args...)
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
	case <-ctx.Done():
		t.Fatal("timed out waiting for RPC access log")
	}
}

func TestRPCLoggerUnaryError(t *testing.T) {
	logf, capturedRecord := captureAccessLog(t)
	logger := newRPCLogger(logf)
	info := &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"}
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
	logf, capturedRecord := captureAccessLog(t)
	logger := newRPCLogger(logf)

	_, err := logger.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"},
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
	logf, capturedRecord := captureAccessLog(t)
	logger := newRPCLogger(logf)
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
	want := accessLogRecord{
		Version:  1,
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

func TestRPCLoggerStreamSuccess(t *testing.T) {
	logf, capturedRecord := captureAccessLog(t)
	logger := newRPCLogger(logf)
	stream := &accessLogServerStream{ctx: context.Background()}

	err := logger.StreamInterceptor()(nil, stream,
		&grpc.StreamServerInfo{FullMethod: "/gnmi.gNMI/Subscribe"},
		func(interface{}, grpc.ServerStream) error { return nil })
	if err != nil {
		t.Fatalf("StreamInterceptor() returned error: %v", err)
	}
	if got := capturedRecord(); got.RPCType != "stream" || got.Code != codes.OK.String() {
		t.Fatalf("access log = %+v, want successful stream", got)
	}
}

func TestRPCLoggerDoesNotPropagateLoggingPanic(t *testing.T) {
	logger := newRPCLogger(func(string, ...interface{}) { panic("log sink failure") })

	response, err := logger.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"},
		func(context.Context, interface{}) (interface{}, error) { return "response", nil })
	if err != nil || response != "response" {
		t.Fatalf("UnaryInterceptor() = %v, %v; want response, nil", response, err)
	}
}

func TestRPCLoggerRecordsInnerShortCircuit(t *testing.T) {
	logf, capturedRecord := captureAccessLog(t)
	logger := newRPCLogger(logf)
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

func TestRPCLoggerUnarySuccess(t *testing.T) {
	logf, capturedRecord := captureAccessLog(t)
	logger := newRPCLogger(logf)
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 50051},
	})
	info := &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"}

	response, err := logger.UnaryInterceptor()(ctx, "secret request", info,
		func(context.Context, interface{}) (interface{}, error) {
			return "response", nil
		})
	if err != nil {
		t.Fatalf("UnaryInterceptor() returned error: %v", err)
	}
	if response != "response" {
		t.Fatalf("UnaryInterceptor() response = %v, want response", response)
	}

	got := capturedRecord()
	want := accessLogRecord{
		Version:  1,
		RPCType:  "unary",
		Method:   "/gnmi.gNMI/Get",
		PeerType: "tcp",
		Peer:     "192.0.2.10:50051",
		Code:     codes.OK.String(),
	}
	if got.Version != want.Version || got.RPCType != want.RPCType || got.Method != want.Method ||
		got.PeerType != want.PeerType || got.Peer != want.Peer || got.Code != want.Code {
		t.Fatalf("access log = %+v, want %+v", got, want)
	}
	if got.DurationMS < 0 {
		t.Fatalf("duration_ms = %d, want non-negative", got.DurationMS)
	}
}
