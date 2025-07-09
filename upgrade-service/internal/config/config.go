package config

import (
	"flag"
	"os"
	"time"

	"github.com/golang/glog"
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

	flag.Parse()

	// TLS is enabled by default unless explicitly disabled via env var
	tlsEnabled := os.Getenv("DISABLE_TLS") != "true"

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
	glog.V(1).Infof("Configuration initialized: addr=%s, rootfs=%s, tls_enabled=%t",
		Global.Addr, Global.RootFS, Global.TLSEnabled)
}
