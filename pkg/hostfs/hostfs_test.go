package hostfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"tmp_ok", "/tmp/firmware.bin", ""},
		{"var_tmp_ok", "/var/tmp/firmware.bin", ""},
		{"host_ok", "/host/image-stage/blob", ""},
		{"nested_ok", "/tmp/sub/dir/file", ""},

		{"relative", "tmp/firmware.bin", "must be absolute"},
		{"etc_blocked", "/etc/passwd", "must be under"},
		{"root_blocked", "/root/secret", "must be under"},
		{"empty", "", "must be absolute"},

		// Literal `..` segments that filepath.Clean can't collapse must be
		// rejected even when the cleaned path lands in the allowlist.
		{"literal_dotdot", "/tmp/..", "must be under"},
		{"embedded_dotdot_collapses_to_root", "/tmp/sub/../../etc/passwd", "must be under"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate(%q): unexpected error %v", tc.path, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate(%q): want error containing %q, got %v", tc.path, tc.wantErr, err)
			}
		})
	}
}

func TestTranslate(t *testing.T) {
	// Override the mount probe to a private temp dir so tests don't depend
	// on whether /mnt/host exists on the host running `go test`.
	dir := t.TempDir()
	old := hostMount
	t.Cleanup(func() { hostMount = old })

	t.Run("mount_present_prepends", func(t *testing.T) {
		hostMount = dir
		got := Translate("/tmp/firmware.bin")
		want := dir + "/tmp/firmware.bin"
		if got != want {
			t.Fatalf("Translate: got %q, want %q", got, want)
		}
	})

	t.Run("mount_present_idempotent", func(t *testing.T) {
		hostMount = dir
		// Already-translated paths must not get double-prefixed.
		input := dir + "/tmp/firmware.bin"
		if got := Translate(input); got != input {
			t.Fatalf("Translate idempotent: got %q, want %q", got, input)
		}
	})

	t.Run("mount_absent_passthrough", func(t *testing.T) {
		hostMount = filepath.Join(dir, "does-not-exist")
		// Sanity check our probe really doesn't exist.
		if _, err := os.Stat(hostMount); err == nil {
			t.Fatalf("test setup: hostMount %q unexpectedly exists", hostMount)
		}
		got := Translate("/tmp/firmware.bin")
		if got != "/tmp/firmware.bin" {
			t.Fatalf("Translate: got %q, want passthrough", got)
		}
	})

	t.Run("cleans_dotdot_within_root", func(t *testing.T) {
		hostMount = filepath.Join(dir, "does-not-exist")
		// filepath.Clean collapses .. against the absolute root.
		got := Translate("/tmp/sub/../firmware.bin")
		if got != "/tmp/firmware.bin" {
			t.Fatalf("Translate: got %q, want cleaned path", got)
		}
	})
}
