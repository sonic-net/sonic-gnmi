// Package operationalhandler provides gNMI handlers for operational state queries.
//
// This package implements server-side gNMI path handlers for operational data
// including disk space monitoring, package management, and system health checks.
//
// The operational handler supports paths like:
//   - /sonic/system/filesystem[path=*]/disk-space
//
// Example usage:
//
//	handler, err := operationalhandler.NewOperationalHandler(paths, prefix)
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
package operationalhandler
