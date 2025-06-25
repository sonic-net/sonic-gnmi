# SONiC Upgrade Service

This directory contains the Go-based gRPC server implementation for all SONiC upgrade-related services.

## Prerequisites

- Go 1.18 or later
- Protocol Buffers compiler (protoc) - use `make install-protoc` to install
- Git

## Directory Structure

```
upgrade-service/
├── cmd/                # Command-line applications
│   └── server/         # gRPC server implementation
├── internal/           # Private code not intended for external use
│   ├── config/         # Global configuration management
│   ├── hostinfo/       # Platform information providers
│   └── util/           # Internal utility functions
├── pkg/                # Public packages that can be imported by other applications
│   └── server/         # Server implementation
├── proto/              # Protocol buffer definitions and generated code
│   ├── system_info.proto        # System info service definition
│   ├── firmware_management.proto # Firmware management service definition
│   ├── *.pb.go                  # Generated Go code for messages
│   └── *_grpc.pb.go             # Generated Go code for gRPC services
├── tests/              # Test files
│   └── e2e/            # End-to-end tests
├── go.mod              # Go module definition
├── go.sum              # Go module checksums
├── Makefile            # Build instructions
└── README.md           # This file
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
./bin/server

# Specify different port and rootfs (useful for containers vs baremetal)
./bin/server --addr=:8080 --rootfs=/host

# Enable verbose logging
./bin/server -v=2

# Show all available options
./bin/server --help
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

# Format checking
make fmt

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
make test                        # Run all tests
```

**CI vs Local Development:**
- CI pipeline (`make ci`) validates everything without installing tools
- Local development can use install targets (`make tools`, `make install-*`) to set up tools
- Clear error messages guide you to the right install command when tools are missing

## Using grpcurl to Test the Service

You can use [grpcurl](https://github.com/fullstorydev/grpcurl) to test the service:

```bash
# List available services
grpcurl -plaintext localhost:50051 list

# List methods in SystemInfo service
grpcurl -plaintext localhost:50051 list sonic.SystemInfo

# List methods in FirmwareManagement service  
grpcurl -plaintext localhost:50051 list sonic.FirmwareManagement

# Call the GetPlatformType method to get platform information
grpcurl -plaintext localhost:50051 sonic.SystemInfo/GetPlatformType

# Call the CleanupOldFirmware method (example - requires implementation)
grpcurl -plaintext localhost:50051 sonic.FirmwareManagement/CleanupOldFirmware
```

## Development

This service is implemented as a separate Go module within the `sonic-gnmi` repository to provide a comprehensive gRPC server for all SONiC upgrade-related operations.

When adding new functionality:
1. Define the service interface in `.proto` files in the `proto/` directory
2. Generate Go code using `make proto`
3. Implement the service in the `pkg/server/` directory
4. Add any internal helpers in the `internal/util/` directory
5. Create entry points in the `cmd/` directory

## CI/CD Integration

This project includes a CI pipeline configured in the `.azure-pipelines/api-service-ci.yml` file, which runs a series of checks to validate code quality and correctness. The CI pipeline utilizes the same Makefile targets that are available for local development.

### CI Pipeline Steps

The CI pipeline runs `make ci` which includes these validation steps:

1. **Code formatting** (`make fmt`) - Ensures consistent code style
2. **Tool validation** (`make validate-tools`) - Checks required tools are available
3. **Proto validation** (`make validate-proto`) - Ensures protobuf files are up-to-date
4. **Mock validation** (`make validate-mocks`) - Ensures mock files are up-to-date  
5. **Module tidiness** (`make tidy`) - Cleans up Go modules
6. **Build verification** (`make build-all`) - Ensures all packages compile
7. **Static analysis** (`make vet`) - Runs Go static analysis
8. **Unit tests** (`make test`) - Runs all unit tests
9. **E2E tests** (`make test-e2e`) - Runs end-to-end tests
10. **Module verification** (`make verify`) - Verifies module integrity

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
./bin/server --rootfs=/mnt/host --addr=:50051
```

### Baremetal Deployment  
```bash
# Direct baremetal installation
./bin/server --rootfs=/ --addr=:50051
```

### Development/Testing
```bash
# Local testing with custom filesystem
./bin/server --rootfs=/tmp/test-env --addr=:50052
```

The `--rootfs` flag allows the server to find system files (like `/host/machine.conf`) regardless of the deployment environment.
