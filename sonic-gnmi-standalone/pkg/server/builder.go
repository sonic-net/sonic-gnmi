// Package server provides a builder pattern for creating gRPC servers with configurable services.
package server

import (
	"github.com/golang/glog"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

// ServerBuilder provides a fluent interface for configuring and building a gRPC server
// with various SONiC services. It follows the builder pattern to allow selective
// enabling/disabling of services based on deployment requirements.
type ServerBuilder struct {
	addr     string
	rootFS   string
	services map[string]bool
}

// NewServerBuilder creates a new ServerBuilder instance with default configuration.
// Services are disabled by default and must be explicitly enabled.
func NewServerBuilder() *ServerBuilder {
	return &ServerBuilder{
		services: make(map[string]bool),
	}
}

// WithAddress sets the network address for the server to listen on.
// If not called, the server will use the global configuration address.
func (b *ServerBuilder) WithAddress(addr string) *ServerBuilder {
	b.addr = addr
	return b
}

// WithRootFS sets the root filesystem path for containerized deployments.
// This is typically "/mnt/host" for containers or "/" for bare metal.
func (b *ServerBuilder) WithRootFS(rootFS string) *ServerBuilder {
	b.rootFS = rootFS
	return b
}

// EnableGNOISystem enables the gNOI System service, which provides system-level
// operations including package management, reboot, and system information.
func (b *ServerBuilder) EnableGNOISystem() *ServerBuilder {
	b.services["gnoi.system"] = true
	return b
}

// EnableServices enables multiple services at once based on a slice of service names.
// Valid service names include: "gnoi.system", "gnoi.file", "gnoi.containerz", "gnmi".
func (b *ServerBuilder) EnableServices(services []string) *ServerBuilder {
	for _, service := range services {
		b.services[service] = true
	}
	return b
}

// Build creates and configures the gRPC server with the specified services.
// It registers only the services that have been explicitly enabled through
// the builder methods. Returns an error if server creation fails.
func (b *ServerBuilder) Build() (*Server, error) {
	// Use provided address or fall back to global config
	addr := b.addr
	if addr == "" {
		addr = config.Global.Addr
	}

	// Use provided rootFS or fall back to global config
	rootFS := b.rootFS
	if rootFS == "" {
		rootFS = config.Global.RootFS
	}

	// Create the base gRPC server
	srv, err := NewServerWithTLS(addr, config.Global.TLSEnabled, config.Global.TLSCertFile, config.Global.TLSKeyFile)
	if err != nil {
		return nil, err
	}

	// Register enabled services
	b.registerServices(srv, rootFS)

	return srv, nil
}

// registerServices registers all enabled services with the gRPC server.
// This method handles the service-specific registration logic and logging.
// Infrastructure-only implementation - service registrations are added by extending this method.
func (b *ServerBuilder) registerServices(srv *Server, rootFS string) {
	serviceCount := 0

	// Service registration will be implemented for:
	// - gNOI System service
	// - gNOI File service
	// - gNOI Containerz service
	// - gNMI service

	if serviceCount == 0 {
		glog.Info("Server created with gRPC reflection only - no services enabled")
	} else {
		glog.Infof("Registered %d services", serviceCount)
	}
}
