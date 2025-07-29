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
		WithRootFS(config.Global.RootFS)

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
