# SONiC Upgrade Service API Reference

This document provides comprehensive API documentation for the SONiC Upgrade Service gRPC interfaces.

## Table of Contents

- [Overview](#overview)
- [SystemInfo Service](#systeminfo-service)
- [FirmwareManagement Service](#firmwaremanagement-service)
- [Error Handling](#error-handling)
- [Examples](#examples)
- [Rate Limiting](#rate-limiting)

## Overview

The SONiC Upgrade Service provides two main gRPC services:

1. **SystemInfo**: Read-only system information and monitoring
2. **FirmwareManagement**: Firmware download, management, and upgrade operations

All APIs use Protocol Buffers for message serialization and support both HTTP/1.1 and HTTP/2 transport.

### Connection Information

- **Default Address**: `localhost:50051`
- **TLS**: Optional (configurable)
- **Reflection**: Enabled (for grpcurl compatibility)

### Common Request Headers

```
Content-Type: application/grpc
User-Agent: grpc-<client>/<version>
```

## SystemInfo Service

Provides system information and monitoring capabilities.

### GetPlatformType

Retrieves platform identification information from the SONiC system.

#### Request

```protobuf
message GetPlatformTypeRequest {
  // Empty - no parameters required
}
```

#### Response

```protobuf
message GetPlatformTypeResponse {
  string platform_identifier = 1;  // Platform ID (e.g., "x86_64-dell_s6000_s1220-r0")
  string vendor = 2;               // Hardware vendor (e.g., "Dell")
  string model = 3;                // Hardware model (e.g., "S6000")
}
```

#### Examples

```bash
# Basic platform information
grpcurl -plaintext localhost:50051 sonic.SystemInfo/GetPlatformType

# Expected response:
{
  "platformIdentifier": "x86_64-dell_s6000_s1220-r0",
  "vendor": "Dell",
  "model": "S6000"
}
```

#### Error Conditions

- `INTERNAL`: Failed to read platform configuration files
- `UNAVAILABLE`: Service temporarily unavailable

### GetDiskSpace

Analyzes disk space usage for specified filesystem paths.

#### Request

```protobuf
message GetDiskSpaceRequest {
  repeated string paths = 1;  // Optional: custom paths to analyze
                              // Default: ["/", "/host", "/tmp"]
}
```

#### Response

```protobuf
message GetDiskSpaceResponse {
  message DiskSpaceInfo {
    string path = 1;            // Filesystem path
    int64 total_mb = 2;         // Total space in megabytes
    int64 free_mb = 3;          // Available space in megabytes
    int64 used_mb = 4;          // Used space in megabytes
    string error_message = 5;   // Error details if path inaccessible
  }
  repeated DiskSpaceInfo filesystems = 1;
}
```

#### Examples

```bash
# Default paths analysis
grpcurl -plaintext localhost:50051 sonic.SystemInfo/GetDiskSpace

# Custom paths analysis
grpcurl -plaintext -d '{"paths": ["/var/log", "/boot", "/opt"]}' \
  localhost:50051 sonic.SystemInfo/GetDiskSpace

# Expected response:
{
  "filesystems": [
    {
      "path": "/",
      "totalMb": "102400",
      "freeMb": "45600",
      "usedMb": "56800"
    },
    {
      "path": "/var/log",
      "totalMb": "10240",
      "freeMb": "8192",
      "usedMb": "2048"
    }
  ]
}
```

#### Error Conditions

- `INVALID_ARGUMENT`: Invalid path format
- `INTERNAL`: Filesystem access errors

## FirmwareManagement Service

Handles firmware download, management, and upgrade operations.

### DownloadFirmware

Initiates firmware download from a URL with progress tracking.

#### Request

```protobuf
message DownloadFirmwareRequest {
  string url = 1;                      // Source URL (required)
  string output_path = 2;              // Destination path (optional)
  int32 connect_timeout_seconds = 3;   // Connection timeout (default: 30)
  int32 total_timeout_seconds = 4;     // Total download timeout (default: 3600)
}
```

#### Response

```protobuf
message DownloadFirmwareResponse {
  string session_id = 1;   // Unique session identifier for tracking
  string status = 2;       // Initial status ("starting")
  string output_path = 3;  // Resolved output file path
}
```

#### Examples

```bash
# Basic download with auto-detected filename
grpcurl -plaintext -d '{"url": "https://example.com/sonic-firmware.bin"}' \
  localhost:50051 sonic.FirmwareManagement/DownloadFirmware

# Download with custom path and timeouts
grpcurl -plaintext -d '{
  "url": "https://example.com/sonic-firmware.bin",
  "output_path": "/tmp/custom-firmware.bin",
  "connect_timeout_seconds": 30,
  "total_timeout_seconds": 1800
}' localhost:50051 sonic.FirmwareManagement/DownloadFirmware

# Expected response:
{
  "sessionId": "download-1640995200123456789",
  "status": "starting",
  "outputPath": "/host/sonic-firmware.bin"
}
```

#### Error Conditions

- `INVALID_ARGUMENT`: Invalid URL format or empty URL
- `ALREADY_EXISTS`: Download already in progress
- `RESOURCE_EXHAUSTED`: Insufficient disk space
- `PERMISSION_DENIED`: Cannot write to output path

### GetDownloadStatus

Retrieves real-time download progress and status information.

#### Request

```protobuf
message GetDownloadStatusRequest {
  string session_id = 1;  // Session ID from DownloadFirmware response (required)
}
```

#### Response

```protobuf
message GetDownloadStatusResponse {
  string session_id = 1;
  
  oneof state {
    DownloadStarting starting = 2;   // Download initialization
    DownloadProgress progress = 3;   // Active download progress
    DownloadResult result = 4;       // Successful completion
    DownloadError error = 5;         // Download failure
  }
}

message DownloadStarting {
  string message = 1;      // Status message
  string start_time = 2;   // ISO 8601 timestamp
}

message DownloadProgress {
  int64 downloaded_bytes = 1;    // Bytes downloaded so far
  int64 total_bytes = 2;         // Total bytes (from Content-Length)
  double percentage = 3;         // Completion percentage (0-100)
  double speed_bytes_per_sec = 4; // Current download speed
  string current_method = 5;     // Active download method
  int32 attempt_count = 6;       // Current retry attempt
  string start_time = 7;         // Download start timestamp
  string last_update = 8;        // Last progress update timestamp
}

message DownloadResult {
  string file_path = 1;      // Final downloaded file path
  int64 file_size_bytes = 2; // Final file size
  int64 duration_ms = 3;     // Total download duration
  int32 attempt_count = 4;   // Total retry attempts
  string final_method = 5;   // Successful download method
  string url = 6;            // Source URL
}

message DownloadError {
  string category = 1;       // Error category ("network", "http", "filesystem", "other")
  int32 http_code = 2;       // HTTP status code (if applicable)
  string message = 3;        // Detailed error message
  string url = 4;            // Source URL
  repeated DownloadAttempt attempts = 5; // All retry attempts
}

message DownloadAttempt {
  string method = 1;         // Download method used
  string interface = 2;      // Network interface used
  string error = 3;          // Error message for this attempt
  int64 duration_ms = 4;     // Attempt duration
  int32 http_status = 5;     // HTTP status code
}
```

#### Examples

```bash
# Check download status
grpcurl -plaintext -d '{"session_id": "download-1640995200123456789"}' \
  localhost:50051 sonic.FirmwareManagement/GetDownloadStatus

# Progress response example:
{
  "sessionId": "download-1640995200123456789",
  "progress": {
    "downloadedBytes": "52428800",
    "totalBytes": "104857600", 
    "percentage": 50.0,
    "speedBytesPerSec": 2097152.0,
    "currentMethod": "eth0",
    "attemptCount": 1,
    "startTime": "2024-01-01T12:00:00Z",
    "lastUpdate": "2024-01-01T12:00:25Z"
  }
}

# Success response example:
{
  "sessionId": "download-1640995200123456789",
  "result": {
    "filePath": "/host/sonic-firmware.bin",
    "fileSizeBytes": "104857600",
    "durationMs": "45000",
    "attemptCount": 1,
    "finalMethod": "eth0",
    "url": "https://example.com/sonic-firmware.bin"
  }
}

# Error response example:
{
  "sessionId": "download-1640995200123456789",
  "error": {
    "category": "http",
    "httpCode": 404,
    "message": "HTTP error 404: Not Found",
    "url": "https://example.com/nonexistent.bin",
    "attempts": [
      {
        "method": "eth0",
        "interface": "eth0",
        "error": "404 Not Found",
        "durationMs": "1500",
        "httpStatus": 404
      }
    ]
  }
}
```

#### Error Conditions

- `INVALID_ARGUMENT`: Empty or invalid session ID
- `NOT_FOUND`: Session ID not found
- `INTERNAL`: Status retrieval error

### ListFirmwareImages

Discovers firmware images in the filesystem with optional filtering.

#### Request

```protobuf
message ListFirmwareImagesRequest {
  repeated string search_directories = 1; // Custom search paths (optional)
  string version_pattern = 2;             // Regex pattern for version filtering (optional)
}
```

#### Response

```protobuf
message ListFirmwareImagesResponse {
  repeated FirmwareImageInfo images = 1;
  repeated string errors = 2;  // Search errors for inaccessible directories
}

message FirmwareImageInfo {
  string file_path = 1;      // Full path to firmware file
  string version = 2;        // Extracted version string
  string full_version = 3;   // Complete version information
  string image_type = 4;     // Image type ("onie", "aboot", "unknown")
  int64 file_size_bytes = 5; // File size in bytes
}
```

#### Examples

```bash
# List all firmware images in default locations
grpcurl -plaintext localhost:50051 sonic.FirmwareManagement/ListFirmwareImages

# List with version filter
grpcurl -plaintext -d '{"version_pattern": "202311\\..*"}' \
  localhost:50051 sonic.FirmwareManagement/ListFirmwareImages

# List in custom directories
grpcurl -plaintext -d '{"search_directories": ["/var/lib/firmware", "/opt/images"]}' \
  localhost:50051 sonic.FirmwareManagement/ListFirmwareImages

# Expected response:
{
  "images": [
    {
      "filePath": "/host/sonic-202311.bin",
      "version": "202311.1",
      "fullVersion": "SONiC-202311.1-Enterprise",
      "imageType": "onie",
      "fileSizeBytes": "104857600"
    },
    {
      "filePath": "/host/sonic-202310.swi", 
      "version": "202310.2",
      "fullVersion": "SONiC-202310.2-Community",
      "imageType": "aboot",
      "fileSizeBytes": "98765432"
    }
  ],
  "errors": []
}
```

#### Error Conditions

- `INVALID_ARGUMENT`: Invalid regex pattern
- `INTERNAL`: Filesystem access errors

### CleanupOldFirmware

Removes old firmware files and reports space reclaimed.

#### Request

```protobuf
message CleanupOldFirmwareRequest {
  // Empty - uses default cleanup configuration
}
```

#### Response

```protobuf
message CleanupOldFirmwareResponse {
  int32 files_deleted = 1;        // Number of files removed
  repeated string deleted_files = 2; // List of deleted file paths
  int64 space_freed_bytes = 3;    // Total bytes reclaimed
  repeated string errors = 4;     // Any cleanup errors encountered
}
```

#### Examples

```bash
# Cleanup old firmware files
grpcurl -plaintext localhost:50051 sonic.FirmwareManagement/CleanupOldFirmware

# Expected response:
{
  "filesDeleted": 3,
  "deletedFiles": [
    "/host/old-sonic-202309.bin",
    "/tmp/firmware-download.rpm", 
    "/host/backup-firmware.swi"
  ],
  "spaceFreedBytes": "314572800",
  "errors": []
}
```

#### Error Conditions

- `PERMISSION_DENIED`: Insufficient permissions for file deletion
- `INTERNAL`: Filesystem operation errors

### ConsolidateImages

Manages installed firmware images using sonic-installer.

#### Request

```protobuf
message ConsolidateImagesRequest {
  bool dry_run = 1;  // If true, show what would be done without executing
}
```

#### Response

```protobuf
message ConsolidateImagesResponse {
  string current_image = 1;       // Currently active image
  repeated string removed_images = 2; // Images that were/would be removed
  int64 space_freed_bytes = 3;    // Space reclaimed/to be reclaimed
  repeated string warnings = 4;   // Any warnings during consolidation
  bool executed = 5;              // Whether consolidation was actually executed
}
```

#### Examples

```bash
# Dry run to preview consolidation
grpcurl -plaintext -d '{"dry_run": true}' \
  localhost:50051 sonic.FirmwareManagement/ConsolidateImages

# Execute consolidation
grpcurl -plaintext -d '{"dry_run": false}' \
  localhost:50051 sonic.FirmwareManagement/ConsolidateImages

# Expected response:
{
  "currentImage": "SONiC-202311.1",
  "removedImages": [
    "SONiC-202310.1",
    "SONiC-202309.2"
  ],
  "spaceFreedBytes": "209715200",
  "warnings": ["Removed older images to free space"],
  "executed": true
}
```

#### Error Conditions

- `FAILED_PRECONDITION`: sonic-installer not available
- `INTERNAL`: sonic-installer execution errors

### ListImages

Lists currently installed firmware images via sonic-installer.

#### Request

```protobuf
message ListImagesRequest {
  // Empty - no parameters required
}
```

#### Response

```protobuf
message ListImagesResponse {
  repeated string images = 1;   // List of installed image names
  string current_image = 2;     // Currently active image
  string next_image = 3;        // Image that will boot next
  repeated string warnings = 4; // Any warnings from sonic-installer
}
```

#### Examples

```bash
# List installed images
grpcurl -plaintext localhost:50051 sonic.FirmwareManagement/ListImages

# Expected response:
{
  "images": [
    "SONiC-202311.1",
    "SONiC-202310.2", 
    "SONiC-202309.3"
  ],
  "currentImage": "SONiC-202311.1",
  "nextImage": "SONiC-202311.1",
  "warnings": []
}
```

#### Error Conditions

- `FAILED_PRECONDITION`: sonic-installer not available
- `INTERNAL`: sonic-installer execution errors

## Error Handling

### Standard gRPC Status Codes

The service uses standard gRPC status codes:

- `OK` (0): Success
- `INVALID_ARGUMENT` (3): Invalid request parameters
- `NOT_FOUND` (5): Resource not found
- `ALREADY_EXISTS` (6): Resource already exists
- `PERMISSION_DENIED` (7): Insufficient permissions
- `RESOURCE_EXHAUSTED` (8): Resource limits exceeded
- `FAILED_PRECONDITION` (9): Prerequisites not met
- `INTERNAL` (13): Internal server error
- `UNAVAILABLE` (14): Service temporarily unavailable

### Error Response Format

```json
{
  "error": {
    "code": 3,
    "message": "Invalid URL format: missing protocol",
    "details": []
  }
}
```

### Retry Guidelines

- **Transient Errors**: `UNAVAILABLE`, `INTERNAL` - Safe to retry with exponential backoff
- **Client Errors**: `INVALID_ARGUMENT`, `NOT_FOUND` - Do not retry
- **Resource Errors**: `RESOURCE_EXHAUSTED`, `ALREADY_EXISTS` - Retry after addressing resource constraints

## Rate Limiting

### Download Operations

- **Concurrent Downloads**: Limited to 1 active download per service instance
- **Status Queries**: No rate limits (read-only operations)
- **Cleanup Operations**: No rate limits (typically infrequent)

### Best Practices

1. **Check Status Regularly**: Poll `GetDownloadStatus` every 1-5 seconds during downloads
2. **Handle Timeouts**: Set appropriate client timeouts for long operations
3. **Batch Operations**: Group multiple operations when possible
4. **Resource Monitoring**: Check disk space before initiating downloads

## Authentication and Authorization

### TLS Configuration

When TLS is enabled:

```bash
# Connect with TLS
grpcurl -cert client.crt -key client.key -cacert ca.crt \
  secure-server:8443 sonic.SystemInfo/GetPlatformType
```

### Future Authentication

The service is designed to support:
- mTLS client certificates
- JWT token authentication
- API key-based authorization

## Monitoring and Observability

### Health Checks

Use gRPC health checking protocol:

```bash
# Check service health
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
```

### Metrics

Future versions will expose:
- Download success/failure rates
- Response time percentiles  
- Active session counts
- Resource utilization metrics

## Version Compatibility

### Protocol Buffer Compatibility

- **Forward Compatible**: New fields can be added without breaking existing clients
- **Backward Compatible**: Removing fields requires major version increment
- **Client Compatibility**: Clients should ignore unknown fields

### API Versioning

Current version: `v1` (stable)
- Breaking changes will introduce new service versions
- Legacy versions supported for defined transition periods