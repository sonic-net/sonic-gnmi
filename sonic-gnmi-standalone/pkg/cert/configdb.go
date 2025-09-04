package cert

import (
	"context"
	"fmt"

	"github.com/golang/glog"
	"github.com/redis/go-redis/v9"
)

// loadFromSONiCConfig loads certificate configuration from SONiC ConfigDB directly via Redis.
func (cm *CertManager) loadFromSONiCConfig(ctx context.Context) error {
	glog.V(1).Info("Loading certificate configuration from SONiC ConfigDB via Redis")

	// Connect to ConfigDB using configured Redis settings
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cm.config.RedisAddr,
		DB:       cm.config.RedisDB,
		Password: "",
	})
	defer redisClient.Close()

	// Get certificate configuration from ConfigDB
	certPaths, err := cm.readCertConfigFromRedis(ctx, redisClient)
	if err != nil {
		return fmt.Errorf("failed to read SONiC certificate configuration: %w", err)
	}

	// Update certificate paths
	cm.updateCertPaths(certPaths)

	// Load certificates from the determined paths
	return cm.loadFromFiles()
}

// readCertConfigFromRedis reads certificate configuration directly from SONiC ConfigDB.
func (cm *CertManager) readCertConfigFromRedis(ctx context.Context, client *redis.Client) (*CertPaths, error) {
	paths := &CertPaths{}

	// Read GNMI|certs table from ConfigDB
	certsConfig, err := client.HGetAll(ctx, "GNMI|certs").Result()
	if err != nil || len(certsConfig) == 0 {
		glog.Warning("No certificate configuration found in ConfigDB")
		return nil, fmt.Errorf("no certificate configuration found in ConfigDB")
	}

	paths.CertFile = certsConfig["server_crt"]
	paths.KeyFile = certsConfig["server_key"]
	paths.CAFile = certsConfig["ca_crt"]
	glog.V(2).Info("Using GNMI|certs configuration from ConfigDB")

	// Validate that we have the required certificate files
	if paths.CertFile == "" || paths.KeyFile == "" {
		glog.Warning("Incomplete certificate configuration - missing server cert/key")
		return nil, fmt.Errorf("incomplete certificate configuration in ConfigDB")
	}

	// Read GNMI configuration for client authentication settings
	if err := cm.updateClientAuthFromConfigDB(ctx, client); err != nil {
		glog.V(2).Infof("Failed to read GNMI config, using defaults: %v", err)
	}

	glog.V(1).Infof("Read certificate paths from ConfigDB: cert=%s, key=%s, ca=%s",
		paths.CertFile, paths.KeyFile, paths.CAFile)

	return paths, nil
}

// updateClientAuthFromConfigDB updates client authentication settings from ConfigDB GNMI config.
func (cm *CertManager) updateClientAuthFromConfigDB(ctx context.Context, client *redis.Client) error {
	// Read GNMI configuration from ConfigDB
	gnmiConfig, err := client.HGetAll(ctx, "GNMI|gnmi").Result()
	if err != nil || len(gnmiConfig) == 0 {
		glog.V(2).Info("No GNMI configuration found, using certificate authentication defaults")
		return nil
	}

	// Parse client_auth setting
	clientAuth := gnmiConfig["client_auth"]
	if clientAuth == "false" {
		// client_auth is false - no client certificates required
		cm.config.RequireClientCert = false
		glog.V(2).Info("Client certificate authentication disabled by ConfigDB")
		return nil
	}

	// Parse user_auth setting (matches gnmi-native.sh logic)
	userAuth := gnmiConfig["user_auth"]
	if userAuth == "" || userAuth == "null" || userAuth == "cert" {
		// Default to certificate authentication
		cm.config.RequireClientCert = true
		glog.V(2).Info("Using certificate authentication from ConfigDB")
	} else {
		// Other authentication modes (password/jwt) - no client certificates
		cm.config.RequireClientCert = false
		glog.V(2).Infof("Using %s authentication from ConfigDB, client certificates disabled", userAuth)
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
		// Don't override RequireClientCert here - it should be set by updateClientAuthFromConfigDB
		// based on the explicit ConfigDB client_auth setting
	}
	// If no CA file is provided, we can't verify client certs even if requested
	if paths.CAFile == "" && cm.config.RequireClientCert {
		glog.V(1).Info("No CA certificate available - disabling client certificate requirement")
		cm.config.RequireClientCert = false
	}

	glog.V(2).Infof("Updated certificate paths: cert=%s, key=%s, ca=%s, requireClient=%t",
		cm.config.CertFile, cm.config.KeyFile, cm.config.CAFile, cm.config.RequireClientCert)
}

// CreateSONiCCertConfig creates a certificate configuration for SONiC ConfigDB integration.
func CreateSONiCCertConfig() *CertConfig {
	config := NewDefaultConfig()
	config.UseSONiCConfig = true
	config.EnableMonitoring = true // Enable monitoring for SONiC certificates

	// Match telemetry container's default client authentication behavior
	config.RequireClientCert = true

	return config
}
