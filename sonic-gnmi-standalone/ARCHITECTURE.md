# SONiC gRPC Server Foundation Architecture

This document describes the architecture and design principles of the minimal SONiC gRPC Server Foundation, a clean baseline for implementing gRPC services.

## Overview

The SONiC gRPC Server Foundation is designed as a minimal, containerized gRPC server that provides only the essential infrastructure needed for gRPC service development. It follows clean architecture principles with clear separation between infrastructure and future business logic.

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        gRPC Clients                        │
│  (grpcurl, custom tooling, web interfaces, service mesh)   │
└─────────────────────┬───────────────────────────────────────┘
                      │ gRPC/HTTP2 + TLS (optional)
┌─────────────────────▼───────────────────────────────────────┐
│                   gRPC Server                              │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │           Reflection Service (built-in)               │ │
│  │  - grpc.reflection.v1.ServerReflection                │ │
│  │  - grpc.reflection.v1alpha.ServerReflection           │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                             │
│  [Ready for Custom Service Registration]                   │
└─────────────────────┬───────────────────────────────────────┘
                      │ Configuration & System calls
┌─────────────────────▼───────────────────────────────────────┐
│                 System Integration                         │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │
│  │Network   │ │TLS Cert  │ │Filesystem│ │Process   │      │
│  │Binding   │ │Management│ │Access    │ │Management│      │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘      │
└─────────────────────────────────────────────────────────────┘
```

## Core Components

### 1. gRPC Server Layer (`pkg/server/`)

The minimal server implementation that handles:
- TLS configuration and certificate management
- Reflection service registration (automatic)
- Graceful shutdown handling
- Network listener management

**Key File:**
- `server.go`: Minimal server with TLS support and reflection

**Key Features:**
- **Configurable TLS**: Optional TLS with certificate validation
- **Reflection Only**: Only built-in reflection services are registered
- **Clean Foundation**: No business logic, ready for service addition
- **Extensible Design**: Easy to add new service registrations

### 2. Configuration Layer (`internal/config/`)

Handles server configuration through command-line flags and environment variables:

**Configuration Options:**
- `--addr`: Server bind address (default: `:50051`)
- `--rootfs`: Root filesystem mount point (default: `/mnt/host`)
- `--shutdown-timeout`: Graceful shutdown timeout (default: `10s`)
- `--tls-cert`, `--tls-key`: TLS certificate paths
- `DISABLE_TLS`: Environment variable to disable TLS

**Key Features:**
- **Container-Aware**: Configurable root filesystem for container deployments
- **Security-First**: TLS enabled by default, explicit disable required
- **Flexible Deployment**: Works in container, baremetal, and development environments

### 3. Main Application (`cmd/server/`)

Simple main application that:
- Initializes configuration from flags and environment
- Creates and starts the gRPC server
- Handles graceful shutdown on system signals
- Provides comprehensive logging

## Design Principles

### 1. Minimalism

The foundation provides only essential infrastructure:
- gRPC server with reflection
- TLS configuration
- Basic configuration management
- Graceful shutdown

**No Business Logic:** The server contains no domain-specific functionality, making it a clean slate for service development.

### 2. Container-First Design

Designed for modern container deployments:
- Configurable filesystem root for mounted host filesystems
- Environment variable configuration
- Clean shutdown handling for container orchestrators
- No assumptions about deployment environment

### 3. Security by Default

Security-conscious defaults:
- TLS enabled by default
- Certificate validation
- Secure configuration options
- Explicit security disable for development

### 4. Extensibility

Clean extension points for adding services:
- Clear service registration pattern in `server.go`
- Modular configuration system
- Standardized error handling
- Consistent logging approach

## Service Registration Pattern

The server is designed to make service registration simple and consistent:

```go
// Current minimal registration (reflection only)
reflection.Register(grpcServer)

// Future service registration pattern:
myServiceServer := NewMyServiceServer()
pb.RegisterMyServiceServer(grpcServer, myServiceServer)
```

## Configuration Architecture

### Command-Line Configuration
```bash
./bin/opsd-server --addr=:8080 --rootfs=/host --shutdown-timeout=30s
```

### Environment-Based Configuration
```bash
DISABLE_TLS=true ./bin/opsd-server
```

### Container-Aware Path Resolution
- `--rootfs=/mnt/host` for container deployments
- `--rootfs=/` for baremetal deployments
- Configurable for any deployment scenario

## Deployment Scenarios

### 1. Container Deployment (Primary Target)

```yaml
# Kubernetes/Docker deployment
apiVersion: v1
kind: Pod
spec:
  containers:
  - name: grpc-server
    image: sonic-grpc-foundation
    args: ["--rootfs=/mnt/host", "--addr=:8080"]
    ports:
    - containerPort: 8080
    volumeMounts:
    - name: host-filesystem
      mountPath: /mnt/host
      readOnly: true
  volumes:
  - name: host-filesystem
    hostPath:
      path: /
```

### 2. Baremetal Deployment

```bash
# Direct installation on SONiC device
./opsd-server --rootfs=/ --addr=:50051 --tls-cert=/etc/ssl/server.crt
```

### 3. Development Environment

```bash
# Local testing
./opsd-server --rootfs=/tmp/test-env --addr=:50052
DISABLE_TLS=true ./opsd-server --addr=:50053
```

## Testing Strategy

### 1. Unit Tests
- Configuration parsing and validation
- Server lifecycle management
- TLS certificate handling
- Error scenarios

### 2. Integration Tests
- Server startup and shutdown
- TLS configuration validation
- Reflection service availability
- Network connectivity

### 3. Development Tools
- `grpcurl` compatibility testing
- Reflection service validation
- Health checking mechanisms

## Security Considerations

### 1. TLS Configuration

**Default Security:**
- TLS enabled by default
- Certificate validation required
- Explicit disable required for development

**Certificate Management:**
- Configurable certificate paths
- File existence validation
- Proper error handling for certificate issues

### 2. Network Security

**Bind Configuration:**
- Configurable bind addresses
- Default to all interfaces (`:50051`)
- Firewall rules responsibility of deployment

**Protocol Security:**
- HTTP/2 with gRPC
- Optional TLS encryption
- Standard gRPC security practices

## Future Architecture Patterns

The foundation is designed to support common gRPC service patterns:

### 1. Business Service Addition

```go
// Add new service implementations
type BusinessServiceServer struct {
    pb.UnimplementedBusinessServiceServer
}

// Register in server.go
businessServer := NewBusinessServiceServer()
pb.RegisterBusinessServiceServer(grpcServer, businessServer)
```

### 2. Middleware Integration

```go
// Add interceptors for cross-cutting concerns
grpcServer = grpc.NewServer(
    grpc.Creds(creds),
    grpc.UnaryInterceptor(loggingInterceptor),
    grpc.StreamInterceptor(streamLoggingInterceptor),
)
```

### 3. Health Checking

```go
// Add gRPC health checking
healthServer := health.NewServer()
grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
```

### 4. Metrics Integration

```go
// Add Prometheus metrics
grpcServer = grpc.NewServer(
    grpc.Creds(creds),
    grpc.UnaryInterceptor(grpc_prometheus.UnaryServerInterceptor),
)
```

## Performance Characteristics

### Resource Usage
- **Minimal Memory**: No business logic or caching
- **Low CPU**: Only reflection service processing
- **Network Efficient**: HTTP/2 multiplexing
- **Fast Startup**: Minimal initialization

### Scalability
- **Horizontal**: Multiple instances with load balancers
- **Vertical**: Efficient resource utilization
- **Connection Handling**: HTTP/2 connection reuse
- **Graceful Scaling**: Clean shutdown for orchestrators

## Monitoring and Observability

### Built-in Capabilities
- **Reflection**: Service discovery through grpcurl
- **Logging**: Configurable glog integration
- **Health**: Reflection service as basic health check

### Extension Points
- **Metrics**: Ready for Prometheus integration
- **Tracing**: Ready for distributed tracing
- **Health Checks**: gRPC health checking protocol support

## Development Workflow

### Adding New Services

1. **Define Protocol**: Create `.proto` files
2. **Generate Code**: Update Makefile for protobuf generation
3. **Implement Service**: Add service implementation in `pkg/server/`
4. **Register Service**: Update `server.go` registration
5. **Add Business Logic**: Implement supporting packages in `internal/`
6. **Test Integration**: Add comprehensive tests
7. **Update Documentation**: Document APIs and architecture changes

The clean foundation ensures each step is straightforward and follows established patterns.