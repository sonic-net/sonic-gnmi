//go:build e2e

package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

type CertificateAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func NewCertificateAuthority(t *testing.T) *CertificateAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(t, err)
	cert := &x509.Certificate{SerialNumber: serial(t), Subject: pkix.Name{CommonName: "rpc-test-ca"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	check(t, err)
	cert, err = x509.ParseCertificate(der)
	check(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &CertificateAuthority{cert: cert, key: key, pool: pool}
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	check(t, err)
	return n
}

func (a *CertificateAuthority) issue(t *testing.T, name string, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(t, err)
	cert := &x509.Certificate{SerialNumber: serial(t), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	if usage == x509.ExtKeyUsageServerAuth {
		cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, a.cert, &key.PublicKey, a.key)
	check(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (a *CertificateAuthority) ServerTLS(t *testing.T) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{a.issue(t, "server", x509.ExtKeyUsageServerAuth)},
		ClientCAs: a.pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
}

func (a *CertificateAuthority) ClientTLS(t *testing.T, name string) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{a.issue(t, name, x509.ExtKeyUsageClientAuth)},
		RootCAs: a.pool, MinVersion: tls.VersionTLS12}
}
