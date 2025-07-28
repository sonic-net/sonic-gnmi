# pkg

This directory contains reusable packages for the application.

## Subdirectories

### `server/`
Contains the core gRPC server infrastructure and service implementations.

#### Core Infrastructure
- `server.go`: Core gRPC server implementation with TLS support and reflection
- `builder.go`: ServerBuilder pattern for dynamic service registration
- `config/`: Server configuration management
  - `config.go`: Global configuration singleton with command-line flags

#### ServerBuilder Pattern
The `builder.go` file implements a fluent API for configuring services:

```go
// Example usage
srv, err := server.NewServerBuilder().
    WithAddress(":50051").              // Configure server address
    WithRootFS("/mnt/host").           // Set root filesystem path
    EnableGNOISystem().                // Enable specific service
    EnableServices([]string{"gnmi"}).  // Enable multiple services
    Build()
```

The builder pattern provides:
- Clean separation between infrastructure and services
- Dynamic service enablement/disablement
- Configuration-driven service selection
- Easy extension for new services

#### Adding New Services
Services can be added by:
1. Creating service implementation packages (e.g., `server/gnoi/system/`)
2. Adding enable methods to the ServerBuilder
3. Registering services in the `registerServices` method
