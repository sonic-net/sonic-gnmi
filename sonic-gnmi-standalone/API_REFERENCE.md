# SONiC gRPC Server API Reference

This document provides API documentation for the minimal SONiC gRPC Server Foundation.

## Overview

The SONiC gRPC Server Foundation provides a minimal gRPC server with only the built-in reflection service. This serves as a clean baseline for implementing specific gRPC services.

## Available Services

The server currently provides only the standard gRPC reflection services:

### gRPC Reflection Services

The server automatically includes the following built-in reflection services:

1. **grpc.reflection.v1.ServerReflection** - Current reflection service
2. **grpc.reflection.v1alpha.ServerReflection** - Legacy reflection service

These services allow development tools like grpcurl to discover and interact with the server.

## Connection Details

- **Default Port**: 50051
- **Protocol**: gRPC over HTTP/2
- **TLS**: Optional (configurable via environment variables)
- **Reflection**: Enabled by default

## Using grpcurl

You can use [grpcurl](https://github.com/fullstorydev/grpcurl) to interact with the server:

### Basic Commands

```bash
# List available services
grpcurl -plaintext localhost:50051 list

# Expected output:
# grpc.reflection.v1.ServerReflection
# grpc.reflection.v1alpha.ServerReflection

# Describe the reflection service
grpcurl -plaintext localhost:50051 describe grpc.reflection.v1.ServerReflection

# Get server information (this will work but return empty results)
grpcurl -plaintext localhost:50051 grpc.reflection.v1.ServerReflection/ServerReflectionInfo
```

### Testing Server Connectivity

```bash
# Test if server is responding
grpcurl -plaintext -d '{}' localhost:50051 grpc.reflection.v1.ServerReflection/ServerReflectionInfo
```

## Configuration

The server can be configured through command-line flags and environment variables:

### Command-Line Configuration

```bash
# Basic configuration
./bin/opsd-server --addr=:8080 --rootfs=/host

# TLS configuration
./bin/opsd-server --tls-cert=server.crt --tls-key=server.key

# Verbose logging
./bin/opsd-server -v=2
```

### Environment Variables

- `DISABLE_TLS=true` - Disable TLS encryption (default: false)

## Development Guide

This minimal server is designed as a foundation for implementing gRPC services. To add new services:

### 1. Add Protocol Buffer Definitions

Create `.proto` files in a `proto/` directory:

```protobuf
syntax = "proto3";

package myservice;

service MyService {
  rpc MyMethod(MyRequest) returns (MyResponse) {}
}

message MyRequest {
  string data = 1;
}

message MyResponse {
  string result = 1;
}
```

### 2. Generate Go Code

Add proto generation to the Makefile:

```makefile
proto: validate-protobuf-tools
	PATH="$(shell go env GOPATH)/bin:$$PATH" protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/*.proto
```

### 3. Implement the Service

Create service implementation in `pkg/server/`:

```go
package server

import (
    "context"
    pb "your-module/proto"
)

type MyServiceServer struct {
    pb.UnimplementedMyServiceServer
}

func NewMyServiceServer() *MyServiceServer {
    return &MyServiceServer{}
}

func (s *MyServiceServer) MyMethod(ctx context.Context, req *pb.MyRequest) (*pb.MyResponse, error) {
    return &pb.MyResponse{
        Result: "Hello " + req.Data,
    }, nil
}
```

### 4. Register the Service

Update `server.go` to register your service:

```go
// Import your proto package
pb "your-module/proto"

// In NewServerWithTLS function, after creating grpcServer:
myServiceServer := NewMyServiceServer()
pb.RegisterMyServiceServer(grpcServer, myServiceServer)
```

### 5. Update Build Dependencies

Ensure your build target includes proto validation:

```makefile
build: validate-proto
    go build -o bin/opsd-server cmd/server/main.go
```

## Error Handling

The server uses standard gRPC status codes:

- `OK` (0): Success
- `INVALID_ARGUMENT` (3): Invalid request parameters
- `NOT_FOUND` (5): Resource not found
- `ALREADY_EXISTS` (6): Resource already exists
- `FAILED_PRECONDITION` (9): Operation cannot be performed
- `INTERNAL` (13): Internal server error
- `UNAVAILABLE` (14): Service temporarily unavailable

## Security

### TLS Configuration

The server supports optional TLS encryption:

```bash
# Enable TLS with custom certificates
./bin/opsd-server --tls-cert=server.crt --tls-key=server.key

# Disable TLS for development
DISABLE_TLS=true ./bin/opsd-server
```

### Network Security

- Configure bind addresses for network isolation
- Use TLS in production deployments
- Consider firewall rules for port access

## Monitoring and Debugging

### Health Checking

Use grpcurl to verify server health:

```bash
# Check if server is responding
grpcurl -plaintext localhost:50051 list

# If this returns the reflection services, the server is healthy
```

### Logging

The server uses glog for logging:

```bash
# Enable verbose logging
./bin/opsd-server -v=2 -logtostderr

# Log to files
./bin/opsd-server -log_dir=/var/log/
```

## Future Development

This minimal foundation is designed to grow with your needs:

1. **Add Business Services**: Implement domain-specific gRPC services
2. **Add Authentication**: Implement mTLS or token-based authentication
3. **Add Monitoring**: Integrate Prometheus metrics or distributed tracing
4. **Add Middleware**: Implement logging, rate limiting, or other cross-cutting concerns

The clean foundation ensures you can add these features without technical debt or architectural constraints.