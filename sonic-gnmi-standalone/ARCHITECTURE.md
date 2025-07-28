# SONiC gRPC Server Architecture

## Overview

A minimal gRPC server foundation with a builder pattern for dynamic service registration. Provides gRPC reflection services and infrastructure for adding business services.

## Components

### Server (`pkg/server/`)
- `server.go`: Core gRPC server with TLS support and reflection service registration
- `builder.go`: ServerBuilder pattern for fluent service configuration
- `config/config.go`: Global server configuration management

### Main Application (`cmd/server/`)
- `main.go`: Server startup and signal handling

## Directory Structure

```
sonic-gnmi-standalone/
├── cmd/server/          # Main application
├── internal/            # Private packages (reserved for future use)
├── pkg/server/          # gRPC server implementation
│   ├── builder.go       # ServerBuilder pattern
│   ├── server.go        # Core server implementation
│   └── config/          # Server configuration
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
- `--no-tls`: Disable TLS (TLS enabled by default)

## Build and Run

```bash
make build                    # Build binary
make test                     # Run tests
make ci                       # Run all checks

./bin/sonic-gnmi-standalone   # Run server
```

## Adding Services

The server uses a builder pattern for dynamic service registration:

1. Create `.proto` file in `proto/`
2. Generate Go code with `make proto`
3. Implement service in appropriate package (e.g., `pkg/server/gnoi/system/`)
4. Add enable method to ServerBuilder in `builder.go`:
   ```go
   func (b *ServerBuilder) EnableMyService() *ServerBuilder {
       b.services["my.service"] = true
       return b
   }
   ```
5. Register service in `registerServices` method:
   ```go
   if b.services["my.service"] {
       myServiceServer := myservice.NewServer(rootFS)
       pb.RegisterMyServiceServer(srv.grpcServer, myServiceServer)
       glog.Info("Registered My service")
   }
   ```
6. Use the builder in main.go:
   ```go
   srv, err := server.NewServerBuilder().
       EnableMyService().
       Build()
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