package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/client/config"
	"github.com/spf13/cobra"
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

	// Load configuration
	cfg, err := config.LoadFromFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
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

	// Apply logic will be implemented in Phase 2 (gRPC client integration)
	return fmt.Errorf("apply command not yet implemented")
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

	// Log the value for debugging
	glog.V(2).Infof("Status command: sessionID=%s", sessionID)

	// Status logic will be implemented in Phase 3 (download client)
	return fmt.Errorf("status command not yet implemented")
}

func runListImages(cmd *cobra.Command, args []string) error {
	// List-images logic will be implemented in Phase 3 (system info client)
	return fmt.Errorf("list-images command not yet implemented")
}

func runDiskSpace(cmd *cobra.Command, args []string) error {
	// Disk-space logic will be implemented in Phase 3 (system info client)
	return fmt.Errorf("disk-space command not yet implemented")
}

func boolPtr(b bool) *bool {
	return &b
}
