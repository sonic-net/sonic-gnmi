package steps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/golang/glog"
	"github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow"
)

const (
	// CheckDiskSpaceStepType is the step type identifier for disk space checks.
	CheckDiskSpaceStepType = "check-disk-space"
)

// CheckDiskSpaceStep verifies that a filesystem path has sufficient available disk space.
// It queries the target device using gNMI to get disk space information and fails
// if available space is below the specified threshold.
type CheckDiskSpaceStep struct {
	// name is the human-readable name for this step
	name string

	// Path is the filesystem path to check (e.g., "/", "/tmp", "/var").
	Path string `yaml:"path" json:"path"`

	// MinAvailableMB is the minimum required available space in megabytes.
	// The step will fail if available space is less than this value.
	MinAvailableMB int64 `yaml:"min_available_mb" json:"min_available_mb"`
}

// NewCheckDiskSpaceStep creates a new CheckDiskSpaceStep from raw parameters.
func NewCheckDiskSpaceStep(name string, params map[string]interface{}) (workflow.Step, error) {
	step := &CheckDiskSpaceStep{name: name}

	// Extract path parameter
	if path, ok := params["path"].(string); ok {
		step.Path = path
	} else {
		return nil, fmt.Errorf("missing or invalid 'path' parameter")
	}

	// Extract min_available_mb parameter
	// Handle both int and float64 since YAML parsing may produce either
	switch v := params["min_available_mb"].(type) {
	case int:
		step.MinAvailableMB = int64(v)
	case int64:
		step.MinAvailableMB = v
	case float64:
		step.MinAvailableMB = int64(v)
	default:
		return nil, fmt.Errorf("missing or invalid 'min_available_mb' parameter")
	}

	// Validate parameters
	if err := step.validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return step, nil
}

// GetName returns the human-readable name of this step.
func (s *CheckDiskSpaceStep) GetName() string {
	return s.name
}

// GetType returns the step type identifier.
func (s *CheckDiskSpaceStep) GetType() string {
	return CheckDiskSpaceStepType
}

// Validate checks that all required fields are properly set.
func (s *CheckDiskSpaceStep) Validate() error {
	if s.Path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	if s.MinAvailableMB <= 0 {
		return fmt.Errorf("min_available_mb must be positive, got %d", s.MinAvailableMB)
	}

	return nil
}

// validate is an internal validation helper (for backward compatibility).
func (s *CheckDiskSpaceStep) validate() error {
	return s.Validate()
}

// Execute performs the disk space check by querying the target device via gNMI.
func (s *CheckDiskSpaceStep) Execute(ctx context.Context, clientConfig interface{}) error {
	glog.Infof("Checking disk space for path %s (minimum: %d MB)", s.Path, s.MinAvailableMB)

	// Type assert clientConfig to map[string]interface{}
	config, ok := clientConfig.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid client configuration type")
	}

	// Extract connection parameters from clientConfig
	serverAddr, ok := config["server_addr"].(string)
	if !ok || serverAddr == "" {
		return fmt.Errorf("server_addr not found in client configuration")
	}

	useTLS, _ := config["use_tls"].(bool)

	// Create gNMI client connection
	conn, err := s.createConnection(serverAddr, useTLS)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", serverAddr, err)
	}
	defer conn.Close()

	// Create gNMI client
	client := gnmi.NewGNMIClient(conn)

	// Query disk space information
	availableMB, totalMB, err := s.queryDiskSpace(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to query disk space: %w", err)
	}

	glog.Infof("Disk space for %s: %d MB available / %d MB total", s.Path, availableMB, totalMB)

	// Check if available space meets requirement
	if availableMB < s.MinAvailableMB {
		return fmt.Errorf("insufficient disk space on %s: %d MB available, %d MB required",
			s.Path, availableMB, s.MinAvailableMB)
	}

	glog.Infof("Disk space check passed: %d MB available >= %d MB required",
		availableMB, s.MinAvailableMB)

	return nil
}

// createConnection establishes a gRPC connection to the target server.
func (s *CheckDiskSpaceStep) createConnection(serverAddr string, useTLS bool) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	if !useTLS {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		// TODO: Add TLS configuration support
		return nil, fmt.Errorf("TLS not yet implemented for disk space checks")
	}

	return grpc.Dial(serverAddr, opts...)
}

// queryDiskSpace queries the target device for disk space information using gNMI.
func (s *CheckDiskSpaceStep) queryDiskSpace(ctx context.Context, client gnmi.GNMIClient) (availableMB, totalMB int64, err error) {
	// Build the gNMI path for disk space query
	// Path format: /system/filesystem/disk-space[path=/some/path]/available-mb
	path := &gnmi.Path{
		Elem: []*gnmi.PathElem{
			{Name: "system"},
			{Name: "filesystem"},
			{
				Name: "disk-space",
				Key:  map[string]string{"path": s.Path},
			},
		},
	}

	// Create Get request
	req := &gnmi.GetRequest{
		Path: []*gnmi.Path{path},
		Type: gnmi.GetRequest_STATE,
	}

	// Execute Get request
	resp, err := client.Get(ctx, req)
	if err != nil {
		return 0, 0, fmt.Errorf("gNMI Get request failed: %w", err)
	}

	// Parse response
	if len(resp.Notification) == 0 || len(resp.Notification[0].Update) == 0 {
		return 0, 0, fmt.Errorf("no data received from device")
	}

	// Extract JSON value
	jsonVal := resp.Notification[0].Update[0].Val.GetJsonVal()
	if jsonVal == nil {
		return 0, 0, fmt.Errorf("unexpected response format: not JSON")
	}

	// Parse JSON response
	var diskInfo struct {
		AvailableMB int64 `json:"available-mb"`
		TotalMB     int64 `json:"total-mb"`
	}

	if err := json.Unmarshal(jsonVal, &diskInfo); err != nil {
		return 0, 0, fmt.Errorf("failed to parse disk space response: %w", err)
	}

	return diskInfo.AvailableMB, diskInfo.TotalMB, nil
}
