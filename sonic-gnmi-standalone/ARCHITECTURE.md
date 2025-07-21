# SONiC gRPC Server Architecture

## Overview

A minimal gRPC server that provides only reflection services. No business logic is included.

## Components

### Server (`pkg/server/`)
- `server.go`: gRPC server with TLS support and reflection service registration

### Configuration (`internal/config/`)
- `config.go`: Command-line flags and environment variable handling

### Main Application (`cmd/server/`)
- `main.go`: Server startup and signal handling

## Directory Structure

```
sonic-gnmi-standalone/
├── cmd/server/          # Main application
├── internal/            # Private packages
│   └── config/          # Configuration management
├── pkg/server/          # gRPC server implementation
├── debian/              # Debian packaging files
├── docker/              # Docker configuration
└── Makefile             # Build automation
```

## Configuration

### Command-line Flags
- `--addr`: Bind address (default: `:50051`)
- `--rootfs`: Root filesystem path (default: `/mnt/host`)
- `--shutdown-timeout`: Graceful shutdown timeout (default: `10s`)
- `--tls-cert`, `--tls-key`: TLS certificate paths

### Environment Variables
- `DISABLE_TLS=true`: Disable TLS

## Build and Run

```bash
make build                    # Build binary
make test                     # Run tests
make ci                       # Run all checks

./bin/sonic-gnmi-standalone   # Run server
```

## Adding Services

1. Create `.proto` file in `proto/`
2. Generate Go code with `make proto`
3. Implement service in `pkg/server/`
4. Register service in `server.go`:
   ```go
   pb.RegisterMyServiceServer(grpcServer, myServiceServer)
   ```

## Deployment

### Container
```bash
docker build -t gnmi-standalone-test -f docker/Dockerfile .
docker run -p 50051:50051 gnmi-standalone-test
```

### Debian Package
```bash
make deb
dpkg -i build/sonic-gnmi-standalone_*.deb
```

## Testing

```bash
# Verify server is running
grpcurl -plaintext localhost:50051 list

# Output:
# grpc.reflection.v1.ServerReflection
# grpc.reflection.v1alpha.ServerReflection
```