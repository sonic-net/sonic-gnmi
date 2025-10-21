# SONiC Upgrade Service Architecture

This document describes the architecture and design principles of the SONiC Upgrade Service, a comprehensive gRPC-based solution for managing SONiC firmware operations.

## Overview

The SONiC Upgrade Service is designed as a modular, containerized gRPC service that provides APIs for firmware management, system information retrieval, and upgrade operations. It follows clean architecture principles with clear separation between service interfaces, business logic, and system integration.

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        gRPC Clients                        │
│  (grpcurl, sonic-mgmt, custom tooling, web interfaces)     │
└─────────────────────┬───────────────────────────────────────┘
                      │ gRPC/HTTP2 + TLS (optional)
┌─────────────────────▼───────────────────────────────────────┐
│                   gRPC Server                              │
│  ┌─────────────────┐  ┌─────────────────┐                 │
│  │  SystemInfo     │  │ FirmwareManag-  │                 │
│  │  Service        │  │ ement Service   │                 │
│  └─────────────────┘  └─────────────────┘                 │
└─────────────────────┬───────────────────────────────────────┘
                      │ Internal API calls
┌─────────────────────▼───────────────────────────────────────┐
│                 Business Logic Layer                       │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │
│  │Firmware  │ │Download  │ │Platform  │ │Installer │      │
│  │Management│ │Engine    │ │Detection │ │Wrapper   │      │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘      │
└─────────────────────┬───────────────────────────────────────┘
                      │ System calls & file operations
┌─────────────────────▼───────────────────────────────────────┐
│                 System Integration                         │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │
│  │Host FS   │ │Network   │ │Disk      │ │Process   │      │
│  │Access    │ │Interface │ │Space     │ │Execution │      │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘      │
└─────────────────────────────────────────────────────────────┘
```

## Core Components

### 1. gRPC Server Layer (`pkg/server/`)

The top-level server implementation that handles:
- TLS configuration and certificate management
- Service registration and lifecycle management
- Request routing and middleware
- Graceful shutdown handling

**Key Files:**
- `server.go`: Main server with TLS support
- `system_info.go`: SystemInfo service implementation
- `firmware_management.go`: FirmwareManagement service implementation

### 2. Business Logic Layer (`internal/`)

Domain-specific packages that implement core functionality:

#### Firmware Management (`internal/firmware/`)
- **Cleanup**: Remove old firmware files with space tracking
- **Version Detection**: Extract version info from ONIE/Aboot images
- **Consolidation**: Manage installed images via sonic-installer
- **Search**: Discover firmware images across directories

#### Download Engine (`internal/download/`)
- **Session Management**: Track download progress with unique sessions
- **Network Binding**: Interface-specific downloads for multi-NIC systems
- **Progress Tracking**: Real-time speed and completion monitoring
- **Error Handling**: Comprehensive retry mechanisms with backoff

#### Platform Detection (`internal/hostinfo/`)
- **Hardware Detection**: Read machine.conf for platform identification
- **Vendor Mapping**: Normalize platform strings across vendors
- **Container Awareness**: Path resolution for mounted filesystems

#### System Integration (`internal/diskspace/`, `internal/paths/`)
- **Disk Analysis**: Filesystem space calculation and monitoring
- **Path Resolution**: Container-to-host path mapping
- **Configuration**: Global settings and deployment-specific config

### 3. Protocol Layer (`proto/`)

gRPC service definitions and generated code:
- `system_info.proto`: Platform info and disk space APIs
- `firmware_management.proto`: Download, cleanup, and image management APIs

## Design Principles

### 1. Container-First Design

The service is designed to run in containers with host filesystem mounted:
- All paths are resolved through `internal/paths` for container compatibility
- Configuration supports both container (`/mnt/host`) and baremetal (`/`) deployments
- Network operations handle container networking constraints

### 2. Concurrent Safety

Critical components use proper synchronization:
- Download sessions protected by `sync.RWMutex` for concurrent status queries
- Global download state prevents resource conflicts
- Thread-safe progress updates from download goroutines

### 3. Error Resilience

Comprehensive error handling throughout:
- Network failures handled with configurable timeouts and retries
- Filesystem operations with proper error propagation
- Graceful degradation when system tools are unavailable

### 4. Observability

Extensive logging and monitoring support:
- Structured logging with configurable verbosity levels
- Progress tracking for long-running operations
- Error categorization for troubleshooting

## Service Interfaces

### SystemInfo Service

Provides read-only system information:

```protobuf
service SystemInfo {
  rpc GetPlatformType(GetPlatformTypeRequest) returns (GetPlatformTypeResponse);
  rpc GetDiskSpace(GetDiskSpaceRequest) returns (GetDiskSpaceResponse);
}
```

**Use Cases:**
- Platform identification for firmware compatibility
- Disk space monitoring before downloads
- System health checks

### FirmwareManagement Service

Handles firmware operations with state management:

```protobuf
service FirmwareManagement {
  rpc DownloadFirmware(DownloadFirmwareRequest) returns (DownloadFirmwareResponse);
  rpc GetDownloadStatus(GetDownloadStatusRequest) returns (GetDownloadStatusResponse);
  rpc ListFirmwareImages(ListFirmwareImagesRequest) returns (ListFirmwareImagesResponse);
  rpc CleanupOldFirmware(CleanupOldFirmwareRequest) returns (CleanupOldFirmwareResponse);
  rpc ConsolidateImages(ConsolidateImagesRequest) returns (ConsolidateImagesResponse);
  rpc ListImages(ListImagesRequest) returns (ListImagesResponse);
}
```

**State Management:**
- Download sessions with unique IDs for tracking
- Progress reporting with real-time updates
- Concurrent download blocking to prevent conflicts

## Deployment Scenarios

### 1. Container Deployment

```yaml
# Docker/Kubernetes deployment
volumes:
  - /host:/mnt/host:ro  # Host filesystem access
environment:
  - ROOTFS=/mnt/host
  - ADDR=:8080
  - DISABLE_TLS=true
```

### 2. Baremetal Deployment

```bash
# Direct installation on SONiC device
./sonic-ops-server --rootfs=/ --addr=:50051 --tls-cert=/etc/ssl/server.crt
```

### 3. Development Environment

```bash
# Local testing with custom paths
./sonic-ops-server --rootfs=/tmp/sonic-test --addr=:50052
```

## Network Architecture

### Download Engine Network Binding

The download engine supports interface-specific binding for multi-interface systems:

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Management    │    │   Data Plane    │    │   Out-of-Band   │
│   Interface     │    │   Interface     │    │   Management    │
│   (eth0)        │    │   (eth1)        │    │   (eth2)        │
└─────────┬───────┘    └─────────┬───────┘    └─────────┬───────┘
          │                      │                      │
          └──────────────────────┼──────────────────────┘
                                 │
                    ┌─────────────▼─────────────┐
                    │     Download Engine       │
                    │  (Interface Selection)    │
                    └───────────────────────────┘
```

**Features:**
- Automatic interface detection and status checking
- Configurable interface preference for downloads
- Fallback mechanisms for interface failures
- IPv4/IPv6 dual-stack support per interface

## Testing Strategy

### 1. Unit Tests
- Individual package functionality
- Mock-based testing for external dependencies
- Edge case coverage for error conditions

### 2. Integration Tests
- Service-to-service communication
- End-to-end download workflows
- Container deployment validation

### 3. End-to-End Tests
- Real network download scenarios
- Platform detection across hardware types
- Performance testing with large files

## Security Considerations

### 1. TLS Configuration
- Optional TLS with certificate validation
- Support for custom CA certificates
- Secure defaults with option to disable for testing

### 2. Input Validation
- URL validation for download requests
- Path sanitization for file operations
- Size limits for download operations

### 3. Filesystem Access
- Read-only access to host filesystem where possible
- Controlled write access to designated directories
- Permission validation before file operations

## Performance Characteristics

### Download Performance
- Interface-specific binding for optimal network utilization
- Configurable timeouts and retry mechanisms
- Progress tracking with minimal overhead

### Memory Usage
- Streaming downloads to avoid memory accumulation
- Bounded memory usage for large file operations
- Efficient progress tracking with minimal allocations

### Concurrency
- Single download enforcement to prevent resource conflicts
- Non-blocking status queries during downloads
- Efficient goroutine management for concurrent requests

## Future Enhancements

### 1. Download Resume
- Persistent session state for download resumption
- Partial file handling with HTTP range requests
- Recovery from network interruptions

### 2. Multi-source Downloads
- Parallel chunk downloads from multiple sources
- Load balancing across available mirrors
- Automatic source failover

### 3. Enhanced Monitoring
- Prometheus metrics integration
- Distributed tracing support
- Performance analytics and reporting

### 4. Advanced Security
- mTLS client authentication
- API key-based authorization
- Audit logging for all operations