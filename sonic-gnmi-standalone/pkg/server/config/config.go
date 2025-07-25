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
	TLSEnabled      bool
}

var Global *Config

// Initialize defines flags and sets up the global configuration.
func Initialize() {
	addr := flag.String("addr", ":50051", "The address to listen on")
	rootfs := flag.String("rootfs", "/mnt/host", "Root filesystem mount point (e.g., /mnt/host for containers)")
	shutdownTimeout := flag.Duration("shutdown-timeout", 10*time.Second, "Maximum time to wait for graceful shutdown")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file (optional)")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file (optional)")
	noTLS := flag.Bool("no-tls", false, "Disable TLS (TLS is enabled by default)")

	flag.Parse()

	// TLS is enabled by default unless explicitly disabled via flag
	tlsEnabled := !*noTLS

	// If TLS is enabled but no cert/key provided, use default paths
	certFile := *tlsCert
	keyFile := *tlsKey
	if tlsEnabled && certFile == "" {
		certFile = "server.crt"
	}
	if tlsEnabled && keyFile == "" {
		keyFile = "server.key"
	}

	Global = &Config{
		Addr:            *addr,
		RootFS:          *rootfs,
		ShutdownTimeout: *shutdownTimeout,
		TLSCertFile:     certFile,
		TLSKeyFile:      keyFile,
		TLSEnabled:      tlsEnabled,
	}
}
