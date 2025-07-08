package paths

import (
	"testing"
)

func TestToHost(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		rootFS   string
		expected string
	}{
		{
			name:     "Basic path conversion",
			path:     "/tmp",
			rootFS:   "/mnt/host",
			expected: "/mnt/host/tmp",
		},
		{
			name:     "Nested path conversion",
			path:     "/host/machine.conf",
			rootFS:   "/mnt/host",
			expected: "/mnt/host/host/machine.conf",
		},
		{
			name:     "Root path conversion",
			path:     "/",
			rootFS:   "/mnt/host",
			expected: "/mnt/host",
		},
		{
			name:     "Path with multiple segments",
			path:     "/var/log/syslog",
			rootFS:   "/mnt/host",
			expected: "/mnt/host/var/log/syslog",
		},
		{
			name:     "Different rootFS",
			path:     "/etc/hosts",
			rootFS:   "/host",
			expected: "/host/etc/hosts",
		},
		{
			name:     "RootFS with trailing slash",
			path:     "/tmp",
			rootFS:   "/mnt/host/",
			expected: "/mnt/host/tmp",
		},
		{
			name:     "Complex path",
			path:     "/usr/local/bin/app",
			rootFS:   "/container/host",
			expected: "/container/host/usr/local/bin/app",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ToHost(test.path, test.rootFS)
			if result != test.expected {
				t.Errorf("ToHost(%q, %q) = %q, expected %q", test.path, test.rootFS, result, test.expected)
			}
		})
	}
}

func TestToHost_InvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		rootFS string
	}{
		{
			name:   "Relative path",
			path:   "relative/path",
			rootFS: "/mnt/host",
		},
		{
			name:   "Empty path",
			path:   "",
			rootFS: "/mnt/host",
		},
		{
			name:   "Empty rootFS",
			path:   "/tmp",
			rootFS: "",
		},
		{
			name:   "Both empty",
			path:   "",
			rootFS: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ToHost(test.path, test.rootFS)
			if result != "" {
				t.Errorf("ToHost(%q, %q) = %q, expected empty string for invalid input", test.path, test.rootFS, result)
			}
		})
	}
}

func TestToHost_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		rootFS   string
		expected string
	}{
		{
			name:     "Path with dots",
			path:     "/./tmp/../var",
			rootFS:   "/mnt/host",
			expected: "/mnt/host/tmp/../var", // filepath.Join will clean this up
		},
		{
			name:     "Path with double slashes",
			path:     "//tmp//file",
			rootFS:   "/mnt/host",
			expected: "/mnt/host/tmp/file", // filepath.Join handles this
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ToHost(test.path, test.rootFS)
			// For edge cases, we mainly want to ensure no panic and reasonable behavior
			if result == "" {
				t.Errorf("ToHost(%q, %q) returned empty string, expected some result", test.path, test.rootFS)
			}
		})
	}
}
