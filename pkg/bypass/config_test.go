//go:build !gnmi_memcheck
// +build !gnmi_memcheck

package bypass

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Save original values
	origTables := AllowedTables
	origSKUs := AllowedSKUPrefixes
	defer func() {
		AllowedTables = origTables
		AllowedSKUPrefixes = origSKUs
	}()

	t.Run("load valid config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := tmpDir + "/gnmi_bypass.yml"

		configContent := `
bypass:
  allowed_tables:
    - TEST_TABLE1
    - TEST_TABLE2
  allowed_sku_prefixes:
    - TestSKU-1
    - TestSKU-2
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		loadConfig(configPath)

		if len(AllowedTables) != 2 {
			t.Errorf("Expected 2 allowed tables, got %d", len(AllowedTables))
		}
		if !AllowedTables["TEST_TABLE1"] {
			t.Error("Expected TEST_TABLE1 in allowed tables")
		}
		if !AllowedTables["TEST_TABLE2"] {
			t.Error("Expected TEST_TABLE2 in allowed tables")
		}
		if len(AllowedSKUPrefixes) != 2 {
			t.Errorf("Expected 2 SKU prefixes, got %d", len(AllowedSKUPrefixes))
		}
		if AllowedSKUPrefixes[0] != "TestSKU-1" {
			t.Errorf("Expected TestSKU-1, got %s", AllowedSKUPrefixes[0])
		}
	})

	t.Run("missing config file uses defaults", func(t *testing.T) {
		loadConfig("/nonexistent/path/gnmi_bypass.yml")

		// Should have default tables
		if !AllowedTables["VNET"] {
			t.Error("Expected VNET in default allowed tables")
		}
		if !AllowedTables["VNET_ROUTE_TUNNEL"] {
			t.Error("Expected VNET_ROUTE_TUNNEL in default allowed tables")
		}
		// Should have default SKU prefixes
		found := false
		for _, sku := range AllowedSKUPrefixes {
			if sku == "Cisco-8102" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Expected Cisco-8102 in default SKU prefixes")
		}
	})

	t.Run("invalid YAML uses defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := tmpDir + "/gnmi_bypass.yml"

		if err := os.WriteFile(configPath, []byte("invalid: yaml: content: ["), 0644); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		loadConfig(configPath)

		// Should fall back to defaults
		if !AllowedTables["VNET"] {
			t.Error("Expected VNET in default allowed tables after invalid YAML")
		}
	})

	t.Run("empty tables in config uses defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := tmpDir + "/gnmi_bypass.yml"

		configContent := `
bypass:
  allowed_tables: []
  allowed_sku_prefixes:
    - TestSKU
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		loadConfig(configPath)

		// Tables should use defaults since empty
		if !AllowedTables["VNET"] {
			t.Error("Expected VNET in default allowed tables when config has empty list")
		}
		// SKU should use config value
		if AllowedSKUPrefixes[0] != "TestSKU" {
			t.Errorf("Expected TestSKU from config, got %s", AllowedSKUPrefixes[0])
		}
	})
}

func TestDefaultConfigPath(t *testing.T) {
	if DefaultConfigPath != "/etc/sonic/gnmi_bypass.yml" {
		t.Errorf("Expected DefaultConfigPath=/etc/sonic/gnmi_bypass.yml, got %s", DefaultConfigPath)
	}
}
