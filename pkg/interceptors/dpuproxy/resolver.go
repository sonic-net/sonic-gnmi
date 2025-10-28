package dpuproxy

import (
	"context"
	"fmt"
)

const (
	// StateDB is database 6 in SONiC Redis
	StateDB = 6

	// DefaultRedisSocket is the default Unix socket path for SONiC Redis
	DefaultRedisSocket = "/var/run/redis/redis.sock"

	// ChassisMidplaneTablePrefix is the Redis key prefix for DPU midplane info
	ChassisMidplaneTablePrefix = "CHASSIS_MIDPLANE_TABLE|DPU"
)

// DPUInfo contains information about a DPU retrieved from Redis.
type DPUInfo struct {
	// Index is the DPU number (e.g., "0", "1", "2")
	Index string

	// IPAddress is the IP address of the DPU
	IPAddress string

	// Reachable indicates if the DPU is currently reachable
	Reachable bool
}

// DPUResolver resolves DPU information from Redis.
type DPUResolver struct {
	client RedisClient
}

// NewDPUResolver creates a new DPU resolver with the given Redis client.
func NewDPUResolver(client RedisClient) *DPUResolver {
	return &DPUResolver{
		client: client,
	}
}

// GetDPUInfo retrieves DPU information from Redis by DPU index.
// It queries the CHASSIS_MIDPLANE_TABLE in STATE_DB (database 6).
//
// Returns:
//   - DPUInfo with the DPU details if found
//   - error if the DPU doesn't exist or Redis query fails
func (r *DPUResolver) GetDPUInfo(ctx context.Context, dpuIndex string) (*DPUInfo, error) {
	// Build Redis key: CHASSIS_MIDPLANE_TABLE|DPU<index>
	key := fmt.Sprintf("%s%s", ChassisMidplaneTablePrefix, dpuIndex)

	// Query Redis for all fields in this hash
	fields, err := r.client.HGetAll(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to query Redis for DPU%s: %w", dpuIndex, err)
	}

	// Check if key exists (empty map means key not found)
	if len(fields) == 0 {
		return nil, fmt.Errorf("DPU%s not found in Redis", dpuIndex)
	}

	// Extract IP address
	ipAddr, ok := fields["ip_address"]
	if !ok || ipAddr == "" {
		return nil, fmt.Errorf("DPU%s missing ip_address field", dpuIndex)
	}

	// Extract reachability status
	// Redis stores "True" or "False" as strings
	accessStr, ok := fields["access"]
	reachable := ok && accessStr == "True"

	return &DPUInfo{
		Index:     dpuIndex,
		IPAddress: ipAddr,
		Reachable: reachable,
	}, nil
}
