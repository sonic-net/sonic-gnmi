package cert

import (
	"crypto/tls"
	"fmt"
	"time"
)

// CertConfig holds configuration for certificate management and verification.
type CertConfig struct {
	// Certificate file paths
	CertFile string
	KeyFile  string
	CAFile   string

	// Client certificate requirements
	RequireClientCert bool // Require client certificates (default: true)

	// Security settings
	MinTLSVersion    uint16        // Minimum TLS version (default: TLS 1.2)
	CipherSuites     []uint16      // Allowed cipher suites
	CurvePreferences []tls.CurveID // Preferred elliptic curves

	// Certificate monitoring
	EnableMonitoring   bool          // Enable file system monitoring for cert changes
	ChecksumValidation bool          // Validate certificate checksums
	MonitoringTimeout  time.Duration // Timeout for certificate loading retries

	// SONiC integration
	UseSONiCConfig     bool          // Load configuration from SONiC ConfigDB via Redis
	RedisAddr          string        // Redis server address (default: "localhost:6379")
	RedisDB            int           // Redis database number for ConfigDB (default: 4)
	ConfigTableName    string        // ConfigDB table name for client certs (default: "GNMI_CLIENT_CERT")
	SONiCConfigTimeout time.Duration // Timeout for SONiC config loading (default: 30s)
}

// NewDefaultConfig returns a CertConfig with production-ready defaults.
func NewDefaultConfig() *CertConfig {
	return &CertConfig{
		// Default paths - match production telemetry location
		CertFile: "/etc/sonic/telemetry/gnmiserver.crt",
		KeyFile:  "/etc/sonic/telemetry/gnmiserver.key",
		CAFile:   "/etc/sonic/telemetry/gnmiCA.pem",

		// Security defaults - match telemetry server
		RequireClientCert: true,
		MinTLSVersion:     tls.VersionTLS12,

		// Production cipher suites (from telemetry server)
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},

		// Preferred curves (from telemetry server)
		CurvePreferences: []tls.CurveID{
			tls.CurveP521,
			tls.CurveP384,
			tls.CurveP256,
		},

		// Monitoring defaults
		EnableMonitoring:   true,
		ChecksumValidation: true,
		MonitoringTimeout:  30 * time.Second,

		// SONiC defaults
		RedisAddr:          "localhost:6379",
		RedisDB:            4, // ConfigDB is database 4 in SONiC
		ConfigTableName:    "GNMI_CLIENT_CERT",
		SONiCConfigTimeout: 30 * time.Second,
	}
}

// Validate checks the certificate configuration for consistency and completeness.
func (c *CertConfig) Validate() error {
	if c.UseSONiCConfig {
		// SONiC config mode - no file paths needed
		return nil
	}

	// File-based mode - validate file paths
	if c.CertFile == "" {
		return fmt.Errorf("CertFile must be specified")
	}
	if c.KeyFile == "" {
		return fmt.Errorf("KeyFile must be specified")
	}
	if c.RequireClientCert && c.CAFile == "" {
		return fmt.Errorf("CAFile must be specified when RequireClientCert is true")
	}

	return nil
}

// GetClientAuthMode returns the appropriate tls.ClientAuthType based on configuration.
func (c *CertConfig) GetClientAuthMode() tls.ClientAuthType {
	if c.RequireClientCert {
		return tls.RequireAndVerifyClientCert // Required client certificates
	}
	return tls.NoClientCert // No client certificates
}

// String returns a string representation of the configuration for logging.
func (c *CertConfig) String() string {
	if c.UseSONiCConfig {
		return fmt.Sprintf("CertConfig{SONiC: true, Redis: %s, DB: %d, ClientAuth: %v}",
			c.RedisAddr, c.RedisDB, c.GetClientAuthMode())
	}
	return fmt.Sprintf("CertConfig{Cert: %s, Key: %s, CA: %s, ClientAuth: %v}",
		c.CertFile, c.KeyFile, c.CAFile, c.GetClientAuthMode())
}
