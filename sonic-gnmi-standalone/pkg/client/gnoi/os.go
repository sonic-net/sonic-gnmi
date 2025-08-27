// Package gnoi provides gNOI service clients.
package gnoi

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/openconfig/gnoi/os"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
)

// ActivateParams contains the parameters for an Activate operation.
type ActivateParams struct {
	// Version is the OS version to activate (required)
	Version string

	// StandbySupervisor indicates whether to activate on standby supervisor (optional)
	StandbySupervisor bool

	// NoReboot indicates whether to activate without rebooting (optional)
	NoReboot bool
}

// OSClient provides access to gNOI OS service methods.
type OSClient struct {
	conn   *grpc.ClientConn
	client os.OSClient
}

// NewOSClient creates a new OSClient with the given configuration.
func NewOSClient(cfg *config.Config) (*OSClient, error) {
	// Set up connection options
	var opts []grpc.DialOption

	if cfg.TLS {
		if cfg.TLSConfig != nil {
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(cfg.TLSConfig)))
		} else {
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(nil)))
		}
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Connect to server
	conn, err := grpc.Dial(cfg.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", cfg.Address, err)
	}

	return &OSClient{
		conn:   conn,
		client: os.NewOSClient(conn),
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *OSClient) Close() error {
	return c.conn.Close()
}

// Activate triggers OS version activation on the target device.
func (c *OSClient) Activate(ctx context.Context, params *ActivateParams) error {
	// Create activate request
	req := &os.ActivateRequest{
		Version:           params.Version,
		StandbySupervisor: params.StandbySupervisor,
		NoReboot:          params.NoReboot,
	}

	// Execute activate
	resp, err := c.client.Activate(ctx, req)
	if err != nil {
		return fmt.Errorf("activate request failed: %w", err)
	}

	// Check response type
	switch r := resp.GetResponse().(type) {
	case *os.ActivateResponse_ActivateOk:
		// Success - nothing more to do
		return nil
	case *os.ActivateResponse_ActivateError:
		// Handle specific error types
		errDetail := ""
		if r.ActivateError.Detail != "" {
			errDetail = ": " + r.ActivateError.Detail
		}

		switch r.ActivateError.Type {
		case os.ActivateError_NON_EXISTENT_VERSION:
			return fmt.Errorf("version does not exist%s", errDetail)
		case os.ActivateError_NOT_SUPPORTED_ON_BACKUP:
			return fmt.Errorf("activation not supported on backup supervisor%s", errDetail)
		default:
			return fmt.Errorf("activation failed%s", errDetail)
		}
	default:
		return fmt.Errorf("unexpected response type: %T", r)
	}
}

// Verify retrieves the current OS version information.
func (c *OSClient) Verify(ctx context.Context) (string, error) {
	// Create verify request (empty message)
	req := &os.VerifyRequest{}

	// Execute verify
	resp, err := c.client.Verify(ctx, req)
	if err != nil {
		return "", fmt.Errorf("verify request failed: %w", err)
	}

	return resp.GetVersion(), nil
}

// Future OS service methods to be implemented:
// - Install(ctx, *InstallParams) error - for installing new OS versions
