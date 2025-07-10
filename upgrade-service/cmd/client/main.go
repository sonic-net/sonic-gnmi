package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/client/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/client/grpc"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// Global flags.
	configFile string
	serverAddr string
	noTLS      bool
	verbose    bool
	dryRun     bool

	// Advanced logging flags.
	logDir      string
	logToStderr bool
	logLevel    int

	// Root command.
	rootCmd = &cobra.Command{
		Use:   "sonic-upgrade-client",
		Short: "SONiC upgrade client",
		Long:  "A client for managing SONiC firmware upgrades via the upgrade service",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Configure glog based on flags

			// Simple verbose flag sets defaults
			if verbose {
				if logLevel == 0 {
					logLevel = 2 // Default verbose level
				}
				if !logToStderr && logDir == "" {
					logToStderr = true // Default to stderr when verbose
				}
			}

			// Set glog flags directly
			if logToStderr {
				flag.Set("logtostderr", "true")
			}
			if logDir != "" {
				flag.Set("log_dir", logDir)
			}
			if logLevel > 0 {
				flag.Set("v", fmt.Sprintf("%d", logLevel))
			}

			// Ensure flag parsing is done for glog
			flag.Parse()
		},
	}

	// Apply command.
	applyCmd = &cobra.Command{
		Use:   "apply",
		Short: "Apply upgrade configuration from file",
		Long:  "Apply an upgrade configuration to download and prepare firmware",
		RunE:  runApply,
	}

	// Download command.
	downloadCmd = &cobra.Command{
		Use:   "download",
		Short: "Download firmware directly",
		Long:  "Download firmware from a URL without using a config file",
		RunE:  runDownload,
	}

	// Status command.
	statusCmd = &cobra.Command{
		Use:   "status",
		Short: "Get download status",
		Long:  "Check the status of an ongoing or completed download",
		RunE:  runStatus,
	}

	// List images command.
	listImagesCmd = &cobra.Command{
		Use:   "list-images",
		Short: "List installed images",
		Long:  "List all installed SONiC images on the system",
		RunE:  runListImages,
	}

	// Disk space command.
	diskSpaceCmd = &cobra.Command{
		Use:   "disk-space",
		Short: "Show disk space information",
		Long:  "Display disk space information for relevant filesystems",
		RunE:  runDiskSpace,
	}
)

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "f", "", "Path to configuration file")
	rootCmd.PersistentFlags().StringVar(&serverAddr, "server", "", "Override server address")
	rootCmd.PersistentFlags().BoolVar(&noTLS, "no-tls", false, "Disable TLS")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes")

	// Advanced logging flags for power users
	rootCmd.PersistentFlags().StringVar(&logDir, "log-dir", "", "Directory to write log files (advanced)")
	rootCmd.PersistentFlags().BoolVar(&logToStderr, "logtostderr", false, "Log to stderr instead of files (advanced)")
	rootCmd.PersistentFlags().IntVar(&logLevel, "log-level", 0, "Log verbosity level 0-3 (advanced)")

	// Command-specific flags
	var downloadURL, outputPath, sessionID string
	downloadCmd.Flags().StringVar(&downloadURL, "url", "", "URL to download from")
	downloadCmd.Flags().StringVar(&outputPath, "output", "", "Output file path")
	downloadCmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID for resuming download")

	statusCmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to check status for")
	statusCmd.MarkFlagRequired("session-id")

	// Add commands to root
	rootCmd.AddCommand(applyCmd)
	rootCmd.AddCommand(downloadCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listImagesCmd)
	rootCmd.AddCommand(diskSpaceCmd)
}

func main() {
	// Initialize glog
	defer glog.Flush()

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runApply(cmd *cobra.Command, args []string) error {
	if configFile == "" {
		return fmt.Errorf("config file required (use -f or --config)")
	}

	// Validate config file exists and is readable
	if err := validateConfigFile(configFile); err != nil {
		return err
	}

	// Load configuration
	cfg, err := config.LoadFromFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config from '%s': %w", configFile, err)
	}

	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	// Override server address if provided
	if serverAddr != "" {
		cfg.Spec.Server.Address = serverAddr
	}

	// Override TLS if provided
	if noTLS {
		cfg.Spec.Server.TLSEnabled = boolPtr(false)
	}

	// Log configuration
	glog.V(1).Infof("Loaded configuration from %s", configFile)
	glog.V(2).Infof("Configuration: %+v", cfg)

	if dryRun {
		glog.Info("Running in dry-run mode")
		fmt.Println("Dry run mode - no changes will be made")
		fmt.Printf("Would download firmware:\n")
		fmt.Printf("  Version: %s\n", cfg.Spec.Firmware.DesiredVersion)
		fmt.Printf("  URL: %s\n", cfg.Spec.Firmware.DownloadURL)
		fmt.Printf("  Save to: %s\n", cfg.Spec.Firmware.SavePath)
		fmt.Printf("  Server: %s (TLS: %v)\n", cfg.Spec.Server.Address, *cfg.Spec.Server.TLSEnabled)
		glog.V(1).Infof("Configuration loaded successfully from %s", configFile)
		glog.V(2).Infof("Full configuration: %+v", cfg)
		return nil
	}

	// Create gRPC client with retry logic
	client, err := createClientWithRetry(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	// Create context with timeout and signal handling
	ctx, cancel := createContextWithSignals(30 * time.Minute)
	defer cancel()

	// Start the download
	fmt.Printf("Starting firmware download...\n")
	fmt.Printf("  Version: %s\n", cfg.Spec.Firmware.DesiredVersion)
	fmt.Printf("  URL: %s\n", cfg.Spec.Firmware.DownloadURL)
	fmt.Printf("  Save to: %s\n", cfg.Spec.Firmware.SavePath)

	downloadOpts := &grpc.DownloadOptions{
		ConnectTimeout: cfg.GetConnectTimeout(),
		TotalTimeout:   cfg.GetTotalTimeout(),
		ExpectedMD5:    cfg.Spec.Firmware.ExpectedMD5,
	}

	resp, err := client.DownloadFirmware(ctx, cfg.Spec.Firmware.DownloadURL, cfg.Spec.Firmware.SavePath, downloadOpts)
	if err != nil {
		return handleGRPCError(err, "download firmware")
	}

	fmt.Printf("\nDownload started successfully!\n")
	fmt.Printf("Session ID: %s\n", resp.SessionId)
	fmt.Printf("Status: %s\n", resp.Status)

	// Monitor progress
	fmt.Println("\nMonitoring download progress...")
	return monitorDownloadProgress(ctx, client, resp.SessionId)
}

func runDownload(cmd *cobra.Command, args []string) error {
	url, _ := cmd.Flags().GetString("url")
	output, _ := cmd.Flags().GetString("output")
	sessionID, _ := cmd.Flags().GetString("session-id")

	if url == "" && sessionID == "" {
		return fmt.Errorf("either --url or --session-id required")
	}

	// Log the values for debugging
	glog.V(2).Infof("Download command: url=%s, output=%s, sessionID=%s", url, output, sessionID)

	// Download logic will be implemented in Phase 3 (download client)
	return fmt.Errorf("download command not yet implemented")
}

func runStatus(cmd *cobra.Command, args []string) error {
	sessionID, _ := cmd.Flags().GetString("session-id")

	// Validate session ID
	if err := validateSessionID(sessionID); err != nil {
		return err
	}

	// Log the value for debugging
	glog.V(2).Infof("Status command: sessionID=%s", sessionID)

	// Create minimal config for client
	cfg := &config.Config{
		Spec: config.Spec{
			Server: config.ServerSpec{
				Address:    serverAddr,
				TLSEnabled: boolPtr(!noTLS),
			},
		},
	}

	// Set defaults
	cfg.SetDefaults()

	// Create gRPC client with retry logic
	client, err := createClientWithRetry(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	// Get download status with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status, err := client.GetDownloadStatus(ctx, sessionID)
	if err != nil {
		return handleGRPCError(err, "get download status")
	}

	// Display status
	fmt.Printf("Session ID: %s\n", status.SessionId)

	switch state := status.State.(type) {
	case *pb.GetDownloadStatusResponse_Starting:
		fmt.Printf("Status: Starting\n")
		fmt.Printf("Message: %s\n", state.Starting.Message)

	case *pb.GetDownloadStatusResponse_Progress:
		fmt.Printf("Status: In Progress\n")
		fmt.Printf("Progress: %d%%\n", int(state.Progress.Percentage))
		fmt.Printf("Downloaded: %s / %s\n",
			formatBytes(state.Progress.DownloadedBytes),
			formatBytes(state.Progress.TotalBytes))
		fmt.Printf("Speed: %s/s\n", formatBytes(int64(state.Progress.SpeedBytesPerSec)))
		fmt.Printf("Method: %s\n", state.Progress.CurrentMethod)

	case *pb.GetDownloadStatusResponse_Result:
		fmt.Printf("Status: Completed\n")
		fmt.Printf("File: %s\n", state.Result.FilePath)
		fmt.Printf("Size: %s\n", formatBytes(state.Result.FileSizeBytes))
		if state.Result.ChecksumValidation != nil && state.Result.ChecksumValidation.ActualChecksum != "" {
			fmt.Printf("MD5: %s\n", state.Result.ChecksumValidation.ActualChecksum)
		}

	case *pb.GetDownloadStatusResponse_Error:
		fmt.Printf("Status: Failed\n")
		fmt.Printf("Error: %s\n", state.Error.Message)
		if state.Error.Category != "" {
			fmt.Printf("Category: %s\n", state.Error.Category)
		}
	}

	return nil
}

func runListImages(cmd *cobra.Command, args []string) error {
	// Create minimal config for client
	cfg := &config.Config{
		Spec: config.Spec{
			Server: config.ServerSpec{
				Address:    serverAddr,
				TLSEnabled: boolPtr(!noTLS),
			},
		},
	}

	// Set defaults
	cfg.SetDefaults()

	// Create gRPC client with retry logic
	client, err := createClientWithRetry(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	// List images with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.ListImages(ctx)
	if err != nil {
		return handleGRPCError(err, "list images")
	}

	// Display images
	fmt.Println("Installed SONiC Images:")
	fmt.Println("=======================")

	for _, image := range resp.Images {
		prefix := "  "
		if image == resp.CurrentImage {
			prefix = "* "
		}
		fmt.Printf("%s%s", prefix, image)
		if image == resp.NextImage {
			fmt.Printf(" (next)")
		}
		fmt.Println()
	}

	fmt.Printf("\nCurrent: %s\n", resp.CurrentImage)
	fmt.Printf("Next:    %s\n", resp.NextImage)

	return nil
}

func runDiskSpace(cmd *cobra.Command, args []string) error {
	// Create minimal config for client
	cfg := &config.Config{
		Spec: config.Spec{
			Server: config.ServerSpec{
				Address:    serverAddr,
				TLSEnabled: boolPtr(!noTLS),
			},
		},
	}

	// Set defaults
	cfg.SetDefaults()

	// Create gRPC client with retry logic
	client, err := createClientWithRetry(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	// Validate and prepare paths
	paths := []string{"/", "/host", "/tmp"} // Default paths
	if len(args) > 0 {
		paths = args
		// Validate paths
		for _, path := range paths {
			if err := validatePath(path); err != nil {
				return fmt.Errorf("invalid path '%s': %w", path, err)
			}
		}
	}

	// Get disk space with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.GetDiskSpace(ctx, paths)
	if err != nil {
		return handleGRPCError(err, "get disk space")
	}

	// Display disk space information
	fmt.Println("Filesystem Usage:")
	fmt.Println("=================")
	fmt.Printf("%-30s %10s %10s %10s %6s\n", "Path", "Total", "Used", "Free", "Use%")
	fmt.Println(strings.Repeat("-", 70))

	for _, fs := range resp.Filesystems {
		if fs.ErrorMessage != "" {
			fmt.Printf("%-30s Error: %s\n", fs.Path, fs.ErrorMessage)
			continue
		}

		totalMB := float64(fs.TotalMb)
		usedMB := float64(fs.UsedMb)
		usePercent := 0.0
		if totalMB > 0 {
			usePercent = (usedMB / totalMB) * 100
		}

		fmt.Printf("%-30s %10s %10s %10s %5.1f%%\n",
			fs.Path,
			formatMB(uint64(fs.TotalMb)),
			formatMB(uint64(fs.UsedMb)),
			formatMB(uint64(fs.FreeMb)),
			usePercent)
	}

	return nil
}

func boolPtr(b bool) *bool {
	return &b
}

// formatMB formats megabytes into human readable format.
func formatMB(mb uint64) string {
	const unit = 1024
	if mb < unit {
		return fmt.Sprintf("%d MB", mb)
	}
	div, exp := uint64(unit), 0
	for n := mb / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(mb)/float64(div), "GTPE"[exp])
}

// monitorDownloadProgress monitors the download progress until completion.
func monitorDownloadProgress(ctx context.Context, client *grpc.Client, sessionID string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastProgress := -1
	spinner := []string{"-", "\\", "|", "/"}
	spinnerIdx := 0

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("download monitoring canceled: %w", ctx.Err())
		case <-ticker.C:
			status, err := client.GetDownloadStatus(ctx, sessionID)
			if err != nil {
				return fmt.Errorf("failed to get download status: %w", err)
			}

			switch state := status.State.(type) {
			case *pb.GetDownloadStatusResponse_Starting:
				fmt.Printf("\r%s Starting: %s", spinner[spinnerIdx], state.Starting.Message)
				spinnerIdx = (spinnerIdx + 1) % len(spinner)

			case *pb.GetDownloadStatusResponse_Progress:
				progress := int(state.Progress.Percentage)
				if progress != lastProgress {
					lastProgress = progress
					downloaded := formatBytes(state.Progress.DownloadedBytes)
					total := formatBytes(state.Progress.TotalBytes)
					speed := formatBytes(int64(state.Progress.SpeedBytesPerSec))
					fmt.Printf("\r[%-50s] %3d%% %s/%s @ %s/s    ",
						progressBar(progress, 50),
						progress,
						downloaded,
						total,
						speed)
				}

			case *pb.GetDownloadStatusResponse_Result:
				fmt.Printf("\n\n✓ Download completed successfully!\n")
				fmt.Printf("  File: %s\n", state.Result.FilePath)
				fmt.Printf("  Size: %s\n", formatBytes(state.Result.FileSizeBytes))
				if state.Result.ChecksumValidation != nil && state.Result.ChecksumValidation.ActualChecksum != "" {
					fmt.Printf("  MD5: %s\n", state.Result.ChecksumValidation.ActualChecksum)
				}
				return nil

			case *pb.GetDownloadStatusResponse_Error:
				fmt.Printf("\n\n✗ Download failed: %s\n", state.Error.Message)
				if state.Error.Category != "" {
					fmt.Printf("  Category: %s\n", state.Error.Category)
				}
				return fmt.Errorf("download failed: %s", state.Error.Message)
			}
		}
	}
}

// progressBar creates a visual progress bar.
func progressBar(percent int, width int) string {
	filled := width * percent / 100
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "="
		} else {
			bar += "-"
		}
	}
	return bar
}

// formatBytes formats bytes into human readable format.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// validateConfigFile validates that the config file exists and is readable.
func validateConfigFile(path string) error {
	if path == "" {
		return fmt.Errorf("config file path cannot be empty")
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config file '%s' does not exist", path)
		}
		return fmt.Errorf("cannot access config file '%s': %w", path, err)
	}

	// Check if it's a regular file
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config file '%s' is not a regular file", path)
	}

	// Check if we can read it
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read config file '%s': %w", path, err)
	}
	file.Close()

	return nil
}

// validateConfig validates the configuration object.
func validateConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("configuration is nil")
	}

	// Validate server configuration
	if cfg.Spec.Server.Address == "" {
		return fmt.Errorf("server address is required")
	}

	// Validate server address format
	if err := validateServerAddress(cfg.Spec.Server.Address); err != nil {
		return fmt.Errorf("invalid server address: %w", err)
	}

	// Validate firmware configuration for apply command
	if cfg.Spec.Firmware.DownloadURL != "" {
		if err := validateURL(cfg.Spec.Firmware.DownloadURL); err != nil {
			return fmt.Errorf("invalid download URL: %w", err)
		}
	}

	if cfg.Spec.Firmware.SavePath != "" {
		if err := validateSavePath(cfg.Spec.Firmware.SavePath); err != nil {
			return fmt.Errorf("invalid save path: %w", err)
		}
	}

	return nil
}

// validateServerAddress validates server address format.
func validateServerAddress(addr string) error {
	if addr == "" {
		return fmt.Errorf("address cannot be empty")
	}

	// Simple validation - should contain host:port format
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		return fmt.Errorf("address must be in host:port format, got '%s'", addr)
	}

	host, port := parts[0], parts[1]
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	if port == "" {
		return fmt.Errorf("port cannot be empty")
	}

	return nil
}

// validateURL validates URL format.
func validateURL(urlStr string) error {
	if urlStr == "" {
		return fmt.Errorf("URL cannot be empty")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if parsedURL.Scheme == "" {
		return fmt.Errorf("URL must include scheme (http/https)")
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL must include host")
	}

	// Check for supported schemes
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme '%s', must be http or https", parsedURL.Scheme)
	}

	return nil
}

// validateSavePath validates save path.
func validateSavePath(path string) error {
	if path == "" {
		return fmt.Errorf("save path cannot be empty")
	}

	// Check if the directory exists
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("save directory '%s' does not exist", dir)
			}
			return fmt.Errorf("cannot access save directory '%s': %w", dir, err)
		}

		if !info.IsDir() {
			return fmt.Errorf("save path parent '%s' is not a directory", dir)
		}
	}

	return nil
}

// validateSessionID validates session ID format.
func validateSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID cannot be empty")
	}

	// Basic validation - should be alphanumeric with optional hyphens
	matched, err := regexp.MatchString("^[a-zA-Z0-9-]+$", sessionID)
	if err != nil {
		return fmt.Errorf("failed to validate session ID: %w", err)
	}

	if !matched {
		return fmt.Errorf("session ID contains invalid characters, must be alphanumeric with optional hyphens")
	}

	if len(sessionID) < 8 {
		return fmt.Errorf("session ID is too short, must be at least 8 characters")
	}

	return nil
}

// validatePath validates filesystem path.
func validatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	// Must be absolute path
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute, got '%s'", path)
	}

	// Clean the path
	cleanPath := filepath.Clean(path)
	if cleanPath != path {
		return fmt.Errorf("path contains invalid components, should be '%s'", cleanPath)
	}

	return nil
}

// createClientWithRetry creates a gRPC client with retry logic and better error handling.
func createClientWithRetry(cfg *config.Config) (*grpc.Client, error) {
	const maxRetries = 3
	const retryDelay = 2 * time.Second

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		client, err := grpc.NewClient(cfg)
		if err != nil {
			lastErr = err
			glog.V(1).Infof("Client creation attempt %d/%d failed: %v", attempt, maxRetries, err)

			if attempt < maxRetries {
				glog.V(1).Infof("Retrying in %v...", retryDelay)
				time.Sleep(retryDelay)
				continue
			}
		} else {
			glog.V(1).Infof("Successfully connected to server on attempt %d", attempt)
			return client, nil
		}
	}

	return nil, fmt.Errorf("failed to connect to server '%s' after %d attempts: %w",
		cfg.Spec.Server.Address, maxRetries, lastErr)
}

// createContextWithSignals creates a context that cancels on SIGINT/SIGTERM.
func createContextWithSignals(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Handle graceful shutdown on signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigChan:
			glog.V(1).Infof("Received signal %v, canceling operation...", sig)
			fmt.Printf("\nReceived %v signal, canceling operation...\n", sig)
			cancel()
		case <-ctx.Done():
			// Context already done, clean up signal handling
		}
		signal.Stop(sigChan)
	}()

	return ctx, cancel
}

// handleGRPCError provides user-friendly error messages for gRPC errors.
func handleGRPCError(err error, operation string) error {
	if err == nil {
		return nil
	}

	// Extract gRPC status
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("failed to %s: %w", operation, err)
	}

	switch st.Code() {
	case codes.Unavailable:
		return fmt.Errorf("server is unavailable, please check if the upgrade service is running " +
			"and the server address is correct")
	case codes.DeadlineExceeded:
		return fmt.Errorf("operation timed out while trying to %s, the server may be overloaded",
			operation)
	case codes.PermissionDenied:
		return fmt.Errorf("permission denied to %s, please check your credentials", operation)
	case codes.NotFound:
		return fmt.Errorf("resource not found while trying to %s", operation)
	case codes.InvalidArgument:
		return fmt.Errorf("invalid arguments provided to %s: %s", operation, st.Message())
	case codes.Unauthenticated:
		return fmt.Errorf("authentication failed, please check your certificates and credentials")
	case codes.Internal:
		return fmt.Errorf("internal server error while trying to %s: %s", operation, st.Message())
	case codes.Canceled:
		return fmt.Errorf("operation was canceled")
	default:
		return fmt.Errorf("failed to %s: %s", operation, st.Message())
	}
}
