// Package hostfs centralizes the small bits of host-filesystem awareness
// that every SONiC gNOI service needs:
//
//   - Validate: the allowlist of writable staging directories on the host.
//   - Translate: prepend /mnt/host when the caller runs inside the gnmi
//     container so absolute host paths resolve through the bind mount.
//
// Both pkg/gnoi/file and internal/diskspace already implement equivalent
// logic privately. Those callers are left as-is for now; new services
// (starting with pkg/gnoi/oras) should depend on this package so we have a
// single source of truth going forward.
package hostfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hostMount is the bind-mount path inside the gnmi container where the
// host root filesystem is exposed. Exported as a var rather than a const
// so tests can override it.
var hostMount = "/mnt/host"

// AllowedPrefixes is the canonical allowlist of writable host directories
// for gNOI staging on SONiC. It mirrors pkg/gnoi/file's whitelist:
//
//   - /tmp/      ephemeral staging (firmware images, layer blobs, …)
//   - /var/tmp/  same, persisted across reboot
//   - /host/     next-image overlay (e.g. /host/image-*/rw/…)
//
// Callers that want to extend the allowlist should add a new prefix here
// in a follow-up rather than building parallel lists.
var AllowedPrefixes = []string{"/tmp/", "/var/tmp/", "/host/"}

// Validate rejects any path that is not absolute, contains a literal ".."
// segment after cleaning, or falls outside AllowedPrefixes. It does NOT
// touch the filesystem.
func Validate(path string) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return fmt.Errorf("path must be absolute, got: %s", path)
	}
	// filepath.Clean collapses traversals against the absolute root, so a
	// remaining `..` can only be a literal segment.
	for _, seg := range strings.Split(cleaned, string(filepath.Separator)) {
		if seg == ".." {
			return fmt.Errorf("path traversal not allowed: %s", path)
		}
	}
	for _, prefix := range AllowedPrefixes {
		if strings.HasPrefix(cleaned, prefix) {
			return nil
		}
	}
	return fmt.Errorf("path must be under %v, got: %s", AllowedPrefixes, cleaned)
}

// Translate returns the path that should be used by syscalls on the
// current process. When running inside the gnmi container (detected by
// the presence of /mnt/host) it prepends the host-mount prefix; otherwise
// it returns filepath.Clean(path) unchanged.
//
// Translate does NOT validate the path; callers should Validate first.
func Translate(path string) string {
	cleaned := filepath.Clean(path)
	if _, err := os.Stat(hostMount); err == nil {
		if !strings.HasPrefix(cleaned, hostMount) {
			return hostMount + cleaned
		}
	}
	return cleaned
}
