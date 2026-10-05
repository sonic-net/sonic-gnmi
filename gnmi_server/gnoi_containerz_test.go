package gnmi

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gnoi_common_pb "github.com/openconfig/gnoi/common"
	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	gnoi_types_pb "github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type dummyDeployServer struct {
	gnoi_containerz_pb.Containerz_DeployServer
	ctx       context.Context
	recvQueue []*gnoi_containerz_pb.DeployRequest
	sendResp  []*gnoi_containerz_pb.DeployResponse
	sendErr   error
	recvErr   error
	events    *[]string
}

func (d *dummyDeployServer) Recv() (*gnoi_containerz_pb.DeployRequest, error) {
	if d.recvErr != nil {
		return nil, d.recvErr
	}
	if len(d.recvQueue) == 0 {
		return nil, io.EOF
	}
	request := d.recvQueue[0]
	d.recvQueue = d.recvQueue[1:]
	return request, nil
}

func (d *dummyDeployServer) Send(response *gnoi_containerz_pb.DeployResponse) error {
	if d.events != nil {
		*d.events = append(*d.events, "send")
	}
	d.sendResp = append(d.sendResp, response)
	return d.sendErr
}

func (d *dummyDeployServer) Context() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

type dummyListServer struct {
	gnoi_containerz_pb.Containerz_ListServer
}

type dummyLogServer struct {
	gnoi_containerz_pb.Containerz_LogServer
}

type fakeContainerzImageLoader struct {
	load  func(string) error
	close func() error
}

func (f *fakeContainerzImageLoader) LoadDockerImage(path string) error {
	return f.load(path)
}

func (f *fakeContainerzImageLoader) Close() error {
	return f.close()
}

type trackingTempFile struct {
	*os.File
	events   *[]string
	closeErr error
}

func (f *trackingTempFile) Close() error {
	*f.events = append(*f.events, "close")
	closeErr := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return closeErr
}

type deployFixture struct {
	events          []string
	downloadRequest download.Request
	downloadPath    string
	loadedPath      string
	tempPattern     string
	tempMode        os.FileMode
	downloadErr     error
	closeTempErr    error
	newLoaderErr    error
	loadErr         error
	closeLoaderErr  error
	removeErr       error
	createTempErr   error
	authenticateErr error
}

func (f *deployFixture) dependencies(t *testing.T) *containerzDeployDependencies {
	t.Helper()
	hostTempDirectory := t.TempDir()

	return &containerzDeployDependencies{
		authenticate: func(
			_ *Config,
			ctx context.Context,
			_ string,
			_ bool,
		) (context.Context, error) {
			return ctx, f.authenticateErr
		},
		createTempFile: func(directory, pattern string) (containerzTemporaryFile, error) {
			if f.createTempErr != nil {
				return nil, f.createTempErr
			}
			if directory != hostTempDirectory {
				t.Errorf("CreateTemp() directory = %q, want %q", directory, hostTempDirectory)
			}
			f.tempPattern = pattern
			file, err := os.CreateTemp(directory, pattern)
			if err != nil {
				return nil, err
			}
			return &trackingTempFile{
				File:     file,
				events:   &f.events,
				closeErr: f.closeTempErr,
			}, nil
		},
		downloadRemote: func(
			_ context.Context,
			request download.Request,
			destination io.Writer,
		) error {
			f.events = append(f.events, "download")
			f.downloadRequest = request
			if file, ok := destination.(interface{ Name() string }); ok {
				f.downloadPath = file.Name()
				info, err := os.Stat(file.Name())
				if err != nil {
					t.Fatalf("Stat() error = %v", err)
				}
				f.tempMode = info.Mode().Perm()
			}
			if f.downloadErr != nil {
				return f.downloadErr
			}
			_, err := io.WriteString(destination, "container image")
			return err
		},
		newImageLoader: func() (containerzImageLoader, error) {
			f.events = append(f.events, "dbus")
			if f.newLoaderErr != nil {
				return nil, f.newLoaderErr
			}
			return &fakeContainerzImageLoader{
				load: func(path string) error {
					f.events = append(f.events, "load")
					f.loadedPath = path
					return f.loadErr
				},
				close: func() error {
					f.events = append(f.events, "dbus-close")
					return f.closeLoaderErr
				},
			}, nil
		},
		removeFile: func(path string) error {
			f.events = append(f.events, "remove")
			if f.removeErr != nil {
				return f.removeErr
			}
			return os.Remove(path)
		},
		translateHostPath: func(path string) string {
			if path != "/tmp" {
				t.Errorf("translateHostPath() path = %q, want /tmp", path)
			}
			return hostTempDirectory
		},
	}
}

func TestContainerzServer_Unimplemented(t *testing.T) {
	server := &ContainerzServer{}

	if _, err := server.Remove(context.Background(), &gnoi_containerz_pb.RemoveRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Remove() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
	if err := server.List(&gnoi_containerz_pb.ListRequest{}, &dummyListServer{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("List() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
	if _, err := server.Start(context.Background(), &gnoi_containerz_pb.StartRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Start() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
	if _, err := server.Stop(context.Background(), &gnoi_containerz_pb.StopRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Stop() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
	if err := server.Log(&gnoi_containerz_pb.LogRequest{}, &dummyLogServer{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Log() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
}

func TestDeploySuccessPreservesLoadAndResponseSequence(t *testing.T) {
	fixture := &deployFixture{}
	server := newContainerzTestServer(fixture.dependencies(t))
	stream := &dummyDeployServer{
		recvQueue: []*gnoi_containerz_pb.DeployRequest{
			deployRequest(&gnoi_containerz_pb.ImageTransfer{
				Name:      "../../sensitive-image-name",
				Tag:       "latest",
				ImageSize: 123,
				RemoteDownload: &gnoi_common_pb.RemoteDownload{
					Path:          "https://download.example/image.tar?token=sensitive",
					Protocol:      gnoi_common_pb.RemoteDownload_HTTPS,
					SourceAddress: "192.0.2.10",
					SourceVrf:     "management",
					Credentials: &gnoi_types_pb.Credentials{
						Username: "user",
						Password: &gnoi_types_pb.Credentials_Cleartext{Cleartext: "password"},
					},
				},
			}),
		},
		events: &fixture.events,
	}

	if err := server.Deploy(stream); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}

	wantEvents := []string{"download", "close", "dbus", "load", "dbus-close", "remove", "send"}
	if !reflect.DeepEqual(fixture.events, wantEvents) {
		t.Errorf("events = %v, want %v", fixture.events, wantEvents)
	}
	if fixture.downloadRequest.Protocol != download.ProtocolHTTPS {
		t.Errorf("download protocol = %q, want %q", fixture.downloadRequest.Protocol, download.ProtocolHTTPS)
	}
	if fixture.downloadRequest.Path != "https://download.example/image.tar?token=sensitive" {
		t.Errorf("download path = %q", fixture.downloadRequest.Path)
	}
	if fixture.downloadRequest.Username != "user" || fixture.downloadRequest.Password != "password" {
		t.Error("download credentials were not preserved")
	}
	if fixture.downloadRequest.MaxSize != 123 {
		t.Errorf("download size limit = %d, want 123", fixture.downloadRequest.MaxSize)
	}
	if fixture.tempMode != 0600 {
		t.Errorf("temporary file mode = %o, want 600", fixture.tempMode)
	}
	if strings.Contains(fixture.tempPattern, "sensitive-image-name") {
		t.Errorf("temporary file pattern contains caller image name: %q", fixture.tempPattern)
	}
	wantLoadedPath := filepath.Join("/tmp", filepath.Base(fixture.downloadPath))
	if fixture.loadedPath != wantLoadedPath {
		t.Errorf("loaded path = %q, want host-visible path %q", fixture.loadedPath, wantLoadedPath)
	}
	if fixture.loadedPath == fixture.downloadPath {
		t.Errorf("loaded path must differ from container-visible path %q", fixture.downloadPath)
	}
	if _, err := os.Stat(fixture.downloadPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temporary file still exists after success: %v", err)
	}
	if len(stream.sendResp) != 1 {
		t.Fatalf("responses = %d, want 1", len(stream.sendResp))
	}
	success, ok := stream.sendResp[0].Response.(*gnoi_containerz_pb.DeployResponse_ImageTransferSuccess)
	if !ok {
		t.Fatalf("response type = %T, want ImageTransferSuccess", stream.sendResp[0].Response)
	}
	if success.ImageTransferSuccess.Name != "../../sensitive-image-name" ||
		success.ImageTransferSuccess.Tag != "latest" ||
		success.ImageTransferSuccess.ImageSize != 0 {
		t.Errorf("success response = %+v", success.ImageTransferSuccess)
	}
}

func TestNewContainerzDownloadRequestSupportsEveryGNOIProtocol(t *testing.T) {
	tests := []struct {
		name         string
		protocol     gnoi_common_pb.RemoteDownload_Protocol
		path         string
		wantProtocol download.Protocol
	}{
		{
			name:         "SFTP",
			protocol:     gnoi_common_pb.RemoteDownload_SFTP,
			path:         "download.example:/image.tar",
			wantProtocol: download.ProtocolSFTP,
		},
		{
			name:         "HTTP",
			protocol:     gnoi_common_pb.RemoteDownload_HTTP,
			path:         "http://download.example/image.tar",
			wantProtocol: download.ProtocolHTTP,
		},
		{
			name:         "HTTPS",
			protocol:     gnoi_common_pb.RemoteDownload_HTTPS,
			path:         "https://download.example/image.tar",
			wantProtocol: download.ProtocolHTTPS,
		},
		{
			name:         "SCP",
			protocol:     gnoi_common_pb.RemoteDownload_SCP,
			path:         "download.example:/image.tar",
			wantProtocol: download.ProtocolSCP,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := newContainerzDownloadRequest(&gnoi_containerz_pb.ImageTransfer{
				ImageSize: 42,
				RemoteDownload: &gnoi_common_pb.RemoteDownload{
					Path:     tt.path,
					Protocol: tt.protocol,
				},
			})
			if err != nil {
				t.Fatalf("newContainerzDownloadRequest() error = %v", err)
			}
			if request.Protocol != tt.wantProtocol {
				t.Errorf("protocol = %q, want %q", request.Protocol, tt.wantProtocol)
			}
			if request.Path != tt.path {
				t.Errorf("path = %q, want %q", request.Path, tt.path)
			}
			if request.MaxSize != 42 {
				t.Errorf("size limit = %d, want 42", request.MaxSize)
			}
		})
	}
}

func TestDeployCleansUpAtEveryFailureStage(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*deployFixture, *dummyDeployServer)
		wantEvents []string
		wantError  string
		fileExists bool
	}{
		{
			name: "download",
			configure: func(fixture *deployFixture, _ *dummyDeployServer) {
				fixture.downloadErr = errors.New("download failed")
			},
			wantEvents: []string{"download", "close", "remove"},
			wantError:  "download failed",
		},
		{
			name: "temporary file close",
			configure: func(fixture *deployFixture, _ *dummyDeployServer) {
				fixture.closeTempErr = errors.New("file close failed")
			},
			wantEvents: []string{"download", "close", "remove"},
			wantError:  "file close failed",
		},
		{
			name: "D-Bus client creation",
			configure: func(fixture *deployFixture, _ *dummyDeployServer) {
				fixture.newLoaderErr = errors.New("dbus failed")
			},
			wantEvents: []string{"download", "close", "dbus", "remove"},
			wantError:  "dbus failed",
		},
		{
			name: "image load",
			configure: func(fixture *deployFixture, _ *dummyDeployServer) {
				fixture.loadErr = errors.New("load failed")
			},
			wantEvents: []string{"download", "close", "dbus", "load", "dbus-close", "remove"},
			wantError:  "load failed",
		},
		{
			name: "D-Bus client close",
			configure: func(fixture *deployFixture, _ *dummyDeployServer) {
				fixture.closeLoaderErr = errors.New("dbus close failed")
			},
			wantEvents: []string{"download", "close", "dbus", "load", "dbus-close", "remove"},
			wantError:  "dbus close failed",
		},
		{
			name: "cleanup",
			configure: func(fixture *deployFixture, _ *dummyDeployServer) {
				fixture.removeErr = errors.New("cleanup failed")
			},
			wantEvents: []string{"download", "close", "dbus", "load", "dbus-close", "remove"},
			wantError:  "cleanup failed",
			fileExists: true,
		},
		{
			name: "response send",
			configure: func(_ *deployFixture, stream *dummyDeployServer) {
				stream.sendErr = errors.New("send failed")
			},
			wantEvents: []string{"download", "close", "dbus", "load", "dbus-close", "remove", "send"},
			wantError:  "send failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := &deployFixture{}
			stream := &dummyDeployServer{
				recvQueue: []*gnoi_containerz_pb.DeployRequest{
					deployRequest(validImageTransfer()),
				},
				events: &fixture.events,
			}
			tt.configure(fixture, stream)
			server := newContainerzTestServer(fixture.dependencies(t))

			err := server.Deploy(stream)
			if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Deploy() error = %v, want Internal containing %q", err, tt.wantError)
			}
			if !reflect.DeepEqual(fixture.events, tt.wantEvents) {
				t.Errorf("events = %v, want %v", fixture.events, tt.wantEvents)
			}
			_, statErr := os.Stat(fixture.downloadPath)
			if tt.fileExists {
				if statErr != nil {
					t.Errorf("temporary file was removed after cleanup failure: %v", statErr)
				}
				_ = os.Remove(fixture.downloadPath)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("temporary file still exists: %v", statErr)
			}
		})
	}
}

func TestDeployReportsPrimaryAndCleanupFailures(t *testing.T) {
	fixture := &deployFixture{
		loadErr:        errors.New("load failed"),
		closeLoaderErr: errors.New("dbus close failed"),
		removeErr:      errors.New("cleanup failed"),
	}
	server := newContainerzTestServer(fixture.dependencies(t))
	stream := &dummyDeployServer{
		recvQueue: []*gnoi_containerz_pb.DeployRequest{
			deployRequest(validImageTransfer()),
		},
		events: &fixture.events,
	}

	err := server.Deploy(stream)
	if status.Code(err) != codes.Internal {
		t.Fatalf("Deploy() code = %v, want Internal", status.Code(err))
	}
	for _, message := range []string{"load failed", "dbus close failed", "cleanup failed"} {
		if !strings.Contains(err.Error(), message) {
			t.Errorf("Deploy() error = %v, want %q", err, message)
		}
	}
	_ = os.Remove(fixture.downloadPath)
}

func TestDeployRejectsInvalidRemoteDownloadBeforeCreatingTempFile(t *testing.T) {
	tests := []struct {
		name     string
		transfer *gnoi_containerz_pb.ImageTransfer
	}{
		{
			name: "missing remote download",
			transfer: &gnoi_containerz_pb.ImageTransfer{
				Name: "image",
				Tag:  "latest",
			},
		},
		{
			name: "unknown protocol",
			transfer: &gnoi_containerz_pb.ImageTransfer{
				Name: "image",
				Tag:  "latest",
				RemoteDownload: &gnoi_common_pb.RemoteDownload{
					Path: "download.example:/image.tar",
				},
			},
		},
		{
			name: "invalid protocol path",
			transfer: &gnoi_containerz_pb.ImageTransfer{
				Name: "image",
				Tag:  "latest",
				RemoteDownload: &gnoi_common_pb.RemoteDownload{
					Path:     "not-a-url",
					Protocol: gnoi_common_pb.RemoteDownload_HTTPS,
				},
			},
		},
		{
			name: "hashed credentials",
			transfer: &gnoi_containerz_pb.ImageTransfer{
				Name: "image",
				Tag:  "latest",
				RemoteDownload: &gnoi_common_pb.RemoteDownload{
					Path:     "download.example:/image.tar",
					Protocol: gnoi_common_pb.RemoteDownload_SFTP,
					Credentials: &gnoi_types_pb.Credentials{
						Username: "user",
						Password: &gnoi_types_pb.Credentials_Hashed{
							Hashed: &gnoi_types_pb.HashType{
								Hash: []byte("must-not-appear"),
							},
						},
					},
				},
			},
		},
		{
			name: "image size overflow",
			transfer: &gnoi_containerz_pb.ImageTransfer{
				Name:      "image",
				Tag:       "latest",
				ImageSize: uint64(math.MaxInt64) + 1,
				RemoteDownload: &gnoi_common_pb.RemoteDownload{
					Path:     "https://download.example/image.tar",
					Protocol: gnoi_common_pb.RemoteDownload_HTTPS,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := &deployFixture{}
			server := newContainerzTestServer(fixture.dependencies(t))
			stream := &dummyDeployServer{
				recvQueue: []*gnoi_containerz_pb.DeployRequest{
					deployRequest(tt.transfer),
				},
			}

			err := server.Deploy(stream)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("Deploy() error = %v, want InvalidArgument", err)
			}
			if len(fixture.events) != 0 {
				t.Errorf("events = %v, want no dependency calls", fixture.events)
			}
			if strings.Contains(err.Error(), "must-not-appear") {
				t.Fatalf("Deploy() error exposed hashed credentials: %v", err)
			}
		})
	}
}

func TestDeployRequestAndAuthenticationErrors(t *testing.T) {
	tests := []struct {
		name      string
		fixture   *deployFixture
		stream    *dummyDeployServer
		wantCode  codes.Code
		wantError string
	}{
		{
			name:    "authentication",
			fixture: &deployFixture{authenticateErr: status.Error(codes.Unauthenticated, "authentication failed")},
			stream: &dummyDeployServer{
				recvQueue: []*gnoi_containerz_pb.DeployRequest{
					deployRequest(validImageTransfer()),
				},
			},
			wantCode:  codes.Unauthenticated,
			wantError: "authentication failed",
		},
		{
			name:      "receive",
			fixture:   &deployFixture{},
			stream:    &dummyDeployServer{recvErr: errors.New("receive failed")},
			wantCode:  codes.InvalidArgument,
			wantError: "receive failed",
		},
		{
			name:    "wrong first request",
			fixture: &deployFixture{},
			stream: &dummyDeployServer{
				recvQueue: []*gnoi_containerz_pb.DeployRequest{
					{
						Request: &gnoi_containerz_pb.DeployRequest_Content{
							Content: []byte("content"),
						},
					},
				},
			},
			wantCode:  codes.InvalidArgument,
			wantError: "first DeployRequest must be ImageTransfer",
		},
		{
			name:    "temporary file creation",
			fixture: &deployFixture{createTempErr: errors.New("create temp failed")},
			stream: &dummyDeployServer{
				recvQueue: []*gnoi_containerz_pb.DeployRequest{
					deployRequest(validImageTransfer()),
				},
			},
			wantCode:  codes.Internal,
			wantError: "create temp failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newContainerzTestServer(tt.fixture.dependencies(t))

			err := server.Deploy(tt.stream)
			if status.Code(err) != tt.wantCode || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Deploy() error = %v, want %v containing %q", err, tt.wantCode, tt.wantError)
			}
		})
	}
}

func newContainerzTestServer(dependencies *containerzDeployDependencies) *ContainerzServer {
	return &ContainerzServer{
		server: &Server{
			config: &Config{},
		},
		deployDependencies: dependencies,
	}
}

func deployRequest(transfer *gnoi_containerz_pb.ImageTransfer) *gnoi_containerz_pb.DeployRequest {
	return &gnoi_containerz_pb.DeployRequest{
		Request: &gnoi_containerz_pb.DeployRequest_ImageTransfer{
			ImageTransfer: transfer,
		},
	}
}

func validImageTransfer() *gnoi_containerz_pb.ImageTransfer {
	return &gnoi_containerz_pb.ImageTransfer{
		Name:      "image",
		Tag:       "latest",
		ImageSize: 1024,
		RemoteDownload: &gnoi_common_pb.RemoteDownload{
			Path:     "download.example:/image.tar",
			Protocol: gnoi_common_pb.RemoteDownload_SFTP,
			Credentials: &gnoi_types_pb.Credentials{
				Username: "user",
				Password: &gnoi_types_pb.Credentials_Cleartext{Cleartext: "password"},
			},
		},
	}
}

func TestDeployTemporaryPathUsesOnlySafePattern(t *testing.T) {
	fixture := &deployFixture{}
	server := newContainerzTestServer(fixture.dependencies(t))
	stream := &dummyDeployServer{
		recvQueue: []*gnoi_containerz_pb.DeployRequest{
			deployRequest(&gnoi_containerz_pb.ImageTransfer{
				Name: "../" + filepath.Join("unsafe", "image"),
				Tag:  "latest",
				RemoteDownload: &gnoi_common_pb.RemoteDownload{
					Path:     "https://download.example/image.tar",
					Protocol: gnoi_common_pb.RemoteDownload_HTTPS,
				},
			}),
		},
		events: &fixture.events,
	}

	if err := server.Deploy(stream); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if fixture.tempPattern != "containerz-image-*.tar" {
		t.Errorf("temporary file pattern = %q, want %q", fixture.tempPattern, "containerz-image-*.tar")
	}
	if strings.Contains(fixture.downloadPath, "unsafe") {
		t.Errorf("temporary path contains caller-controlled image name: %q", fixture.downloadPath)
	}
}
