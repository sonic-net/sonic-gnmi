package gnmi

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	log "github.com/golang/glog"
)

// certzArtifact matches the file names gNSI Certz gives rotated credentials:
// "<profile>_<version>_<nanoseconds>_cert.pem", "..._key.pem" and
// "ca_<profile>_<version>_<nanoseconds>_bundle.pem".
var certzArtifact = regexp.MustCompile(`_[0-9]+_(cert|key|bundle)\.pem$`)

// tlsPaths names the files a TLS handshake loads its credentials from.
type tlsPaths struct {
	cert, key, ca string
}

// RotatingTLSConfig returns a copy of base that loads the server certificate,
// the private key and, if cfg.CaCertFile is set, the client CA bundle on every
// TLS handshake. base supplies every other TLS setting.
//
// The credentials are read through cfg's symlinks, which gNSI Certz repoints
// when it rotates them, so a rotation applies to the next connection without
// restarting the server. If the symlinks cannot be created, for example
// because the certificate directory is read-only, the configured files are
// read instead and Certz rotations are not served.
func RotatingTLSConfig(cfg *Config, base *tls.Config) (*tls.Config, error) {
	paths := tlsPaths{cert: cfg.SrvCertLnk, key: cfg.SrvKeyLnk, ca: cfg.CaCertLnk}
	if err := linkConfiguredCerts(cfg); err != nil {
		log.Warningf("Serving the configured certificates, gNSI Certz rotations will not take effect: %v", err)
		paths = tlsPaths{cert: cfg.SrvCertFile, key: cfg.SrvKeyFile, ca: cfg.CaCertFile}
	}
	// Report unusable credentials now rather than on the first handshake.
	if _, err := tlsConfigFrom(paths, cfg.CaCertFile != "", base); err != nil {
		return nil, err
	}
	tlsCfg := base.Clone()
	tlsCfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return tlsConfigFrom(paths, cfg.CaCertFile != "", base)
	}
	return tlsCfg, nil
}

// linkConfiguredCerts points the symlinks at the configured certificate, key
// and CA, except for links that point at credentials installed by a gNSI Certz
// rotation, which are kept so the rotation survives a restart.
func linkConfiguredCerts(cfg *Config) error {
	muPath.Lock()
	defer muPath.Unlock()

	if cfg.SrvCertFile == "" || cfg.SrvKeyFile == "" {
		return fmt.Errorf("server certificate or key file path is empty")
	}
	if !isCertzRotation(cfg.SrvCertLnk) || !isCertzRotation(cfg.SrvKeyLnk) {
		if !linksTo(cfg.SrvCertLnk, cfg.SrvCertFile) || !linksTo(cfg.SrvKeyLnk, cfg.SrvKeyFile) {
			if err := atomicSetSrvCertKeyPair(cfg, cfg.SrvCertFile, cfg.SrvKeyFile); err != nil {
				return fmt.Errorf("could not link the server certificate: %v", err)
			}
		}
	}
	if cfg.CaCertFile != "" && !isCertzRotation(cfg.CaCertLnk) && !linksTo(cfg.CaCertLnk, cfg.CaCertFile) {
		if err := atomicSetCACert(cfg, cfg.CaCertFile); err != nil {
			return fmt.Errorf("could not link the CA certificate: %v", err)
		}
	}
	return nil
}

// isCertzRotation reports whether link resolves to an existing file installed
// by a gNSI Certz rotation.
func isCertzRotation(link string) bool {
	// NOTE: muPath has to be writer-locked when entering this function.
	target, err := os.Readlink(link)
	if err != nil || !certzArtifact.MatchString(filepath.Base(target)) {
		return false
	}
	return isSymlinkValid(link)
}

// linksTo reports whether link is a symlink to file.
func linksTo(link, file string) bool {
	target, err := os.Readlink(link)
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(file)
	return err == nil && target == abs
}

// tlsConfigFrom returns a copy of base serving the credentials in paths.
func tlsConfigFrom(paths tlsPaths, verifyClients bool, base *tls.Config) (*tls.Config, error) {
	muPath.RLock()
	defer muPath.RUnlock()

	cert, err := tls.LoadX509KeyPair(paths.cert, paths.key)
	if err != nil {
		return nil, fmt.Errorf("could not load server key pair: %v", err)
	}
	tlsCfg := base.Clone()
	tlsCfg.Certificates = []tls.Certificate{cert}
	tlsCfg.GetConfigForClient = nil
	if verifyClients {
		ca, err := os.ReadFile(paths.ca)
		if err != nil {
			return nil, fmt.Errorf("could not read CA certificate: %v", err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("failed to append CA certificate %s", paths.ca)
		}
		tlsCfg.ClientCAs = certPool
	}
	return tlsCfg, nil
}
