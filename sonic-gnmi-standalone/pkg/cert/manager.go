package cert

import (
	"context"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
)

// CertificateManager manages TLS certificates with monitoring and automatic reloading.
type CertificateManager interface {
	// LoadCertificates loads certificates from the configured source
	LoadCertificates() error

	// GetTLSConfig returns the current TLS configuration
	GetTLSConfig() (*tls.Config, error)

	// GetServerCertificate returns the current server certificate
	GetServerCertificate() *tls.Certificate

	// GetCACertPool returns the current CA certificate pool
	GetCACertPool() *x509.CertPool

	// StartMonitoring begins certificate file monitoring for automatic reloading
	StartMonitoring() error

	// StopMonitoring stops certificate file monitoring
	StopMonitoring()

	// Reload manually reloads certificates
	Reload() error

	// IsHealthy returns true if certificates are loaded and valid
	IsHealthy() bool
}

// CertManager implements CertificateManager with file system monitoring.
type CertManager struct {
	config        *CertConfig
	serverCert    atomic.Value // stores *tls.Certificate
	caCertPool    atomic.Value // stores *x509.CertPool
	tlsConfig     atomic.Value // stores *tls.Config
	clientAuthMgr *ClientAuthManager

	// Monitoring
	watcher      *fsnotify.Watcher
	stopChan     chan struct{}
	reloadChan   chan struct{}
	certLoaded   int32 // atomic boolean for certificate status
	mu           sync.RWMutex
	isMonitoring bool
}

// NewCertificateManager creates a new certificate manager with the given configuration.
func NewCertificateManager(config *CertConfig) CertificateManager {
	if config == nil {
		config = NewDefaultConfig()
	}

	cm := &CertManager{
		config:     config,
		stopChan:   make(chan struct{}),
		reloadChan: make(chan struct{}, 1),
	}

	// Initialize client auth manager only when using SONiC ConfigDB mode
	// For file-based certificates, client auth manager is optional
	if config.UseSONiCConfig {
		cm.clientAuthMgr = NewClientAuthManager(
			config.RedisAddr,
			config.RedisDB,
			config.ConfigTableName,
		)
	}

	return cm
}

// LoadCertificates loads certificates based on the configuration.
func (cm *CertManager) LoadCertificates() error {
	if err := cm.config.Validate(); err != nil {
		return fmt.Errorf("invalid certificate configuration: %w", err)
	}

	var certErr error
	if cm.config.UseSONiCConfig {
		// Create context with configurable timeout for SONiC config loading
		timeout := cm.config.SONiCConfigTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second // fallback default
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		certErr = cm.loadFromSONiCConfig(ctx)
	} else {
		certErr = cm.loadFromFiles()
	}

	if certErr != nil {
		return certErr
	}

	// Load client authorization config if available
	if cm.clientAuthMgr != nil {
		if err := cm.clientAuthMgr.LoadClientCertConfig(); err != nil {
			glog.V(1).Infof("Failed to load client cert config: %v", err)
			// This is not fatal - continue without client CN authorization
		}
	}

	return nil
}

// GetTLSConfig returns the current TLS configuration.
func (cm *CertManager) GetTLSConfig() (*tls.Config, error) {
	if config := cm.tlsConfig.Load(); config != nil {
		return config.(*tls.Config), nil
	}
	return nil, fmt.Errorf("TLS configuration not loaded")
}

// GetServerCertificate returns the current server certificate.
func (cm *CertManager) GetServerCertificate() *tls.Certificate {
	if cert := cm.serverCert.Load(); cert != nil {
		return cert.(*tls.Certificate)
	}
	return nil
}

// GetCACertPool returns the current CA certificate pool.
func (cm *CertManager) GetCACertPool() *x509.CertPool {
	if pool := cm.caCertPool.Load(); pool != nil {
		return pool.(*x509.CertPool)
	}
	return nil
}

// StartMonitoring begins certificate file monitoring for automatic reloading.
func (cm *CertManager) StartMonitoring() error {
	if !cm.config.EnableMonitoring {
		glog.V(1).Info("Certificate monitoring disabled")
		return nil
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.isMonitoring {
		return fmt.Errorf("monitoring already started")
	}

	var err error
	cm.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create certificate watcher: %w", err)
	}

	// Watch certificate directory
	certDir := filepath.Dir(cm.config.CertFile)
	if err := cm.watcher.Add(certDir); err != nil {
		cm.watcher.Close()
		return fmt.Errorf("failed to watch certificate directory %s: %w", certDir, err)
	}

	cm.isMonitoring = true
	go cm.monitorCertificates()

	glog.V(1).Infof("Started certificate monitoring on directory: %s", certDir)
	return nil
}

// StopMonitoring stops certificate file monitoring.
func (cm *CertManager) StopMonitoring() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if !cm.isMonitoring {
		return
	}

	close(cm.stopChan)
	if cm.watcher != nil {
		cm.watcher.Close()
	}

	cm.isMonitoring = false
	glog.V(1).Info("Stopped certificate monitoring")
}

// Reload manually reloads certificates.
func (cm *CertManager) Reload() error {
	glog.V(1).Info("Reloading certificates...")

	if err := cm.LoadCertificates(); err != nil {
		glog.Errorf("Certificate reload failed: %v", err)
		return err
	}

	glog.V(1).Info("Certificates reloaded successfully")
	return nil
}

// IsHealthy returns true if certificates are loaded and valid.
func (cm *CertManager) IsHealthy() bool {
	return atomic.LoadInt32(&cm.certLoaded) == 1 && cm.GetServerCertificate() != nil
}

// loadFromFiles loads certificates from file system.
func (cm *CertManager) loadFromFiles() error {
	glog.V(1).Infof("Loading certificates from files: cert=%s, key=%s, ca=%s",
		cm.config.CertFile, cm.config.KeyFile, cm.config.CAFile)

	// Load server certificate and key
	cert, err := tls.LoadX509KeyPair(cm.config.CertFile, cm.config.KeyFile)
	if err != nil {
		// Compute checksums for debugging
		cm.computeAndLogChecksum(cm.config.CertFile)
		cm.computeAndLogChecksum(cm.config.KeyFile)
		return fmt.Errorf("failed to load server certificate: %w", err)
	}

	cm.serverCert.Store(&cert)
	atomic.StoreInt32(&cm.certLoaded, 1)

	// Load CA certificate if client certificates are required
	if cm.config.RequireClientCert && cm.config.CAFile != "" {
		caCertPool, err := cm.loadCACertificate()
		if err != nil {
			return fmt.Errorf("failed to load CA certificate: %w", err)
		}
		cm.caCertPool.Store(caCertPool)
	}

	// Generate TLS configuration
	tlsConfig, err := cm.generateTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}
	cm.tlsConfig.Store(tlsConfig)

	glog.V(1).Info("Certificates loaded successfully")
	return nil
}

// loadCACertificate loads and validates the CA certificate.
func (cm *CertManager) loadCACertificate() (*x509.CertPool, error) {
	caCert, err := ioutil.ReadFile(cm.config.CAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	glog.V(2).Infof("Loaded CA certificate from %s", cm.config.CAFile)
	return caCertPool, nil
}

// generateTLSConfig creates a TLS configuration with production security settings.
func (cm *CertManager) generateTLSConfig() (*tls.Config, error) {
	cert := cm.GetServerCertificate()
	if cert == nil {
		return nil, fmt.Errorf("server certificate not loaded")
	}

	config := &tls.Config{
		Certificates:             []tls.Certificate{*cert},
		ClientAuth:               cm.config.GetClientAuthMode(),
		MinVersion:               cm.config.MinTLSVersion,
		CurvePreferences:         cm.config.CurvePreferences,
		PreferServerCipherSuites: true,
		CipherSuites:             cm.config.CipherSuites,
	}

	// Set CA certificate pool if client certificates are required
	if caCertPool := cm.GetCACertPool(); caCertPool != nil {
		config.ClientCAs = caCertPool
	}

	// Add custom client certificate verification if client auth manager is available
	if cm.clientAuthMgr != nil && cm.config.RequireClientCert {
		config.VerifyPeerCertificate = cm.clientAuthMgr.VerifyClientCertificate
	}

	glog.V(2).Infof("Generated TLS config: MinTLS=%x, ClientAuth=%v, CipherSuites=%d",
		config.MinVersion, config.ClientAuth, len(config.CipherSuites))

	return config, nil
}

// monitorCertificates monitors certificate files for changes.
func (cm *CertManager) monitorCertificates() {
	glog.V(2).Info("Certificate monitoring goroutine started")

	for {
		select {
		case <-cm.stopChan:
			glog.V(2).Info("Certificate monitoring stopped")
			return

		case <-cm.reloadChan:
			if err := cm.Reload(); err != nil {
				glog.Errorf("Failed to reload certificates: %v", err)
			}

		case event, ok := <-cm.watcher.Events:
			if !ok {
				glog.Warning("Certificate watcher events channel closed")
				return
			}

			if cm.isCertificateFile(event.Name) {
				glog.V(1).Infof("Certificate file event: %v", event)

				if event.Op&fsnotify.Write == fsnotify.Write ||
					event.Op&fsnotify.Create == fsnotify.Create {
					// Certificate file was modified - trigger reload
					select {
					case cm.reloadChan <- struct{}{}:
					default:
						// Reload already pending
					}
				}

				if event.Op&fsnotify.Remove == fsnotify.Remove ||
					event.Op&fsnotify.Rename == fsnotify.Rename {
					glog.Warning("Certificate file was removed or renamed")
					atomic.StoreInt32(&cm.certLoaded, 0)
				}
			}

		case err, ok := <-cm.watcher.Errors:
			if !ok {
				return
			}
			glog.Errorf("Certificate watcher error: %v", err)
		}
	}
}

// isCertificateFile checks if the file is a certificate-related file.
func (cm *CertManager) isCertificateFile(filename string) bool {
	ext := filepath.Ext(filename)
	return ext == ".crt" || ext == ".cert" || ext == ".pem" || ext == ".key" || ext == ".cer"
}

// computeAndLogChecksum computes and logs SHA512 checksum for debugging.
func (cm *CertManager) computeAndLogChecksum(filepath string) {
	if !cm.config.ChecksumValidation {
		return
	}

	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		glog.V(2).Infof("Could not read file for checksum %s: %v", filepath, err)
		return
	}

	hash := sha512.Sum512(data)
	checksum := hex.EncodeToString(hash[:])
	glog.V(2).Infof("SHA512 checksum for %s: %s", filepath, checksum)
}
