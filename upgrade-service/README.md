# SONiC Upgrade Service

This directory contains the Go-based gRPC server implementation for all SONiC upgrade-related services.

## Prerequisites

- Go 1.18 or later
- Protocol Buffers compiler (protoc)
- Git

## Directory Structure

```
upgrade-service/
├── cmd/                # Command-line applications
│   └── server/         # gRPC server implementation
├── internal/           # Private code not intended for external use
│   ├── hostinfo/       # Platform information providers
│   └── util/           # Internal utility functions
├── pkg/                # Public packages that can be imported by other applications
│   └── server/         # Server implementation
├── proto/              # Protocol buffer definitions and generated code
│   ├── system_info.proto   # System info service definition
│   ├── system_info.pb.go   # Generated Go code for messages
│   └── system_info_grpc.pb.go # Generated Go code for gRPC service
├── tests/              # Test files
│   └── e2e/            # End-to-end tests
├── go.mod              # Go module definition
├── go.sum              # Go module checksums
├── Makefile            # Build instructions
└── README.md           # This file
```

## Getting Started

### Installing Required Tools

You can install the required Go protobuf tools with:

```bash
make tools
```

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

By default, the server runs on port 50051. You can specify a different port with:

```bash
./bin/server --addr=:8080
```

## Testing and Verification

The project includes several targets for testing and verification:

```bash
# Run the entire CI pipeline locally
make ci

# Format checking
make fmt

# Validate protobuf files are up-to-date
make validate-proto

# Verify Go modules are tidy
make tidy

# Run static analysis
make vet

# Run tests
make test

# Verify Go modules
make verify
```

## Using grpcurl to Test the Service

You can use [grpcurl](https://github.com/fullstorydev/grpcurl) to test the service:

```bash
# List available services
grpcurl -plaintext localhost:50051 list

# List methods in the service
grpcurl -plaintext localhost:50051 list sonic.SystemInfo

# Call the GetPlatformType method to get platform information
grpcurl -plaintext localhost:50051 sonic.SystemInfo/GetPlatformType
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

1. Check Go version
2. Check code formatting (`make fmt`)
3. Validate proto-generated files (`make validate-proto`)
4. Verify modules are tidy (`make tidy`)
5. Build verification (`make build-all`)
6. Static analysis (`make vet`)
7. Run tests (`make test`)
8. Verify modules (`make verify`)

The pipeline ensures consistency between development and CI environments by using the same commands.

## Protobuf Development

When updating `.proto` files, you need to regenerate the Go code. You can do this in two ways:

### Using the Makefile

```bash
make proto
```

### Using the Update Script

For a more comprehensive update that handles installing protoc:

```bash
./scripts/update_proto.sh
```

After regenerating the files, validate them with:

```bash
make validate-proto
```

This will check if the generated files match what would be produced by the current `.proto` files.
