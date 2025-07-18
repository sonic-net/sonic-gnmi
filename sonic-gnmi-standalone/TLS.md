# TLS Configuration Guide

This document explains how to configure and test TLS encryption for the sonic-ops-server.

## Overview

The server now supports configurable TLS encryption:
- **Production**: TLS enabled by default with certificates
- **Development/Testing**: TLS can be disabled via environment variable
- **Security**: Addresses Semgrep security finding about insecure gRPC connections

## Configuration Options

### Command Line Flags

```bash
-tls-cert string    Path to TLS certificate file (optional, default: server.crt)
-tls-key string     Path to TLS private key file (optional, default: server.key)
```

### Environment Variables

```bash
DISABLE_TLS=true    Disables TLS for development/testing (insecure)
```

## Usage Examples

### Production (TLS Enabled - Default)

```bash
# Generate certificates (one-time setup)
make test-certs

# Run server with TLS (default behavior)
./bin/sonic-ops-server -addr localhost:9999

# Test with grpcurl
grpcurl -cacert ca.crt localhost:9999 list
# or with self-signed certs
grpcurl -insecure localhost:9999 list
```

### Development/Testing (TLS Disabled)

```bash
# Run server without TLS
DISABLE_TLS=true ./bin/sonic-ops-server -addr localhost:9999

# Test with grpcurl (plaintext)
grpcurl -plaintext localhost:9999 list
grpcurl -plaintext localhost:9999 sonic.SystemInfo/GetPlatformType
```

### Custom Certificate Paths

```bash
# Run with custom certificate files
./bin/sonic-ops-server -tls-cert /path/to/cert.pem -tls-key /path/to/key.pem
```

## Certificate Generation

### Using Make Target

```bash
# Generate test certificates in current directory
make test-certs
```

This creates:
- `server.crt` - Server certificate
- `server.key` - Server private key  
- `ca.crt` - Certificate Authority certificate (for client verification)

### Manual Generation

```bash
# Run the script directly
./scripts/generate-test-certs.sh [output-directory]
```

## Testing

### Unit Tests

Unit tests use `NewServerWithTLS()` with TLS disabled:

```go
server, err := NewServerWithTLS("localhost:0", false, "", "")
```

### E2E Tests

E2E tests use `bufconn` for in-memory testing and are unaffected by TLS configuration.

### Manual Testing Workflow

1. **Build the server:**
   ```bash
   make build
   ```

2. **Copy binary anywhere for testing:**
   ```bash
   cp bin/sonic-ops-server /tmp/
   cd /tmp
   ```

3. **Test without TLS (simple):**
   ```bash
   DISABLE_TLS=true ./sonic-ops-server -addr localhost:9999 &
   grpcurl -plaintext localhost:9999 list
   ```

4. **Test with TLS (when needed):**
   ```bash
   # Generate certs once
   make test-certs
   
   # Run with TLS
   ./sonic-ops-server -addr localhost:9999 &
   grpcurl -insecure localhost:9999 list
   ```

## Security Considerations

- **Production**: Always use TLS with proper certificates from a trusted CA
- **Development**: Use `DISABLE_TLS=true` only for local development/testing
- **Test Certificates**: Generated certificates are self-signed and only suitable for testing
- **File Permissions**: Ensure private key files have restricted permissions (600)

## Troubleshooting

### Certificate Not Found Error

```bash
Error: TLS certificate file not found: server.crt
```

**Solution**: Generate certificates with `make test-certs` or provide valid paths with `-tls-cert` and `-tls-key`.

### Connection Refused with TLS

```bash
Error: rpc error: code = Unavailable desc = connection error
```

**Solutions**:
- Use `grpcurl -insecure` for self-signed certificates
- Use `grpcurl -cacert ca.crt` for CA-signed certificates
- Use `DISABLE_TLS=true` and `grpcurl -plaintext` for testing

### Tests Failing

If unit tests fail after TLS implementation, ensure they use `NewServerWithTLS()` with TLS disabled:

```go
server, err := NewServerWithTLS("localhost:0", false, "", "")
```

## Implementation Details

- **Default Behavior**: TLS enabled unless `DISABLE_TLS=true`
- **Certificate Validation**: Server checks for certificate file existence at startup
- **Graceful Fallback**: Clear error messages when certificates are missing
- **Backward Compatibility**: Existing test infrastructure continues to work