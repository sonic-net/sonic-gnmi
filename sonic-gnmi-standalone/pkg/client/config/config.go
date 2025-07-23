// Package config provides shared configuration for gNOI and gNMI clients.
package config

import (
	"crypto/tls"
	"time"
)

// Config contains basic configuration for gRPC clients.
type Config struct {
	// Address is the target server address in "host:port" format
	Address string

	// TLS enables TLS encryption for the connection
	TLS bool

	// TLSConfig provides custom TLS configuration (optional)
	TLSConfig *tls.Config

	// Timeout for RPC requests (important for long SetPackage operations)
	Timeout time.Duration
}

// DefaultConfig returns a Config with basic defaults.
func DefaultConfig() *Config {
	return &Config{
		TLS:     false,
		Timeout: 5 * time.Minute, // SetPackage can take a while
	}
}

// New creates a new Config with the specified address.
func New(address string) *Config {
	config := DefaultConfig()
	config.Address = address
	return config
}