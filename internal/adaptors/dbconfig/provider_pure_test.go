//go:build pure

package dbconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPureProviderConfigurationErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		global bool
		want   string
	}{
		{name: "missing", want: "read database configuration"},
		{name: "malformed", config: "{", want: "parse database configuration"},
		{name: "includes", config: `{"INCLUDES":["other.json"]}`, want: "INCLUDES"},
		{name: "global", config: "{}", global: true, want: "global database configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := useConfig(t)
			if tc.config == "" {
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(config, []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.global {
				path := filepath.Join(filepath.Dir(config), "database_global.json")
				if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := GetDbId("CONFIG_DB", ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("GetDbId() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPureProviderRejectsMissingInstance(t *testing.T) {
	config := useConfig(t)
	data := `{"DATABASES":{"CONFIG_DB":{"instance":"missing"}}}`
	if err := os.WriteFile(config, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GetDbId("CONFIG_DB", ""); err == nil || !strings.Contains(err.Error(), `instance "missing"`) {
		t.Errorf("GetDbId() error = %v, want missing instance", err)
	}
}
