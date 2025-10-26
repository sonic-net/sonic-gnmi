package debug

import (
	"os"

	"github.com/golang/glog"
	"gopkg.in/yaml.v3"
)

var (
	WHITELIST_FILE_PATH = "/etc/sonic/command_whitelist.yaml"
)

type WhitelistFile struct {
	ReadWhitelist  []string `yaml:"read_whitelist"`
	WriteWhitelist []string `yaml:"write_whitelist"`
}

// Function which constructs a whitelist from the YAML file present at `/etc/sonic/command_whitelist.yaml`.
// If there is any issue reading this file, returns a default set of commands.
func ConstructWhitelists() (read, write []string) {
	whitelistBytes, err := os.ReadFile(WHITELIST_FILE_PATH)
	if err != nil {
		glog.Warningf("No whitelist found at path '%s', using default whitelists: %v", WHITELIST_FILE_PATH, err)
		return defaultWhitelists()
	}

	var whitelists WhitelistFile
	err = yaml.Unmarshal(whitelistBytes, &whitelists)
	if err != nil {
		glog.Warningf("Could not unmarshal whitelist at '%s', using defaults: %v", WHITELIST_FILE_PATH, err)
		return defaultWhitelists()
	}

	if whitelists.ReadWhitelist == nil || whitelists.WriteWhitelist == nil {
		if whitelists.ReadWhitelist == nil {
			glog.Warningf("Key 'read_whitelist' in '%s' was missing", WHITELIST_FILE_PATH)
		}
		if whitelists.WriteWhitelist == nil {
			glog.Warningf("Key 'write_whitelist' in '%s' was missing", WHITELIST_FILE_PATH)
		}
		glog.Warningf("Whitelist at '%s' contained one or more missing whitelists, using defaults", WHITELIST_FILE_PATH)
		return defaultWhitelists()
	}

	return whitelists.ReadWhitelist, whitelists.WriteWhitelist
}

// Helper function to construct lists of allowed commands for read and write users,
// when there is an issue reading the whitelist at the expected path.
//
// Commands have been chosen based on what is found in existing TSGs, and are
// conservatively marked as write if there is any chance of impact on a running device.
func defaultWhitelists() (read, write []string) {
	readCommands := []string{
		"ls",
		"cat",
		"echo",
		"ping",
		"grep",
		"dmesg",
		"zgrep",
		"tail",
		"teamshow",
		"ps",
		"uptime",
		"awk",
		"xargs",
		"show",
		"TSC",
	}

	writeCommands := []string{
		"docker",
		"reboot",
		"mv",
		"systemctl",
		"ip",
		"ifconfig",
		"TSA",
		"TSB",
		"sonic-installer",
		"sonic_installer",
		"config",
		"tcpdump",
		"acl-loader",
		"counterpoll",
		"bcmcmd",
		"redis-cli",
		"sonic-db-cli",
		"sonic-clear",
		"wc",
		"portstat",
		"pfcwd",
		"lspci",
		"pcieutil",
		"include",
		"vtysh",
	}
	writeCommands = append(writeCommands, readCommands...)

	return readCommands, writeCommands
}
