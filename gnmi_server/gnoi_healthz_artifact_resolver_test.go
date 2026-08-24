package gnmi

import (
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestArtifactPathResolver(t *testing.T) {
	resolver := newArtifactTestResolver(t)

	opaqueID := "dldd-0123456789abcdef0123456789abcdef.tar.gz"
	opaquePath := writeArtifactTestFile(t, resolver, filepath.Join(resolver.dlddDirectory, opaqueID), []byte("opaque"))
	legacyID := "/tmp/dump/legacy/healthz.tar.gz"
	legacyPath := writeArtifactTestFile(t, resolver, legacyID, []byte("legacy"))

	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "opaque DLDD ID", id: opaqueID, want: opaquePath},
		{name: "legacy path", id: legacyID, want: legacyPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolver.resolve(test.id)
			if err != nil {
				t.Fatalf("resolve(%q) failed: %v", test.id, err)
			}
			if got != test.want {
				t.Fatalf("resolve(%q) = %q, want %q", test.id, got, test.want)
			}
			file, openedPath, err := resolver.open(test.id)
			if err != nil {
				t.Fatalf("open(%q) failed: %v", test.id, err)
			}
			defer file.Close()
			if openedPath != test.want {
				t.Fatalf("open(%q) path = %q, want %q", test.id, openedPath, test.want)
			}
			if _, err := io.ReadAll(file); err != nil {
				t.Fatalf("read open(%q): %v", test.id, err)
			}
		})
	}
}

func TestArtifactPathResolverOpensOnlyAbsoluteLegacyIDsForAcknowledgement(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	legacyID := "/tmp/dump/legacy/healthz.tar.gz"
	writeArtifactTestFile(t, resolver, legacyID, []byte("legacy"))

	file, _, err := resolver.openLegacy(legacyID)
	if err != nil {
		t.Fatalf("openLegacy(%q) failed: %v", legacyID, err)
	}
	file.Close()

	opaqueID := "dldd-legacy-looking.tar.gz"
	writeArtifactTestFile(t, resolver, filepath.Join(resolver.dlddDirectory, opaqueID), []byte("dldd"))
	if _, _, err := resolver.openLegacy(opaqueID); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("openLegacy(%q) code = %v, want %v; err=%v", opaqueID, status.Code(err), codes.InvalidArgument, err)
	}
	if _, _, err := resolver.openLegacy("/tmp/dump/nested/../healthz.tar.gz"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("openLegacy() contained traversal code = %v, want %v; err=%v", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestArtifactPathResolverRejectsInvalidIDs(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	tests := []string{
		"",
		".",
		"..",
		"../outside",
		"nested/artifact",
		"/tmp/dump",
		"/tmp/dump2/artifact",
		"/tmp/dump/../artifact",
		"/var/lib/sonic/dldd/artifacts/artifact",
		"dldd-0123456789abcdef0123456789abcdef.json",
		"dldd-not-a-generated-identifier.tar.gz",
		strings.Repeat("a", 256),
	}
	for _, artifactID := range tests {
		t.Run(artifactID, func(t *testing.T) {
			_, err := resolver.resolve(artifactID)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("resolve(%q) code = %v, want %v; err=%v", artifactID, status.Code(err), codes.InvalidArgument, err)
			}
		})
	}
}

func TestArtifactPathResolverRejectsSymlinks(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	outside := filepath.Join(resolver.hostMount, "outside.tar.gz")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}

	opaqueID := "dldd-22222222222222222222222222222222.tar.gz"
	opaqueLink := resolver.containerPath(filepath.Join(resolver.dlddDirectory, opaqueID))
	if err := os.Symlink(outside, opaqueLink); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.resolve(opaqueID); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("opaque symlink code = %v, want %v; err=%v", status.Code(err), codes.PermissionDenied, err)
	}

	legacyLinkDir := resolver.containerPath("/tmp/dump/link")
	if err := os.Symlink(filepath.Dir(outside), legacyLinkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.resolve("/tmp/dump/link/outside.tar.gz"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("legacy intermediate symlink code = %v, want %v; err=%v", status.Code(err), codes.PermissionDenied, err)
	}
}

func TestArtifactPathResolverRejectsNonRegularArtifact(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	artifactID := "dldd-33333333333333333333333333333333.tar.gz"
	if err := os.Mkdir(resolver.containerPath(filepath.Join(resolver.dlddDirectory, artifactID)), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.resolve(artifactID); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("directory artifact code = %v, want %v; err=%v", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestArtifactPathResolverReportsMissingArtifact(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	if _, err := resolver.resolve("dldd-44444444444444444444444444444444.tar.gz"); status.Code(err) != codes.NotFound {
		t.Fatalf("missing artifact code = %v, want %v; err=%v", status.Code(err), codes.NotFound, err)
	}
}
