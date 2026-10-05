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
	err := NewRemoteClient().Download(context.Background(), Request{
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

	err := NewRemoteClient().Download(context.Background(), Request{
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

	client := NewRemoteClient()
	client.HTTPTransport = server.Client().Transport

	var destination bytes.Buffer
	err := client.Download(context.Background(), Request{
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
	client := NewRemoteClient()
	transport, ok := client.HTTPTransport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTPTransport type = %T, want *http.Transport", client.HTTPTransport)
	}
	if transport.Proxy != nil {
		t.Error("HTTP transport must not use proxy environment variables")
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("HTTP transport must verify TLS certificates")
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

	err := NewRemoteClient().Download(context.Background(), Request{
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

	err := NewRemoteClient().Download(context.Background(), Request{
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
			err := NewRemoteClient().Download(context.Background(), Request{
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
	client := NewRemoteClient()
	client.HTTPTransport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
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
	err := client.Download(context.Background(), Request{
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

func TestRemoteClientHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := NewRemoteClient().Download(ctx, Request{
		Protocol: ProtocolHTTP,
		Path:     "http://example.com/image.tar",
	}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Download() error = %v, want context.Canceled", err)
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
			client := NewRemoteClient()
			client.KnownHostsFiles = []string{tt.prepare(t)}
			client.DialContext = func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("DialContext() called before known-host data was validated")
				return nil, errors.New("unexpected dial")
			}

			err := client.Download(context.Background(), Request{
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
			client.KnownHostsFiles = []string{tt.knownHosts(t, server, requestedAddress)}

			err := client.Download(context.Background(), Request{
				Protocol: ProtocolSFTP,
				Path:     requestedAddress + ":/image.tar",
				Username: "user",
				Password: "password",
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "host-key") {
				t.Fatalf("Download() error = %v, want host-key verification error", err)
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
	client.KnownHostsFiles = []string{
		filepath.Join(t.TempDir(), "missing-known-hosts"),
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	var destination bytes.Buffer
	err := client.Download(context.Background(), Request{
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

func TestRemoteClientSCPSuccessQuotesRemotePath(t *testing.T) {
	content := []byte("scp image")
	server := startSSHTestServer(t, content)
	const requestedAddress = "download.example:2222"
	client := remoteClientForSSHServer(server)
	client.KnownHostsFiles = []string{
		writeKnownHosts(t, requestedAddress, server.signer.PublicKey(), false),
	}

	const remotePath = `/images/image'; touch /tmp/injected; echo '.tar`
	var destination bytes.Buffer
	err := client.Download(context.Background(), Request{
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

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

type sshTestServer struct {
	address   string
	signer    ssh.Signer
	authCalls atomic.Int32
	commands  chan string
	listener  net.Listener
	scpData   []byte
}

func startSSHTestServer(t *testing.T, scpData []byte) *sshTestServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	server := &sshTestServer{
		address:  listener.Addr().String(),
		signer:   newSigner(t),
		commands: make(chan string, 1),
		listener: listener,
		scpData:  scpData,
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
			server, err := sftp.NewServer(channel)
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

func remoteClientForSSHServer(server *sshTestServer) *RemoteClient {
	client := NewRemoteClient()
	client.Timeout = 5 * time.Second
	client.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
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
