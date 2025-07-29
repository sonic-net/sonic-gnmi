# cmd - Server Binary

This directory contains the entry point for the gRPC server binary.

## Production Binary

### server/
The main gRPC server binary. This provides the foundation for gRPC services with reflection support and a builder pattern for dynamic service registration.

**Build:** `make build`  
**Usage:** `./bin/sonic-gnmi-standalone`

## Command-line Options

The server supports the following command-line options:

```bash
# Basic usage with default settings (port 50055, rootfs /mnt/host)
./bin/sonic-gnmi-standalone

# Specify different port and rootfs (useful for containers vs baremetal)
./bin/sonic-gnmi-standalone --addr=:8080 --rootfs=/host

# Enable verbose logging
./bin/sonic-gnmi-standalone -v=2

# Run without TLS (for development)
./bin/sonic-gnmi-standalone --no-tls

# Show all available options
./bin/sonic-gnmi-standalone --help
```

## Building

```bash
# Build the server binary
make build

# Clean built binaries
make clean
```

## Purpose

This server provides a minimal gRPC foundation with:
- gRPC reflection for development tools
- TLS support with configurable certificates
- Builder pattern for dynamic service enablement
- Container-aware filesystem configuration
