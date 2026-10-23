package debug

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writePolicyFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "command-policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write policy: %v", err)
	}
	return path
}

func TestLoadPolicy(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name: "valid",
			content: `
version: 1
enabled_actions:
  - uptime
  - process-list
`,
		},
		{
			name: "empty action set",
			content: `
version: 1
enabled_actions: []
`,
		},
		{
			name: "unknown action",
			content: `
version: 1
enabled_actions:
  - arbitrary-command
`,
			wantErr: true,
		},
		{
			name: "duplicate action",
			content: `
version: 1
enabled_actions:
  - uptime
  - uptime
`,
			wantErr: true,
		},
		{
			name: "legacy executable whitelist",
			content: `
read_whitelist:
  - uptime
write_whitelist: []
`,
			wantErr: true,
		},
		{
			name: "unknown field",
			content: `
version: 1
enabled_actions: []
extra: true
`,
			wantErr: true,
		},
		{
			name: "unsupported version",
			content: `
version: 2
enabled_actions: []
`,
			wantErr: true,
		},
		{
			name:    "malformed yaml",
			content: "version: [",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := LoadPolicy(writePolicyFile(t, tt.content))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected policy load to fail")
				}
				if policy != nil {
					t.Fatal("failed policy load must not return an executable policy")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadPolicy() error: %v", err)
			}
			if policy == nil {
				t.Fatal("LoadPolicy() returned nil policy")
			}
		})
	}
}

func TestLoadPolicyMissingFileFailsClosed(t *testing.T) {
	policy, err := LoadPolicy(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected missing policy to fail")
	}
	if policy != nil {
		t.Fatal("missing policy must not activate defaults")
	}
}

func TestPolicyResolve(t *testing.T) {
	policy, err := LoadPolicy(writePolicyFile(t, `
version: 1
enabled_actions:
  - uptime
  - process-list
`))
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}

	t.Run("read action", func(t *testing.T) {
		plan, err := policy.Resolve("uptime", AccessReadOnly, 0, 0)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if plan.Executable != "/usr/bin/uptime" {
			t.Fatalf("unexpected executable: %q", plan.Executable)
		}
		if len(plan.Args) != 0 {
			t.Fatalf("unexpected args: %q", plan.Args)
		}
		if plan.User != "admin" {
			t.Fatalf("unexpected user: %q", plan.User)
		}
		if !reflect.DeepEqual(plan.Namespaces, []string{"mount"}) {
			t.Fatalf("unexpected namespaces: %q", plan.Namespaces)
		}
		if plan.Timeout != 10*time.Second {
			t.Fatalf("unexpected timeout: %v", plan.Timeout)
		}
		if plan.OutputLimit != 64*1024 {
			t.Fatalf("unexpected output limit: %d", plan.OutputLimit)
		}
	})

	t.Run("fixed arguments", func(t *testing.T) {
		plan, err := policy.Resolve("ps", AccessReadWrite, 0, 0)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if !reflect.DeepEqual(plan.Args, []string{"-ef"}) {
			t.Fatalf("unexpected args: %q", plan.Args)
		}
	})

	t.Run("caller limits may reduce server limits", func(t *testing.T) {
		plan, err := policy.Resolve("uptime", AccessReadOnly, int64(time.Second), 1024)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if plan.Timeout != time.Second {
			t.Fatalf("unexpected timeout: %v", plan.Timeout)
		}
		if plan.OutputLimit != 1024 {
			t.Fatalf("unexpected output limit: %d", plan.OutputLimit)
		}
	})

	for _, tt := range []struct {
		name      string
		command   string
		access    AccessLevel
		timeout   int64
		byteLimit int64
	}{
		{name: "unconfigured action", command: "dmesg", access: AccessReadOnly},
		{name: "extra argument", command: "uptime --help", access: AccessReadOnly},
		{name: "pipeline", command: "uptime | ps", access: AccessReadOnly},
		{name: "multiple commands", command: "uptime; ps", access: AccessReadOnly},
		{name: "absolute executable", command: "/usr/bin/uptime", access: AccessReadOnly},
		{name: "negative timeout", command: "uptime", access: AccessReadOnly, timeout: -1},
		{name: "oversized timeout", command: "uptime", access: AccessReadOnly, timeout: int64(11 * time.Second)},
		{name: "negative output", command: "uptime", access: AccessReadOnly, byteLimit: -1},
		{name: "oversized output", command: "uptime", access: AccessReadOnly, byteLimit: 64*1024 + 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := policy.Resolve(tt.command, tt.access, tt.timeout, tt.byteLimit); err == nil {
				t.Fatal("expected Resolve() to reject request")
			}
		})
	}
}

func TestUnavailablePolicyNeverResolves(t *testing.T) {
	policy := NewUnavailablePolicy(errors.New("invalid policy"))
	if _, err := policy.Resolve("uptime", AccessReadWrite, 0, 0); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("expected ErrPolicyUnavailable, got %v", err)
	}
}

func FuzzPolicyResolve(f *testing.F) {
	path := filepath.Join(f.TempDir(), "command-policy.yaml")
	if err := os.WriteFile(path, []byte(`
version: 1
enabled_actions:
  - uptime
`), 0o600); err != nil {
		f.Fatalf("failed to write policy: %v", err)
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		f.Fatalf("LoadPolicy() error: %v", err)
	}

	for _, seed := range []string{"uptime", "uptime --help", "uptime | ps", "sh -c uptime", ""} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		plan, err := policy.Resolve(input, AccessReadOnly, 0, 0)
		if err != nil {
			return
		}
		if input != "uptime" {
			t.Fatalf("unexpected accepted command %q", input)
		}
		if plan.Executable != "/usr/bin/uptime" || len(plan.Args) != 0 {
			t.Fatalf("unexpected plan: %+v", plan)
		}
	})
}
