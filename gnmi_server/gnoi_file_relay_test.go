package gnmi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	gnoifile "github.com/sonic-net/sonic-gnmi/pkg/gnoi/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	relayCN        = "hardware-proxy"
	desiredPath    = "/var/tmp/device-ops-agent/desired-software.json"
	statusPath     = "/var/tmp/device-ops-agent/software-status.json"
	journalPath    = "/host/doa/state/relay-journal.json"
	completionPath = "/host/doa/state/relay-completion.json"
	otherPath      = "/var/tmp/device-ops-agent/other.json"
)

func relayConfig() *Config {
	return &Config{
		Port:                    50052,
		UnixSocket:              "/var/run/gnmi/gnmi.sock",
		FileRelayCertificateCN:  relayCN,
		FileRelayDesiredPath:    desiredPath,
		FileRelayStatusPath:     statusPath,
		FileRelayJournalPath:    journalPath,
		FileRelayCompletionPath: completionPath,
	}
}

func certificateContext(commonName string) context.Context {
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: commonName}}
	tlsInfo := credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}}}
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr:     &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1234},
		AuthInfo: tlsInfo,
	})
}

func unixContext() context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.UnixAddr{Name: "/var/run/gnmi/gnmi.sock", Net: "unix"},
	})
}

type relayGetServer struct {
	gnoi_file_pb.File_GetServer
	ctx context.Context
}

func (s *relayGetServer) Context() context.Context { return s.ctx }

func (s *relayGetServer) Send(*gnoi_file_pb.GetResponse) error { return nil }

func TestFileServiceRegistrationGate(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want bool
	}{
		{name: "read only", cfg: &Config{}},
		{name: "native write", cfg: &Config{EnableNativeWrite: true}, want: true},
		{name: "translib write", cfg: &Config{EnableTranslibWrite: true}, want: true},
		{name: "relay", cfg: relayConfig(), want: true},
		{name: "partial relay", cfg: &Config{FileRelayCertificateCN: relayCN}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcServer := grpc.NewServer()
			server := &Server{config: tt.cfg}
			registerFileService(grpcServer, server, &FileServer{Server: server})
			_, registered := grpcServer.GetServiceInfo()["gnoi.file.File"]
			if registered != tt.want {
				t.Fatalf("File registration = %t, want %t", registered, tt.want)
			}
		})
	}
}

func TestRelayOnlyServiceDescriptors(t *testing.T) {
	server := &Server{config: relayConfig()}
	tcpGRPC := grpc.NewServer()
	registerFileService(tcpGRPC, server, &FileServer{Server: server, transport: fileTransportTCP})
	tcpInfo := tcpGRPC.GetServiceInfo()["gnoi.file.File"]
	if len(tcpInfo.Methods) != 2 || tcpInfo.Methods[0].Name != "Get" || tcpInfo.Methods[1].Name != "Put" {
		t.Fatalf("relay-only TCP methods = %+v", tcpInfo.Methods)
	}

	udsGRPC := grpc.NewServer()
	registerFileService(udsGRPC, server, &FileServer{Server: server, transport: fileTransportUDS})
	udsInfo := udsGRPC.GetServiceInfo()["gnoi.file.File"]
	want := map[string]bool{"Get": true, "Put": true, "Stat": true, "Remove": true, "TransferToRemote": true}
	for _, method := range udsInfo.Methods {
		delete(want, method.Name)
	}
	if len(want) != 0 {
		t.Fatalf("relay-only UDS missing methods: %v", want)
	}
}

func TestLegacyWriteGateKeepsFullFileService(t *testing.T) {
	cfg := relayConfig()
	cfg.EnableNativeWrite = true
	server := &Server{config: cfg}
	grpcServer := grpc.NewServer()
	registerFileService(grpcServer, server, &FileServer{Server: server, transport: fileTransportTCP})
	methods := grpcServer.GetServiceInfo()["gnoi.file.File"].Methods
	want := map[string]bool{"Get": true, "Put": true, "Stat": true, "Remove": true, "TransferToRemote": true}
	for _, method := range methods {
		delete(want, method.Name)
	}
	if len(want) != 0 {
		t.Fatalf("legacy File service missing methods: %v", want)
	}
}

func TestLegacyModeUsesNormalAuthentication(t *testing.T) {
	server := &FileServer{Server: &Server{config: &Config{
		EnableNativeWrite: true,
		UserAuth:          AuthTypes{"cert": true},
	}}, transport: fileTransportTCP}
	patch := gomonkey.ApplyFuncReturn(ClientCertAuthenAndAuthor, context.Background(), status.Error(codes.Unauthenticated, "denied"))
	defer patch.Reset()
	if _, err := server.TransferToRemote(certificateContext("legacy"), &gnoi_file_pb.TransferToRemoteRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("TransferToRemote code = %v", status.Code(err))
	}
	if _, err := server.Remove(certificateContext("legacy"), &gnoi_file_pb.RemoveRequest{RemoteFile: "/tmp/x"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Remove code = %v", status.Code(err))
	}
}

func TestLegacyTCPCallersCannotAccessReservedRelayPaths(t *testing.T) {
	cfg := relayConfig()
	cfg.EnableNativeWrite = true
	server := &FileServer{Server: &Server{config: cfg}, transport: fileTransportTCP}
	ctx := certificateContext("legacy-client")
	patch := gomonkey.ApplyFunc(authenticate, func(_ *Config, gotCtx context.Context, _ string, _ bool) (context.Context, error) {
		return gotCtx, nil
	})
	defer patch.Reset()

	for _, path := range []string{desiredPath, statusPath, journalPath, completionPath} {
		t.Run(path, func(t *testing.T) {
			if err := server.Get(&gnoi_file_pb.GetRequest{RemoteFile: path}, &relayGetServer{ctx: ctx}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("Get code = %v", status.Code(err))
			}
			put := &mockPutStream{ctx: ctx}
			put.addOpenRequest(path, 0600)
			if err := server.Put(put); status.Code(err) != codes.PermissionDenied {
				t.Errorf("Put code = %v", status.Code(err))
			}
			if _, err := server.Remove(ctx, &gnoi_file_pb.RemoveRequest{RemoteFile: path}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("Remove code = %v", status.Code(err))
			}
			if _, err := server.TransferToRemote(ctx, &gnoi_file_pb.TransferToRemoteRequest{LocalPath: path}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("TransferToRemote code = %v", status.Code(err))
			}
		})
	}
}

func TestRelayPathAliasesCannotBypassLegacyTCPReservation(t *testing.T) {
	cfg := relayConfig()
	cfg.EnableNativeWrite = true
	server := &FileServer{Server: &Server{config: cfg}, transport: fileTransportTCP}
	ctx := certificateContext("legacy-client")
	patch := gomonkey.ApplyFunc(authenticate, func(_ *Config, gotCtx context.Context, _ string, _ bool) (context.Context, error) {
		return gotCtx, nil
	})
	defer patch.Reset()

	for _, path := range []string{
		"/var/tmp/device-ops-agent/sub/../desired-software.json",
		"/var/tmp//device-ops-agent/desired-software.json",
		"/var/tmp/device-ops-agent/./desired-software.json",
		"/mnt/host/var/tmp/device-ops-agent/desired-software.json",
		"/host/doa/state/sub/../relay-journal.json",
		"/host//doa/state/relay-completion.json",
		"/host/doa/./state/relay-journal.json",
		"/mnt/host/host/doa/state/relay-completion.json",
	} {
		t.Run(path, func(t *testing.T) {
			if err := server.Get(&gnoi_file_pb.GetRequest{RemoteFile: path}, &relayGetServer{ctx: ctx}); status.Code(err) != codes.InvalidArgument {
				t.Errorf("Get code = %v", status.Code(err))
			}
			put := &mockPutStream{ctx: ctx}
			put.addOpenRequest(path, 0600)
			if err := server.Put(put); status.Code(err) != codes.InvalidArgument {
				t.Errorf("Put code = %v", status.Code(err))
			}
			if _, err := server.Remove(ctx, &gnoi_file_pb.RemoveRequest{RemoteFile: path}); status.Code(err) != codes.InvalidArgument {
				t.Errorf("Remove code = %v", status.Code(err))
			}
			if _, err := server.TransferToRemote(ctx, &gnoi_file_pb.TransferToRemoteRequest{LocalPath: path}); status.Code(err) != codes.InvalidArgument {
				t.Errorf("TransferToRemote code = %v", status.Code(err))
			}
		})
	}
}

func TestCanonicalRelayPathsRemainValid(t *testing.T) {
	server := &FileServer{Server: &Server{config: relayConfig()}, transport: fileTransportTCP}
	for _, path := range []string{desiredPath, statusPath, journalPath, completionPath} {
		got, err := server.canonicalFilePolicyPath(path)
		if err != nil || got != path {
			t.Errorf("path %q canonicalized to %q, error=%v", path, got, err)
		}
	}
}

func TestReservedRelayPathsRemainAvailableToUDSExactOperations(t *testing.T) {
	server := &FileServer{Server: &Server{config: relayConfig()}, transport: fileTransportUDS}
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	getCalls := 0
	putCalls := 0
	patches.ApplyFunc(gnoifile.HandleRestrictedGet, func(req *gnoi_file_pb.GetRequest, _ gnoi_file_pb.File_GetServer, allowedPath string) error {
		getCalls++
		if req.GetRemoteFile() != allowedPath {
			t.Fatalf("Get path = %q, allowed = %q", req.GetRemoteFile(), allowedPath)
		}
		return nil
	})
	patches.ApplyFunc(gnoifile.HandleRestrictedPut, func(_ gnoi_file_pb.File_PutServer, _ string) error {
		putCalls++
		return nil
	})
	for _, path := range []string{desiredPath, journalPath, completionPath} {
		if err := server.Get(&gnoi_file_pb.GetRequest{RemoteFile: path}, &relayGetServer{ctx: unixContext()}); err != nil {
			t.Errorf("Get %q: %v", path, err)
		}
	}
	for _, path := range []string{statusPath, journalPath, completionPath} {
		put := &mockPutStream{ctx: unixContext()}
		put.addOpenRequest(path, 0600)
		if err := server.Put(put); err != nil {
			t.Errorf("Put %q: %v", path, err)
		}
	}
	if getCalls != 3 || putCalls != 3 {
		t.Fatalf("Get calls=%d Put calls=%d", getCalls, putCalls)
	}
}

func TestFileDispatchMatrix(t *testing.T) {
	cfg := relayConfig()
	tcp := &FileServer{Server: &Server{config: cfg}, transport: fileTransportTCP}
	uds := &FileServer{Server: &Server{config: cfg}, transport: fileTransportUDS}
	if !tcp.useTCPRelayPolicy(certificateContext(relayCN)) {
		t.Fatal("relay-only TCP must apply HardwareProxy relay policy")
	}
	if tcp.useTCPRelayPolicy(certificateContext("other")) != true {
		t.Fatal("relay-only TCP must deny every non-HardwareProxy client through relay policy")
	}
	if uds.useTCPRelayPolicy(unixContext()) {
		t.Fatal("UDS must not apply TCP HardwareProxy policy")
	}

	for _, path := range []string{desiredPath, journalPath, completionPath} {
		if !uds.restrictedUDSGetPath(path) {
			t.Errorf("UDS Get path %q is not hardened", path)
		}
	}
	for _, path := range []string{statusPath, journalPath, completionPath} {
		if !uds.restrictedUDSPutPath(path) {
			t.Errorf("UDS Put path %q is not hardened", path)
		}
	}
	for _, path := range []string{statusPath, desiredPath, otherPath} {
		if tcp.internalRelayPath(path) {
			t.Errorf("TCP path %q incorrectly classified as internal", path)
		}
	}
	for _, path := range []string{journalPath, completionPath, "/host/doa/state/other.json"} {
		if !tcp.internalRelayPath(path) {
			t.Errorf("internal path %q not protected from TCP", path)
		}
	}
}

func TestRelayAuthenticationValidatesCertificateAndCRL(t *testing.T) {
	server := &FileServer{Server: &Server{config: relayConfig()}, transport: fileTransportTCP}
	if err := server.authenticateHardwareProxyRelay(certificateContext(relayCN)); err != nil {
		t.Fatalf("valid relay certificate rejected: %v", err)
	}
	if err := server.authenticateHardwareProxyRelay(certificateContext("other")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong identity code = %v, want PermissionDenied", status.Code(err))
	}
	if err := server.authenticateHardwareProxyRelay(context.Background()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing certificate code = %v, want Unauthenticated", status.Code(err))
	}

	server.config.EnableCrl = true
	patch := gomonkey.ApplyFuncReturn(VerifyCertCrl, status.Error(codes.Unauthenticated, "revoked"))
	defer patch.Reset()
	if err := server.authenticateHardwareProxyRelay(certificateContext(relayCN)); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("CRL failure code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestHardwareProxyPrincipalRPCAllowlist(t *testing.T) {
	cfg := relayConfig()
	ctx := certificateContext(relayCN)
	for _, method := range []string{"/gnoi.file.File/Put", "/gnoi.file.File/Get"} {
		if err := AuthorizeHardwareProxyRPC(cfg, ctx, method); err != nil {
			t.Errorf("allowed method %s denied: %v", method, err)
		}
	}
	for _, method := range []string{
		"/gnoi.factory_reset.FactoryReset/Start",
		"/gnoi.system.System/Time",
		"/gnmi.gNMI/Get",
		"/gnmi.gNMI/Subscribe",
		"/gnsi.authz.v1.Authz/Get",
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
	} {
		if err := AuthorizeHardwareProxyRPC(cfg, ctx, method); status.Code(err) != codes.PermissionDenied {
			t.Errorf("method %s code = %v, want PermissionDenied", method, status.Code(err))
		}
	}
}

func TestHardwareProxyPrincipalRestrictionDoesNotAffectOthersOrUDS(t *testing.T) {
	cfg := relayConfig()
	for name, ctx := range map[string]context.Context{
		"other principal": certificateContext("legacy-client"),
		"UDS":             unixContext(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := AuthorizeHardwareProxyRPC(cfg, ctx, "/gnoi.factory_reset.FactoryReset/Start"); err != nil {
				t.Fatalf("unaffected caller denied: %v", err)
			}
		})
	}
}

type relayPrincipalStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *relayPrincipalStream) Context() context.Context { return s.ctx }

func TestHardwareProxyPrincipalUnaryAndStreamInterceptors(t *testing.T) {
	cfg := relayConfig()
	ctx := certificateContext(relayCN)
	unary := hardwareProxyUnaryInterceptor(cfg)
	stream := hardwareProxyStreamInterceptor(cfg)

	for _, method := range []string{
		"/gnoi.factory_reset.FactoryReset/Start",
		"/gnoi.system.System/Time",
		"/gnmi.gNMI/Get",
	} {
		handlerCalled := false
		_, err := unary(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, interface{}) (interface{}, error) {
			handlerCalled = true
			return nil, nil
		})
		if status.Code(err) != codes.PermissionDenied || handlerCalled {
			t.Errorf("unary %s code=%v handler=%t", method, status.Code(err), handlerCalled)
		}
	}
	for _, method := range []string{
		"/gnmi.gNMI/Subscribe",
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
		"/gnoi.system.System/SetPackage",
	} {
		handlerCalled := false
		err := stream(nil, &relayPrincipalStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: method}, func(interface{}, grpc.ServerStream) error {
			handlerCalled = true
			return nil
		})
		if status.Code(err) != codes.PermissionDenied || handlerCalled {
			t.Errorf("stream %s code=%v handler=%t", method, status.Code(err), handlerCalled)
		}
	}
	for _, method := range []string{"/gnoi.file.File/Get", "/gnoi.file.File/Put"} {
		handlerCalled := false
		err := stream(nil, &relayPrincipalStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: method}, func(interface{}, grpc.ServerStream) error {
			handlerCalled = true
			return nil
		})
		if err != nil || !handlerCalled {
			t.Errorf("allowed stream %s error=%v handler=%t", method, err, handlerCalled)
		}
	}
}

func TestRelayOnlyTCPDeniesLegacyMethodsAndPaths(t *testing.T) {
	server := &FileServer{Server: &Server{config: relayConfig()}, transport: fileTransportTCP}
	ctx := certificateContext(relayCN)
	if _, err := server.Stat(ctx, &gnoi_file_pb.StatRequest{Path: desiredPath}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Stat code = %v", status.Code(err))
	}
	if _, err := server.Remove(ctx, &gnoi_file_pb.RemoveRequest{RemoteFile: desiredPath}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Remove code = %v", status.Code(err))
	}
	if _, err := server.TransferToRemote(ctx, &gnoi_file_pb.TransferToRemoteRequest{LocalPath: desiredPath}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("TransferToRemote code = %v", status.Code(err))
	}
	stream := &relayGetServer{ctx: ctx}
	if err := server.Get(&gnoi_file_pb.GetRequest{RemoteFile: journalPath}, stream); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("journal Get code = %v", status.Code(err))
	}
}

func TestRelayOnlyUDSPreservesSafeLocalOperations(t *testing.T) {
	server := &FileServer{Server: &Server{config: relayConfig()}, transport: fileTransportUDS}
	called := map[string]bool{}
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(gnoifile.HandleStat, func(context.Context, *gnoi_file_pb.StatRequest) (*gnoi_file_pb.StatResponse, error) {
		called["Stat"] = true
		return &gnoi_file_pb.StatResponse{}, nil
	})
	patches.ApplyFunc(gnoifile.HandleGet, func(*gnoi_file_pb.GetRequest, gnoi_file_pb.File_GetServer) error {
		called["Get"] = true
		return nil
	})
	patches.ApplyFunc(gnoifile.HandlePut, func(gnoi_file_pb.File_PutServer) error {
		called["Put"] = true
		return nil
	})
	patches.ApplyFunc(gnoifile.HandleFileRemove, func(context.Context, *gnoi_file_pb.RemoveRequest) (*gnoi_file_pb.RemoveResponse, error) {
		called["Remove"] = true
		return &gnoi_file_pb.RemoveResponse{}, nil
	})
	patches.ApplyFunc(gnoifile.HandleTransferToRemote, func(context.Context, *gnoi_file_pb.TransferToRemoteRequest) (*gnoi_file_pb.TransferToRemoteResponse, error) {
		called["TransferToRemote"] = true
		return &gnoi_file_pb.TransferToRemoteResponse{}, nil
	})

	if _, err := server.Stat(unixContext(), &gnoi_file_pb.StatRequest{Path: "/host/doa/markers"}); err != nil {
		t.Fatal(err)
	}
	if err := server.Get(&gnoi_file_pb.GetRequest{RemoteFile: "/host/doa/markers/marker-a.json"}, &relayGetServer{ctx: unixContext()}); err != nil {
		t.Fatal(err)
	}
	put := &mockPutStream{ctx: unixContext()}
	put.addOpenRequest("/host/doa/markers/marker-a.json", 0600)
	if err := server.Put(put); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Remove(unixContext(), &gnoi_file_pb.RemoveRequest{RemoteFile: "/host/doa/markers/marker-a.json"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.TransferToRemote(unixContext(), &gnoi_file_pb.TransferToRemoteRequest{LocalPath: "/var/tmp/image.bin"}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"Stat", "Get", "Put", "Remove", "TransferToRemote"} {
		if !called[method] {
			t.Errorf("safe local %s did not reach existing handler", method)
		}
	}
}

func TestRelayOnlyUDSUsesRestrictedHandlers(t *testing.T) {
	server := &FileServer{Server: &Server{config: relayConfig()}, transport: fileTransportUDS}
	getCalls := 0
	putCalls := 0
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(gnoifile.HandleRestrictedGet, func(req *gnoi_file_pb.GetRequest, _ gnoi_file_pb.File_GetServer, allowedPath string) error {
		getCalls++
		if req.GetRemoteFile() != allowedPath {
			t.Fatalf("Get request path %q, allowed %q", req.GetRemoteFile(), allowedPath)
		}
		return nil
	})
	patches.ApplyFunc(gnoifile.HandleRestrictedPut, func(_ gnoi_file_pb.File_PutServer, allowedPath string) error {
		putCalls++
		if allowedPath != statusPath && allowedPath != journalPath && allowedPath != completionPath {
			t.Fatalf("unexpected restricted Put path %q", allowedPath)
		}
		return nil
	})

	for _, path := range []string{desiredPath, journalPath, completionPath} {
		if err := server.Get(&gnoi_file_pb.GetRequest{RemoteFile: path}, &relayGetServer{ctx: unixContext()}); err != nil {
			t.Fatalf("UDS Get %q: %v", path, err)
		}
	}
	for _, path := range []string{statusPath, journalPath, completionPath} {
		stream := &mockPutStream{ctx: unixContext()}
		stream.addOpenRequest(path, 0600)
		if err := server.Put(stream); err != nil {
			t.Fatalf("UDS Put %q: %v", path, err)
		}
	}
	if getCalls != 3 || putCalls != 3 {
		t.Fatalf("restricted Get calls=%d Put calls=%d", getCalls, putCalls)
	}
}

func TestDPUFileGetAuthorization(t *testing.T) {
	cfg := relayConfig()
	for _, method := range []string{
		"/gnoi.file.File/Get",
		"/gnoi.file.File/Put",
		"/gnoi.system.System/Time",
		"/gnoi.system.System/Reboot",
		"/gnoi.os.OS/Verify",
	} {
		if err := AuthorizeDPURequest(cfg, certificateContext(relayCN), method); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("HardwareProxy %s code = %v", method, status.Code(err))
		}
	}
	if err := AuthorizeDPURequest(cfg, unixContext(), "/gnoi.file.File/Get"); err != nil {
		t.Fatalf("authorized UDS DPU Get rejected: %v", err)
	}
}

func TestDPURequestPreservesAuthorizedLegacyRouting(t *testing.T) {
	cfg := &Config{EnableNativeWrite: true}
	for _, test := range []struct {
		method string
		write  bool
	}{
		{method: "/gnoi.system.System/Time"},
		{method: "/gnoi.file.File/Get"},
		{method: "/gnoi.file.File/Put", write: true},
		{method: "/gnoi.system.System/Reboot", write: true},
	} {
		t.Run(test.method, func(t *testing.T) {
			called := false
			patch := gomonkey.ApplyFunc(authenticate, func(_ *Config, _ context.Context, _ string, writeAccess bool) (context.Context, error) {
				called = true
				if writeAccess != test.write {
					t.Fatalf("writeAccess=%t, want %t", writeAccess, test.write)
				}
				return context.Background(), nil
			})
			defer patch.Reset()
			if err := AuthorizeDPURequest(cfg, certificateContext("legacy"), test.method); err != nil {
				t.Fatalf("legacy route denied: %v", err)
			}
			if !called {
				t.Fatal("normal authentication did not run")
			}
		})
	}
}

func TestRelayPathConfigurationValidation(t *testing.T) {
	if err := relayConfig().validateFileRelay(); err != nil {
		t.Fatalf("valid relay configuration rejected: %v", err)
	}
	for _, path := range []string{"relative", "/tmp/status.json", "/var/tmp/x/../status.json"} {
		cfg := relayConfig()
		cfg.FileRelayStatusPath = path
		if err := cfg.validateFileRelay(); err == nil {
			t.Fatalf("invalid status path %q accepted", path)
		}
	}
	cfg := relayConfig()
	cfg.FileRelayJournalPath = "/var/tmp/journal.json"
	if err := cfg.validateFileRelay(); err == nil {
		t.Fatal("journal path outside /host/doa/state accepted")
	}
}

func TestRelayTargetMetadataDenied(t *testing.T) {
	server := &FileServer{Server: &Server{config: relayConfig()}, transport: fileTransportTCP}
	ctx := metadata.NewIncomingContext(certificateContext(relayCN), metadata.Pairs(
		"x-sonic-ss-target-type", "dpu",
		"x-sonic-ss-target-index", "0",
	))
	if err := server.Get(&gnoi_file_pb.GetRequest{RemoteFile: statusPath}, &relayGetServer{ctx: ctx}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", status.Code(err))
	}
}

var _ gnoi_file_pb.FileServer = (*FileServer)(nil)
