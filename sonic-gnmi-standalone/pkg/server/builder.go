// Package server creates gRPC servers with configurable services.
//
// Example usage:
//
//	// Basic server with gNOI System service
//	srv, err := server.NewServerBuilder().
//	    WithAddress(":50055").
//	    WithRootFS("/mnt/host").
//	    EnableGNOISystem().
//	    Build()
//
//	// Server with TLS
//	srv, err := server.NewServerBuilder().
//	    WithAddress(":50055").
//	    WithTLS("server.crt", "server.key").
//	    EnableGNOISystem().
//	    Build()
//
//	// Server with mTLS
//	srv, err := server.NewServerBuilder().
//	    WithAddress(":50055").
//	    WithMTLS("server.crt", "server.key", "ca.crt").
//	    EnableServices([]string{"gnoi.system"}).
//	    Build()
package server

import (
	"github.com/golang/glog"
	"github.com/openconfig/gnoi/system"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/cert"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
	gnoiSystem "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/gnoi/system"
)

// ServerBuilder provides a fluent interface for configuring and building a gRPC server
// with various SONiC services. It follows the builder pattern to allow selective
// enabling/disabling of services based on deployment requirements.
type ServerBuilder struct {
	addr       string
	rootFS     string
	services   map[string]bool
	tlsConfig  *tlsConfig
	certConfig *certConfig
}

// tlsConfig holds TLS configuration for the server builder.
type tlsConfig struct {
	enabled     bool
	mtlsEnabled bool
	certFile    string
	keyFile     string
	caCertFile  string
}

// certConfig holds certificate manager configuration for the server builder.
type certConfig struct {
	useFiles        bool
	useSONiC        bool
	certFile        string
	keyFile         string
	caFile          string
	redisAddr       string
	redisDB         int
	requireClient   bool
	allowNoClient   bool
	configTableName string
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

// WithTLS enables TLS with the specified certificate and key files.
// This overrides global TLS configuration from command-line flags.
func (b *ServerBuilder) WithTLS(certFile, keyFile string) *ServerBuilder {
	b.tlsConfig = &tlsConfig{
		enabled:  true,
		certFile: certFile,
		keyFile:  keyFile,
	}
	return b
}

// WithMTLS enables mutual TLS with the specified certificate, key, and CA certificate files.
// This overrides global TLS configuration from command-line flags.
func (b *ServerBuilder) WithMTLS(certFile, keyFile, caCertFile string) *ServerBuilder {
	b.tlsConfig = &tlsConfig{
		enabled:     true,
		mtlsEnabled: true,
		certFile:    certFile,
		keyFile:     keyFile,
		caCertFile:  caCertFile,
	}
	return b
}

// WithoutTLS disables TLS for the server.
// This overrides global TLS configuration from command-line flags.
func (b *ServerBuilder) WithoutTLS() *ServerBuilder {
	b.tlsConfig = &tlsConfig{
		enabled: false,
	}
	return b
}

// WithCertificateFiles configures the server to use certificate files directly.
// This is suitable for containers with mounted /etc/sonic/ or file-based deployments.
func (b *ServerBuilder) WithCertificateFiles(certFile, keyFile, caFile string) *ServerBuilder {
	b.certConfig = &certConfig{
		useFiles:        true,
		certFile:        certFile,
		keyFile:         keyFile,
		caFile:          caFile,
		requireClient:   true,               // Default to secure
		configTableName: "GNMI_CLIENT_CERT", // Default table
		redisAddr:       "localhost:6379",   // Default Redis for client auth
		redisDB:         4,                  // Default ConfigDB
	}
	return b
}

// WithSONiCCertificates configures the server to read certificates from SONiC ConfigDB.
// This integrates with SONiC's Redis-based configuration system.
func (b *ServerBuilder) WithSONiCCertificates(redisAddr string, redisDB int) *ServerBuilder {
	b.certConfig = &certConfig{
		useSONiC:        true,
		redisAddr:       redisAddr,
		redisDB:         redisDB,
		requireClient:   true,               // Default to secure
		configTableName: "GNMI_CLIENT_CERT", // Default table name
	}
	return b
}

// WithClientCertPolicy sets the client certificate requirements.
// Can be chained with WithCertificateFiles() or WithSONiCCertificates().
func (b *ServerBuilder) WithClientCertPolicy(requireClient, allowNoClient bool) *ServerBuilder {
	if b.certConfig != nil {
		b.certConfig.requireClient = requireClient
		b.certConfig.allowNoClient = allowNoClient
	}
	return b
}

// WithConfigTableName sets the ConfigDB table name for client certificate authorization.
// This is typically "GNMI_CLIENT_CERT" (default) but can be customized for different services.
func (b *ServerBuilder) WithConfigTableName(tableName string) *ServerBuilder {
	if b.certConfig != nil {
		b.certConfig.configTableName = tableName
	}
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

	// Determine TLS configuration - use builder config if provided, otherwise global config
	var tlsEnabled, mtlsEnabled bool
	var certFile, keyFile, caCertFile string

	if b.tlsConfig != nil {
		// Use builder-specific TLS configuration
		tlsEnabled = b.tlsConfig.enabled
		mtlsEnabled = b.tlsConfig.mtlsEnabled
		certFile = b.tlsConfig.certFile
		keyFile = b.tlsConfig.keyFile
		caCertFile = b.tlsConfig.caCertFile
	} else {
		// Fall back to global configuration
		tlsEnabled = config.Global.TLSEnabled
		mtlsEnabled = config.Global.MTLSEnabled
		certFile = config.Global.TLSCertFile
		keyFile = config.Global.TLSKeyFile
		caCertFile = config.Global.TLSCACertFile
	}

	// Create the base gRPC server - prefer certificate manager over legacy TLS
	var srv *Server
	var err error

	if b.certConfig != nil {
		// Create certificate manager from builder configuration
		var certMgr cert.CertificateManager
		certMgr, err = b.createCertificateManager()
		if err != nil {
			return nil, err
		}
		srv, err = NewServerWithCertManager(addr, certMgr)
	} else {
		// Fall back to legacy TLS configuration
		srv, err = NewServerWithTLS(
			addr,
			tlsEnabled,
			certFile,
			keyFile,
			mtlsEnabled,
			caCertFile,
		)
	}

	if err != nil {
		return nil, err
	}

	// Register enabled services
	b.registerServices(srv, rootFS)

	return srv, nil
}

// createCertificateManager creates a certificate manager from the builder configuration.
func (b *ServerBuilder) createCertificateManager() (cert.CertificateManager, error) {
	if b.certConfig.useFiles {
		// File-based certificate configuration
		certConfig := cert.NewDefaultConfig()
		certConfig.CertFile = b.certConfig.certFile
		certConfig.KeyFile = b.certConfig.keyFile
		certConfig.CAFile = b.certConfig.caFile
		certConfig.RequireClientCert = b.certConfig.requireClient
		certConfig.AllowNoClientCert = b.certConfig.allowNoClient
		certConfig.ConfigTableName = b.certConfig.configTableName
		certConfig.RedisAddr = b.certConfig.redisAddr // For client auth manager
		certConfig.RedisDB = b.certConfig.redisDB     // For client auth manager
		certConfig.EnableMonitoring = false           // Disable monitoring in builder pattern for now
		certConfig.UseSONiCConfig = false             // Explicitly disable SONiC mode for file-based

		certMgr := cert.NewCertificateManager(certConfig)
		if err := certMgr.LoadCertificates(); err != nil {
			return nil, err
		}
		return certMgr, nil
	}

	if b.certConfig.useSONiC {
		// SONiC ConfigDB certificate configuration
		certConfig := cert.CreateSONiCCertConfig()
		certConfig.RedisAddr = b.certConfig.redisAddr
		certConfig.RedisDB = b.certConfig.redisDB
		certConfig.RequireClientCert = b.certConfig.requireClient
		certConfig.AllowNoClientCert = b.certConfig.allowNoClient
		certConfig.ConfigTableName = b.certConfig.configTableName
		certConfig.EnableMonitoring = false // Disable monitoring in builder pattern for now

		certMgr := cert.NewCertificateManager(certConfig)
		if err := certMgr.LoadCertificates(); err != nil {
			return nil, err
		}
		return certMgr, nil
	}

	return nil, nil
}

// registerServices registers all enabled services with the gRPC server.
// This method handles the service-specific registration logic and logging.
func (b *ServerBuilder) registerServices(srv *Server, rootFS string) {
	serviceCount := 0

	// Register gNOI System service
	if b.services["gnoi.system"] {
		systemServer := gnoiSystem.NewServer(rootFS)
		system.RegisterSystemServer(srv.grpcServer, systemServer)
		glog.Info("Registered gNOI System service")
		serviceCount++
	}

	// Future services will be implemented:
	// - gNOI File service
	// - gNOI Containerz service
	// - gNMI service

	if serviceCount == 0 {
		glog.Info("Server created with gRPC reflection only - no services enabled")
	} else {
		glog.Infof("Registered %d services", serviceCount)
	}
}
