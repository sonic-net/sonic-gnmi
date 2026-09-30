package telemetry_dialout

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

type dialoutTestCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newDialoutTestCA(t *testing.T) *dialoutTestCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &dialoutTestCA{cert: cert, key: key, pool: pool}
}

func (ca *dialoutTestCA) serverCertificate(t *testing.T, modify func(*x509.Certificate)) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	if modify != nil {
		modify(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type dialoutTLSCollector struct {
	received chan *gpb.SubscribeResponse
}

func (c *dialoutTLSCollector) Publish(stream spb.GNMIDialOut_PublishServer) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	c.received <- message
	return nil
}

func TestDialOutTLS(t *testing.T) {
	// DialOutRun uses package-global configuration and other integration tests
	// leave background subscriptions running. Isolate transport tests from them.
	if os.Getenv("SONIC_DIALOUT_TLS_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDialOutTLS$", "-test.v")
		cmd.Env = append(os.Environ(), "SONIC_DIALOUT_TLS_TEST=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated transport tests: %v\n%s", err, output)
		}
		t.Logf("%s", output)
		return
	}

	ca := newDialoutTestCA(t)
	for _, tt := range []struct {
		name       string
		modify     func(*x509.Certificate)
		serverName string
		untrusted  bool
		insecure   bool
		plaintext  bool
		nilTLS     bool
		wantErr    bool
	}{
		{name: "trusted IP"},
		{name: "unknown CA", untrusted: true, wantErr: true},
		{name: "wrong IP", modify: func(c *x509.Certificate) {
			c.IPAddresses = []net.IP{net.ParseIP("127.0.0.2")}
		}, wantErr: true},
		{name: "DNS override", serverName: "collector.example", modify: func(c *x509.Certificate) {
			c.IPAddresses = nil
			c.DNSNames = []string{"collector.example"}
		}},
		{name: "wrong DNS override", serverName: "other.example", wantErr: true},
		{name: "expired", modify: func(c *x509.Certificate) {
			c.NotBefore = time.Now().Add(-2 * time.Hour)
			c.NotAfter = time.Now().Add(-time.Hour)
		}, wantErr: true},
		{name: "not yet valid", modify: func(c *x509.Certificate) {
			c.NotBefore = time.Now().Add(10 * time.Minute)
		}, wantErr: true},
		{name: "wrong certificate usage", modify: func(c *x509.Certificate) {
			c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}, wantErr: true},
		{name: "explicit development override", untrusted: true, insecure: true},
		{name: "plaintext collector", plaintext: true, wantErr: true},
		{name: "missing TLS config", nilTLS: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			cert := ca.serverCertificate(t, tt.modify)
			var handshakes atomic.Int32
			var opts []grpc.ServerOption
			if !tt.plaintext {
				opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{
					MinVersion: tls.VersionTLS13,
					GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
						handshakes.Add(1)
						return &cert, nil
					},
				})))
			}
			server := grpc.NewServer(opts...)
			collector := &dialoutTLSCollector{received: make(chan *gpb.SubscribeResponse, 1)}
			spb.RegisterGNMIDialOutServer(server, collector)
			serveDone := make(chan error, 1)
			go func() { serveDone <- server.Serve(listener) }()
			defer func() {
				server.Stop()
				if err := <-serveDone; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
					t.Errorf("Serve: %v", err)
				}
			}()

			roots := ca.pool
			if tt.untrusted {
				roots = x509.NewCertPool()
			}
			clientCfg = &ClientConfig{
				RetryInterval: time.Second,
				TLS: &tls.Config{
					MinVersion:         tls.VersionTLS13,
					RootCAs:            roots,
					ServerName:         tt.serverName,
					InsecureSkipVerify: tt.insecure,
				},
			}
			if tt.nilTLS {
				clientCfg.TLS = nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := newClient(ctx, Destination{Addrs: listener.Addr().String()})
			if client != nil {
				defer client.Close()
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("newClient = %v, want error %v", err, tt.wantErr)
			}
			if !tt.plaintext && !tt.nilTLS && handshakes.Load() == 0 {
				t.Fatal("test did not exercise a TLS handshake")
			}
			if tt.wantErr {
				select {
				case message := <-collector.received:
					t.Fatalf("unverified collector received telemetry: %v", message)
				default:
				}
				return
			}

			stream, err := client.client.Publish(ctx)
			if err != nil {
				t.Fatal(err)
			}
			message := &gpb.SubscribeResponse{
				Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true},
			}
			if err := stream.Send(message); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-collector.received:
				if !proto.Equal(got, message) {
					t.Fatalf("collector received %v, want %v", got, message)
				}
			case <-ctx.Done():
				t.Fatal("collector did not receive telemetry")
			}
		})
	}
}
