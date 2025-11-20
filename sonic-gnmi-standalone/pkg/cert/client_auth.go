package cert

import (
	"context"
	"crypto/x509"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/redis/go-redis/v9"
)

// ClientAuthManager manages client certificate authorization using ConfigDB.
type ClientAuthManager struct {
	redisAddr     string
	redisDB       int
	configTable   string
	clientCNRoles map[string]string // CN -> roles mapping
	mu            sync.RWMutex
}

// NewClientAuthManager creates a new client certificate authorization manager.
func NewClientAuthManager(redisAddr string, redisDB int, configTable string) *ClientAuthManager {
	if configTable == "" {
		configTable = "GNMI_CLIENT_CERT"
	}
	return &ClientAuthManager{
		redisAddr:     redisAddr,
		redisDB:       redisDB,
		configTable:   configTable,
		clientCNRoles: make(map[string]string),
	}
}

// LoadClientCertConfig loads client certificate authorization from ConfigDB.
func (cam *ClientAuthManager) LoadClientCertConfig() error {
	glog.V(1).Infof("Loading client certificate config from table: %s", cam.configTable)

	// Connect to Redis
	client := redis.NewClient(&redis.Options{
		Addr: cam.redisAddr,
		DB:   cam.redisDB,
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Scan for all keys matching the pattern GNMI_CLIENT_CERT|*
	pattern := fmt.Sprintf("%s|*", cam.configTable)
	iter := client.Scan(ctx, 0, pattern, 0).Iterator()

	cam.mu.Lock()
	defer cam.mu.Unlock()

	// Clear existing entries
	cam.clientCNRoles = make(map[string]string)

	for iter.Next(ctx) {
		key := iter.Val()
		// Extract CN from key (format: GNMI_CLIENT_CERT|<CN>)
		parts := strings.Split(key, "|")
		if len(parts) != 2 {
			continue
		}
		cn := parts[1]

		// Get the role for this CN
		role, err := client.HGet(ctx, key, "role@").Result()
		if err != nil {
			glog.V(2).Infof("Failed to get role for CN %s: %v", cn, err)
			continue
		}

		cam.clientCNRoles[cn] = role
		glog.V(2).Infof("Loaded client CN: %s with role: %s", cn, role)
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan client certificates: %w", err)
	}

	glog.V(1).Infof("Loaded %d client certificate entries", len(cam.clientCNRoles))
	return nil
}

// VerifyClientCertificate verifies if a client certificate is authorized.
func (cam *ClientAuthManager) VerifyClientCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("no client certificate provided")
	}

	// Parse the client certificate
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("failed to parse client certificate: %w", err)
	}

	// Get the common name from the certificate
	cn := cert.Subject.CommonName
	if cn == "" {
		return fmt.Errorf("client certificate has no common name")
	}

	// Check if the CN is authorized
	cam.mu.RLock()
	role, authorized := cam.clientCNRoles[cn]
	cam.mu.RUnlock()

	if !authorized {
		glog.V(1).Infof("Unauthorized client CN: %s", cn)
		return fmt.Errorf("client CN %s is not authorized", cn)
	}

	glog.V(2).Infof("Authorized client CN: %s with role: %s", cn, role)
	return nil
}

// GetAuthorizedCNs returns a list of authorized client common names.
func (cam *ClientAuthManager) GetAuthorizedCNs() []string {
	cam.mu.RLock()
	defer cam.mu.RUnlock()

	cns := make([]string, 0, len(cam.clientCNRoles))
	for cn := range cam.clientCNRoles {
		cns = append(cns, cn)
	}
	return cns
}

// AddClientCN adds a client CN with its role (for testing).
func (cam *ClientAuthManager) AddClientCN(cn, role string) {
	cam.mu.Lock()
	defer cam.mu.Unlock()
	cam.clientCNRoles[cn] = role
}
