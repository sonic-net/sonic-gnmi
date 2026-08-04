package interceptors

import (
	"context"
	"crypto/x509"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	sharedlog "github.com/sonic-net/sonic-gnmi/pkg/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

const (
	gnmiAuditPrefix    = "GNMI_AUDIT"
	gnmiAuditVersion   = 1
	gnmiAuditPathLimit = 32
	gnmiGetMethod      = "/gnmi.gNMI/Get"
	gnmiSetMethod      = "/gnmi.gNMI/Set"
)

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

type gnmiAuditLogger struct {
	sink    sharedlog.LineSink
	now     func() time.Time
	counter uint64
}

func newGNMIAuditLogger(sink sharedlog.LineSink) *gnmiAuditLogger {
	return newGNMIAuditLoggerWithClock(sink, time.Now)
}

func newGNMIAuditLoggerWithClock(sink sharedlog.LineSink, now func() time.Time) *gnmiAuditLogger {
	return &gnmiAuditLogger{sink: sink, now: now}
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
	_ = sharedlog.WriteJSON(gnmiAuditPrefix, record, l.sink)
}

func (l *gnmiAuditLogger) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response interface{}, err error) {
		if info.FullMethod != gnmiGetMethod && info.FullMethod != gnmiSetMethod {
			return handler(ctx, req)
		}

		record := l.newRecord(ctx, req, info.FullMethod)
		defer l.finish(&record, &err)
		return handler(ctx, req)
	}
}

func (l *gnmiAuditLogger) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, stream)
	}
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
