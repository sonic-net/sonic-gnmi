// Package exec provides utilities for executing host commands from within a container.
//
// This package is primarily designed for SONiC containers that need to execute
// commands on the host system. It uses nsenter to safely enter the host's
// namespaces and execute commands with proper error handling and timeout support.
//
// Key Features:
//   - Safe execution of host commands from containers using nsenter
//   - Configurable timeout and namespace selection
//   - Structured error handling and result reporting
//   - Support for both simple and advanced use cases
//
// Basic Usage:
//
//	// Simple command execution
//	output, err := exec.RunHostCommandSimple("hostname")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Host name: %s", output)
//
//	// Advanced command execution with options
//	ctx := context.Background()
//	opts := &exec.RunHostCommandOptions{
//	    Timeout: 30 * time.Second,
//	    Namespaces: []string{"pid", "net"},
//	}
//	result, err := exec.RunHostCommand(ctx, "ip", []string{"addr"}, opts)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Exit code: %d\nOutput: %s", result.ExitCode, result.Stdout)
//
// Security Considerations:
//
// This package uses nsenter to execute commands on the host system, which requires
// appropriate privileges. Ensure that:
//   - The container has the necessary capabilities (typically CAP_SYS_ADMIN)
//   - Commands are properly validated before execution
//   - Input is sanitized to prevent command injection
//
// The package is designed to be used in trusted environments where the container
// needs legitimate access to host resources, such as SONiC management containers.
package exec
