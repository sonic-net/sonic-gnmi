package cert

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
)

// SONiCCertConfig represents certificate configuration from SONiC ConfigDB.
type SONiCCertConfig struct {
	X509  *X509Config  `json:"x509,omitempty"`
	GNMI  *GNMIConfig  `json:"gnmi,omitempty"`
	Certs *CertsConfig `json:"certs,omitempty"`
}

// X509Config represents X.509 certificate configuration.
type X509Config struct {
	ServerCert string `json:"server_crt"`
	ServerKey  string `json:"server_key"`
	CACert     string `json:"ca_crt"`
}

// GNMIConfig represents gNMI service configuration.
type GNMIConfig struct {
	Port              int    `json:"port"`
	ClientAuth        bool   `json:"client_auth"`
	LogLevel          int    `json:"log_level"`
	UserAuth          string `json:"user_auth"`
	EnableCRL         bool   `json:"enable_crl"`
	CRLExpireDuration int    `json:"crl_expire_duration"`
}

// CertsConfig represents certificate configuration (alternative to X509).
type CertsConfig struct {
	ServerCert string `json:"server_crt"`
	ServerKey  string `json:"server_key"`
	CACert     string `json:"ca_crt"`
}

// loadFromSONiCConfig loads certificate configuration from SONiC ConfigDB like telemetry container.
func (cm *CertManager) loadFromSONiCConfig() error {
	glog.V(1).Info("Loading certificate configuration from SONiC ConfigDB")

	// Execute sonic-cfggen to get telemetry configuration
	sonicConfig, err := cm.executeSONiCConfigGen()
	if err != nil {
		return fmt.Errorf("failed to get SONiC configuration: %w", err)
	}

	// Parse the configuration
	certPaths, err := cm.parseSONiCConfig(sonicConfig)
	if err != nil {
		return fmt.Errorf("failed to parse SONiC configuration: %w", err)
	}

	// Update certificate paths
	cm.updateCertPaths(certPaths)

	// Load certificates from the determined paths
	return cm.loadFromFiles()
}

// executeSONiCConfigGen runs sonic-cfggen to extract telemetry configuration.
func (cm *CertManager) executeSONiCConfigGen() (*SONiCCertConfig, error) {
	// Use the same template path as telemetry container
	templatePath := "/usr/share/sonic/templates/telemetry_vars.j2"

	glog.V(2).Infof("Executing sonic-cfggen with template: %s", templatePath)

	// Run sonic-cfggen -d -t template.j2
	cmd := exec.Command("sonic-cfggen", "-d", "-t", templatePath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sonic-cfggen execution failed: %w", err)
	}

	glog.V(2).Infof("sonic-cfggen output: %s", string(output))

	// Clean up the output (remove single quotes like telemetry container does)
	cleanOutput := strings.ReplaceAll(string(output), "'", "\"")

	// Parse JSON configuration
	var config SONiCCertConfig
	if err := json.Unmarshal([]byte(cleanOutput), &config); err != nil {
		return nil, fmt.Errorf("failed to parse SONiC config JSON: %w", err)
	}

	return &config, nil
}

// parseSONiCConfig extracts certificate file paths from SONiC configuration.
func (cm *CertManager) parseSONiCConfig(config *SONiCCertConfig) (*CertPaths, error) {
	paths := &CertPaths{}

	// Try CERTS configuration first (newer format)
	if config.Certs != nil {
		paths.CertFile = config.Certs.ServerCert
		paths.KeyFile = config.Certs.ServerKey
		paths.CAFile = config.Certs.CACert
		glog.V(2).Info("Using CERTS configuration from SONiC")
	} else if config.X509 != nil {
		// Fallback to X509 configuration (older format)
		paths.CertFile = config.X509.ServerCert
		paths.KeyFile = config.X509.ServerKey
		paths.CAFile = config.X509.CACert
		glog.V(2).Info("Using X509 configuration from SONiC")
	} else {
		// No certificate configuration found - use insecure mode
		glog.Warning("No certificate configuration found in SONiC ConfigDB - using insecure mode")
		return nil, fmt.Errorf("no certificate configuration found in SONiC ConfigDB")
	}

	// Validate that we have the required certificate files
	if paths.CertFile == "" || paths.KeyFile == "" {
		glog.Warning("Incomplete certificate configuration - missing server cert/key")
		return nil, fmt.Errorf("incomplete certificate configuration in SONiC ConfigDB")
	}

	// Update client authentication settings from GNMI config
	if config.GNMI != nil {
		cm.updateClientAuthFromSONiC(config.GNMI)
	}

	glog.V(1).Infof("Parsed SONiC certificate paths: cert=%s, key=%s, ca=%s",
		paths.CertFile, paths.KeyFile, paths.CAFile)

	return paths, nil
}

// CertPaths holds certificate file paths.
type CertPaths struct {
	CertFile string
	KeyFile  string
	CAFile   string
}

// updateCertPaths updates the certificate manager's configuration with new paths.
func (cm *CertManager) updateCertPaths(paths *CertPaths) {
	if paths == nil {
		return
	}

	cm.config.CertFile = paths.CertFile
	cm.config.KeyFile = paths.KeyFile
	if paths.CAFile != "" {
		cm.config.CAFile = paths.CAFile
		cm.config.RequireClientCert = true
	} else {
		// No CA certificate - allow connections without client certs
		cm.config.RequireClientCert = false
		cm.config.AllowNoClientCert = true
	}

	glog.V(2).Infof("Updated certificate paths: cert=%s, key=%s, ca=%s, requireClient=%t",
		cm.config.CertFile, cm.config.KeyFile, cm.config.CAFile, cm.config.RequireClientCert)
}

// updateClientAuthFromSONiC updates client authentication settings from SONiC GNMI config.
func (cm *CertManager) updateClientAuthFromSONiC(gnmiConfig *GNMIConfig) {
	if gnmiConfig == nil {
		return
	}

	// Update client authentication based on SONiC configuration
	// This matches the logic from gnmi-native.sh and telemetry.sh
	if gnmiConfig.UserAuth == "" || gnmiConfig.UserAuth == "null" {
		// Default to certificate authentication like gnmi-native.sh
		cm.config.RequireClientCert = true
		cm.config.AllowNoClientCert = false
		glog.V(2).Info("Using default certificate authentication from SONiC")
	} else if gnmiConfig.UserAuth == "cert" {
		cm.config.RequireClientCert = true
		cm.config.AllowNoClientCert = false
		glog.V(2).Info("Using certificate authentication from SONiC")
	} else {
		// Other authentication modes - allow no client cert
		cm.config.RequireClientCert = false
		cm.config.AllowNoClientCert = true
		glog.V(2).Infof("Using %s authentication from SONiC", gnmiConfig.UserAuth)
	}

	// Handle client_auth setting (like telemetry.sh does)
	if !gnmiConfig.ClientAuth {
		// client_auth is false - allow no client certificates
		cm.config.AllowNoClientCert = true
		cm.config.RequireClientCert = false
		glog.V(2).Info("Client authentication disabled by SONiC config")
	}
}

// GetSharedCertificatesPath returns the path for shared certificates with another container.
func GetSharedCertificatesPath(containerName, mountPath string) string {
	return filepath.Join(mountPath, containerName)
}

// CreateContainerCertConfig creates a certificate configuration for container sharing.
func CreateContainerCertConfig(containerName, mountPath string) *CertConfig {
	config := NewDefaultConfig()
	config.ShareWithContainer = containerName
	config.CertMountPath = mountPath
	config.EnableMonitoring = true // Enable monitoring for shared certificates

	return config
}

// CreateSONiCCertConfig creates a certificate configuration for SONiC ConfigDB integration.
func CreateSONiCCertConfig() *CertConfig {
	config := NewDefaultConfig()
	config.UseSONiCConfig = true
	config.EnableMonitoring = true // Enable monitoring for SONiC certificates

	// Match telemetry container's default client authentication behavior
	config.RequireClientCert = true
	config.AllowNoClientCert = false

	return config
}
