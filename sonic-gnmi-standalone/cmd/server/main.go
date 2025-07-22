package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/internal/config"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
)

func main() {
	// Initialize configuration and parse flags
	config.Initialize()

	// Initialize glog
	defer glog.Flush()

	// Log configuration
	glog.Infof("Starting sonic-gnmi-standalone: addr=%s, rootfs=%s, tls=%t",
		config.Global.Addr, config.Global.RootFS, config.Global.TLSEnabled)

	// Create a new server instance
	srv, err := server.NewServer(config.Global.Addr)
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
