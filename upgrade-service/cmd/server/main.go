package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/pkg/server"
)

func main() {
	// Define command line flags
	addr := flag.String("addr", ":50051", "The address to listen on")
	shutdownTimeout := flag.Duration("shutdown-timeout", 10*time.Second, "Maximum time to wait for graceful shutdown")
	flag.Parse()

	// Create a new server instance
	log.Printf("Sonic Metadata Service starting...")
	srv, err := server.NewServer(*addr)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
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
			log.Fatalf("Server error: %v", err)
		}
	case sig := <-signalChan:
		log.Printf("Received signal: %v", sig)

		// Create a context with timeout for shutdown
		ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
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
			log.Println("Shutdown timed out, forcing exit")
		case <-done:
			log.Println("Graceful shutdown completed")
		}
	}
}
