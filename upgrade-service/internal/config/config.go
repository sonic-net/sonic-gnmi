package config

import (
	"flag"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/glog"
)

// Config holds global configuration for the upgrade service.
type Config struct {
	Addr            string
	RootFS          string
	ShutdownTimeout time.Duration
}

var Global *Config

// Initialize defines flags and sets up the global configuration.
func Initialize() {
	addr := flag.String("addr", ":50051", "The address to listen on")
	rootfs := flag.String("rootfs", "/mnt/host", "Root filesystem mount point (e.g., /mnt/host for containers)")
	shutdownTimeout := flag.Duration("shutdown-timeout", 10*time.Second, "Maximum time to wait for graceful shutdown")

	flag.Parse()

	Global = &Config{
		Addr:            *addr,
		RootFS:          *rootfs,
		ShutdownTimeout: *shutdownTimeout,
	}
	glog.V(1).Infof("Configuration initialized: addr=%s, rootfs=%s", Global.Addr, Global.RootFS)
}

// GetHostPath returns the path to a file/directory on the host filesystem
// The path parameter must be absolute (starting with "/")
// Example: GetHostPath("/tmp") -> "/mnt/host/tmp"
// Example: GetHostPath("/host/machine.conf") -> "/mnt/host/host/machine.conf".
func GetHostPath(path string) string {
	if !filepath.IsAbs(path) {
		glog.Errorf("GetHostPath requires absolute path, got: %s", path)
		return ""
	}
	// Remove leading "/" to avoid double slash when joining
	cleanPath := strings.TrimPrefix(path, "/")
	return filepath.Join(Global.RootFS, cleanPath)
}
