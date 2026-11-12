package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"flag"
	"math/big"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

type testCertificateAuthority struct {
	certificate *x509.Certificate
	privateKey  *ecdsa.PrivateKey
	pool        *x509.CertPool
}

func randomSerialNumber(t *testing.T) *big.Int {
	t.Helper()

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("failed to generate certificate serial number: %v", err)
	}
	return serialNumber
}

func newTestCertificateAuthority(t *testing.T, commonName string) *testCertificateAuthority {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate CA private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          randomSerialNumber(t),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("failed to parse CA certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)

	return &testCertificateAuthority{
		certificate: certificate,
		privateKey:  privateKey,
		pool:        pool,
	}
}

func newTestSignedCertificate(t *testing.T, authority *testCertificateAuthority, commonName string, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: randomSerialNumber(t),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	if usage == x509.ExtKeyUsageServerAuth {
		template.DNSNames = []string{commonName}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, &privateKey.PublicKey, authority.privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}
}

type serverHandshakeResult struct {
	state tls.ConnectionState
	err   error
}

func runTLSHandshake(t *testing.T, serverConfig, clientConfig *tls.Config) (tls.ConnectionState, error, error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create TLS test listener: %v", err)
	}
	defer listener.Close()

	serverResult := make(chan serverHandshakeResult, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverResult <- serverHandshakeResult{err: err}
			return
		}
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(5 * time.Second))
		server := tls.Server(connection, serverConfig)
		err = server.Handshake()
		serverResult <- serverHandshakeResult{state: server.ConnectionState(), err: err}
	}()

	clientConnection, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to TLS test listener: %v", err)
	}
	defer clientConnection.Close()
	clientConnection.SetDeadline(time.Now().Add(5 * time.Second))
	client := tls.Client(clientConnection, clientConfig)
	clientErr := client.Handshake()
	result := <-serverResult

	return result.state, result.err, clientErr
}

func TestOptionalClientCertificatePolicy(t *testing.T) {
	tests := []struct {
		name              string
		allowNoClientCert bool
		want              tls.ClientAuthType
	}{
		{
			name:              "client certificate required",
			allowNoClientCert: false,
			want:              tls.RequireAndVerifyClientCert,
		},
		{
			name:              "client certificate optional and verified",
			allowNoClientCert: true,
			want:              tls.VerifyClientCertIfGiven,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := tlsClientAuthPolicy(test.allowNoClientCert); got != test.want {
				t.Fatalf("tlsClientAuthPolicy(%t) = %v, want %v", test.allowNoClientCert, got, test.want)
			}
		})
	}
}

func TestOptionalClientCertificateHandshake(t *testing.T) {
	trustedCA := newTestCertificateAuthority(t, "trusted-test-ca")
	untrustedCA := newTestCertificateAuthority(t, "untrusted-test-ca")
	serverCertificate := newTestSignedCertificate(t, trustedCA, "test-server", x509.ExtKeyUsageServerAuth)
	trustedClientCertificate := newTestSignedCertificate(t, trustedCA, "trusted-client", x509.ExtKeyUsageClientAuth)
	untrustedClientCertificate := newTestSignedCertificate(t, untrustedCA, "untrusted-client", x509.ExtKeyUsageClientAuth)

	tests := []struct {
		name                 string
		certificate          *tls.Certificate
		wantHandshakeSuccess bool
		wantVerifiedChains   bool
	}{
		{
			name:                 "certificate omitted",
			wantHandshakeSuccess: true,
			wantVerifiedChains:   false,
		},
		{
			name:                 "trusted certificate",
			certificate:          &trustedClientCertificate,
			wantHandshakeSuccess: true,
			wantVerifiedChains:   true,
		},
		{
			name:                 "untrusted certificate",
			certificate:          &untrustedClientCertificate,
			wantHandshakeSuccess: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverConfig := &tls.Config{
				Certificates: []tls.Certificate{serverCertificate},
				ClientAuth:   tlsClientAuthPolicy(true),
				ClientCAs:    trustedCA.pool,
				MinVersion:   tls.VersionTLS12,
				MaxVersion:   tls.VersionTLS12,
			}
			clientConfig := &tls.Config{
				RootCAs:    trustedCA.pool,
				ServerName: "test-server",
				MinVersion: tls.VersionTLS12,
				MaxVersion: tls.VersionTLS12,
			}
			if test.certificate != nil {
				clientConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
					return test.certificate, nil
				}
			}

			state, serverErr, clientErr := runTLSHandshake(t, serverConfig, clientConfig)
			if test.wantHandshakeSuccess {
				if serverErr != nil || clientErr != nil {
					t.Fatalf("TLS handshake failed: server error = %v, client error = %v", serverErr, clientErr)
				}
				if got := len(state.VerifiedChains) > 0; got != test.wantVerifiedChains {
					t.Fatalf("verified client certificate chains present = %t, want %t", got, test.wantVerifiedChains)
				}
				return
			}
			if serverErr == nil {
				t.Fatalf("server accepted an untrusted client certificate; client error = %v", clientErr)
			}
		})
	}
}

func TestOptionalClientCertificateApplicationAuthModes(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })

	tests := []struct {
		name     string
		authMode string
	}{
		{name: "no application authentication", authMode: "none"},
		{name: "password authentication", authMode: "password"},
		{name: "JWT authentication", authMode: "jwt"},
		{name: "certificate or password authentication", authMode: "cert,password"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			os.Args = []string{
				"cmd", "-port", "8080", "-insecure", "-allow_no_client_auth",
				"-client_auth", test.authMode, "-ca_crt", "test-ca.pem",
			}

			telemetryCfg, _, err := setupFlags(fs)
			if err != nil {
				t.Fatalf("setupFlags() error = %v", err)
			}

			if test.authMode == "none" {
				if telemetryCfg.UserAuth.Any() {
					t.Fatalf("UserAuth = %v, want no application authentication", telemetryCfg.UserAuth)
				}
				return
			}

			for _, mode := range strings.Split(test.authMode, ",") {
				if !telemetryCfg.UserAuth.Enabled(mode) {
					t.Fatalf("UserAuth = %v, want %q enabled", telemetryCfg.UserAuth, mode)
				}
			}
		})
	}
}
