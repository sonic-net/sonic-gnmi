# cmd - Binaries and Test Utilities

This directory contains the entry points for the upgrade service binaries and test utilities.

## Production Binary

### server/
The main upgrade service server binary. This provides the gRPC API for firmware management operations including image consolidation, cleanup, and listing.

**Build:** `make build`  
**Usage:** `./bin/sonic-ops-server`

## Test Utilities

All test utilities are prefixed with `test-` to clearly indicate their purpose for testing and validation.

### test-bootloader/
Tests bootloader detection and image management functionality.

- **Purpose:** Validate bootloader package integration
- **Safety:** Read-only, does not modify system configuration
- **Build:** `make build-test-tools`
- **Usage:** `./bin/test-bootloader [--help]`

### test-installer/
Tests sonic-installer CLI wrapper functionality.

- **Purpose:** Validate installer package integration with sonic-installer
- **Safety:** `list` command is read-only; `set-default` and `cleanup` **modify system**
- **Build:** `make build-test-tools`
- **Usage:** `./bin/test-installer <command> [args...]`
- **⚠️ Warning:** Some commands modify bootloader configuration

### test-list-images/
Tests ListImages RPC functionality.

- **Purpose:** Validate ListImages RPC implementation
- **Safety:** Read-only, calls sonic-installer list only
- **Build:** `make build-test-tools`
- **Usage:** `./bin/test-list-images [--help]`

### test-image-inspector/
Tests image analysis and version extraction functionality.

- **Purpose:** Validate firmware package image analysis capabilities
- **Safety:** Read-only, analyzes image files without modification
- **Build:** `make build-test-tools`
- **Usage:** `./bin/test-image-inspector [options] [image-file]`

## Building

```bash
# Build production server
make build

# Build all test utilities
make build-test-tools

# Get detailed help about test utilities
make help-test-tools

# Clean all built binaries
make clean
```

## Test Utility Safety

- **Read-only utilities:** `test-bootloader`, `test-list-images`, `test-image-inspector`
- **System-modifying utilities:** `test-installer` (set-default and cleanup commands)

Always test on non-production systems first, especially when using utilities that modify system configuration.

## Purpose

These test utilities help validate that the upgrade service components work correctly on your system before deploying the service in production. They test individual packages and functionalities in isolation.
