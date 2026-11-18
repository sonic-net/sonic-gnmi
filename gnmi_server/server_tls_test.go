package gnmi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTLSTestCert writes a self-signed certificate named cn and its key to
// dir/name.crt and dir/name.key.
func writeTLSTestCert(t *testing.T, dir, name, cn string) (certPath, keyPath string) {
	t.Helper()
	certPath = filepath.Join(dir, name+".crt")
	keyPath = filepath.Join(dir, name+".key")
	writeTLSTestCertAt(t, certPath, keyPath, cn)
	return certPath, keyPath
}

// writeTLSTestCertAt writes a self-signed certificate named cn to certPath and
// its key to keyPath.
func writeTLSTestCertAt(t *testing.T, certPath, keyPath, cn string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
}

func newTLSTestConfig(t *testing.T, dir string) *Config {
	t.Helper()
	cert, key := writeTLSTestCert(t, dir, "server", "configured")
	ca, _ := writeTLSTestCert(t, dir, "ca", "configured-ca")
	return &Config{
		SrvCertFile: cert,
		SrvKeyFile:  key,
		CaCertFile:  ca,
		SrvCertLnk:  filepath.Join(dir, "server_cert.lnk"),
		SrvKeyLnk:   filepath.Join(dir, "server_key.lnk"),
		CaCertLnk:   filepath.Join(dir, "ca_cert.lnk"),
	}
}

func testBaseTLSConfig() *tls.Config {
	return &tls.Config{
		ClientAuth:             tls.RequireAndVerifyClientCert,
		MinVersion:             tls.VersionTLS12,
		SessionTicketsDisabled: true,
		CurvePreferences:       []tls.CurveID{tls.CurveP384},
		CipherSuites:           []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384},
	}
}

// servedTLS returns the TLS configuration a new connection is served.
func servedTLS(t *testing.T, tlsCfg *tls.Config) *tls.Config {
	t.Helper()
	if tlsCfg.GetConfigForClient == nil {
		t.Fatal("GetConfigForClient is not set")
	}
	served, err := tlsCfg.GetConfigForClient(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	return served
}

func servedCN(t *testing.T, tlsCfg *tls.Config) string {
	t.Helper()
	served := servedTLS(t, tlsCfg)
	leaf, err := x509.ParseCertificate(served.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.Subject.CommonName
}

func caPoolOf(t *testing.T, path string) *x509.CertPool {
	t.Helper()
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemBytes)
	return pool
}

func TestRotatingTLSConfigServesRotation(t *testing.T) {
	dir := t.TempDir()
	cfg := newTLSTestConfig(t, dir)
	base := testBaseTLSConfig()

	tlsCfg, err := RotatingTLSConfig(cfg, base)
	if err != nil {
		t.Fatalf("RotatingTLSConfig: %v", err)
	}
	if got := servedCN(t, tlsCfg); got != "configured" {
		t.Errorf("served %q before rotation, want %q", got, "configured")
	}
	served := servedTLS(t, tlsCfg)
	if served.ClientAuth != base.ClientAuth || served.MinVersion != base.MinVersion ||
		!served.SessionTicketsDisabled || len(served.CipherSuites) != 1 || len(served.CurvePreferences) != 1 {
		t.Errorf("served TLS settings differ from the base configuration: %+v", served)
	}
	if !served.ClientCAs.Equal(caPoolOf(t, cfg.CaCertFile)) {
		t.Error("served client CA is not the configured CA")
	}

	// Rotate the way gNSI Certz does: write new files and repoint the links.
	newCert, newKey := writeTLSTestCert(t, dir, "gnxi_v2_1_cert", "rotated")
	newCA, _ := writeTLSTestCert(t, dir, "ca_gnxi_v2_1_bundle", "rotated-ca")
	muPath.Lock()
	if err := atomicSetSrvCertKeyPair(cfg, newCert, newKey); err != nil {
		t.Fatal(err)
	}
	if err := atomicSetCACert(cfg, newCA); err != nil {
		t.Fatal(err)
	}
	muPath.Unlock()

	if got := servedCN(t, tlsCfg); got != "rotated" {
		t.Errorf("served %q after rotation, want %q", got, "rotated")
	}
	if !servedTLS(t, tlsCfg).ClientCAs.Equal(caPoolOf(t, newCA)) {
		t.Error("served client CA is not the rotated CA")
	}
}

func TestRotatingTLSConfigHandshake(t *testing.T) {
	dir := t.TempDir()
	cfg := newTLSTestConfig(t, dir)
	cfg.CaCertFile = ""
	base := testBaseTLSConfig()
	base.ClientAuth = tls.NoClientCert

	tlsCfg, err := RotatingTLSConfig(cfg, base)
	if err != nil {
		t.Fatalf("RotatingTLSConfig: %v", err)
	}
	lis, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			conn.(*tls.Conn).Handshake()
			conn.Close()
		}
	}()
	dialCN := func() string {
		conn, err := tls.Dial("tcp", lis.Addr().String(), &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		return conn.ConnectionState().PeerCertificates[0].Subject.CommonName
	}

	if got := dialCN(); got != "configured" {
		t.Errorf("new connection served %q before rotation, want %q", got, "configured")
	}
	newCert, newKey := writeTLSTestCert(t, dir, "gnxi_v2_1_cert", "rotated")
	muPath.Lock()
	err = atomicSetSrvCertKeyPair(cfg, newCert, newKey)
	muPath.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := dialCN(); got != "rotated" {
		t.Errorf("new connection served %q after rotation, want %q", got, "rotated")
	}
}

func TestRotatingTLSConfigKeepsRotationAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := newTLSTestConfig(t, dir)
	rotatedCert := filepath.Join(dir, "gnxi_v2_1795027142000000000_cert.pem")
	rotatedKey := filepath.Join(dir, "gnxi_v2_1795027142000000000_key.pem")
	writeTLSTestCertAt(t, rotatedCert, rotatedKey, "rotated")
	if err := os.Symlink(rotatedCert, cfg.SrvCertLnk); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rotatedKey, cfg.SrvKeyLnk); err != nil {
		t.Fatal(err)
	}

	tlsCfg, err := RotatingTLSConfig(cfg, testBaseTLSConfig())
	if err != nil {
		t.Fatalf("RotatingTLSConfig: %v", err)
	}
	if got := servedCN(t, tlsCfg); got != "rotated" {
		t.Errorf("served %q after restart, want the rotated certificate", got)
	}
}

func TestRotatingTLSConfigRelinksChangedConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := newTLSTestConfig(t, dir)
	oldCert, oldKey := writeTLSTestCert(t, dir, "old_server", "old-config")
	if err := os.Symlink(oldCert, cfg.SrvCertLnk); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldKey, cfg.SrvKeyLnk); err != nil {
		t.Fatal(err)
	}

	tlsCfg, err := RotatingTLSConfig(cfg, testBaseTLSConfig())
	if err != nil {
		t.Fatalf("RotatingTLSConfig: %v", err)
	}
	if got := servedCN(t, tlsCfg); got != "configured" {
		t.Errorf("served %q, want the configured certificate %q", got, "configured")
	}
}

func TestRotatingTLSConfigReadOnlyLinkDir(t *testing.T) {
	dir := t.TempDir()
	cfg := newTLSTestConfig(t, dir)
	missing := filepath.Join(dir, "missing")
	cfg.SrvCertLnk = filepath.Join(missing, "server_cert.lnk")
	cfg.SrvKeyLnk = filepath.Join(missing, "server_key.lnk")
	cfg.CaCertLnk = filepath.Join(missing, "ca_cert.lnk")

	tlsCfg, err := RotatingTLSConfig(cfg, testBaseTLSConfig())
	if err != nil {
		t.Fatalf("RotatingTLSConfig: %v", err)
	}
	if got := servedCN(t, tlsCfg); got != "configured" {
		t.Errorf("served %q, want %q", got, "configured")
	}

	// The configured files are still read on every handshake.
	writeTLSTestCert(t, dir, "server", "replaced")
	if got := servedCN(t, tlsCfg); got != "replaced" {
		t.Errorf("served %q after replacing the configured certificate, want %q", got, "replaced")
	}
}

func TestRotatingTLSConfigErrors(t *testing.T) {
	t.Run("no configured certificate", func(t *testing.T) {
		cfg := newTLSTestConfig(t, t.TempDir())
		cfg.SrvCertFile = ""
		if _, err := RotatingTLSConfig(cfg, testBaseTLSConfig()); err == nil {
			t.Error("RotatingTLSConfig succeeded without a server certificate")
		}
	})
	t.Run("unreadable certificate", func(t *testing.T) {
		cfg := newTLSTestConfig(t, t.TempDir())
		if err := os.WriteFile(cfg.SrvCertFile, []byte("not a certificate"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := RotatingTLSConfig(cfg, testBaseTLSConfig()); err == nil {
			t.Error("RotatingTLSConfig succeeded with an invalid server certificate")
		}
	})
	t.Run("invalid CA", func(t *testing.T) {
		cfg := newTLSTestConfig(t, t.TempDir())
		if err := os.WriteFile(cfg.CaCertFile, []byte("not a certificate"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := RotatingTLSConfig(cfg, testBaseTLSConfig()); err == nil {
			t.Error("RotatingTLSConfig succeeded with an invalid CA certificate")
		}
	})
}

func TestIsCertzRotation(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "server_cert.lnk")
	for _, tc := range []struct {
		target string
		create bool
		want   bool
	}{
		{"gnxi_v1_1795027142000000000_cert.pem", true, true},
		{"gnxi_production-1794504592_1794504615522558083_key.pem", true, true},
		{"ca_gnxi_v1_1795027142000000000_bundle.pem", true, true},
		{"gnxi_v1_1795027142000000000_cert.pem", false, false},
		{"server_crt.cer", true, false},
		{"server_cert.pem", true, false},
	} {
		target := filepath.Join(dir, tc.target)
		os.Remove(target)
		if tc.create {
			if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if got := isCertzRotation(link); got != tc.want {
			t.Errorf("isCertzRotation(-> %s, exists=%v) = %v, want %v", tc.target, tc.create, got, tc.want)
		}
	}
	if isCertzRotation(filepath.Join(dir, "no_such.lnk")) {
		t.Error("isCertzRotation reported a missing link as a rotation")
	}
}
