package hostfs

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDefaultPolicy(t *testing.T) {
	tests := []struct {
		path    string
		wantErr string
	}{
		{path: "/tmp/firmware.bin"},
		{path: "/var/tmp/firmware.bin"},
		{path: "/host/image-stage/blob"},
		{path: "tmp/firmware.bin", wantErr: "path must be absolute"},
		{path: "/tmp/sub/../../etc/passwd", wantErr: "path must be under"},
		{path: "/var/lib/sonic/dldd/inbox/dld_rules.yaml", wantErr: "path must be under"},
	}
	for _, test := range tests {
		err := Validate(test.path)
		if test.wantErr == "" && err != nil {
			t.Errorf("Validate(%q): unexpected error %v", test.path, err)
		}
		if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
			t.Errorf("Validate(%q): want error containing %q, got %v", test.path, test.wantErr, err)
		}
	}
}

func TestPolicySupportsExactSecureInbox(t *testing.T) {
	const inbox = "/var/lib/sonic/dldd/inbox/dld_rules.yaml"
	policy := Policy{
		Prefixes:                 []string{"/tmp/"},
		ExactPaths:               []string{inbox},
		RejectRawParentTraversal: true,
		RejectNUL:                true,
	}
	tests := []struct {
		path    string
		wantErr string
	}{
		{path: "/tmp/file"},
		{path: inbox},
		{path: inbox + ".bak", wantErr: "path must be under"},
		{path: "/tmp/a/../b", wantErr: "path traversal not allowed"},
		{path: "/tmp/bad\x00name", wantErr: "path contains a null byte"},
	}
	for _, test := range tests {
		err := policy.Validate(test.path)
		if test.wantErr == "" && err != nil {
			t.Errorf("Validate(%q): unexpected error %v", test.path, err)
		}
		if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
			t.Errorf("Validate(%q): want error containing %q, got %v", test.path, test.wantErr, err)
		}
	}
}

func TestMapper(t *testing.T) {
	mount := t.TempDir()
	absent := filepath.Join(mount, "absent")
	tests := []struct {
		mapper Mapper
		path   string
		want   string
	}{
		{mapper: Mapper{Mount: mount}, path: "/tmp/file", want: mount + "/tmp/file"},
		{mapper: Mapper{Mount: mount, PreserveMapped: true}, path: mount + "/tmp/file", want: mount + "/tmp/file"},
		{mapper: Mapper{Mount: absent}, path: "/tmp/sub/../file", want: "/tmp/file"},
	}
	for _, test := range tests {
		if got := test.mapper.Translate(test.path); got != test.want {
			t.Fatalf("Translate(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}
