//go:build e2e

package rpc_test

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

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newAuthority(t *testing.T) *authority {
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
	return &authority{cert: cert, key: key, pool: pool}
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	check(t, err)
	return n
}

func (a *authority) issue(t *testing.T, name string, usage x509.ExtKeyUsage) tls.Certificate {
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
