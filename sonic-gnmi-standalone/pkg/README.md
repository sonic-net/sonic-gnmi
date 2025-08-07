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
The `builder.go` file implements a fluent API for configuring services. See the inline documentation in `server/builder.go` for usage examples and details.
