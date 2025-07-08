# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common Development Commands

### Building
```bash
make build              # Build main server binary (bin/opsd-server)
make build-all          # Validate all packages compile
make build-test-tools   # Build test utilities in bin/
```

### Testing
```bash
make test               # Run all unit tests
make test-coverage      # Run tests with coverage report
make test-e2e           # Run end-to-end integration tests
make coverage-html      # Generate HTML coverage report
make validate-coverage  # Ensure coverage meets 25% threshold
```

### Code Quality
```bash
make format             # Format code (MUST run after any edits)
make validate-format    # Check code formatting
make vet                # Run static analysis
make lint               # Run golangci-lint
make semgrep            # Run security analysis
```

### CI Pipeline
```bash
make ci                 # Run complete CI validation before commits
```

### Running the Server
```bash
make run                # Build and start server on :50051
./bin/opsd-server --addr=:8080 --rootfs=/mnt/host  # Custom options
```

## Architecture Overview

This is a gRPC-based service for SONiC firmware management with two main services:

1. **SystemInfo Service** - Platform detection and disk space monitoring
2. **FirmwareManagement Service** - Firmware download, cleanup, and image operations

### Key Design Principles
- **Container-aware**: Designed to run in containers with host filesystem mounted at `/mnt/host`
- **Path resolution**: All host filesystem access goes through `internal/paths` for container compatibility
- **Concurrent safety**: Download operations are synchronized to prevent conflicts
- **Error resilience**: Comprehensive error handling with retries for network operations

### Project Structure
```
cmd/
├── server/           # Main gRPC server binary (opsd-server)
└── test/            # Test utilities for debugging

internal/            # Private packages
├── bootloader/      # Bootloader detection
├── checksum/        # MD5 checksum validation
├── download/        # Network download engine with interface binding
├── firmware/        # Firmware management logic
├── hostinfo/        # Platform detection
├── installer/       # sonic-installer wrapper
├── paths/           # Container-to-host path resolution
└── redis/           # Redis client for image status

pkg/server/          # Public gRPC server implementations

proto/               # Protocol buffer definitions
```

### Testing Approach
- Unit tests alongside source files (`*_test.go`)
- Mock generation using `mockgen` for interfaces
- E2e tests in `tests/e2e/` for integration testing
- Test utilities in `cmd/test/` for component debugging

## Development Workflow

1. **Before starting**: Run `make ci` to ensure clean state
2. **After editing**: Always run `make format`
3. **Before committing**: Run `make ci` to validate all checks pass
4. **Testing changes**: Use `make test` for quick validation

## Important Notes

- The server binary was recently renamed from `sonic-ops-server` to `opsd-server`
- Always use absolute paths when working with filesystem operations
- Network downloads support interface-specific binding for multi-NIC systems
- Download operations include MD5 checksum validation for integrity
- The service follows gRPC best practices with proper error codes and status handling