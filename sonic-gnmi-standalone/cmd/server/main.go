// Package main implements the sonic-gnmi-standalone server.
//
// Available command-line flags:
//
//	-addr string
//	    The address to listen on (default ":50055")
//	-rootfs string
//	    Root filesystem mount point (default "/mnt/host")
//	-shutdown-timeout duration
//	    Maximum time to wait for graceful shutdown (default 10s)
//	-tls-cert string
//	    Path to TLS certificate file (default "server.crt" if TLS enabled)
//	-tls-key string
//	    Path to TLS private key file (default "server.key" if TLS enabled)
//	-tls-ca-cert string
//	    Path to TLS CA certificate file for client verification
//	-no-tls
//	    Disable TLS (TLS is enabled by default)
//	-mtls
//	    Enable mutual TLS (requires CA certificate)
//	-v int
//	    Verbose logging level (0-2)
//	-logtostderr
//	    Log to stderr instead of files
//
// Examples:
//
//	# Basic usage with default settings
//	./sonic-gnmi-standalone
//
//	# With custom address and verbose logging
//	./sonic-gnmi-standalone -addr=:8080 -v=2 -logtostderr
//
//	# With TLS disabled
//	./sonic-gnmi-standalone -no-tls
//
//	# With mTLS enabled
//	./sonic-gnmi-standalone -mtls -tls-ca-cert=ca.crt
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

func main() {
	// Initialize configuration and parse flags
	config.Initialize()

	// Initialize glog
	defer glog.Flush()

	// Log configuration
	glog.Infof("Starting sonic-gnmi-standalone: addr=%s, rootfs=%s, tls=%t, mtls=%t",
		config.Global.Addr, config.Global.RootFS, config.Global.TLSEnabled, config.Global.MTLSEnabled)

	// Create a new server instance using the builder pattern
	builder := server.NewServerBuilder().
		WithAddress(config.Global.Addr).
		WithRootFS(config.Global.RootFS).
		EnableGNOISystem().
		EnableGNMI()

	// Configure certificates based on advanced options
	if config.Global.UseSONiCConfig {
		glog.V(1).Infof("Using SONiC ConfigDB certificates: redis=%s, db=%d, table=%s",
			config.Global.RedisAddr, config.Global.RedisDB, config.Global.ConfigTableName)
		builder = builder.WithSONiCCertificates(config.Global.RedisAddr, config.Global.RedisDB).
			WithConfigTableName(config.Global.ConfigTableName)
	}

	// Configure TLS based on command-line flags
	if !config.Global.TLSEnabled {
		glog.V(1).Info("TLS disabled via command-line flag")
		builder = builder.WithoutTLS()
	} else if config.Global.MTLSEnabled {
		glog.V(1).Infof("mTLS enabled with cert=%s, key=%s, ca=%s",
			config.Global.TLSCertFile, config.Global.TLSKeyFile, config.Global.TLSCACertFile)
		builder = builder.WithMTLS(
			config.Global.TLSCertFile,
			config.Global.TLSKeyFile,
			config.Global.TLSCACertFile,
		)
	} else {
		glog.V(1).Infof("TLS enabled with cert=%s, key=%s",
			config.Global.TLSCertFile, config.Global.TLSKeyFile)
		builder = builder.WithTLS(
			config.Global.TLSCertFile,
			config.Global.TLSKeyFile,
		)
	}

	srv, err := builder.Build()
	if err != nil {
		glog.Fatalf("Failed to create server: %v", err)
	}

	// Set up signal handling for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Start()
	}()

	// Wait for termination signal or server error
	select {
	case err := <-errChan:
		if err != nil {
			glog.Fatalf("Server error: %v", err)
		}
	case sig := <-signalChan:
		glog.Infof("Received signal: %v", sig)

		// Create a context with timeout for shutdown
		ctx, cancel := context.WithTimeout(context.Background(), config.Global.ShutdownTimeout)
		defer cancel()

		// Create a channel to signal completion of cleanup tasks
		done := make(chan struct{})
		go func() {
			// Perform graceful shutdown
			srv.Stop()

			// Any additional cleanup can be added here

			close(done)
		}()

		// Wait for shutdown to complete or timeout
		select {
		case <-ctx.Done():
			glog.Warning("Shutdown timed out, forcing exit")
		case <-done:
			glog.Info("Graceful shutdown completed")
		}
	}
}
