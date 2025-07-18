# proto

This directory contains Protocol Buffer (protobuf) definitions for the gRPC services.

## Files

- `system_info.proto`: Defines the SystemInfo service for retrieving platform information

## Generated Files

The following files are generated from the proto definitions and should not be committed:

- `*.pb.go`: Go code generated from proto files
- `*_grpc.pb.go`: Go gRPC service code generated from proto files

To regenerate these files, run:

```bash
make proto
```
