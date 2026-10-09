package gnmi

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newArtifactTestResolver(t *testing.T) artifactPathResolver {
	t.Helper()
	resolver := artifactPathResolver{
		hostMount:        t.TempDir(),
		legacyDirectory:  legacyArtifactDirectory,
		dlddDirectory:    dlddArtifactDirectory,
		healthzDirectory: healthzArtifactDirectory,
	}
	for _, directory := range []string{resolver.legacyDirectory, resolver.dlddDirectory, resolver.healthzDirectory} {
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

func requireArtifactOpenCode(t *testing.T, resolver artifactPathResolver, id string, code codes.Code) {
	t.Helper()
	file, _, err := resolver.open(id)
	if file != nil {
		file.Close()
	}
	if status.Code(err) != code {
		t.Fatalf("open(%q) code = %v, want %v; err=%v", id, status.Code(err), code, err)
	}
}

func TestArtifactPathResolverOpensSupportedArtifacts(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	tests := []struct {
		id       string
		hostPath string
		content  string
	}{
		{id: "healthz-0123456789abcdef0123456789abcdef.tar.gz", content: "healthz"},
		{id: "dldd-0123456789abcdef0123456789abcdef.tar.gz", content: "dldd"},
		{id: "/tmp/dump/legacy/healthz.tar.gz", hostPath: "/tmp/dump/legacy/healthz.tar.gz", content: "legacy"},
	}
	for _, test := range tests {
		if test.hostPath == "" {
			test.hostPath = filepath.Join(resolver.artifactDirectory(test.id), test.id)
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
		"healthz-0123456789abcdef0123456789abcdef.tar.gz",
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
		{id: "healthz-not-a-generated-identifier.tar.gz", code: codes.InvalidArgument},
		{id: "healthz-44444444444444444444444444444444.tar.gz", code: codes.NotFound},
		{id: "dldd-44444444444444444444444444444444.tar.gz", code: codes.NotFound},
	}
	for _, test := range tests {
		requireArtifactOpenCode(t, resolver, test.id, test.code)
	}

	directoryID := "dldd-55555555555555555555555555555555.tar.gz"
	if err := os.Mkdir(resolver.containerPath(filepath.Join(resolver.dlddDirectory, directoryID)), 0755); err != nil {
		t.Fatal(err)
	}
	requireArtifactOpenCode(t, resolver, directoryID, codes.InvalidArgument)
	fifoID := "healthz-55555555555555555555555555555555.tar.gz"
	if err := unix.Mkfifo(resolver.containerPath(filepath.Join(resolver.healthzDirectory, fifoID)), 0600); err != nil {
		t.Fatal(err)
	}
	requireArtifactOpenCode(t, resolver, fifoID, codes.InvalidArgument)
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
	requireArtifactOpenCode(t, resolver, opaqueID, codes.PermissionDenied)
	healthzID := "healthz-22222222222222222222222222222222.tar.gz"
	if err := os.Symlink(outside, resolver.containerPath(filepath.Join(resolver.healthzDirectory, healthzID))); err != nil {
		t.Fatal(err)
	}
	requireArtifactOpenCode(t, resolver, healthzID, codes.PermissionDenied)

	linkDir := resolver.containerPath("/tmp/dump/link")
	if err := os.Symlink(filepath.Dir(outside), linkDir); err != nil {
		t.Fatal(err)
	}
	requireArtifactOpenCode(t, resolver, "/tmp/dump/link/outside.tar.gz", codes.PermissionDenied)
}

func TestArtifactPathResolverRejectsSymlinkAncestors(t *testing.T) {
	for _, test := range []struct {
		name     string
		id       string
		ancestor string
	}{
		{name: "healthz", id: "healthz-0123456789abcdef0123456789abcdef.tar.gz", ancestor: "/var/lib/sonic/healthz"},
		{name: "dldd", id: "dldd-0123456789abcdef0123456789abcdef.tar.gz", ancestor: "/var"},
		{name: "legacy", id: "/tmp/dump/legacy.tar.gz", ancestor: "/tmp"},
		{name: "mount", id: "healthz-0123456789abcdef0123456789abcdef.tar.gz", ancestor: "/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := newArtifactTestResolver(t)
			hostPath := test.id
			if !filepath.IsAbs(hostPath) {
				hostPath = filepath.Join(resolver.artifactDirectory(test.id), test.id)
			}
			writeArtifactTestFile(t, resolver, hostPath, []byte("outside mount"))
			ancestor := resolver.containerPath(test.ancestor)
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.Rename(ancestor, outside); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, ancestor); err != nil {
				t.Fatal(err)
			}
			requireArtifactOpenCode(t, resolver, test.id, codes.PermissionDenied)
		})
	}
}

func TestArtifactPathResolverKeepsOpenedFilePinnedAcrossAncestorReplacement(t *testing.T) {
	resolver := newArtifactTestResolver(t)
	id := "healthz-0123456789abcdef0123456789abcdef.tar.gz"
	writeArtifactTestFile(t, resolver, filepath.Join(resolver.healthzDirectory, id), []byte("trusted"))
	file, _, err := resolver.open(id)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	ancestor := resolver.containerPath(filepath.Dir(resolver.healthzDirectory))
	if err := os.Rename(ancestor, filepath.Join(t.TempDir(), "original")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "artifacts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "artifacts", id), []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, ancestor); err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(file)
	if err != nil || string(content) != "trusted" {
		t.Fatalf("opened artifact after ancestor replacement = %q, %v; want trusted", content, err)
	}
	requireArtifactOpenCode(t, resolver, id, codes.PermissionDenied)
}
