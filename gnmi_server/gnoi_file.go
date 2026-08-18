package gnmi

import (
	"context"
	"net"
	"path/filepath"
	"strings"

	log "github.com/golang/glog"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	gnoifile "github.com/sonic-net/sonic-gnmi/pkg/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type fileGetServer struct{ grpc.ServerStream }

func (s *fileGetServer) Send(response *gnoi_file_pb.GetResponse) error {
	return s.ServerStream.SendMsg(response)
}

type filePutServer struct{ grpc.ServerStream }

func (s *filePutServer) SendAndClose(response *gnoi_file_pb.PutResponse) error {
	return s.ServerStream.SendMsg(response)
}

func (s *filePutServer) Recv() (*gnoi_file_pb.PutRequest, error) {
	request := new(gnoi_file_pb.PutRequest)
	if err := s.ServerStream.RecvMsg(request); err != nil {
		return nil, err
	}
	return request, nil
}

func fileGetHandler(server interface{}, stream grpc.ServerStream) error {
	request := new(gnoi_file_pb.GetRequest)
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	return server.(gnoi_file_pb.FileServer).Get(request, &fileGetServer{ServerStream: stream})
}

func filePutHandler(server interface{}, stream grpc.ServerStream) error {
	return server.(gnoi_file_pb.FileServer).Put(&filePutServer{ServerStream: stream})
}

var relayTCPFileServiceDesc = grpc.ServiceDesc{
	ServiceName: "gnoi.file.File",
	HandlerType: (*gnoi_file_pb.FileServer)(nil),
	Streams: []grpc.StreamDesc{
		{StreamName: "Get", Handler: fileGetHandler, ServerStreams: true},
		{StreamName: "Put", Handler: filePutHandler, ClientStreams: true},
	},
	Metadata: "file/file.proto",
}

func isUnixPeer(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return false
	}
	_, ok = p.Addr.(*net.UnixAddr)
	return ok
}

func presentedCertificateCommonName(ctx context.Context) (string, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil || isUnixPeer(ctx) {
		return "", false
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return "", false
	}
	return tlsInfo.State.VerifiedChains[0][0].Subject.CommonName, true
}

func (srv *FileServer) isHardwareProxyCertificate(ctx context.Context) bool {
	commonName, ok := presentedCertificateCommonName(ctx)
	return ok && srv.config.FileRelayCertificateCN != "" && commonName == srv.config.FileRelayCertificateCN
}

func (srv *FileServer) authenticateHardwareProxyRelay(ctx context.Context) error {
	if !srv.config.fileRelayConfigured() {
		return status.Error(codes.PermissionDenied, "HardwareProxy file relay is not configured")
	}
	commonName, err := AuthenticateClientCertificate(ctx, srv.config.EnableCrl)
	if err != nil {
		return err
	}
	if commonName != srv.config.FileRelayCertificateCN {
		return status.Error(codes.PermissionDenied, "client certificate identity is not authorized for HardwareProxy file relay")
	}
	return nil
}

func (srv *FileServer) useTCPRelayPolicy(ctx context.Context) bool {
	return srv.transport != fileTransportUDS && srv.config.fileRelayConfigured() &&
		(!srv.config.legacyFileEnabled() || srv.isHardwareProxyCertificate(ctx))
}

func (srv *FileServer) authenticateLegacy(ctx context.Context, writeAccess bool) error {
	if !srv.config.legacyFileEnabled() {
		return status.Error(codes.PermissionDenied, "legacy gNOI File access requires an existing write gate")
	}
	_, err := authenticate(srv.config, ctx, "gnoi", writeAccess)
	return err
}

func (srv *FileServer) authenticateFileCaller(ctx context.Context, writeAccess bool) error {
	if srv.transport == fileTransportUDS {
		_, err := authenticate(srv.config, ctx, "gnoi", writeAccess)
		return err
	}
	return srv.authenticateLegacy(ctx, writeAccess)
}

func (srv *FileServer) internalRelayPath(path string) bool {
	stateDir := filepath.Dir(srv.config.FileRelayJournalPath)
	return path == stateDir || strings.HasPrefix(path, stateDir+"/")
}

func (srv *FileServer) restrictedUDSGetPath(path string) bool {
	return path == srv.config.FileRelayDesiredPath ||
		path == srv.config.FileRelayJournalPath ||
		path == srv.config.FileRelayCompletionPath
}

func (srv *FileServer) restrictedUDSPutPath(path string) bool {
	return path == srv.config.FileRelayStatusPath ||
		path == srv.config.FileRelayJournalPath ||
		path == srv.config.FileRelayCompletionPath
}

func (srv *FileServer) restrictedRelayPath(path string) bool {
	return path == srv.config.FileRelayDesiredPath ||
		path == srv.config.FileRelayStatusPath ||
		path == srv.config.FileRelayJournalPath ||
		path == srv.config.FileRelayCompletionPath
}

func (srv *FileServer) canonicalFilePolicyPath(path string) (string, error) {
	if !srv.config.fileRelayConfigured() {
		return path, nil
	}
	return gnoifile.CanonicalHostVisiblePath(path)
}

func (srv *FileServer) rejectReservedTCPRelayPath(path string) error {
	if srv.transport != fileTransportUDS && srv.config.fileRelayConfigured() && srv.restrictedRelayPath(path) {
		return status.Error(codes.PermissionDenied, "configured relay paths are reserved from legacy TCP File access")
	}
	return nil
}

type replayPutServer struct {
	gnoi_file_pb.File_PutServer
	first *gnoi_file_pb.PutRequest
}

func (s *replayPutServer) Recv() (*gnoi_file_pb.PutRequest, error) {
	if s.first != nil {
		first := s.first
		s.first = nil
		return first, nil
	}
	return s.File_PutServer.Recv()
}

func (srv *FileServer) Stat(ctx context.Context, req *gnoi_file_pb.StatRequest) (*gnoi_file_pb.StatResponse, error) {
	log.Infof("GNOI File Stat RPC called with request: %+v", req)
	path := ""
	if req != nil {
		path = req.GetPath()
	}
	var err error
	path, err = srv.canonicalFilePolicyPath(path)
	if err != nil {
		return nil, err
	}
	if srv.useTCPRelayPolicy(ctx) {
		if err := srv.authenticateHardwareProxyRelay(ctx); err != nil {
			return nil, err
		}
		return nil, status.Error(codes.PermissionDenied, "HardwareProxy is not authorized for File.Stat")
	}
	if srv.transport == fileTransportUDS && srv.config.fileRelayConfigured() && srv.restrictedRelayPath(path) {
		return nil, status.Error(codes.PermissionDenied, "File.Stat is not allowed for a fixed relay file")
	}
	if err := srv.authenticateFileCaller(ctx, false); err != nil {
		return nil, err
	}
	if err := srv.rejectReservedTCPRelayPath(path); err != nil {
		return nil, err
	}
	if srv.transport != fileTransportUDS && srv.internalRelayPath(path) {
		return nil, status.Error(codes.PermissionDenied, "relay state paths are available only through the local Unix socket")
	}
	return gnoifile.HandleStat(ctx, req)
}

func (srv *FileServer) Get(req *gnoi_file_pb.GetRequest, stream gnoi_file_pb.File_GetServer) error {
	log.Infof("GNOI File Get RPC called with request: %+v", req)
	path := ""
	if req != nil {
		path = req.GetRemoteFile()
	}
	var err error
	path, err = srv.canonicalFilePolicyPath(path)
	if err != nil {
		return err
	}
	ctx := stream.Context()

	if srv.transport == fileTransportUDS && srv.config.fileRelayConfigured() && srv.restrictedUDSGetPath(path) {
		return gnoifile.HandleRestrictedGet(req, stream, path)
	}
	if srv.useTCPRelayPolicy(ctx) {
		if err := srv.authenticateHardwareProxyRelay(ctx); err != nil {
			return err
		}
		if dpuproxy.ExtractTargetMetadata(ctx).HasMetadata || path != srv.config.FileRelayStatusPath {
			return status.Error(codes.PermissionDenied, "HardwareProxy is authorized only for the configured relay status path")
		}
		return gnoifile.HandleRestrictedGet(req, stream, path)
	}
	if err := srv.authenticateFileCaller(ctx, false); err != nil {
		return err
	}
	if err := srv.rejectReservedTCPRelayPath(path); err != nil {
		return err
	}
	if srv.transport != fileTransportUDS && srv.internalRelayPath(path) {
		return status.Error(codes.PermissionDenied, "relay state paths are available only through the local Unix socket")
	}
	return gnoifile.HandleGet(req, stream)
}

func (srv *FileServer) TransferToRemote(ctx context.Context, req *gnoi_file_pb.TransferToRemoteRequest) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	log.Infof("GNOI File TransferToRemote RPC called with request: %+v", req)
	path := ""
	if req != nil {
		path = req.GetLocalPath()
	}
	var err error
	path, err = srv.canonicalFilePolicyPath(path)
	if err != nil {
		return nil, err
	}
	if srv.useTCPRelayPolicy(ctx) {
		if err := srv.authenticateHardwareProxyRelay(ctx); err != nil {
			return nil, err
		}
		return nil, status.Error(codes.PermissionDenied, "HardwareProxy is not authorized for File.TransferToRemote")
	}
	if srv.transport == fileTransportUDS && srv.config.fileRelayConfigured() && srv.restrictedRelayPath(path) {
		return nil, status.Error(codes.PermissionDenied, "File.TransferToRemote cannot target a fixed relay file")
	}
	if err := srv.authenticateFileCaller(ctx, true); err != nil {
		return nil, err
	}
	if err := srv.rejectReservedTCPRelayPath(path); err != nil {
		return nil, err
	}
	return gnoifile.HandleTransferToRemote(ctx, req)
}

func (srv *FileServer) Put(stream gnoi_file_pb.File_PutServer) error {
	log.Infof("GNOI File Put RPC called")
	ctx := stream.Context()
	relayPolicy := srv.useTCPRelayPolicy(ctx)
	if relayPolicy {
		if err := srv.authenticateHardwareProxyRelay(ctx); err != nil {
			return err
		}
	}
	if !relayPolicy {
		if err := srv.authenticateFileCaller(ctx, true); err != nil {
			return err
		}
	}

	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to receive File.Put open message: %v", err)
	}
	path := ""
	if first != nil && first.GetOpen() != nil {
		path = first.GetOpen().GetRemoteFile()
	}
	path, err = srv.canonicalFilePolicyPath(path)
	if err != nil {
		return err
	}
	replay := &replayPutServer{File_PutServer: stream, first: first}

	if srv.transport == fileTransportUDS && srv.config.fileRelayConfigured() && srv.restrictedUDSPutPath(path) {
		return gnoifile.HandleRestrictedPut(replay, path)
	}
	if relayPolicy {
		if dpuproxy.ExtractTargetMetadata(ctx).HasMetadata || path != srv.config.FileRelayDesiredPath {
			return status.Error(codes.PermissionDenied, "HardwareProxy is authorized only for the configured relay desired path")
		}
		return gnoifile.HandleRestrictedPut(replay, path)
	}
	if err := srv.rejectReservedTCPRelayPath(path); err != nil {
		return err
	}
	if srv.transport != fileTransportUDS && srv.internalRelayPath(path) {
		return status.Error(codes.PermissionDenied, "relay state paths are available only through the local Unix socket")
	}
	return gnoifile.HandlePut(replay)
}

func (srv *FileServer) Remove(ctx context.Context, req *gnoi_file_pb.RemoveRequest) (*gnoi_file_pb.RemoveResponse, error) {
	log.Infof("GNOI File Remove RPC called with request: %+v", req)
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid nil request.")
	}
	path, err := srv.canonicalFilePolicyPath(req.GetRemoteFile())
	if err != nil {
		return nil, err
	}
	if srv.useTCPRelayPolicy(ctx) {
		if err := srv.authenticateHardwareProxyRelay(ctx); err != nil {
			return nil, err
		}
		return nil, status.Error(codes.PermissionDenied, "HardwareProxy is not authorized for File.Remove")
	}
	if srv.transport == fileTransportUDS && srv.config.fileRelayConfigured() && srv.restrictedRelayPath(path) {
		return nil, status.Error(codes.PermissionDenied, "File.Remove is not allowed for a fixed relay file")
	}
	if err := srv.authenticateFileCaller(ctx, true); err != nil {
		return nil, err
	}
	if err := srv.rejectReservedTCPRelayPath(path); err != nil {
		return nil, err
	}
	if srv.transport != fileTransportUDS && srv.internalRelayPath(path) {
		return nil, status.Error(codes.PermissionDenied, "relay state paths are available only through the local Unix socket")
	}
	return gnoifile.HandleFileRemove(ctx, req)
}

// AuthorizeDPURequest authenticates and authorizes a routed request before DPU resolution.
func AuthorizeDPURequest(config *Config, ctx context.Context, method string) error {
	if config == nil {
		return status.Error(codes.PermissionDenied, "DPU routing authorization is not configured")
	}
	if isUnixPeer(ctx) {
		if config.fileRelayConfigured() || config.legacyFileEnabled() {
			return nil
		}
		return status.Error(codes.PermissionDenied, "DPU routing is not enabled")
	}
	fileServer := &FileServer{Server: &Server{config: config}, transport: fileTransportTCP}
	if fileServer.isHardwareProxyCertificate(ctx) {
		if err := fileServer.authenticateHardwareProxyRelay(ctx); err != nil {
			return err
		}
		return status.Errorf(codes.PermissionDenied, "HardwareProxy file relay cannot route %s to a DPU", method)
	}
	writeAccess := method == "/gnoi.file.File/Put" ||
		method == "/gnoi.file.File/TransferToRemote" ||
		method == "/gnoi.system.System/Reboot" ||
		method == "/gnoi.system.System/SetPackage" ||
		method == "/gnoi.os.OS/Activate"
	return fileServer.authenticateLegacy(ctx, writeAccess)
}

// AuthorizeHardwareProxyRPC restricts the configured relay principal across the TCP server.
// File path authorization remains in FileServer after Put/Get request decoding.
func AuthorizeHardwareProxyRPC(config *Config, ctx context.Context, method string) error {
	if config == nil || !config.fileRelayConfigured() || isUnixPeer(ctx) {
		return nil
	}
	commonName, ok := presentedCertificateCommonName(ctx)
	if !ok || commonName != config.FileRelayCertificateCN {
		return nil
	}
	fileServer := &FileServer{Server: &Server{config: config}, transport: fileTransportTCP}
	if err := fileServer.authenticateHardwareProxyRelay(ctx); err != nil {
		return err
	}
	if method != "/gnoi.file.File/Get" && method != "/gnoi.file.File/Put" {
		return status.Errorf(codes.PermissionDenied, "HardwareProxy file relay principal is not authorized for %s", method)
	}
	return nil
}

func hardwareProxyUnaryInterceptor(config *Config) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := AuthorizeHardwareProxyRPC(config, ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func hardwareProxyStreamInterceptor(config *Config) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := AuthorizeHardwareProxyRPC(config, stream.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}
