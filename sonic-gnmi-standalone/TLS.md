# TLS Configuration

TLS and mTLS configuration for sonic-gnmi-standalone server.

## Command Line Flags

```bash
-tls-cert string     Path to TLS certificate file (default: server.crt)
-tls-key string      Path to TLS private key file (default: server.key)
-tls-ca-cert string  Path to TLS CA certificate file (default: ca.crt)
-mtls                Enable mutual TLS
-no-tls              Disable TLS
```

## Usage

### TLS (Default)

```bash
# Generate certificates
make test-certs

# Run server
./bin/sonic-gnmi-standalone

# Test
grpcurl -cacert ca.crt localhost:50051 list
grpcurl -insecure localhost:50051 list
```

### mTLS

```bash
# Generate certificates
make test-certs

# Run server
./bin/sonic-gnmi-standalone --mtls

# Test
grpcurl -cacert ca.crt -cert client.crt -key client.key localhost:50051 list
```

### No TLS

```bash
# Run server
./bin/sonic-gnmi-standalone --no-tls

# Test
grpcurl -plaintext localhost:50051 list
```

### Custom Paths

```bash
# TLS
./bin/sonic-gnmi-standalone -tls-cert /path/to/cert.pem -tls-key /path/to/key.pem

# mTLS
./bin/sonic-gnmi-standalone --mtls -tls-cert /path/to/cert.pem -tls-key /path/to/key.pem -tls-ca-cert /path/to/ca.pem
```

## Programmatic Configuration

```go
// TLS
srv, err := server.NewServerBuilder().
    WithAddress(":50051").
    WithTLS("server.crt", "server.key").
    Build()

// mTLS
srv, err := server.NewServerBuilder().
    WithAddress(":50051").
    WithMTLS("server.crt", "server.key", "ca.crt").
    Build()

// No TLS
srv, err := server.NewServerBuilder().
    WithAddress(":50051").
    WithoutTLS().
    Build()
```

## Certificate Generation

```bash
# Generate test certificates
make test-certs

# Or run script directly
./scripts/generate-test-certs.sh [output-directory]
```

**Files created:**
- `ca.crt` - Certificate Authority certificate
- `ca.key` - Certificate Authority private key
- `server.crt` - Server certificate
- `server.key` - Server private key  
- `client.crt` - Client certificate (for mTLS)
- `client.key` - Client private key (for mTLS)

## Testing

```bash
# Build
make build

# Test no TLS
./bin/sonic-gnmi-standalone --no-tls &
grpcurl -plaintext localhost:50051 list

# Test TLS
make test-certs
./bin/sonic-gnmi-standalone &
grpcurl -insecure localhost:50051 list

# Test mTLS
./bin/sonic-gnmi-standalone --mtls &
grpcurl -cacert ca.crt -cert client.crt -key client.key localhost:50051 list
```

## Notes

- Production: Use CA-signed certificates
- Development: Use `--no-tls` for local testing
- Test certificates: Self-signed, testing only
- File permissions: Restrict private key files (600)
- mTLS: Requires client certificates

## Troubleshooting

**Certificate not found:**
```bash
Error: TLS certificate file not found: server.crt
```
Solution: `make test-certs` or provide valid paths.

**Connection refused:**
```bash
Error: rpc error: code = Unavailable desc = connection error
```
Solutions:
- `grpcurl -insecure` for self-signed certificates
- `grpcurl -cacert ca.crt` for TLS
- `grpcurl -cacert ca.crt -cert client.crt -key client.key` for mTLS
- `--no-tls` for testing

**mTLS authentication failed:**
```bash
Error: rpc error: code = Unavailable desc = authentication handshake failed
```
Solution: Provide client certificate with `-cert` and `-key` flags.