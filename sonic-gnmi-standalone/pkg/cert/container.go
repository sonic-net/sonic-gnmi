package cert

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/golang/glog"
	"github.com/redis/go-redis/v9"
)

// loadFromSONiCConfig loads certificate configuration from SONiC ConfigDB directly via Redis.
func (cm *CertManager) loadFromSONiCConfig() error {
	glog.V(1).Info("Loading certificate configuration from SONiC ConfigDB via Redis")

	// Connect to ConfigDB using configured Redis settings
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cm.config.RedisAddr,
		DB:       cm.config.RedisDB,
		Password: "",
	})
	defer redisClient.Close()

	// Get certificate configuration from ConfigDB
	certPaths, err := cm.readCertConfigFromRedis(redisClient)
	if err != nil {
		return fmt.Errorf("failed to read SONiC certificate configuration: %w", err)
	}

	// Update certificate paths
	cm.updateCertPaths(certPaths)

	// Load certificates from the determined paths
	return cm.loadFromFiles()
}

// readCertConfigFromRedis reads certificate configuration directly from SONiC ConfigDB.
func (cm *CertManager) readCertConfigFromRedis(client *redis.Client) (*CertPaths, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	paths := &CertPaths{}

	// Try GNMI|certs table first (newer format)
	certsConfig, err := client.HGetAll(ctx, "GNMI|certs").Result()
	if err == nil && len(certsConfig) > 0 {
		paths.CertFile = certsConfig["server_crt"]
		paths.KeyFile = certsConfig["server_key"]
		paths.CAFile = certsConfig["ca_crt"]
		glog.V(2).Info("Using GNMI|certs configuration from ConfigDB")
	} else {
		// Fallback to DEVICE_METADATA|x509 table (older format)
		x509Config, err := client.HGetAll(ctx, "DEVICE_METADATA|x509").Result()
		if err == nil && len(x509Config) > 0 {
			paths.CertFile = x509Config["server_crt"]
			paths.KeyFile = x509Config["server_key"]
			paths.CAFile = x509Config["ca_crt"]
			glog.V(2).Info("Using DEVICE_METADATA|x509 configuration from ConfigDB")
		} else {
			glog.Warning("No certificate configuration found in ConfigDB")
			return nil, fmt.Errorf("no certificate configuration found in ConfigDB")
		}
	}

	// Validate that we have the required certificate files
	if paths.CertFile == "" || paths.KeyFile == "" {
		glog.Warning("Incomplete certificate configuration - missing server cert/key")
		return nil, fmt.Errorf("incomplete certificate configuration in ConfigDB")
	}

	// Read GNMI configuration for client authentication settings
	if err := cm.updateClientAuthFromConfigDB(client, ctx); err != nil {
		glog.V(2).Infof("Failed to read GNMI config, using defaults: %v", err)
	}

	glog.V(1).Infof("Read certificate paths from ConfigDB: cert=%s, key=%s, ca=%s",
		paths.CertFile, paths.KeyFile, paths.CAFile)

	return paths, nil
}

// updateClientAuthFromConfigDB updates client authentication settings from ConfigDB GNMI config.
func (cm *CertManager) updateClientAuthFromConfigDB(client *redis.Client, ctx context.Context) error {
	// Read GNMI configuration from ConfigDB
	gnmiConfig, err := client.HGetAll(ctx, "GNMI|gnmi").Result()
	if err != nil || len(gnmiConfig) == 0 {
		glog.V(2).Info("No GNMI configuration found, using certificate authentication defaults")
		return nil
	}

	// Parse client_auth setting
	clientAuth := gnmiConfig["client_auth"]
	if clientAuth == "false" {
		// client_auth is false - allow no client certificates
		cm.config.AllowNoClientCert = true
		cm.config.RequireClientCert = false
		glog.V(2).Info("Client authentication disabled by ConfigDB")
		return nil
	}

	// Parse user_auth setting (matches gnmi-native.sh logic)
	userAuth := gnmiConfig["user_auth"]
	if userAuth == "" || userAuth == "null" || userAuth == "cert" {
		// Default to certificate authentication
		cm.config.RequireClientCert = true
		cm.config.AllowNoClientCert = false
		glog.V(2).Info("Using certificate authentication from ConfigDB")
	} else {
		// Other authentication modes - allow no client cert
		cm.config.RequireClientCert = false
		cm.config.AllowNoClientCert = true
		glog.V(2).Infof("Using %s authentication from ConfigDB", userAuth)
	}

	return nil
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
