// Package hostfs centralizes the small bits of host-filesystem awareness
// that SONiC gNOI services need:
//
//   - Validate: the allowlist of writable staging directories on the host.
//   - Translate: prepend /mnt/host when the caller runs inside the gnmi
//     container so absolute host paths resolve through the bind mount.
package hostfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HostMount is the bind-mount path inside the gnmi container where the host
// root filesystem is exposed.
const HostMount = "/mnt/host"

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

// Mapper maps logical host paths to the process filesystem. PreserveMapped
// keeps Translate idempotent for callers such as ORAS that may receive an
// already-mapped path. File intentionally leaves it false to preserve its
// historical always-prepend behavior when the mount exists.
type Mapper struct {
	Mount          string
	PreserveMapped bool
}

// Translate returns the cleaned syscall path for this mapper.
func (mapper Mapper) Translate(path string) string {
	cleaned := filepath.Clean(path)
	if _, err := os.Stat(mapper.Mount); err == nil {
		if !mapper.PreserveMapped || !strings.HasPrefix(cleaned, mapper.Mount) {
			return mapper.Mount + cleaned
		}
	}
	return cleaned
}

// Policy describes a write-path allowlist. RejectRawParentTraversal and
// RejectNUL opt into the stricter checks used by gNOI File. ExactPaths can
// grant individual files without broadening Prefixes.
type Policy struct {
	Prefixes                 []string
	ExactPaths               []string
	RejectRawParentTraversal bool
	RejectNUL                bool
	AllowedDescription       string
}

// Validate rejects paths outside the policy without touching the filesystem.
func (policy Policy) Validate(path string) error {
	if policy.RejectNUL && strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("path contains a null byte")
	}
	if policy.RejectRawParentTraversal {
		for _, component := range strings.Split(path, string(filepath.Separator)) {
			if component == ".." {
				return fmt.Errorf("path traversal not allowed: %s", path)
			}
		}
	}

	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return fmt.Errorf("path must be absolute, got: %s", path)
	}
	if !policy.RejectRawParentTraversal {
		for _, component := range strings.Split(cleaned, string(filepath.Separator)) {
			if component == ".." {
				return fmt.Errorf("path traversal not allowed: %s", path)
			}
		}
	}
	for _, exactPath := range policy.ExactPaths {
		if cleaned == filepath.Clean(exactPath) {
			return nil
		}
	}
	for _, prefix := range policy.Prefixes {
		if strings.HasPrefix(cleaned, prefix) {
			return nil
		}
	}
	if policy.AllowedDescription != "" {
		return fmt.Errorf("path must be %s, got: %s", policy.AllowedDescription, cleaned)
	}
	return fmt.Errorf("path must be under %v, got: %s", policy.Prefixes, cleaned)
}

// Validate rejects any path that is not absolute, contains a literal ".."
// segment after cleaning, or falls outside AllowedPrefixes. It does NOT
// touch the filesystem.
func Validate(path string) error {
	return (Policy{Prefixes: AllowedPrefixes}).Validate(path)
}

// Translate returns the path that should be used by syscalls on the
// current process. When running inside the gnmi container (detected by
// the presence of /mnt/host) it prepends the host-mount prefix; otherwise
// it returns filepath.Clean(path) unchanged.
//
// Translate does NOT validate the path; callers should Validate first.
func Translate(path string) string {
	return (Mapper{Mount: HostMount, PreserveMapped: true}).Translate(path)
}
