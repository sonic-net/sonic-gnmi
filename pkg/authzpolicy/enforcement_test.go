package authzpolicy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestConfigDBPolicyEnforcesUnaryAndStreamingRPCs(t *testing.T) {
	ca, serverCert, allowedClientCert, deniedClientCert := testCertificates(t, "client.example.com")
	source := NewConfigDBSource(staticTableReader{tables: map[string]map[string]map[string]string{
		PrincipalTable: {
			"client.example.com": {"roles@": "reader"},
		},
		RuleTable: {
			"check": {"roles@": "reader", "rpc": "/grpc.health.v1.Health/Check", "effect": "allow"},
			"watch": {"roles@": "reader", "rpc": "/grpc.health.v1.Health/Watch", "effect": "allow"},
		},
	}})
	interceptor, err := source.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    ca,
			MinVersion:   tls.VersionTLS13,
		})),
		grpc.UnaryInterceptor(interceptor.UnaryInterceptor()),
		grpc.StreamInterceptor(interceptor.StreamInterceptor()),
	)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	go server.Serve(listener)
	t.Cleanup(server.Stop)

	client := newHealthClient(t, listener.Addr().String(), ca, allowedClientCert)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("allowed unary RPC failed: %v", err)
	}
	watch, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("allowed streaming RPC failed: %v", err)
	}
	if _, err := watch.Recv(); err != nil {
		t.Fatalf("allowed streaming RPC receive failed: %v", err)
	}
	if _, err := client.List(ctx, &healthpb.HealthListRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unmatched RPC status = %v, want PermissionDenied", status.Code(err))
	}

	deniedClient := newHealthClient(t, listener.Addr().String(), ca, deniedClientCert)
	if _, err := deniedClient.Check(ctx, &healthpb.HealthCheckRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unknown principal status = %v, want PermissionDenied", status.Code(err))
	}
}

func testCertificates(t *testing.T, clientDNS string) (*x509.CertPool, tls.Certificate, tls.Certificate, tls.Certificate) {
	t.Helper()
	now := time.Now()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	newLeaf := func(serial int64, commonName, dns string, usage x509.ExtKeyUsage) tls.Certificate {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: commonName},
			DNSNames:     []string{dns},
			NotBefore:    now.Add(-time.Minute),
			NotAfter:     now.Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}
	}
	return pool,
		newLeaf(2, "server-cn", "server.example.com", x509.ExtKeyUsageServerAuth),
		newLeaf(3, "allowed-client-cn", clientDNS, x509.ExtKeyUsageClientAuth),
		newLeaf(4, "denied-client-cn", "other.example.com", x509.ExtKeyUsageClientAuth)
}

func newHealthClient(t *testing.T, address string, roots *x509.CertPool, certificate tls.Certificate) healthpb.HealthClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, address,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{certificate},
			RootCAs:      roots,
			ServerName:   "server.example.com",
			MinVersion:   tls.VersionTLS13,
		})),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("DialContext() failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return healthpb.NewHealthClient(conn)
}
