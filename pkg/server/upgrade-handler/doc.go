// Package upgradehandler provides gNMI handlers for upgrade-related functionality.
//
// This package implements server-side gNMI path handlers for upgrade operations
// including disk space monitoring, package management, and system health checks.
//
// The upgrade handler supports paths like:
//   - /sonic/system/filesystem[path=*]/disk-space
//
// Example usage:
//
//	handler, err := upgradehandler.NewUpgradeHandler(paths, prefix)
//	if err != nil {
//		return err
//	}
//	defer handler.Close()
//
//	values, err := handler.Get(nil)
//	if err != nil {
//		return err
//	}
//
// This package is designed to be buildable and testable in vanilla Linux
// environments without requiring complex SONiC container setups.
package upgradehandler
