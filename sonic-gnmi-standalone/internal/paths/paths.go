// Package paths provides utilities for path manipulation in containerized environments.
package paths

import (
	"path/filepath"
	"strings"

	"github.com/golang/glog"
)

// ToHost converts a container path to the corresponding host filesystem path.
// The path parameter must be absolute (starting with "/").
// The rootFS parameter specifies the mount point of the host filesystem within the container.
//
// Example: ToHost("/tmp", "/mnt/host") -> "/mnt/host/tmp"
// Example: ToHost("/host/machine.conf", "/mnt/host") -> "/mnt/host/host/machine.conf".
func ToHost(path, rootFS string) string {
	if !filepath.IsAbs(path) {
		glog.Errorf("ToHost requires absolute path, got: %s", path)
		return ""
	}

	if rootFS == "" {
		glog.Errorf("ToHost requires non-empty rootFS")
		return ""
	}

	// Remove leading "/" to avoid double slash when joining
	cleanPath := strings.TrimPrefix(path, "/")
	return filepath.Join(rootFS, cleanPath)
}
