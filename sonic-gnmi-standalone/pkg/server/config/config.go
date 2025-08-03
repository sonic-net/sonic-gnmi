package config

import (
	"flag"
	"time"
)

// Config holds global configuration for the upgrade service.
type Config struct {
	Addr            string
	RootFS          string
	ShutdownTimeout time.Duration
	TLSCertFile     string
	TLSKeyFile      string
	TLSCACertFile   string
	TLSEnabled      bool
	MTLSEnabled     bool
}

var Global *Config

// Initialize defines flags and sets up the global configuration.
func Initialize() {
	addr := flag.String("addr", ":50055", "The address to listen on")
	rootfs := flag.String("rootfs", "/mnt/host", "Root filesystem mount point (e.g., /mnt/host for containers)")
	shutdownTimeout := flag.Duration("shutdown-timeout", 10*time.Second, "Maximum time to wait for graceful shutdown")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file (optional)")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file (optional)")
	tlsCACert := flag.String("tls-ca-cert", "", "Path to TLS CA certificate file for client verification (optional)")
	noTLS := flag.Bool("no-tls", false, "Disable TLS (TLS is enabled by default)")
	enableMTLS := flag.Bool("mtls", false, "Enable mutual TLS (requires CA certificate)")

	flag.Parse()

	// TLS is enabled by default unless explicitly disabled via flag
	tlsEnabled := !*noTLS

	// mTLS requires TLS to be enabled
	mtlsEnabled := *enableMTLS && tlsEnabled

	// If TLS is enabled but no cert/key provided, use default paths
	certFile := *tlsCert
	keyFile := *tlsKey
	caCertFile := *tlsCACert

	if tlsEnabled && certFile == "" {
		certFile = "server.crt"
	}
	if tlsEnabled && keyFile == "" {
		keyFile = "server.key"
	}
	if mtlsEnabled && caCertFile == "" {
		caCertFile = "ca.crt"
	}

	Global = &Config{
		Addr:            *addr,
		RootFS:          *rootfs,
		ShutdownTimeout: *shutdownTimeout,
		TLSCertFile:     certFile,
		TLSKeyFile:      keyFile,
		TLSCACertFile:   caCertFile,
		TLSEnabled:      tlsEnabled,
		MTLSEnabled:     mtlsEnabled,
	}
}
