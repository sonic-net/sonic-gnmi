package gnmi

import (
	"fmt"
	"strings"

	"github.com/openconfig/gnmi/proto/gnmi"
)

// pathToString converts a gNMI path to a string representation for logging and debugging.
// It handles path elements with keys (e.g., filesystem[path=/host]).
func pathToString(path *gnmi.Path) string {
	if path == nil {
		return "/"
	}

	parts := make([]string, 0, len(path.Elem))
	for _, elem := range path.Elem {
		part := elem.Name
		if len(elem.Key) > 0 {
			// Add keys if present (e.g., [path=/host])
			var keys []string
			for k, v := range elem.Key {
				keys = append(keys, fmt.Sprintf("%s=%s", k, v))
			}
			part += "[" + strings.Join(keys, ",") + "]"
		}
		parts = append(parts, part)
	}

	return "/" + strings.Join(parts, "/")
}

// isFilesystemPath checks if the given path is requesting filesystem information.
// Returns true if the path starts with /sonic/system/filesystem.
func isFilesystemPath(path *gnmi.Path) bool {
	return len(path.Elem) >= 3 &&
		path.Elem[0].Name == "sonic" &&
		path.Elem[1].Name == "system" &&
		path.Elem[2].Name == "filesystem"
}

// extractFilesystemPath extracts the filesystem path from a gNMI path.
// For example, from /sonic/system/filesystem[path=/host]/disk-space,
// it extracts "/host".
func extractFilesystemPath(path *gnmi.Path) (string, error) {
	if !isFilesystemPath(path) {
		return "", fmt.Errorf("not a filesystem path: %s", pathToString(path))
	}

	fsPath, ok := path.Elem[2].Key["path"]
	if !ok {
		return "", fmt.Errorf("filesystem path not specified, expected format: /sonic/system/filesystem[path=<path>]/...")
	}

	return fsPath, nil
}

// isDiskSpacePath checks if the path is requesting disk space information.
// Returns true if the path contains /disk-space.
func isDiskSpacePath(path *gnmi.Path) bool {
	return isFilesystemPath(path) &&
		len(path.Elem) >= 4 &&
		path.Elem[3].Name == "disk-space"
}

// validateDiskSpacePath validates that the disk space path is correctly formatted.
func validateDiskSpacePath(path *gnmi.Path) error {
	if !isDiskSpacePath(path) {
		return fmt.Errorf("not a disk space path: %s", pathToString(path))
	}

	if len(path.Elem) != 4 {
		return fmt.Errorf("invalid disk space path: %s", pathToString(path))
	}

	return nil
}
