// Package gnmi provides a gNMI client for retrieving network management data.
package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Client represents a gNMI client connection.
type Client struct {
	conn   *grpc.ClientConn
	client gnmi.GNMIClient
	target string
}

// ClientConfig holds configuration options for the gNMI client.
type ClientConfig struct {
	// Target is the gNMI server address (e.g., "localhost:50055")
	Target string

	// Timeout for connection and RPC operations
	Timeout time.Duration

	// TLS configuration
	TLSEnabled  bool
	TLSInsecure bool // Skip certificate verification
	TLSCertFile string
	TLSKeyFile  string

	// Authentication (for future use)
	Username string
	Password string
}

// NewClient creates a new gNMI client with the specified configuration.
func NewClient(config *ClientConfig) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("client configuration is required")
	}

	if config.Target == "" {
		return nil, fmt.Errorf("target address is required")
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	// Setup connection options
	var opts []grpc.DialOption

	// Configure TLS
	if config.TLSEnabled {
		var creds credentials.TransportCredentials
		if config.TLSInsecure {
			// nosemgrep: problem-based-packs.insecure-transport.go-stdlib.bypass-tls-verification.bypass-tls-verification
			creds = credentials.NewTLS(&tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS13,
			})
		} else {
			// Use system root CAs or provided certificate
			creds = credentials.NewTLS(&tls.Config{
				MinVersion: tls.VersionTLS13,
			})
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Set timeout
	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()

	// Establish connection
	glog.V(2).Infof("Connecting to gNMI server at %s", config.Target)
	conn, err := grpc.DialContext(ctx, config.Target, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", config.Target, err)
	}

	client := gnmi.NewGNMIClient(conn)

	return &Client{
		conn:   conn,
		client: client,
		target: config.Target,
	}, nil
}

// Close closes the gNMI client connection.
func (c *Client) Close() error {
	if c.conn != nil {
		glog.V(2).Infof("Closing connection to %s", c.target)
		return c.conn.Close()
	}
	return nil
}

// Capabilities retrieves the server's capabilities.
func (c *Client) Capabilities(ctx context.Context) (*gnmi.CapabilityResponse, error) {
	glog.V(2).Info("Sending Capabilities request")

	req := &gnmi.CapabilityRequest{}
	resp, err := c.client.Capabilities(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("capabilities request failed: %w", err)
	}

	return resp, nil
}

// Get retrieves data for the specified paths.
func (c *Client) Get(ctx context.Context, paths []*gnmi.Path, encoding gnmi.Encoding) (*gnmi.GetResponse, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one path is required")
	}

	glog.V(2).Infof("Sending Get request for %d paths", len(paths))

	req := &gnmi.GetRequest{
		Path:     paths,
		Encoding: encoding,
	}

	resp, err := c.client.Get(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get request failed: %w", err)
	}

	return resp, nil
}
