package gnmi

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newArtifactTestResolver(t *testing.T) artifactPathResolver {
	t.Helper()
	resolver := artifactPathResolver{
		hostMount:       t.TempDir(),
		legacyDirectory: legacyArtifactDirectory,
		dlddDirectory:   dlddArtifactDirectory,
	}
	for _, directory := range []string{resolver.legacyDirectory, resolver.dlddDirectory} {
		if err := os.MkdirAll(resolver.containerPath(directory), 0755); err != nil {
			t.Fatalf("failed to create artifact test directory: %v", err)
		}
	}
	return resolver
}

func writeArtifactTestFile(t *testing.T, resolver artifactPathResolver, hostPath string, content []byte) string {
	t.Helper()
	containerPath := resolver.containerPath(hostPath)
	if err := os.MkdirAll(filepath.Dir(containerPath), 0755); err != nil {
		t.Fatalf("failed to create artifact parent directory: %v", err)
	}
	if err := os.WriteFile(containerPath, content, 0644); err != nil {
		t.Fatalf("failed to write artifact: %v", err)
	}
	return containerPath
}

func TestArtifactPathResolverOpensSupportedArtifacts(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	tests := []struct {
		id       string
		hostPath string
		content  string
	}{
		{id: "dldd-0123456789abcdef0123456789abcdef.tar.gz", content: "dldd"},
		{id: "/tmp/dump/legacy/healthz.tar.gz", hostPath: "/tmp/dump/legacy/healthz.tar.gz", content: "legacy"},
	}
	for _, test := range tests {
		if test.hostPath == "" {
			test.hostPath = filepath.Join(resolver.dlddDirectory, test.id)
		}
		wantPath := writeArtifactTestFile(t, resolver, test.hostPath, []byte(test.content))
		file, gotPath, err := resolver.open(test.id)
		if err != nil {
			t.Fatalf("open(%q) failed: %v", test.id, err)
		}
		got, readErr := io.ReadAll(file)
		file.Close()
		if readErr != nil || gotPath != wantPath || string(got) != test.content {
			t.Fatalf("open(%q) = (%q, %q, %v), want (%q, %q, nil)", test.id, gotPath, got, readErr, wantPath, test.content)
		}
	}
}

func TestArtifactPathResolverKeepsLegacyAcknowledgementSeparate(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	legacyID := "/tmp/dump/legacy.tar.gz"
	writeArtifactTestFile(t, resolver, legacyID, []byte("legacy"))
	file, _, err := resolver.openLegacy(legacyID)
	if err != nil {
		t.Fatalf("openLegacy(%q) failed: %v", legacyID, err)
	}
	file.Close()

	for _, id := range []string{
		"dldd-0123456789abcdef0123456789abcdef.tar.gz",
		"/tmp/dump/nested/../legacy.tar.gz",
	} {
		if _, _, err := resolver.openLegacy(id); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("openLegacy(%q) code = %v, want %v; err=%v", id, status.Code(err), codes.InvalidArgument, err)
		}
	}
}

func TestArtifactPathResolverRejectsUnsafeOrMissingArtifacts(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	tests := []struct {
		id   string
		code codes.Code
	}{
		{id: "../outside", code: codes.InvalidArgument},
		{id: "/tmp/dump2/artifact", code: codes.InvalidArgument},
		{id: "dldd-not-a-generated-identifier.tar.gz", code: codes.InvalidArgument},
		{id: "dldd-44444444444444444444444444444444.tar.gz", code: codes.NotFound},
	}
	for _, test := range tests {
		if _, err := resolver.resolve(test.id); status.Code(err) != test.code {
			t.Fatalf("resolve(%q) code = %v, want %v; err=%v", test.id, status.Code(err), test.code, err)
		}
	}

	directoryID := "dldd-55555555555555555555555555555555.tar.gz"
	if err := os.Mkdir(resolver.containerPath(filepath.Join(resolver.dlddDirectory, directoryID)), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.resolve(directoryID); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("directory artifact code = %v, want %v; err=%v", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestArtifactPathResolverRejectsSymlinks(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	outside := filepath.Join(resolver.hostMount, "outside.tar.gz")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}

	opaqueID := "dldd-22222222222222222222222222222222.tar.gz"
	if err := os.Symlink(outside, resolver.containerPath(filepath.Join(resolver.dlddDirectory, opaqueID))); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.resolve(opaqueID); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("opaque symlink code = %v, want %v; err=%v", status.Code(err), codes.PermissionDenied, err)
	}

	linkDir := resolver.containerPath("/tmp/dump/link")
	if err := os.Symlink(filepath.Dir(outside), linkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.resolve("/tmp/dump/link/outside.tar.gz"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("intermediate symlink code = %v, want %v; err=%v", status.Code(err), codes.PermissionDenied, err)
	}
}
