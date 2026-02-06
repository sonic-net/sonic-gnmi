package bypass

import (
	"os"

	"github.com/golang/glog"
	"gopkg.in/yaml.v2"
)

const (
	// DefaultConfigPath is the default path to the bypass config file
	DefaultConfigPath = "/etc/sonic/gnmi_bypass.yml"
)

// Config represents the bypass configuration loaded from YAML
type Config struct {
	Bypass struct {
		AllowedTables      []string `yaml:"allowed_tables"`
		AllowedSKUPrefixes []string `yaml:"allowed_sku_prefixes"`
	} `yaml:"bypass"`
}

// AllowedTables lists ConfigDB tables that can bypass validation (exact match)
var AllowedTables = map[string]bool{}

// AllowedSKUPrefixes lists HwSku prefixes that can use bypass validation
var AllowedSKUPrefixes = []string{}

// Default values used when config file is not available
var defaultAllowedTables = []string{
	"VNET",
	"VNET_ROUTE_TUNNEL",
	"VLAN_SUB_INTERFACE",
	"ACL_RULE",
	"BGP_PEER_RANGE",
}

var defaultAllowedSKUPrefixes = []string{
	"Cisco-8102",
	"Cisco-8101",
	"Cisco-8223",
}

func init() {
	loadConfig(DefaultConfigPath)
}

// loadConfig loads bypass configuration from YAML file
// Falls back to defaults if file doesn't exist or can't be parsed
func loadConfig(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		glog.V(2).Infof("Bypass: config file not found at %s, using defaults", path)
		applyDefaults()
		return
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		glog.Warningf("Bypass: failed to parse config file %s: %v, using defaults", path, err)
		applyDefaults()
		return
	}

	if len(cfg.Bypass.AllowedTables) > 0 {
		AllowedTables = make(map[string]bool)
		for _, table := range cfg.Bypass.AllowedTables {
			AllowedTables[table] = true
		}
		glog.V(2).Infof("Bypass: loaded %d allowed tables from config", len(AllowedTables))
	} else {
		applyDefaultTables()
	}

	if len(cfg.Bypass.AllowedSKUPrefixes) > 0 {
		AllowedSKUPrefixes = cfg.Bypass.AllowedSKUPrefixes
		glog.V(2).Infof("Bypass: loaded %d allowed SKU prefixes from config", len(AllowedSKUPrefixes))
	} else {
		applyDefaultSKUPrefixes()
	}
}

func applyDefaults() {
	applyDefaultTables()
	applyDefaultSKUPrefixes()
}

func applyDefaultTables() {
	AllowedTables = make(map[string]bool)
	for _, table := range defaultAllowedTables {
		AllowedTables[table] = true
	}
}

func applyDefaultSKUPrefixes() {
	AllowedSKUPrefixes = defaultAllowedSKUPrefixes
}
