# SONiC gRPC Server Foundation

This directory contains a minimal gRPC server foundation for SONiC operations. It provides a clean baseline with gRPC reflection support and a builder pattern for implementing specific gRPC services.

## Overview

This minimal gRPC server provides:

- **Base gRPC Infrastructure**: Configurable server with TLS support
- **gRPC Reflection**: Built-in reflection service for development tools like grpcurl
- **Service Builder Pattern**: Fluent API for dynamically enabling/disabling services
- **Clean Foundation**: Infrastructure ready for service implementations
- **Container-Ready**: Designed for container deployment with configurable filesystem paths

## Prerequisites

- Go 1.18 or later
- Git

## Directory Structure

```
sonic-gnmi-standalone/
├── cmd/
│   └── server/                   # Main gRPC server application
├── internal/                     # Private packages (reserved for future use)
├── pkg/server/                   # Public gRPC server implementation
│   ├── builder.go                # ServerBuilder pattern for service registration
│   ├── server.go                 # Main server with TLS support
│   └── config/                   # Server configuration
│       └── config.go             # Global configuration management
├── go.mod                        # Go module definition
├── go.sum                        # Go module checksums
├── Makefile                      # Build and test automation
└── README.md                     # This documentation
```

## Getting Started

### Building the Project

Build the server:

```bash
make build
```

Build all packages:

```bash
make build-all
```

### Running the Server

Start the server (this will build the project first if needed):

```bash
make run
```

The server supports the following command-line options:

```bash
# Basic usage with default settings (port 50055, rootfs /mnt/host)
./bin/sonic-gnmi-standalone

# Specify different port and rootfs (useful for containers vs baremetal)
./bin/sonic-gnmi-standalone --addr=:8080 --rootfs=/host

# Enable verbose logging
./bin/sonic-gnmi-standalone -v=2

# Show all available options
./bin/sonic-gnmi-standalone --help
```

**Configuration Options:**
- `--addr`: Server address (default: `:50055`)
- `--rootfs`: Root filesystem mount point (default: `/mnt/host`)
- `--shutdown-timeout`: Graceful shutdown timeout (default: `10s`)
- `--no-tls`: Disable TLS (TLS is enabled by default)
- `-v`: Verbose logging level for glog

## Testing and Verification

The project includes several targets for testing and verification:

```bash
# Run the entire CI pipeline locally
make ci

# Code formatting
make format                      # Fix code formatting automatically
make validate-format             # Validate code formatting (CI use)

# Go module management
make tidy                        # Tidy Go modules
make verify                      # Verify Go modules

# Code quality
make vet                         # Run static analysis
make lint                        # Run golangci-lint for comprehensive linting
make test                        # Run all tests

# Code coverage
make test-coverage               # Run tests with coverage collection
make coverage-summary            # Show coverage summary in terminal
make coverage-html               # Generate HTML coverage report (coverage.html)
make validate-coverage           # Validate coverage meets minimum threshold

# Clean build artifacts
make clean                       # Remove built binaries
```

## Using grpcurl to Test the Server

You can use [grpcurl](https://github.com/fullstorydev/grpcurl) to explore and test the reflection service:

```bash
# List available services (should show only reflection)
grpcurl -plaintext localhost:50055 list

# Expected output:
# grpc.reflection.v1.ServerReflection
# grpc.reflection.v1alpha.ServerReflection
```

## Development

This server provides a minimal foundation for gRPC service development. To add new services:

1. **Define Proto Files**: Add `.proto` files in the `proto/` directory
2. **Generate Code**: Update Makefile to generate Go protobuf files
3. **Implement Services**: Add service implementations in appropriate packages (e.g., `pkg/server/gnoi/`)
4. **Register Services**: Use the ServerBuilder pattern to register your services
5. **Add Business Logic**: Implement supporting functionality in `internal/` packages
6. **Add Tests**: Create comprehensive unit and integration tests
7. **Update Documentation**: Document your APIs and services

### Using the ServerBuilder Pattern

The ServerBuilder provides a fluent API for configuring which services are enabled:

```go
// In main.go
srv, err := server.NewServerBuilder().
    WithAddress(":50055").
    WithRootFS("/mnt/host").
    EnableGNOISystem().                // Enable specific services
    EnableServices([]string{"gnmi"}).  // Or enable multiple at once
    Build()
```

This pattern allows for:
- Dynamic service configuration
- Clean separation of concerns
- Easy addition of new services
- Configuration-driven service enablement

### Development Workflow

1. **Before starting**: Run `make ci` to ensure clean state
2. **After editing**: Run `make format` to fix code formatting
3. **Before committing**: Run `make ci` to validate all checks pass
4. **Testing changes**: Use `make test` for quick validation

## Deployment Configurations

The server is designed to work in different deployment scenarios:

### Container Deployment
```bash
# Container with host filesystem mounted at /mnt/host
./bin/sonic-gnmi-standalone --rootfs=/mnt/host --addr=:50055
```

### Baremetal Deployment
```bash
# Direct baremetal installation
./bin/sonic-gnmi-standalone --rootfs=/ --addr=:50055
```

### Development/Testing
```bash
# Local testing with custom filesystem
./bin/sonic-gnmi-standalone --rootfs=/tmp/test-env --addr=:50052
```

The `--rootfs` flag allows the server to find system files regardless of the deployment environment.

## CI/CD Integration

This project includes CI pipeline integration that validates code quality and correctness. The CI pipeline runs `make ci` which includes these validation steps:

1. **Code formatting** (`make validate-format`) - Validates consistent code style
2. **Module tidiness** (`make tidy`) - Cleans up Go modules
3. **Build verification** (`make build-all`) - Ensures all packages compile
4. **Static analysis** (`make vet`) - Runs Go static analysis
5. **Code linting** (`make lint`) - Runs golangci-lint for comprehensive code quality checks
6. **Unit tests** (`make test`) - Runs all unit tests
7. **Coverage validation** (`make validate-coverage`) - Ensures code coverage meets minimum threshold
8. **Module verification** (`make verify`) - Verifies module integrity
9. **Security analysis** (`make semgrep`) - Runs semgrep security analysis

## Security Considerations

### TLS Configuration
- Optional TLS with certificate validation
- Support for custom CA certificates
- Secure defaults with option to disable for testing

### Network Security
- Configurable bind addresses for network isolation
- Support for TLS encryption in production deployments

## Architecture

This minimal server follows clean architecture principles:

- **Separation of Concerns**: Clear separation between server infrastructure and business logic
- **Extensibility**: Easy to extend with new gRPC services
- **Container-Aware**: Designed for container-based deployments as well as baremetal

The foundation provides everything needed to build robust gRPC services while maintaining simplicity and clarity.
