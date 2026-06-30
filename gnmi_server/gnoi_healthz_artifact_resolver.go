package gnmi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// The gNMI container mounts the host filesystem read-only at /mnt/host.
	// DLDD owns artifact creation and lifecycle under dlddArtifactDirectory;
	// Healthz only resolves and streams artifacts from that directory.
	hostFilesystemMount     string = "/mnt/host"
	legacyArtifactDirectory string = "/tmp/dump"
	dlddArtifactDirectory   string = "/var/lib/sonic/dldd/artifacts"
)

var (
	// DLDD is the only producer for relative IDs. Restricting them to its exact
	// generated form keeps private sibling state manifests out of Artifact RPCs.
	opaqueArtifactIDPattern = regexp.MustCompile(`^dldd-[0-9a-f]{32}\.tar\.gz$`)
	defaultArtifactResolver = artifactPathResolver{
		hostMount:       hostFilesystemMount,
		legacyDirectory: legacyArtifactDirectory,
		dlddDirectory:   dlddArtifactDirectory,
	}
)

type artifactPathResolver struct {
	hostMount       string
	legacyDirectory string
	dlddDirectory   string
}

// openLegacy opens an absolute artifact ID beneath the legacy debug dump
// directory. Acknowledge must never accept opaque DLDD IDs: those artifacts
// have a separate lifecycle and are not removed through the legacy D-Bus API.
func (r artifactPathResolver) openLegacy(artifactID string) (*os.File, string, error) {
	if !filepath.IsAbs(artifactID) {
		return nil, "", status.Error(codes.InvalidArgument, "legacy artifact ID must be an absolute path")
	}
	for _, component := range strings.Split(artifactID, string(filepath.Separator)) {
		if component == ".." {
			return nil, "", status.Error(codes.InvalidArgument, "legacy artifact ID must not contain parent traversal")
		}
	}
	return r.open(artifactID)
}

// open resolves and opens an artifact beneath an os.Root. The preliminary
// resolve rejects symbolic links for clear API errors; os.Root provides the
// race-resistant containment guarantee between validation and open.
func (r artifactPathResolver) open(artifactID string) (*os.File, string, error) {
	containerPath, err := r.resolve(artifactID)
	if err != nil {
		return nil, "", err
	}

	artifactDirectory := r.dlddDirectory
	if filepath.IsAbs(artifactID) {
		artifactDirectory = r.legacyDirectory
	}
	containerDirectory := r.containerPath(filepath.Clean(artifactDirectory))
	relativePath, err := filepath.Rel(containerDirectory, containerPath)
	if err != nil || relativePath == "." || filepath.IsAbs(relativePath) ||
		relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return nil, "", status.Error(codes.InvalidArgument, "artifact path is outside the allowed directory")
	}

	root, err := os.OpenRoot(containerDirectory)
	if err != nil {
		return nil, "", artifactPathError(err)
	}
	defer root.Close()

	file, err := root.Open(relativePath)
	if err != nil {
		return nil, "", artifactPathError(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, "", artifactPathError(err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, "", status.Error(codes.InvalidArgument, "artifact is not a regular file")
	}
	return file, containerPath, nil
}

func (r artifactPathResolver) resolve(artifactID string) (string, error) {
	if artifactID == "" || strings.IndexByte(artifactID, 0) >= 0 {
		return "", status.Error(codes.InvalidArgument, "artifact ID is empty or invalid")
	}

	var artifactDirectory string
	var hostPath string
	if filepath.IsAbs(artifactID) {
		artifactDirectory = filepath.Clean(r.legacyDirectory)
		hostPath = filepath.Clean(artifactID)
		if !isPathWithin(artifactDirectory, hostPath) {
			return "", status.Error(codes.InvalidArgument, "legacy artifact path is outside the allowed directory")
		}
	} else {
		if !opaqueArtifactIDPattern.MatchString(artifactID) {
			return "", status.Error(codes.InvalidArgument, "artifact ID contains unsupported characters")
		}
		artifactDirectory = filepath.Clean(r.dlddDirectory)
		hostPath = filepath.Join(artifactDirectory, artifactID)
	}

	containerDirectory := r.containerPath(artifactDirectory)
	containerPath := r.containerPath(hostPath)
	if !isPathWithin(containerDirectory, containerPath) {
		return "", status.Error(codes.InvalidArgument, "artifact path is outside the allowed directory")
	}
	if err := validateArtifactPath(containerDirectory, containerPath); err != nil {
		return "", err
	}
	return containerPath, nil
}

func (r artifactPathResolver) containerPath(hostPath string) string {
	cleanPath := filepath.Clean(hostPath)
	return filepath.Join(r.hostMount, strings.TrimPrefix(cleanPath, string(filepath.Separator)))
}

func isPathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validateArtifactPath(root, candidate string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return artifactPathError(err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return status.Error(codes.PermissionDenied, "artifact directory is not a trusted directory")
	}

	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return status.Error(codes.InvalidArgument, "artifact path is outside the allowed directory")
	}

	current := root
	parts := strings.Split(rel, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return artifactPathError(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return status.Error(codes.PermissionDenied, "artifact path must not contain symbolic links")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return status.Error(codes.InvalidArgument, "artifact path contains a non-directory component")
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return status.Error(codes.InvalidArgument, "artifact is not a regular file")
		}
	}

	return nil
}

func artifactPathError(err error) error {
	if os.IsNotExist(err) {
		return status.Error(codes.NotFound, "artifact not found")
	}
	if os.IsPermission(err) {
		return status.Error(codes.PermissionDenied, "artifact is not accessible")
	}
	return status.Errorf(codes.Internal, "failed to resolve artifact: %v", err)
}
