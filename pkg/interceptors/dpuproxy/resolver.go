package dpuproxy

import (
	"context"
	"fmt"
)

const (
	// StateDB is database 6 in SONiC Redis
	StateDB = 6

	// ConfigDB is database 4 in SONiC Redis
	ConfigDB = 4

	// DefaultRedisSocket is the default Unix socket path for SONiC Redis
	DefaultRedisSocket = "/var/run/redis/redis.sock"

	// DefaultGNMIPort is the fallback gNMI port if not configured in CONFIG_DB
	DefaultGNMIPort = "50052"

	// ChassisMidplaneTablePrefix is the Redis key prefix for DPU midplane info
	ChassisMidplaneTablePrefix = "CHASSIS_MIDPLANE_TABLE|DPU"

	// DPUConfigTablePrefix is the Redis key prefix for DPU configuration
	DPUConfigTablePrefix = "DPU|dpu"
)

// DPUInfo contains information about a DPU retrieved from Redis.
type DPUInfo struct {
	// Index is the DPU number (e.g., "0", "1", "2")
	Index string

	// IPAddress is the IP address of the DPU
	IPAddress string

	// Reachable indicates if the DPU is currently reachable
	Reachable bool

	// GNMIPort is the gNMI server port on the DPU (from CONFIG_DB)
	GNMIPort string

	// GNMIPortsToTry is a list of ports to try in order (for robustness)
	GNMIPortsToTry []string
}

// DPUResolver resolves DPU information from Redis.
type DPUResolver struct {
	stateClient  RedisClient // For StateDB (DPU status info)
	configClient RedisClient // For ConfigDB (DPU configuration)
}

// NewDPUResolver creates a new DPU resolver with the given Redis clients.
func NewDPUResolver(stateClient, configClient RedisClient) *DPUResolver {
	return &DPUResolver{
		stateClient:  stateClient,
		configClient: configClient,
	}
}

// GetDPUInfo retrieves DPU information from Redis by DPU index.
// It queries both STATE_DB (database 6) for status info and CONFIG_DB (database 4) for configuration.
//
// Returns:
//   - DPUInfo with the DPU details if found
//   - error if the DPU doesn't exist or Redis query fails
func (r *DPUResolver) GetDPUInfo(ctx context.Context, dpuIndex string) (*DPUInfo, error) {
	// Step 1: Query STATE_DB for DPU status info
	stateKey := fmt.Sprintf("%s%s", ChassisMidplaneTablePrefix, dpuIndex)
	stateFields, err := r.stateClient.HGetAll(ctx, stateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to query StateDB for DPU%s: %w", dpuIndex, err)
	}

	// Check if DPU exists in StateDB
	if len(stateFields) == 0 {
		return nil, fmt.Errorf("DPU%s not found in StateDB", dpuIndex)
	}

	// Extract IP address from StateDB
	ipAddr, ok := stateFields["ip_address"]
	if !ok || ipAddr == "" {
		return nil, fmt.Errorf("DPU%s missing ip_address field in StateDB", dpuIndex)
	}

	// Extract reachability status from StateDB
	// Redis stores "True" or "False" as strings
	accessStr, ok := stateFields["access"]
	reachable := ok && accessStr == "True"

	// Step 2: Query CONFIG_DB for DPU configuration
	configKey := fmt.Sprintf("%s%s", DPUConfigTablePrefix, dpuIndex)
	configFields, err := r.configClient.HGetAll(ctx, configKey)
	if err != nil {
		return nil, fmt.Errorf("failed to query ConfigDB for DPU%s: %w", dpuIndex, err)
	}

	// Extract gNMI port from CONFIG_DB, use default if not found
	gnmiPort, ok := configFields["gnmi_port"]
	if !ok || gnmiPort == "" {
		gnmiPort = DefaultGNMIPort
	}

	// Build list of ports to try - prioritize configured port, then common ports
	commonGNMIPorts := []string{"8080", "50052"}
	portsToTry := []string{gnmiPort}
	for _, commonPort := range commonGNMIPorts {
		if commonPort != gnmiPort {
			portsToTry = append(portsToTry, commonPort)
		}
	}

	return &DPUInfo{
		Index:          dpuIndex,
		IPAddress:      ipAddr,
		Reachable:      reachable,
		GNMIPort:       gnmiPort,
		GNMIPortsToTry: portsToTry,
	}, nil
}
