// Package gnoi provides gNOI service clients.
package gnoi

import (
	"context"
	"encoding/hex"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/openconfig/gnoi/common"
	"github.com/openconfig/gnoi/system"
	"github.com/openconfig/gnoi/types"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
)

// SetPackageParams contains the parameters for a SetPackage operation.
type SetPackageParams struct {
	// URL is the HTTP URL to download the package from
	URL string

	// Filename is the destination path on the device
	Filename string

	// MD5 is the expected MD5 checksum (hex string)
	MD5 string

	// Version is the package version (optional)
	Version string

	// Activate indicates whether to activate the package after installation (optional)
	Activate bool
}

// SystemClient provides access to gNOI System service methods.
type SystemClient struct {
	conn   *grpc.ClientConn
	client system.SystemClient
}

// NewSystemClient creates a new SystemClient with the given configuration.
func NewSystemClient(cfg *config.Config) (*SystemClient, error) {
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

	return &SystemClient{
		conn:   conn,
		client: system.NewSystemClient(conn),
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *SystemClient) Close() error {
	return c.conn.Close()
}

// SetPackage installs a software package on the target device via remote download.
func (c *SystemClient) SetPackage(ctx context.Context, params *SetPackageParams) error {
	// Create the gRPC stream
	stream, err := c.client.SetPackage(ctx)
	if err != nil {
		return fmt.Errorf("failed to create SetPackage stream: %w", err)
	}

	// Send package metadata
	packageMsg := &system.SetPackageRequest{
		Request: &system.SetPackageRequest_Package{
			Package: &system.Package{
				Filename: params.Filename,
				Version:  params.Version,
				Activate: params.Activate,
				RemoteDownload: &common.RemoteDownload{
					Path:     params.URL,
					Protocol: common.RemoteDownload_HTTP,
				},
			},
		},
	}

	if err := stream.Send(packageMsg); err != nil {
		return fmt.Errorf("failed to send package info: %w", err)
	}

	// Convert MD5 hex string to bytes
	md5Bytes, err := hex.DecodeString(params.MD5)
	if err != nil {
		return fmt.Errorf("invalid MD5 checksum format: %w", err)
	}

	// Send hash message
	hashMsg := &system.SetPackageRequest{
		Request: &system.SetPackageRequest_Hash{
			Hash: &types.HashType{
				Method: types.HashType_MD5,
				Hash:   md5Bytes,
			},
		},
	}

	if err := stream.Send(hashMsg); err != nil {
		return fmt.Errorf("failed to send hash info: %w", err)
	}

	// Close send side and wait for response
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("SetPackage failed: %w", err)
	}

	// Response is empty on success
	_ = resp
	return nil
}

// Future System service methods to be implemented:
// - Reboot(ctx, *RebootParams) error
// - RebootStatus(ctx, *RebootStatusParams) (*RebootStatusResult, error)
// - CancelReboot(ctx, *CancelRebootParams) error
// - KillProcess(ctx, *KillProcessParams) error
// - Ping(ctx, *PingParams) (stream *PingResult, error)
// - Traceroute(ctx, *TracerouteParams) (stream *TracerouteResult, error)
// - Time(ctx, *TimeParams) (*TimeResult, error)
// - SwitchControlProcessor(ctx, *SwitchControlProcessorParams) error
