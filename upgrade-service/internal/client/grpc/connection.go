package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// ConnectionConfig holds the configuration for gRPC connections.
type ConnectionConfig struct {
	Address         string
	TLSEnabled      bool
	TLSCertFile     string
	TLSKeyFile      string
	ConnectTimeout  time.Duration
	MaxRetries      int
	RetryDelay      time.Duration
	InsecureSkipTLS bool // For testing only
}

// Connection wraps a gRPC connection with connection management.
type Connection struct {
	conn   *grpc.ClientConn
	config ConnectionConfig
}

// NewConnection creates a new gRPC connection with the given configuration.
func NewConnection(config ConnectionConfig) (*Connection, error) {
	// Validate configuration
	if config.Address == "" {
		return nil, fmt.Errorf("address is required")
	}

	// Set defaults
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 10 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 1 * time.Second
	}

	connection := &Connection{
		config: config,
	}

	// Establish the connection
	if err := connection.connect(); err != nil {
		return nil, fmt.Errorf("failed to establish connection: %w", err)
	}

	return connection, nil
}

// Conn returns the underlying gRPC connection.
func (c *Connection) Conn() *grpc.ClientConn {
	return c.conn
}

// Close closes the gRPC connection.
func (c *Connection) Close() error {
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// IsConnected checks if the connection is still active.
func (c *Connection) IsConnected() bool {
	if c.conn == nil {
		return false
	}

	state := c.conn.GetState()
	return state == connectivity.Connecting || state == connectivity.Ready
}

// Reconnect attempts to re-establish a failed connection.
func (c *Connection) Reconnect() error {
	glog.V(1).Infof("Attempting to reconnect to %s", c.config.Address)

	// Close existing connection if any
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	// Re-establish connection
	return c.connect()
}

// connect establishes the gRPC connection with retry logic.
func (c *Connection) connect() error {
	var lastErr error

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			glog.V(1).Infof("Connection attempt %d/%d to %s", attempt+1, c.config.MaxRetries+1, c.config.Address)
			time.Sleep(c.config.RetryDelay)
		}

		conn, err := c.dial()
		if err != nil {
			lastErr = err
			glog.V(2).Infof("Connection attempt %d failed: %v", attempt+1, err)
			continue
		}

		c.conn = conn
		glog.V(1).Infof("Successfully connected to %s", c.config.Address)
		return nil
	}

	return fmt.Errorf("failed to connect after %d attempts: %w", c.config.MaxRetries+1, lastErr)
}

// dial creates a single connection attempt.
func (c *Connection) dial() (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.config.ConnectTimeout)
	defer cancel()

	// Prepare dial options
	var opts []grpc.DialOption

	tlsOpts, err := c.getTLSDialOption()
	if err != nil {
		return nil, err
	}
	opts = append(opts, tlsOpts...)

	// Add other dial options
	opts = append(opts, grpc.WithBlock()) // Wait for connection to be ready

	// Dial with context
	conn, err := grpc.DialContext(ctx, c.config.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("gRPC dial failed: %w", err)
	}

	return conn, nil
}

// getTLSDialOption returns the appropriate TLS dial options.
func (c *Connection) getTLSDialOption() ([]grpc.DialOption, error) {
	if !c.config.TLSEnabled {
		return []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, nil
	}

	// Configure TLS with secure defaults
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13, // Enforce TLS 1.3 minimum for security
	}

	// InsecureSkipTLS is only used for testing - never in production
	if c.config.InsecureSkipTLS {
		// nosemgrep: problem-based-packs.insecure-transport.go-stdlib.bypass-tls-verification.bypass-tls-verification
		tlsConfig.InsecureSkipVerify = true
	}

	// Load client certificates if provided
	if c.config.TLSCertFile != "" && c.config.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.config.TLSCertFile, c.config.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS certificates: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	creds := credentials.NewTLS(tlsConfig)
	return []grpc.DialOption{grpc.WithTransportCredentials(creds)}, nil
}
