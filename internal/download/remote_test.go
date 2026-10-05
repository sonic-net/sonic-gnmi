package download

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestParseRemoteTarget(t *testing.T) {
	tests := []struct {
		name     string
		request  Request
		wantURL  string
		wantAddr string
		wantPath string
		wantErr  bool
	}{
		{
			name:    "HTTP URL",
			request: Request{Protocol: ProtocolHTTP, Path: "http://example.com/image.tar"},
			wantURL: "http://example.com/image.tar",
		},
		{
			name:    "HTTPS URL with port",
			request: Request{Protocol: ProtocolHTTPS, Path: "https://example.com:8443/image.tar"},
			wantURL: "https://example.com:8443/image.tar",
		},
		{
			name:     "SFTP default port",
			request:  Request{Protocol: ProtocolSFTP, Path: "download.example:/images/image.tar"},
			wantAddr: "download.example:22",
			wantPath: "/images/image.tar",
		},
		{
			name:     "SCP home-relative path",
			request:  Request{Protocol: ProtocolSCP, Path: "download.example:~/images/image.tar"},
			wantAddr: "download.example:22",
			wantPath: "~/images/image.tar",
		},
		{
			name:     "SFTP explicit port",
			request:  Request{Protocol: ProtocolSFTP, Path: "download.example:2222:/images/image.tar"},
			wantAddr: "download.example:2222",
			wantPath: "/images/image.tar",
		},
		{
			name:     "SCP bracketed IPv6",
			request:  Request{Protocol: ProtocolSCP, Path: "[2001:db8::10]:/images/image.tar"},
			wantAddr: "[2001:db8::10]:22",
			wantPath: "/images/image.tar",
		},
		{
			name:     "SFTP bracketed IPv6 and port",
			request:  Request{Protocol: ProtocolSFTP, Path: "[2001:db8::10]:2222:/images/image.tar"},
			wantAddr: "[2001:db8::10]:2222",
			wantPath: "/images/image.tar",
		},
		{
			name:    "scheme mismatch",
			request: Request{Protocol: ProtocolHTTPS, Path: "http://example.com/image.tar"},
			wantErr: true,
		},
		{
			name:    "embedded URL credentials",
			request: Request{Protocol: ProtocolHTTPS, Path: "https://user:secret@example.com/image.tar"},
			wantErr: true,
		},
		{
			name:    "ambiguous URL authority",
			request: Request{Protocol: ProtocolHTTP, Path: `http://example.com\@attacker.invalid/image.tar`},
			wantErr: true,
		},
		{
			name:    "missing HTTP host",
			request: Request{Protocol: ProtocolHTTP, Path: "http:///image.tar"},
			wantErr: true,
		},
		{
			name:    "missing SSH host",
			request: Request{Protocol: ProtocolSFTP, Path: ":/images/image.tar"},
			wantErr: true,
		},
		{
			name:    "unbracketed IPv6",
			request: Request{Protocol: ProtocolSFTP, Path: "2001:db8::10:/images/image.tar"},
			wantErr: true,
		},
		{
			name:    "invalid SSH port",
			request: Request{Protocol: ProtocolSCP, Path: "download.example:not-a-port:/images/image.tar"},
			wantErr: true,
		},
		{
			name:    "out of range SSH port",
			request: Request{Protocol: ProtocolSCP, Path: "download.example:65536:/images/image.tar"},
			wantErr: true,
		},
		{
			name:    "relative SSH path",
			request: Request{Protocol: ProtocolSFTP, Path: "download.example:images/image.tar"},
			wantErr: true,
		},
		{
			name:    "control character",
			request: Request{Protocol: ProtocolSCP, Path: "download.example:/images/\nimage.tar"},
			wantErr: true,
		},
		{
			name:    "unknown protocol",
			request: Request{Path: "download.example:/images/image.tar"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRemoteTarget(tt.request)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseRemoteTarget() expected error, got nil")
				}
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("parseRemoteTarget() error = %v, want ErrInvalidRequest", err)
				}
				if strings.Contains(err.Error(), tt.request.Path) {
					t.Fatalf("parseRemoteTarget() error exposed remote path: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRemoteTarget() error = %v", err)
			}
			if got.httpURL != nil && got.httpURL.String() != tt.wantURL {
				t.Errorf("parseRemoteTarget() URL = %q, want %q", got.httpURL, tt.wantURL)
			}
			if got.sshAddress != tt.wantAddr {
				t.Errorf("parseRemoteTarget() SSH address = %q, want %q", got.sshAddress, tt.wantAddr)
			}
			if got.remotePath != tt.wantPath {
				t.Errorf("parseRemoteTarget() remote path = %q, want %q", got.remotePath, tt.wantPath)
			}
		})
	}
}

func TestRemoteClientHTTPSuccess(t *testing.T) {
	const content = "container image"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "user" || password != "password" {
			t.Errorf("BasicAuth() = %q, %q, %v", username, password, ok)
		}
		_, _ = io.WriteString(w, content)
	}))
	defer server.Close()

	var destination bytes.Buffer
	err := newRemoteClient().download(context.Background(), Request{
		Protocol: ProtocolHTTP,
		Path:     server.URL + "/image.tar",
		Username: "user",
		Password: "password",
	}, &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if destination.String() != content {
		t.Errorf("Download() content = %q, want %q", destination.String(), content)
	}
}

func TestRemoteClientHTTPDoesNotSendPasswordWithoutUsername(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Errorf("Authorization header = %q, want empty", authorization)
		}
		_, _ = io.WriteString(w, "image")
	}))
	defer server.Close()

	err := newRemoteClient().download(context.Background(), Request{
		Protocol: ProtocolHTTP,
		Path:     server.URL + "/image.tar",
		Password: "must-not-be-sent",
	}, io.Discard)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
}

func TestRemoteClientHTTPSSuccess(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "secure image")
	}))
	defer server.Close()

	client := newRemoteClient()
	client.httpTransport = server.Client().Transport

	var destination bytes.Buffer
	err := client.download(context.Background(), Request{
		Protocol: ProtocolHTTPS,
		Path:     server.URL + "/image.tar",
	}, &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if destination.String() != "secure image" {
		t.Errorf("Download() content = %q, want %q", destination.String(), "secure image")
	}
}

func TestNewRemoteClientDisablesAmbientHTTPState(t *testing.T) {
	client := newRemoteClient()
	transport, ok := client.httpTransport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP transport type = %T, want *http.Transport", client.httpTransport)
	}
	if transport.Proxy != nil {
		t.Error("HTTP transport must not use proxy environment variables")
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("HTTP transport must verify TLS certificates")
	}
	if transport.DialTLSContext != nil {
		t.Error("HTTP transport must use the standard verified TLS dial path")
	}
}

func TestNewRemoteClientTranslatesDefaultKnownHostsFiles(t *testing.T) {
	client := newRemoteClientWithHostPath(func(path string) string {
		return "/mnt/host" + path
	})
	want := []string{
		"/mnt/host/etc/ssh/ssh_known_hosts",
		"/mnt/host/root/.ssh/known_hosts",
	}
	if !reflect.DeepEqual(client.knownHostsFiles, want) {
		t.Errorf("known-host files = %v, want %v", client.knownHostsFiles, want)
	}
}

func TestRemoteClientHTTPClosesIdleConnections(t *testing.T) {
	transport := &closeTrackingRoundTripper{
		roundTrip: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("image")),
				Header:     make(http.Header),
			}, nil
		},
	}
	client := newRemoteClient()
	client.httpTransport = transport

	if err := client.download(context.Background(), Request{
		Protocol: ProtocolHTTP,
		Path:     "http://example.com/image.tar",
	}, io.Discard); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if got := transport.closeCalls.Load(); got != 1 {
		t.Errorf("CloseIdleConnections() calls = %d, want 1", got)
	}
}

func TestRemoteClientHTTPRejectsRedirectWithoutForwardingCredentials(t *testing.T) {
	var redirectedRequests atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Errorf("redirect target received Authorization header %q", authorization)
		}
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/stolen", http.StatusFound)
	}))
	defer server.Close()

	err := newRemoteClient().download(context.Background(), Request{
		Protocol: ProtocolHTTP,
		Path:     server.URL + "/token-in-path",
		Username: "user",
		Password: "password",
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("Download() error = %v, want redirect error", err)
	}
	if strings.Contains(err.Error(), "token-in-path") || strings.Contains(err.Error(), "password") {
		t.Fatalf("Download() error exposed secret request data: %v", err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Errorf("redirect target requests = %d, want 0", got)
	}
}

func TestRemoteClientHTTPStatusErrorIsSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "server-secret")
	}))
	defer server.Close()

	err := newRemoteClient().download(context.Background(), Request{
		Protocol: ProtocolHTTP,
		Path:     server.URL + "/token-in-path",
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("Download() error = %v, want status 503", err)
	}
	for _, secret := range []string{"server-secret", "token-in-path"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Download() error exposed %q: %v", secret, err)
		}
	}
}

func TestRemoteClientHTTPSizeLimit(t *testing.T) {
	tests := []struct {
		name          string
		contentLength bool
	}{
		{name: "declared size", contentLength: true},
		{name: "streamed size"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.contentLength {
					w.Header().Set("Content-Length", "6")
				}
				_, _ = io.WriteString(w, "123456")
			}))
			defer server.Close()

			var destination bytes.Buffer
			err := newRemoteClient().download(context.Background(), Request{
				Protocol: ProtocolHTTP,
				Path:     server.URL + "/image.tar",
				MaxSize:  5,
			}, &destination)
			if err == nil || !strings.Contains(err.Error(), "size limit") {
				t.Fatalf("Download() error = %v, want size limit error", err)
			}
		})
	}
}

func TestRemoteClientHTTPReadFailureIsNotSuccessAndIsSanitized(t *testing.T) {
	client := newRemoteClient()
	client.httpTransport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: &readCloser{
				Reader: &failingReader{
					data: []byte("partial"),
					err:  errors.New("remote-secret"),
				},
			},
			Header: make(http.Header),
		}, nil
	})

	var destination bytes.Buffer
	err := client.download(context.Background(), Request{
		Protocol: ProtocolHTTP,
		Path:     "http://example.com/token-in-path",
	}, &destination)
	if err == nil {
		t.Fatal("Download() expected a read error, got nil")
	}
	for _, secret := range []string{"remote-secret", "token-in-path"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Download() error exposed %q: %v", secret, err)
		}
	}
}

func TestRemoteClientHTTPStopsAfterRepeatedEmptyReads(t *testing.T) {
	client := newRemoteClient()
	client.httpTransport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       &readCloser{Reader: zeroReader{}},
			Header:     make(http.Header),
		}, nil
	})

	err := client.download(context.Background(), Request{
		Protocol: ProtocolHTTP,
		Path:     "http://example.com/image.tar",
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no progress") {
		t.Fatalf("Download() error = %v, want no-progress error", err)
	}
}

func TestRemoteClientHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := newRemoteClient().download(ctx, Request{
		Protocol: ProtocolHTTP,
		Path:     "http://example.com/image.tar",
	}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Download() error = %v, want context.Canceled", err)
	}
}

func TestRemoteClientEnforcesTransferTimeout(t *testing.T) {
	client := newRemoteClient()
	client.timeout = 20 * time.Millisecond
	client.httpTransport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	err := client.download(context.Background(), Request{
		Protocol: ProtocolHTTP,
		Path:     "http://example.com/image.tar",
	}, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Download() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestRemoteClientRejectsInvalidKnownHostsBeforeDial(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T) string
	}{
		{
			name: "unreadable",
			prepare: func(t *testing.T) string {
				return t.TempDir()
			},
		},
		{
			name: "malformed",
			prepare: func(t *testing.T) string {
				return writeFile(t, "known_hosts", "broken-entry\n")
			},
		},
		{
			name: "unsupported key",
			prepare: func(t *testing.T) string {
				return writeFile(t, "known_hosts", "download.example ssh-unknown AAAA\n")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newRemoteClient()
			client.knownHostsFiles = []string{tt.prepare(t)}
			client.dialContext = func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("DialContext() called before known-host data was validated")
				return nil, errors.New("unexpected dial")
			}

			err := client.download(context.Background(), Request{
				Protocol: ProtocolSFTP,
				Path:     "download.example:/image.tar",
				Username: "user",
				Password: "password",
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "known-host") {
				t.Fatalf("Download() error = %v, want known-host error", err)
			}
		})
	}
}

func TestRemoteClientRejectsUntrustedSSHKeysBeforeAuthentication(t *testing.T) {
	tests := []struct {
		name       string
		knownHosts func(*testing.T, *sshTestServer, string) string
	}{
		{
			name: "all trust stores absent",
			knownHosts: func(t *testing.T, _ *sshTestServer, _ string) string {
				return filepath.Join(t.TempDir(), "missing-known-hosts")
			},
		},
		{
			name: "unknown key",
			knownHosts: func(t *testing.T, _ *sshTestServer, _ string) string {
				return writeFile(t, "known_hosts", "")
			},
		},
		{
			name: "changed key",
			knownHosts: func(t *testing.T, _ *sshTestServer, host string) string {
				return writeKnownHosts(t, host, newSigner(t).PublicKey(), false)
			},
		},
		{
			name: "revoked key",
			knownHosts: func(t *testing.T, server *sshTestServer, host string) string {
				return writeKnownHosts(t, host, server.signer.PublicKey(), true)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startSSHTestServer(t, nil)
			const requestedAddress = "download.example:2222"
			client := remoteClientForSSHServer(server)
			client.knownHostsFiles = []string{tt.knownHosts(t, server, requestedAddress)}

			err := client.download(context.Background(), Request{
				Protocol: ProtocolSFTP,
				Path:     requestedAddress + ":/secret-path/image.tar",
				Username: "secret-user",
				Password: "password",
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "host-key") {
				t.Fatalf("Download() error = %v, want host-key verification error", err)
			}
			for _, secret := range []string{"secret-path", "secret-user", "password"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("Download() error exposed %q: %v", secret, err)
				}
			}
			if got := server.authCalls.Load(); got != 0 {
				t.Errorf("authentication attempts = %d, want 0", got)
			}
		})
	}
}

func TestRemoteClientSFTPSuccessWithAliasAndNonDefaultPort(t *testing.T) {
	content := []byte("sftp image")
	remotePath := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(remotePath, content, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	server := startSSHTestServer(t, nil)
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.knownHostsFiles = []string{
		filepath.Join(t.TempDir(), "missing-known-hosts"),
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	var destination bytes.Buffer
	err := client.download(context.Background(), Request{
		Protocol: ProtocolSFTP,
		Path:     requestedAddress + ":" + remotePath,
		Username: "user",
		Password: "password",
	}, &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if !bytes.Equal(destination.Bytes(), content) {
		t.Errorf("Download() content = %q, want %q", destination.Bytes(), content)
	}
	if got := server.authCalls.Load(); got != 1 {
		t.Errorf("authentication attempts = %d, want 1", got)
	}
}

func TestRemoteClientSFTPResolvesHomeRelativePath(t *testing.T) {
	content := []byte("sftp home image")
	homeDirectory := t.TempDir()
	imagesDirectory := filepath.Join(homeDirectory, "images")
	if err := os.Mkdir(imagesDirectory, 0700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(imagesDirectory, "image.tar"), content, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	server := startSSHTestServerWithSFTPDirectory(t, homeDirectory)
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.knownHostsFiles = []string{
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	var destination bytes.Buffer
	err := client.download(context.Background(), Request{
		Protocol: ProtocolSFTP,
		Path:     requestedAddress + ":~/images/image.tar",
		Username: "user",
		Password: "password",
	}, &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if !bytes.Equal(destination.Bytes(), content) {
		t.Errorf("Download() content = %q, want %q", destination.Bytes(), content)
	}
}

func TestRemoteClientSFTPPreservesHomeRelativeDotSegments(t *testing.T) {
	content := []byte("symlink-aware image")
	homeDirectory := t.TempDir()
	targetDirectory := filepath.Join(homeDirectory, "target")
	nestedDirectory := filepath.Join(targetDirectory, "nested")
	if err := os.MkdirAll(nestedDirectory, 0700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDirectory, "image.tar"), []byte("lexically cleaned image"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDirectory, "image.tar"), content, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(nestedDirectory, filepath.Join(homeDirectory, "link")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	server := startSSHTestServerWithSFTPDirectory(t, homeDirectory)
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.knownHostsFiles = []string{
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	var destination bytes.Buffer
	err := client.download(context.Background(), Request{
		Protocol: ProtocolSFTP,
		Path:     requestedAddress + ":~/link/../image.tar",
		Username: "user",
		Password: "password",
	}, &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if !bytes.Equal(destination.Bytes(), content) {
		t.Errorf("Download() content = %q, want %q", destination.Bytes(), content)
	}
}

func TestRemoteClientSFTPCancellationDuringHomeResolution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	lister := &blockingRealPathLister{
		started: started,
		release: release,
	}
	server := startSSHTestServerWithSFTPHandlers(t, sftp.Handlers{
		FileList: lister,
	})
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.knownHostsFiles = []string{
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- client.download(ctx, Request{
			Protocol: ProtocolSFTP,
			Path:     requestedAddress + ":~/image.tar",
			Username: "user",
			Password: "password",
		}, io.Discard)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SFTP home resolution")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Download() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for canceled SFTP download")
	}
}

func TestRemoteClientSFTPSizeLimit(t *testing.T) {
	remotePath := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(remotePath, []byte("123456"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	server := startSSHTestServer(t, nil)
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.knownHostsFiles = []string{
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	err := client.download(context.Background(), Request{
		Protocol: ProtocolSFTP,
		Path:     requestedAddress + ":" + remotePath,
		Username: "user",
		Password: "password",
		MaxSize:  5,
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("Download() error = %v, want size limit error", err)
	}
}

func TestResolveSFTPRemotePath(t *testing.T) {
	tests := []struct {
		name       string
		remotePath string
		getwd      func() (string, error)
		want       string
		wantErr    bool
	}{
		{
			name:       "absolute path",
			remotePath: "/images/image.tar",
			getwd: func() (string, error) {
				t.Fatal("Getwd() called for an absolute path")
				return "", nil
			},
			want: "/images/image.tar",
		},
		{
			name:       "home directory",
			remotePath: "~",
			getwd:      func() (string, error) { return "/home/user", nil },
			want:       "/home/user",
		},
		{
			name:       "home-relative path",
			remotePath: "~/images/image.tar",
			getwd:      func() (string, error) { return "/home/user", nil },
			want:       "/home/user/images/image.tar",
		},
		{
			name:       "home-relative path preserves dot segments",
			remotePath: "~/link/../image.tar",
			getwd:      func() (string, error) { return "/home/user", nil },
			want:       "/home/user/link/../image.tar",
		},
		{
			name:       "root home-relative path",
			remotePath: "~/image.tar",
			getwd:      func() (string, error) { return "/", nil },
			want:       "/image.tar",
		},
		{
			name:       "resolution failure",
			remotePath: "~/images/image.tar",
			getwd:      func() (string, error) { return "", errors.New("remote-secret") },
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSFTPRemotePath(tt.remotePath, tt.getwd)
			if tt.wantErr {
				if err == nil {
					t.Fatal("resolveSFTPRemotePath() expected error, got nil")
				}
				if strings.Contains(err.Error(), "remote-secret") {
					t.Fatalf("resolveSFTPRemotePath() exposed server error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSFTPRemotePath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveSFTPRemotePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRemoteClientSCPSuccessQuotesRemotePath(t *testing.T) {
	content := []byte("scp image")
	server := startSSHTestServer(t, content)
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.knownHostsFiles = []string{
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	const remotePath = `/images/image'; touch /tmp/injected; echo '.tar`
	var destination bytes.Buffer
	err := client.download(context.Background(), Request{
		Protocol: ProtocolSCP,
		Path:     requestedAddress + ":" + remotePath,
		Username: "user",
		Password: "password",
	}, &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if !bytes.Equal(destination.Bytes(), content) {
		t.Errorf("Download() content = %q, want %q", destination.Bytes(), content)
	}

	select {
	case command := <-server.commands:
		const want = `scp -f -- '/images/image'"'"'; touch /tmp/injected; echo '"'"'.tar'`
		if command != want {
			t.Errorf("SCP command = %q, want %q", command, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SCP command")
	}
}

func TestRemoteClientSCPExpandsHomeWithoutCommandInjection(t *testing.T) {
	server := startSSHTestServer(t, []byte("scp image"))
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.knownHostsFiles = []string{
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	const remotePath = `~/images/image'; touch /tmp/injected; echo '.tar`
	if err := client.download(context.Background(), Request{
		Protocol: ProtocolSCP,
		Path:     requestedAddress + ":" + remotePath,
		Username: "user",
		Password: "password",
	}, io.Discard); err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	select {
	case command := <-server.commands:
		const want = `scp -f -- "$HOME"/'images/image'"'"'; touch /tmp/injected; echo '"'"'.tar'`
		if command != want {
			t.Errorf("SCP command = %q, want %q", command, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SCP command")
	}
}

func TestRemoteClientSCPSizeLimit(t *testing.T) {
	server := startSSHTestServer(t, []byte("123456"))
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.knownHostsFiles = []string{
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	err := client.download(context.Background(), Request{
		Protocol: ProtocolSCP,
		Path:     requestedAddress + ":/image.tar",
		Username: "user",
		Password: "password",
		MaxSize:  5,
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("Download() error = %v, want size limit error", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type closeTrackingRoundTripper struct {
	roundTrip  func(*http.Request) (*http.Response, error)
	closeCalls atomic.Int32
}

func (t *closeTrackingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.roundTrip(request)
}

func (t *closeTrackingRoundTripper) CloseIdleConnections() {
	t.closeCalls.Add(1)
}

type readCloser struct {
	io.Reader
}

func (r *readCloser) Close() error {
	return nil
}

type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) {
	return 0, nil
}

type blockingRealPathLister struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (l *blockingRealPathLister) Filelist(*sftp.Request) (sftp.ListerAt, error) {
	return nil, errors.New("unexpected SFTP list request")
}

func (l *blockingRealPathLister) RealPath(string) (string, error) {
	l.started <- struct{}{}
	<-l.release
	return "", errors.New("SFTP connection closed")
}

type sshTestServer struct {
	address              string
	signer               ssh.Signer
	authCalls            atomic.Int32
	commands             chan string
	listener             net.Listener
	scpData              []byte
	sftpWorkingDirectory string
	sftpHandlers         *sftp.Handlers
}

func startSSHTestServer(t *testing.T, scpData []byte) *sshTestServer {
	return startSSHTestServerWithOptions(t, scpData, "", nil)
}

func startSSHTestServerWithSFTPDirectory(
	t *testing.T,
	workingDirectory string,
) *sshTestServer {
	return startSSHTestServerWithOptions(t, nil, workingDirectory, nil)
}

func startSSHTestServerWithSFTPHandlers(
	t *testing.T,
	handlers sftp.Handlers,
) *sshTestServer {
	return startSSHTestServerWithOptions(t, nil, "", &handlers)
}

func startSSHTestServerWithOptions(
	t *testing.T,
	scpData []byte,
	sftpWorkingDirectory string,
	sftpHandlers *sftp.Handlers,
) *sshTestServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	server := &sshTestServer{
		address:              listener.Addr().String(),
		signer:               newSigner(t),
		commands:             make(chan string, 1),
		listener:             listener,
		scpData:              scpData,
		sftpWorkingDirectory: sftpWorkingDirectory,
		sftpHandlers:         sftpHandlers,
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			server.authCalls.Add(1)
			if string(password) != "password" {
				return nil, errors.New("authentication failed")
			}
			return nil, nil
		},
	}
	config.AddHostKey(server.signer)

	go server.serve(config)
	t.Cleanup(func() {
		_ = listener.Close()
	})
	return server
}

func (s *sshTestServer) serve(config *ssh.ServerConfig) {
	connection, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()

	_, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)

	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			return
		}
		go s.serveSession(channel, channelRequests)
	}
}

func (s *sshTestServer) serveSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	for request := range requests {
		switch request.Type {
		case "subsystem":
			var payload struct {
				Name string
			}
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Name != "sftp" {
				_ = request.Reply(false, nil)
				return
			}
			_ = request.Reply(true, nil)
			if s.sftpHandlers != nil {
				server := sftp.NewRequestServer(channel, *s.sftpHandlers)
				_ = server.Serve()
				_ = server.Close()
				return
			}
			options := []sftp.ServerOption{}
			if s.sftpWorkingDirectory != "" {
				options = append(
					options,
					sftp.WithServerWorkingDirectory(s.sftpWorkingDirectory),
				)
			}
			server, err := sftp.NewServer(channel, options...)
			if err != nil {
				return
			}
			_ = server.Serve()
			_ = server.Close()
			return
		case "exec":
			var payload struct {
				Command string
			}
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				_ = request.Reply(false, nil)
				return
			}
			s.commands <- payload.Command
			_ = request.Reply(true, nil)
			s.serveSCP(channel)
			return
		default:
			_ = request.Reply(false, nil)
		}
	}
}

func (s *sshTestServer) serveSCP(channel ssh.Channel) {
	if err := readSCPAck(channel); err != nil {
		return
	}
	if _, err := fmt.Fprintf(channel, "C0600 %d ignored-name\n", len(s.scpData)); err != nil {
		return
	}
	if err := readSCPAck(channel); err != nil {
		return
	}
	if _, err := channel.Write(s.scpData); err != nil {
		return
	}
	if _, err := channel.Write([]byte{0}); err != nil {
		return
	}
	if err := readSCPAck(channel); err != nil {
		return
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct {
		Status uint32
	}{Status: 0}))
}

func readSCPAck(reader io.Reader) error {
	var ack [1]byte
	if _, err := io.ReadFull(reader, ack[:]); err != nil {
		return err
	}
	if ack[0] != 0 {
		return fmt.Errorf("unexpected SCP acknowledgement %d", ack[0])
	}
	return nil
}

func remoteClientForSSHServer(server *sshTestServer) *remoteClient {
	client := newRemoteClient()
	client.timeout = 5 * time.Second
	client.dialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.address)
	}
	return client
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	return signer
}

func writeKnownHosts(t *testing.T, address string, publicKey ssh.PublicKey, revoked bool) string {
	t.Helper()

	entry := knownhosts.Line([]string{knownhosts.Normalize(address)}, publicKey)
	if revoked {
		entry = "@revoked " + entry
	}
	return writeFile(t, "known_hosts", entry+"\n")
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
