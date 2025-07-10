package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/client/config"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

// Client wraps the gRPC clients for SONiC upgrade service.
type Client struct {
	conn         *Connection
	systemInfo   pb.SystemInfoClient
	firmwareMgmt pb.FirmwareManagementClient
}

// NewClient creates a new gRPC client from configuration.
func NewClient(cfg *config.Config) (*Client, error) {
	// Convert config to connection config
	connConfig := ConnectionConfig{
		Address:        cfg.Spec.Server.Address,
		TLSEnabled:     *cfg.Spec.Server.TLSEnabled,
		TLSCertFile:    cfg.Spec.Server.TLSCertFile,
		TLSKeyFile:     cfg.Spec.Server.TLSKeyFile,
		ConnectTimeout: cfg.GetConnectTimeout(),
		MaxRetries:     3, // Default retries
		RetryDelay:     1 * time.Second,
	}

	// Create connection
	conn, err := NewConnection(connConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	glog.V(1).Infof("Connected to upgrade service at %s", cfg.Spec.Server.Address)

	return &Client{
		conn:         conn,
		systemInfo:   pb.NewSystemInfoClient(conn.Conn()),
		firmwareMgmt: pb.NewFirmwareManagementClient(conn.Conn()),
	}, nil
}

// Close closes the client connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsConnected checks if the client is connected.
func (c *Client) IsConnected() bool {
	return c.conn != nil && c.conn.IsConnected()
}

// Reconnect attempts to reconnect the client.
func (c *Client) Reconnect() error {
	if c.conn == nil {
		return fmt.Errorf("no connection to reconnect")
	}

	err := c.conn.Reconnect()
	if err != nil {
		return err
	}

	// Recreate clients with new connection
	c.systemInfo = pb.NewSystemInfoClient(c.conn.Conn())
	c.firmwareMgmt = pb.NewFirmwareManagementClient(c.conn.Conn())

	return nil
}

// SystemInfo Methods

// GetPlatformType retrieves the platform type from the server.
func (c *Client) GetPlatformType(ctx context.Context) (*pb.GetPlatformTypeResponse, error) {
	req := &pb.GetPlatformTypeRequest{}

	glog.V(2).Infof("Requesting platform type")
	resp, err := c.systemInfo.GetPlatformType(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("GetPlatformType failed: %w", err)
	}

	glog.V(2).Infof("Platform type: %s", resp.PlatformIdentifier)
	return resp, nil
}

// GetDiskSpace retrieves disk space information from the server.
func (c *Client) GetDiskSpace(ctx context.Context, paths []string) (*pb.GetDiskSpaceResponse, error) {
	req := &pb.GetDiskSpaceRequest{
		Paths: paths,
	}

	glog.V(2).Infof("Requesting disk space for paths: %v", paths)
	resp, err := c.systemInfo.GetDiskSpace(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("GetDiskSpace failed: %w", err)
	}

	glog.V(2).Infof("Retrieved disk space for %d filesystems", len(resp.Filesystems))
	return resp, nil
}

// FirmwareManagement Methods

// DownloadFirmware initiates a firmware download.
func (c *Client) DownloadFirmware(
	ctx context.Context, url, outputPath string, opts *DownloadOptions,
) (*pb.DownloadFirmwareResponse, error) {
	req := &pb.DownloadFirmwareRequest{
		Url:        url,
		OutputPath: outputPath,
	}

	if opts != nil {
		if opts.ConnectTimeout > 0 {
			req.ConnectTimeoutSeconds = int32(opts.ConnectTimeout.Seconds())
		}
		if opts.TotalTimeout > 0 {
			req.TotalTimeoutSeconds = int32(opts.TotalTimeout.Seconds())
		}
		if opts.ExpectedMD5 != "" {
			req.ExpectedMd5 = opts.ExpectedMD5
		}
	}

	glog.V(1).Infof("Starting firmware download: %s -> %s", url, outputPath)
	resp, err := c.firmwareMgmt.DownloadFirmware(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("DownloadFirmware failed: %w", err)
	}

	glog.V(1).Infof("Download started with session ID: %s", resp.SessionId)
	return resp, nil
}

// GetDownloadStatus retrieves the status of a download session.
func (c *Client) GetDownloadStatus(ctx context.Context, sessionID string) (*pb.GetDownloadStatusResponse, error) {
	req := &pb.GetDownloadStatusRequest{
		SessionId: sessionID,
	}

	glog.V(2).Infof("Getting download status for session: %s", sessionID)
	resp, err := c.firmwareMgmt.GetDownloadStatus(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("GetDownloadStatus failed: %w", err)
	}

	// Log status based on state
	switch state := resp.State.(type) {
	case *pb.GetDownloadStatusResponse_Starting:
		glog.V(2).Infof("Download starting: %s", state.Starting.Message)
	case *pb.GetDownloadStatusResponse_Progress:
		glog.V(3).Infof("Download progress: %.1f%% (%d/%d bytes) @ %.1f KB/s",
			state.Progress.Percentage,
			state.Progress.DownloadedBytes,
			state.Progress.TotalBytes,
			state.Progress.SpeedBytesPerSec/1024)
	case *pb.GetDownloadStatusResponse_Result:
		glog.V(2).Infof("Download completed: %s (%d bytes)",
			state.Result.FilePath, state.Result.FileSizeBytes)
	case *pb.GetDownloadStatusResponse_Error:
		glog.V(2).Infof("Download failed: %s", state.Error.Message)
	}

	return resp, nil
}

// ListFirmwareImages lists available firmware images.
func (c *Client) ListFirmwareImages(
	ctx context.Context, searchDirs []string, versionPattern string,
) (*pb.ListFirmwareImagesResponse, error) {
	req := &pb.ListFirmwareImagesRequest{
		SearchDirectories: searchDirs,
		VersionPattern:    versionPattern,
	}

	glog.V(2).Infof("Listing firmware images in directories: %v", searchDirs)
	resp, err := c.firmwareMgmt.ListFirmwareImages(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ListFirmwareImages failed: %w", err)
	}

	glog.V(2).Infof("Found %d firmware images", len(resp.Images))
	return resp, nil
}

// CleanupOldFirmware removes old firmware files.
func (c *Client) CleanupOldFirmware(ctx context.Context) (*pb.CleanupOldFirmwareResponse, error) {
	req := &pb.CleanupOldFirmwareRequest{}

	glog.V(1).Infof("Starting firmware cleanup")
	resp, err := c.firmwareMgmt.CleanupOldFirmware(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("CleanupOldFirmware failed: %w", err)
	}

	glog.V(1).Infof("Cleanup completed: %d files deleted, %d bytes freed",
		resp.FilesDeleted, resp.SpaceFreedBytes)
	return resp, nil
}

// ConsolidateImages consolidates installed SONiC images.
func (c *Client) ConsolidateImages(ctx context.Context, dryRun bool) (*pb.ConsolidateImagesResponse, error) {
	req := &pb.ConsolidateImagesRequest{
		DryRun: dryRun,
	}

	glog.V(1).Infof("Starting image consolidation (dry_run=%v)", dryRun)
	resp, err := c.firmwareMgmt.ConsolidateImages(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ConsolidateImages failed: %w", err)
	}

	glog.V(1).Infof("Consolidation completed: current=%s, removed=%d images",
		resp.CurrentImage, len(resp.RemovedImages))
	return resp, nil
}

// ListImages lists installed SONiC images.
func (c *Client) ListImages(ctx context.Context) (*pb.ListImagesResponse, error) {
	req := &pb.ListImagesRequest{}

	glog.V(2).Infof("Listing installed images")
	resp, err := c.firmwareMgmt.ListImages(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ListImages failed: %w", err)
	}

	glog.V(2).Infof("Found %d installed images, current: %s, next: %s",
		len(resp.Images), resp.CurrentImage, resp.NextImage)
	return resp, nil
}

// DownloadOptions contains optional parameters for firmware downloads.
type DownloadOptions struct {
	ConnectTimeout time.Duration
	TotalTimeout   time.Duration
	ExpectedMD5    string
}
