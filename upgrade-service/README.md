# SONiC Upgrade Service

This directory contains the Go-based gRPC server implementation for dynamic SONiC operations including firmware management, system information, and upgrade operations.

## Overview

The SONiC Upgrade Service provides a comprehensive gRPC API for managing SONiC firmware and system operations. It supports:

- **Firmware Management**: Download, list, cleanup, and consolidate firmware images
- **System Information**: Platform detection, disk space monitoring, and system status
- **Download Operations**: Robust firmware downloading with progress tracking and retry mechanisms
- **Image Operations**: List installed images and manage image consolidation

## Key Features

### Firmware Management
- Download firmware from URLs with configurable timeouts and interface binding
- Real-time download progress tracking with session management
- Automatic filename detection from URLs
- Concurrent download blocking to prevent resource conflicts
- Comprehensive error handling with retry mechanisms
- Cleanup of old firmware files with space reclamation reporting
- Image consolidation using sonic-installer

### System Information
- Platform type detection (vendor, model, identifier)
- Disk space analysis for multiple filesystem paths
- Context-aware path resolution for container deployments

### Network & Download Features
- Interface-specific download binding (eth0, etc.)
- IPv4/IPv6 dual-stack support
- HTTP client with configurable timeouts
- Progress tracking with speed calculations
- Automatic retry with exponential backoff

## Prerequisites

- Go 1.18 or later
- Protocol Buffers compiler (protoc) - use `make install-protoc` to install
- Git

## Directory Structure

```
sonic-gnmi/upgrade-service/
├── cmd/                          # Command-line applications
│   ├── server/                   # Main gRPC server implementation
│   └── test/                     # Test utilities and debugging tools
│       ├── download/             # Download testing tool
│       ├── diskspace/            # Disk space analysis tool
│       ├── image-inspector/      # Firmware image inspection tool
│       ├── installer/            # sonic-installer wrapper testing
│       ├── list-images/          # Image listing tool
│       ├── bootloader/           # Bootloader detection testing
│       └── redis/                # Redis client testing
├── internal/                     # Private packages not intended for external use
│   ├── config/                   # Global configuration management
│   ├── hostinfo/                 # Platform information detection
│   ├── firmware/                 # Firmware operations (cleanup, version detection, consolidation)
│   ├── download/                 # Download engine with progress tracking
│   ├── installer/                # sonic-installer wrapper
│   ├── bootloader/               # Bootloader detection (GRUB, Aboot)
│   ├── diskspace/                # Disk space analysis utilities
│   ├── paths/                    # Path resolution for container deployments
│   └── redis/                    # Redis client wrapper
├── pkg/                          # Public packages for external consumption
│   └── server/                   # gRPC server implementations
│       ├── server.go             # Main server with TLS support
│       ├── system_info.go        # SystemInfo service implementation
│       └── firmware_management.go # FirmwareManagement service implementation
├── proto/                        # Protocol buffer definitions and generated code
│   ├── system_info.proto         # System information service definition
│   ├── firmware_management.proto # Firmware management service definition
│   ├── *.pb.go                   # Generated Go protobuf messages
│   └── *_grpc.pb.go              # Generated Go gRPC service code
├── tests/                        # Test files
│   └── e2e/                      # End-to-end integration tests
├── go.mod                        # Go module definition
├── go.sum                        # Go module checksums
├── Makefile                      # Comprehensive build and test automation
└── README.md                     # This documentation
```

## Getting Started

### Installing Required Tools

The project provides fine-grained tool management. You can install individual tools or all at once:

```bash
# Install all Go tools (protoc-gen-go, protoc-gen-go-grpc, mockgen)
make tools

# Install individual tools
make install-protoc-gen-go      # Install protoc-gen-go plugin
make install-protoc-gen-go-grpc # Install protoc-gen-go-grpc plugin  
make install-mockgen            # Install mockgen tool
make install-protoc             # Install protoc compiler

# Show all available tool management targets
make help-tools
```

**Note:** The `tools` target installs Go tools but excludes `protoc`. Install `protoc` separately with `make install-protoc`.

**Tool Versions:** This project uses pinned tool versions for reproducible builds:
- `protoc-gen-go v1.36.6` (latest)
- `protoc-gen-go-grpc v1.5.1` (latest) 
- `mockgen v0.5.2` from `go.uber.org/mock` (maintained fork)
- `golangci-lint v2.1.6` (latest) for comprehensive code linting

### Generating Protobuf Files

Generate the protobuf Go files with:

```bash
make proto
```

This will create the Go protobuf files in the `proto/` directory. These files are:
- `proto/*.pb.go` - The generated Go protobuf code
- `proto/*_grpc.pb.go` - The generated Go gRPC service code

## Building and Running

### Building the Project

Build the server (this will also generate protobuf files if needed):

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
# Basic usage with default settings (port 50051, rootfs /mnt/host)
./bin/sonic-ops-server

# Specify different port and rootfs (useful for containers vs baremetal)
./bin/sonic-ops-server --addr=:8080 --rootfs=/host

# Enable verbose logging
./bin/sonic-ops-server -v=2

# Show all available options
./bin/sonic-ops-server --help
```

**Configuration Options:**
- `--addr`: Server address (default: `:50051`)
- `--rootfs`: Root filesystem mount point (default: `/mnt/host`)
- `--shutdown-timeout`: Graceful shutdown timeout (default: `10s`)
- `-v`: Verbose logging level for glog

## Testing and Verification

The project includes several targets for testing and verification:

```bash
# Run the entire CI pipeline locally (validation only, no tool installation)
make ci

# Code formatting
make format                      # Fix code formatting automatically
make validate-format             # Validate code formatting (CI use)

# Tool validation (checks if tools are available)
make validate-tools              # Check all tools
make validate-protobuf-tools     # Check protobuf tools only
make validate-protoc             # Check protoc only
make validate-protoc-gen-go      # Check protoc-gen-go only
make validate-mockgen            # Check mockgen only

# Code generation validation
make validate-proto              # Validate protobuf files are up-to-date
make validate-mocks              # Validate mock files are up-to-date

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
                                 # (configurable via COVERAGE_THRESHOLD)
```

**CI vs Local Development:**
- CI pipeline (`make ci`) validates everything without installing tools
- Local development can use install targets (`make tools`, `make install-*`) to set up tools
- Clear error messages guide you to the right install command when tools are missing

**Coverage Configuration:**
- Coverage threshold is configurable via environment variable
- Example: `COVERAGE_THRESHOLD=50 make validate-coverage`
- Coverage reports exclude generated code (proto, mocks) automatically
- See Makefile for current default threshold value

## API Documentation

The service provides two main gRPC services:

### SystemInfo Service

Provides system information and monitoring capabilities.

#### GetPlatformType
Retrieves platform identification information.

```bash
# Get platform information
grpcurl -plaintext localhost:50051 sonic.SystemInfo/GetPlatformType
```

**Response includes:**
- `platform_identifier`: Platform identifier string
- `vendor`: Hardware vendor
- `model`: Hardware model

#### GetDiskSpace
Analyzes disk space usage for specified paths.

```bash
# Get disk space for default paths (/, /host, /tmp)
grpcurl -plaintext localhost:50051 sonic.SystemInfo/GetDiskSpace

# Get disk space for custom paths
grpcurl -plaintext -d '{"paths": ["/var/log", "/boot"]}' \
  localhost:50051 sonic.SystemInfo/GetDiskSpace
```

**Response includes:**
- `filesystems`: Array of disk space information per path
  - `path`: Filesystem path
  - `total_mb`: Total space in MB
  - `free_mb`: Available space in MB  
  - `used_mb`: Used space in MB
  - `error_message`: Error details if path inaccessible

### FirmwareManagement Service

Handles firmware download, management, and system operations.

#### DownloadFirmware
Downloads firmware from a URL with progress tracking.

```bash
# Download with auto-detected filename to /host
grpcurl -plaintext -d '{"url": "https://httpbin.org/bytes/32768"}' \
  localhost:50051 sonic.FirmwareManagement/DownloadFirmware

# Download with custom output path and timeouts
grpcurl -plaintext -d '{
  "url": "https://example.com/firmware.bin",
  "output_path": "/tmp/my-firmware.bin",
  "connect_timeout_seconds": 30,
  "total_timeout_seconds": 300
}' localhost:50051 sonic.FirmwareManagement/DownloadFirmware
```

**Parameters:**
- `url`: Source URL for firmware download (required)
- `output_path`: Destination file path (optional, auto-detected if empty)
- `connect_timeout_seconds`: Connection timeout (optional, default: 30)
- `total_timeout_seconds`: Total download timeout (optional, default: 3600)

**Response:**
- `session_id`: Unique session identifier for tracking
- `status`: Current download status
- `output_path`: Resolved output file path

#### GetDownloadStatus
Retrieves real-time download progress and status.

```bash
# Get download status using session ID
grpcurl -plaintext -d '{"session_id": "download-1234567890"}' \
  localhost:50051 sonic.FirmwareManagement/GetDownloadStatus
```

**Response states:**
- `starting`: Download initialization
- `progress`: Active download with progress metrics
- `result`: Successful completion with file details
- `error`: Download failure with error information

#### ListFirmwareImages
Discovers firmware images in the system.

```bash
# List all firmware images in default locations
grpcurl -plaintext localhost:50051 sonic.FirmwareManagement/ListFirmwareImages

# List images with version filter
grpcurl -plaintext -d '{"version_pattern": "202311.*"}' \
  localhost:50051 sonic.FirmwareManagement/ListFirmwareImages

# List images in custom directories
grpcurl -plaintext -d '{"search_directories": ["/var/lib/firmware"]}' \
  localhost:50051 sonic.FirmwareManagement/ListFirmwareImages
```

#### CleanupOldFirmware
Removes old firmware files and reports space reclaimed.

```bash
# Cleanup firmware files from default locations (/host, /tmp)
grpcurl -plaintext localhost:50051 sonic.FirmwareManagement/CleanupOldFirmware
```

**Response:**
- `files_deleted`: Number of files removed
- `deleted_files`: List of deleted file paths
- `space_freed_bytes`: Total bytes reclaimed
- `errors`: Any cleanup errors encountered

#### ConsolidateImages
Manages installed firmware images using sonic-installer.

```bash
# Dry run to see what would be consolidated
grpcurl -plaintext -d '{"dry_run": true}' \
  localhost:50051 sonic.FirmwareManagement/ConsolidateImages

# Execute consolidation
grpcurl -plaintext -d '{"dry_run": false}' \
  localhost:50051 sonic.FirmwareManagement/ConsolidateImages
```

#### ListImages
Lists currently installed firmware images via sonic-installer.

```bash
# List installed images
grpcurl -plaintext localhost:50051 sonic.FirmwareManagement/ListImages
```

## Using grpcurl to Test the Service

You can use [grpcurl](https://github.com/fullstorydev/grpcurl) to explore and test the service:

```bash
# List available services
grpcurl -plaintext localhost:50051 list

# List methods in SystemInfo service
grpcurl -plaintext localhost:50051 list sonic.SystemInfo

# List methods in FirmwareManagement service  
grpcurl -plaintext localhost:50051 list sonic.FirmwareManagement

# Describe a specific method
grpcurl -plaintext localhost:50051 describe sonic.FirmwareManagement.DownloadFirmware
```

## Documentation

### Comprehensive Guides

- **[API Reference](API_REFERENCE.md)**: Complete gRPC API documentation with examples
- **[Architecture Guide](ARCHITECTURE.md)**: System design, components, and deployment patterns
- **[README](README.md)**: This file - getting started and basic usage

### Quick Links

- **API Examples**: See [API_REFERENCE.md](API_REFERENCE.md#examples) for detailed gRPC usage
- **System Design**: See [ARCHITECTURE.md](ARCHITECTURE.md) for component architecture  
- **Testing Guide**: See [Testing and Verification](#testing-and-verification) section above
- **Deployment**: See [Deployment Configurations](#deployment-configurations) section above

## Development

This service is implemented as a separate Go module within the `sonic-gnmi` repository to provide a comprehensive gRPC server for dynamic SONiC operations.

### Development Workflow

When adding new functionality:

1. **Define API**: Update `.proto` files in the `proto/` directory
2. **Generate Code**: Run `make proto` to update Go protobuf files
3. **Implement Service**: Add business logic in the `pkg/server/` directory
4. **Add Internal Logic**: Implement supporting functionality in `internal/` packages
5. **Create Tests**: Add unit tests and update e2e tests
6. **Update Documentation**: Update API_REFERENCE.md and add godoc comments
7. **Validate**: Run `make ci` to ensure all checks pass

### Code Organization

- **`pkg/server/`**: Public gRPC service implementations
- **`internal/`**: Private business logic packages
- **`cmd/`**: Command-line applications and test utilities
- **`proto/`**: Protocol buffer definitions and generated code
- **`tests/e2e/`**: End-to-end integration tests

### Documentation Standards

- **Godoc Comments**: All public types and functions must have comprehensive godoc comments
- **API Documentation**: Update API_REFERENCE.md for any proto changes
- **Architecture Updates**: Update ARCHITECTURE.md for significant design changes
- **Examples**: Include working examples in documentation

## CI/CD Integration

This project includes a CI pipeline configured in the `.azure-pipelines/api-service-ci.yml` file, which runs a series of checks to validate code quality and correctness. The CI pipeline utilizes the same Makefile targets that are available for local development.

### CI Pipeline Steps

The CI pipeline runs `make ci` which includes these validation steps:

1. **Code formatting** (`make validate-format`) - Validates consistent code style
2. **Tool validation** (`make validate-tools`) - Checks required tools are available
3. **Proto validation** (`make validate-proto`) - Ensures protobuf files are up-to-date
4. **Mock validation** (`make validate-mocks`) - Ensures mock files are up-to-date  
5. **Module tidiness** (`make tidy`) - Cleans up Go modules
6. **Build verification** (`make build-all`) - Ensures all packages compile
7. **Static analysis** (`make vet`) - Runs Go static analysis
8. **Code linting** (`make lint`) - Runs golangci-lint for comprehensive code quality checks
9. **Unit tests** (`make test`) - Runs all unit tests
10. **Coverage validation** (`make validate-coverage`) - Ensures code coverage meets minimum threshold
11. **E2E tests** (`make test-e2e`) - Runs end-to-end tests
12. **Module verification** (`make verify`) - Verifies module integrity

**Key Design Principles:**
- CI validates rather than installs tools (fails fast with clear error messages)
- Consistent commands between local development and CI
- Fine-grained targets allow targeted validation and troubleshooting

## Protobuf Development

When updating `.proto` files, you need to regenerate the Go code. You can do this in two ways:

### Using the Makefile

```bash
make proto
```

### Manual Regeneration

You can also regenerate protobuf files manually using protoc directly:

```bash
# Ensure tools are available
make validate-protobuf-tools

# Generate protobuf files
PATH="$(go env GOPATH)/bin:$PATH" protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    proto/*.proto
```

After regenerating the files, always validate them:

```bash
make validate-proto
```

This ensures the generated files match what would be produced by the current `.proto` files.

## Deployment Configurations

The server is designed to work in different deployment scenarios:

### Container Deployment
```bash
# Container with host filesystem mounted at /mnt/host
./bin/sonic-ops-server --rootfs=/mnt/host --addr=:50051
```

### Baremetal Deployment  
```bash
# Direct baremetal installation
./bin/sonic-ops-server --rootfs=/ --addr=:50051
```

### Development/Testing
```bash
# Local testing with custom filesystem
./bin/sonic-ops-server --rootfs=/tmp/test-env --addr=:50052
```

The `--rootfs` flag allows the server to find system files (like `/host/machine.conf`) regardless of the deployment environment.
