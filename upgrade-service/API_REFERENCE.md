# SONiC Upgrade Service API Reference

This document provides comprehensive API documentation for the SONiC Upgrade Service gRPC interface.

## Overview

The SONiC Upgrade Service provides two main gRPC services:

1. **SystemInfo Service** - Platform detection and system monitoring
2. **FirmwareManagement Service** - Firmware download, cleanup, and image operations

## Connection Details

- **Default Port**: 50051
- **Protocol**: gRPC over HTTP/2
- **TLS**: Optional (configurable via environment variables)
- **Reflection**: Enabled (supports grpcurl and other gRPC tools)

## SystemInfo Service

### GetPlatform

Retrieves platform information for the current system.

**Request**: `GetPlatformRequest` (empty)

**Response**: `GetPlatformResponse`
```protobuf
message GetPlatformResponse {
  string platform = 1;  // Platform identifier (e.g., "x86_64-dellemc_s5232f_c3538-r0")
}
```

**Usage**:
```bash
grpcurl -plaintext localhost:50051 SystemInfo/GetPlatform
```

### GetDiskSpace

Retrieves disk space information for specified paths.

**Request**: `GetDiskSpaceRequest`
```protobuf
message GetDiskSpaceRequest {
  repeated string paths = 1;  // Paths to check disk space for
}
```

**Response**: `GetDiskSpaceResponse`
```protobuf
message GetDiskSpaceResponse {
  repeated DiskSpaceInfo disk_spaces = 1;
}

message DiskSpaceInfo {
  string path = 1;           // The path that was checked
  uint64 total_bytes = 2;    // Total disk space in bytes
  uint64 available_bytes = 3; // Available disk space in bytes
  uint64 used_bytes = 4;     // Used disk space in bytes
}
```

**Usage**:
```bash
grpcurl -plaintext -d '{"paths": ["/", "/tmp"]}' localhost:50051 SystemInfo/GetDiskSpace
```

## FirmwareManagement Service

### DownloadFirmware

Downloads firmware from a URL with MD5 checksum validation and optional network interface binding.

**Request**: `DownloadFirmwareRequest`
```protobuf
message DownloadFirmwareRequest {
  string url = 1;              // URL to download firmware from
  string destination_path = 2;  // Local path to save the firmware
  string md5_checksum = 3;     // Expected MD5 checksum for validation (optional)
  string interface_name = 4;   // Network interface to bind to (optional)
}
```

**Response**: `DownloadFirmwareResponse`
```protobuf
message DownloadFirmwareResponse {
  bool success = 1;            // Whether download was successful
  string message = 2;          // Status message or error description
  int64 bytes_downloaded = 3;  // Number of bytes downloaded
  string checksum = 4;         // Actual MD5 checksum of downloaded file
}
```

**Features**:
- Automatic MD5 checksum validation
- Network interface binding for multi-NIC systems
- Concurrent download protection (one download at a time)
- Comprehensive error handling with retries

**Usage**:
```bash
grpcurl -plaintext -d '{
  "url": "https://example.com/firmware.bin",
  "destination_path": "/tmp/firmware.bin",
  "md5_checksum": "d41d8cd98f00b204e9800998ecf8427e",
  "interface_name": "eth0"
}' localhost:50051 FirmwareManagement/DownloadFirmware
```

### CleanupOldFirmware

Removes old firmware files from the system based on predefined patterns.

**Request**: `CleanupOldFirmwareRequest` (empty)

**Response**: `CleanupOldFirmwareResponse`
```protobuf
message CleanupOldFirmwareResponse {
  bool success = 1;              // Whether cleanup was successful
  string message = 2;            // Status message or error description
  int32 files_deleted = 3;       // Number of files successfully deleted
  repeated string deleted_files = 4; // List of files that were deleted
}
```

**Cleanup Patterns**:
- `*.bin` files (SONiC images)
- `*.swi` files (Arista images)
- `*.rpm` files (Package files)

**Cleanup Locations**:
- `/host` directory (mounted host filesystem)
- `/tmp` directory (temporary files)

**Usage**:
```bash
grpcurl -plaintext localhost:50051 FirmwareManagement/CleanupOldFirmware
```

### ListInstalledImages

Retrieves information about installed SONiC images from the bootloader.

**Request**: `ListInstalledImagesRequest` (empty)

**Response**: `ListInstalledImagesResponse`
```protobuf
message ListInstalledImagesResponse {
  repeated string images = 1;    // List of installed image names
  string current_image = 2;      // Currently running image
  string next_image = 3;         // Image set for next boot
}
```

**Supported Bootloaders**:
- GRUB (Grand Unified Bootloader)
- Aboot (Arista Boot Loader)
- U-Boot (Das U-Boot)

**Usage**:
```bash
grpcurl -plaintext localhost:50051 FirmwareManagement/ListInstalledImages
```

### SetNextBootImage

Sets the image to boot on next system restart.

**Request**: `SetNextBootImageRequest`
```protobuf
message SetNextBootImageRequest {
  string image_name = 1;  // Name of the image to set as next boot
}
```

**Response**: `SetNextBootImageResponse`
```protobuf
message SetNextBootImageResponse {
  bool success = 1;     // Whether operation was successful
  string message = 2;   // Status message or error description
}
```

**Usage**:
```bash
grpcurl -plaintext -d '{"image_name": "SONiC-OS-202311.01"}' localhost:50051 FirmwareManagement/SetNextBootImage
```

### InstallImage

Installs a new SONiC image using sonic-installer.

**Request**: `InstallImageRequest`
```protobuf
message InstallImageRequest {
  string image_path = 1;  // Path to the image file to install
  bool set_default = 2;   // Whether to set as default boot image
  bool install_only = 3;  // Whether to install without setting as next boot
}
```

**Response**: `InstallImageResponse`
```protobuf
message InstallImageResponse {
  bool success = 1;         // Whether installation was successful
  string message = 2;       // Status message or error description
  string installed_image = 3; // Name of the installed image
}
```

**Usage**:
```bash
grpcurl -plaintext -d '{
  "image_path": "/tmp/sonic-image.bin",
  "set_default": true,
  "install_only": false
}' localhost:50051 FirmwareManagement/InstallImage
```

### RemoveImage

Removes an installed SONiC image.

**Request**: `RemoveImageRequest`
```protobuf
message RemoveImageRequest {
  string image_name = 1;  // Name of the image to remove
  bool force = 2;         // Whether to force removal
}
```

**Response**: `RemoveImageResponse`
```protobuf
message RemoveImageResponse {
  bool success = 1;   // Whether removal was successful
  string message = 2; // Status message or error description
}
```

**Usage**:
```bash
grpcurl -plaintext -d '{
  "image_name": "SONiC-OS-202305.01",
  "force": false
}' localhost:50051 FirmwareManagement/RemoveImage
```

## Error Handling

The service uses standard gRPC status codes:

- `OK` (0): Success
- `INVALID_ARGUMENT` (3): Invalid request parameters
- `NOT_FOUND` (5): Resource not found
- `ALREADY_EXISTS` (6): Resource already exists
- `FAILED_PRECONDITION` (9): Operation cannot be performed
- `INTERNAL` (13): Internal server error
- `UNAVAILABLE` (14): Service temporarily unavailable

All error responses include descriptive messages to help with debugging.

## Authentication and Security

- **TLS**: Optional TLS encryption (configure via `TLS_ENABLED`, `TLS_CERT_FILE`, `TLS_KEY_FILE`)
- **Authentication**: Currently no authentication required (suitable for internal network deployment)
- **Network Binding**: Download operations can bind to specific network interfaces

## Configuration

Environment variables for server configuration:

- `LISTEN_ADDR`: Server listen address (default: ":50051")
- `ROOT_FS`: Root filesystem path for container deployment (default: "/")
- `TLS_ENABLED`: Enable TLS encryption (default: "false")
- `TLS_CERT_FILE`: Path to TLS certificate file
- `TLS_KEY_FILE`: Path to TLS private key file

## Development and Testing

The service includes gRPC reflection, making it compatible with development tools:

```bash
# List available services
grpcurl -plaintext localhost:50051 list

# Describe a service
grpcurl -plaintext localhost:50051 describe SystemInfo

# Get service metadata
grpcurl -plaintext localhost:50051 describe FirmwareManagement.DownloadFirmware
```

## Container Deployment

The service is designed for container deployment with host filesystem access:

```bash
docker run -v /:/mnt/host -p 50051:50051 sonic-upgrade-service --rootfs=/mnt/host
```

This ensures proper path resolution for bootloader access and firmware management operations.