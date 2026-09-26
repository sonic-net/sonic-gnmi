package dpuephemeraltls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"testing"
	"time"
)

type testAddr string

func (a testAddr) Network() string { return "ip+net" }
func (a testAddr) String() string  { return string(a) }

func TestNew(t *testing.T) {
	originalInterfaceAddrs := interfaceAddrs
	originalHostName := hostName
	defer func() {
		interfaceAddrs = originalInterfaceAddrs
		hostName = originalHostName
	}()

	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			testAddr("127.0.0.1/8"),
			testAddr("169.254.200.1/24"),
			&net.IPAddr{IP: net.ParseIP("10.0.0.2")},
			testAddr("10.0.0.2/24"),
		}, nil
	}
	hostName = func() (string, error) { return "dpu0", nil }
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

	certificate, err := New(now)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	leaf := certificate.Leaf
	if leaf == nil {
		t.Fatal("generated certificate has no parsed leaf")
	}
	for _, identity := range []string{"169.254.200.1", "10.0.0.2", "dpu0"} {
		if err := leaf.VerifyHostname(identity); err != nil {
			t.Errorf("certificate does not match %q: %v", identity, err)
		}
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err == nil {
		t.Error("certificate unexpectedly contains a loopback SAN")
	}
	if got, want := leaf.NotAfter.Sub(now), certificateLifetime; got != want {
		t.Errorf("certificate lifetime = %v, want %v", got, want)
	}

	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("tls.Listen() failed: %v", err)
	}
	defer listener.Close()
	serverErr := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			err = connection.(*tls.Conn).Handshake()
			connection.Close()
		}
		serverErr <- err
	}()

	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{
		RootCAs:    roots,
		ServerName: "169.254.200.1",
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("TLS client failed to verify generated certificate by IP: %v", err)
	}
	client.Close()
	if err := <-serverErr; err != nil {
		t.Fatalf("TLS server handshake failed: %v", err)
	}
}

func TestNewRequiresAddress(t *testing.T) {
	originalInterfaceAddrs := interfaceAddrs
	originalHostName := hostName
	defer func() {
		interfaceAddrs = originalInterfaceAddrs
		hostName = originalHostName
	}()

	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{testAddr("127.0.0.1/8")}, nil
	}
	hostName = func() (string, error) { return "dpu0", nil }
	if _, err := New(time.Now()); err == nil {
		t.Fatal("New() succeeded without a non-loopback address")
	}
}

func TestNewAddressError(t *testing.T) {
	originalInterfaceAddrs := interfaceAddrs
	defer func() { interfaceAddrs = originalInterfaceAddrs }()

	interfaceAddrs = func() ([]net.Addr, error) {
		return nil, fmt.Errorf("address failure")
	}
	if _, err := New(time.Now()); err == nil {
		t.Fatal("New() succeeded after an address error")
	}
}
