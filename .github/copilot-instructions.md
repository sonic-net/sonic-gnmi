# Copilot Instructions for sonic-gnmi

## Project Overview

sonic-gnmi implements the gNMI (gRPC Network Management Interface) and gNOI (gRPC Network Operations Interface) servers for SONiC. It provides telemetry services supporting both dial-in (server streaming) and dial-out (client streaming) modes. This is the primary programmatic management interface for SONiC switches, enabling modern network automation via gRPC.

## Architecture

```
sonic-gnmi/
├── gnmi_server/         # gNMI server implementation
├── gnoi_client/         # gNOI client utilities
├── telemetry/           # Telemetry service entry point
├── dialout/             # Dial-out telemetry client
├── sonic_data_client/   # SONiC-specific data providers
├── sonic_db_config/     # Database configuration handling
├── sonic_service_client/ # Service interaction client
├── common_utils/        # Shared utilities
├── transl_utils/        # Translation utilities (translib integration)
├── proto/               # Protobuf definitions
├── internal/            # Internal packages
├── patches/             # Patches for dependencies
├── pkg/                 # Go packages
├── swsscommon/          # Go bindings for swss-common
├── test/                # Integration tests
├── test_utils/          # Test utilities
├── testdata/            # Test data files
├── tools/               # Development tools
├── doc/                 # Documentation
│   ├── grpc_telemetry.md
│   ├── dialout.md
│   └── gNMI_usage_examples.md
├── sonic-gnmi-standalone/ # Standalone build support
├── Makefile             # Build entry point
└── debian/              # Debian packaging
```

### Key Concepts
- **gNMI server**: Handles Get, Set, Subscribe RPCs on SONiC data
- **Dial-in mode**: Clients connect to the switch and subscribe to telemetry streams
- **Dial-out mode**: Switch pushes telemetry to remote collectors
- **Data sources**: Redis DBs (CONFIG_DB, STATE_DB, COUNTERS_DB), translib (YANG-based)
- **Events**: SONiC publishes state-change events (BGP, link state) via gNMI streaming

## Language & Style

- **Primary language**: Go (golang 1.8+)
- **Secondary**: Python (tests), Protobuf
- **Indentation**: Go standard (tabs for Go, 4 spaces for Python)
- **Naming conventions**:
  - Go packages: `lowercase`
  - Go exported: `PascalCase`
  - Go unexported: `camelCase`
  - Proto files: `snake_case.proto`
- **Go formatting**: Always run `gofmt` / `goimports`
- **Error handling**: Return errors explicitly; don't panic in library code

## Build Instructions

```bash
# Install Go (1.8+)
# See https://golang.org/doc/install

# Install telemetry server
go get -u github.com/sonic-net/sonic-gnmi/telemetry

# Install dial-out client
go get -u github.com/sonic-net/sonic-gnmi/dialout/dialout_client_cli

# Build Debian package
git clone https://github.com/sonic-net/sonic-gnmi.git
pushd sonic-gnmi
dpkg-buildpackage -rfakeroot -b -us -uc
popd

# Build with Make
make all
```

## Testing

```bash
# Run Go tests
go test ./...

# Run specific package tests
go test ./gnmi_server/ -v

# Integration tests (require Redis and SONiC environment)
cd test
pytest -v

# Run with coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

- Go unit tests alongside source code (`*_test.go`)
- Python integration tests in `test/`
- Test utilities in `test_utils/`
- Test data in `testdata/`

## PR Guidelines

- **Commit format**: `[component]: Description`
- **Signed-off-by**: REQUIRED (`git commit -s`)
- **CLA**: Sign Linux Foundation EasyCLA
- **Go formatting**: Code MUST pass `gofmt`
- **Proto changes**: Update generated code when modifying `.proto` files
- **Security**: gNMI server handles authentication — be security-conscious

## Common Patterns

### gNMI Path Handling
```go
// gNMI paths map to SONiC database tables
// /sonic-db:CONFIG_DB/TABLE/KEY/FIELD
```

### Data Client Pattern
```go
// sonic_data_client provides SONiC-specific data access
// Translates gNMI paths to Redis DB queries
// Supports both direct DB access and translib-based paths
```

### Event Streaming
```go
// ON_CHANGE subscriptions use Redis keyspace notifications
// SAMPLE subscriptions poll at specified intervals
```

## Dependencies

- **sonic-swss-common**: Database connectivity (Go bindings via CGo)
- **sonic-mgmt-common/translib**: YANG-based data translation
- **gRPC/protobuf**: RPC framework
- **Redis**: Backend database
- **Go modules**: See `go.mod` for full dependency list
- **OpenConfig models**: YANG models for gNMI paths

## Gotchas

- **CGo dependency**: Go bindings to swss-common use CGo — requires C/C++ build environment
- **Authentication**: gNMI server supports TLS and token-based auth — test both paths
- **Path translation**: gNMI paths must correctly map to SONiC DB tables — mismatches cause silent failures
- **Subscribe modes**: ON_CHANGE, SAMPLE, and TARGET_DEFINED behave differently — test all modes
- **Proto compatibility**: Protobuf changes must be backwards compatible
- **Dial-out reconnection**: Dial-out client must handle connection failures gracefully
- **Performance**: High-frequency subscriptions can overwhelm Redis — use appropriate sampling intervals
- **Multi-ASIC**: gNMI must handle namespace-aware database connections for multi-ASIC platforms
