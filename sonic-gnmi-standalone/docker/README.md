# Docker Container for sonic-gnmi-standalone

This directory contains Docker configuration for containerizing the sonic-gnmi-standalone gRPC server.

## Overview

The sonic-gnmi-standalone container provides a minimal gRPC server foundation with:
- gRPC reflection support for development tools
- Optional TLS/mTLS support for secure connections
- Container-aware filesystem access
- Configurable network interfaces
- Clean deployment to SONiC devices

## Files

- **`Dockerfile`**: Multi-stage container image definition
- **`docker-entrypoint.sh`**: Container entry point script
- **`build_deploy_testonly.sh`**: Automated build and deployment script (test environments only)

## Building the Container

### Using Makefile (Recommended)

```bash
# Build with default settings
make docker-build

# Build with custom tag
make docker-build DOCKER_TAG=v1.0.0
```

### Direct Docker Build

```bash
# Build from project root
docker build -t sonic-gnmi-standalone:latest -f docker/Dockerfile .

# Build with custom tag
docker build -t sonic-gnmi-standalone:v1.0.0 -f docker/Dockerfile .
```

## Running the Container

### Basic Usage

```bash
# Run with default settings (port 50055)
docker run -d --name sonic-gnmi \
  --network host \
  sonic-gnmi-standalone:latest

# Run with custom port
docker run -d --name sonic-gnmi \
  --network host \
  sonic-gnmi-standalone:latest \
  --addr=:8080

# Run with TLS disabled (for testing)
docker run -d --name sonic-gnmi \
  --network host \
  sonic-gnmi-standalone:latest \
  --no-tls
```

### Advanced Configuration

```bash
# Run with custom TLS certificates
docker run -d --name sonic-gnmi \
  --network host \
  -v /path/to/certs:/certs:ro \
  sonic-gnmi-standalone:latest \
  --tls-cert=/certs/server.crt \
  --tls-key=/certs/server.key

# Run with mTLS enabled
docker run -d --name sonic-gnmi \
  --network host \
  -v /path/to/certs:/certs:ro \
  sonic-gnmi-standalone:latest \
  --mtls \
  --tls-cert=/certs/server.crt \
  --tls-key=/certs/server.key \
  --tls-ca-cert=/certs/ca.crt

# Run with host filesystem access (for SONiC operations)
docker run -d --name sonic-gnmi \
  --network host \
  -v /:/host:ro \
  -v /etc/sonic:/etc/sonic:ro \
  sonic-gnmi-standalone:latest \
  --rootfs=/host
```

## Deployment to SONiC

### Method 1: Automated Deployment (Test Only)

Use the provided script for quick deployment to test environments:

```bash
# Deploy to a SONiC device
./docker/build_deploy_testonly.sh -t admin@sonic-device

# Deploy with custom tag
./docker/build_deploy_testonly.sh -t admin@sonic-device -i v1.0.0

# Deploy with custom port
./docker/build_deploy_testonly.sh -t admin@sonic-device -a ":8080"

# Deploy with TLS enabled
./docker/build_deploy_testonly.sh -t admin@sonic-device --enable-tls
```

The script will:
1. Build the Docker image locally
2. Save the image to a temporary tar file
3. Transfer the image to the target device
4. Load and run the container on the device
5. Clean up temporary files

### Method 2: Manual Deployment

For production or custom deployments:

```bash
# 1. Build the image
make docker-build

# 2. Save the image
docker save sonic-gnmi-standalone:latest > sonic-gnmi.tar

# 3. Transfer to SONiC device
scp sonic-gnmi.tar admin@sonic-device:/tmp/

# 4. Load on SONiC device
ssh admin@sonic-device 'docker load < /tmp/sonic-gnmi.tar'

# 5. Run on SONiC device
ssh admin@sonic-device 'docker run -d \
  --name sonic-gnmi \
  --restart=always \
  --network host \
  -v /:/host:ro \
  -v /etc/sonic:/etc/sonic:ro \
  sonic-gnmi-standalone:latest \
  --rootfs=/host'

# 6. Verify deployment
ssh admin@sonic-device 'docker ps | grep sonic-gnmi'
```

## Configuration

### Command-Line Arguments

The container accepts all sonic-gnmi-standalone command-line arguments:

- `--addr`: Server address (default: `:50055`)
- `--rootfs`: Root filesystem path (default: `/mnt/host`)
- `--shutdown-timeout`: Graceful shutdown timeout (default: `10s`)
- `--no-tls`: Disable TLS (TLS enabled by default)
- `--tls-cert`: TLS certificate file path
- `--tls-key`: TLS private key file path
- `--mtls`: Enable mutual TLS
- `--tls-ca-cert`: CA certificate for client verification

### Environment Variables

While the Dockerfile sets default environment variables, the server uses command-line arguments. Pass configuration through docker run arguments, not environment variables.

### Volume Mounts

For SONiC operations, mount these directories:

| Host Path | Container Path | Purpose | Mode |
|-----------|---------------|---------|------|
| `/` | `/host` | Host filesystem access | Read-only |
| `/etc/sonic` | `/etc/sonic` | SONiC configuration | Read-only |
| `/path/to/certs` | `/certs` | TLS certificates | Read-only |

## Networking

### Host Networking (Recommended for SONiC)

```bash
docker run --network host sonic-gnmi-standalone:latest
```

Benefits:
- Direct access to all host interfaces
- No port mapping required
- Better performance
- Access to SONiC network namespaces

### Bridge Networking (Development/Testing)

```bash
# Map specific port
docker run -p 50055:50055 sonic-gnmi-standalone:latest

# Map custom port
docker run -p 8080:8080 sonic-gnmi-standalone:latest --addr=:8080
```

## Security Considerations

### Running as Root

The container runs as root by default to enable:
- Access to host filesystem when using `--rootfs`
- Binding to privileged ports if needed
- Reading SONiC configuration files

This is acceptable for test deployments but should be reviewed for production use.

### TLS/mTLS Support

For secure deployments:

```bash
# Generate certificates (for testing)
make test-certs

# Run with TLS
docker run -d \
  --network host \
  -v $(pwd):/certs:ro \
  sonic-gnmi-standalone:latest \
  --tls-cert=/certs/server.crt \
  --tls-key=/certs/server.key

# Run with mTLS
docker run -d \
  --network host \
  -v $(pwd):/certs:ro \
  sonic-gnmi-standalone:latest \
  --mtls \
  --tls-cert=/certs/server.crt \
  --tls-key=/certs/server.key \
  --tls-ca-cert=/certs/ca.crt
```

## Monitoring and Debugging

### View Logs

```bash
# View container logs
docker logs sonic-gnmi

# Follow logs
docker logs -f sonic-gnmi

# View with timestamps
docker logs -t sonic-gnmi
```

### Health Check

```bash
# Using grpcurl (no TLS)
docker run --rm --network host fullstorydev/grpcurl \
  -plaintext localhost:50055 list

# Using grpcurl (with TLS)
docker run --rm --network host -v $(pwd):/certs:ro fullstorydev/grpcurl \
  -cacert /certs/ca.crt localhost:50055 list
```

### Interactive Debugging

```bash
# Execute shell in running container
docker exec -it sonic-gnmi /bin/bash

# Test connectivity from inside container
docker exec sonic-gnmi grpcurl -plaintext localhost:50055 list
```

## Container Management

### Stop and Remove

```bash
# Stop container
docker stop sonic-gnmi

# Remove container
docker rm sonic-gnmi

# Stop and remove in one command
docker rm -f sonic-gnmi
```

### Update Container

```bash
# 1. Build new image
make docker-build

# 2. Stop old container
docker stop sonic-gnmi

# 3. Remove old container
docker rm sonic-gnmi

# 4. Run new container
docker run -d --name sonic-gnmi \
  --network host \
  --restart=always \
  sonic-gnmi-standalone:latest
```

## Best Practices

1. **Use host networking** for SONiC deployments to access all interfaces
2. **Mount filesystems read-only** unless write access is specifically needed
3. **Enable TLS/mTLS** for production deployments
4. **Use --restart=always** for automatic recovery
5. **Monitor logs** regularly for errors or warnings
6. **Tag images properly** for version tracking

## Troubleshooting

### Container Won't Start

```bash
# Check container logs
docker logs sonic-gnmi

# Common issues:
# - Port already in use: Change --addr parameter
# - Certificate files not found: Check volume mounts
# - Permission denied: Ensure proper file permissions
```

### Cannot Connect to Server

```bash
# Verify container is running
docker ps | grep sonic-gnmi

# Check listening ports
docker exec sonic-gnmi netstat -tlnp | grep 50055

# Test with grpcurl
docker run --rm --network host fullstorydev/grpcurl \
  -plaintext localhost:50055 list
```

### Certificate Issues

```bash
# Verify certificate paths
docker exec sonic-gnmi ls -la /certs/

# Check certificate validity
docker exec sonic-gnmi openssl x509 -in /certs/server.crt -text -noout
```

## Development Tips

### Local Testing

```bash
# Run with local code changes
docker run --rm -it \
  --network host \
  -v $(pwd)/bin/sonic-gnmi-standalone:/usr/local/bin/sonic-gnmi-standalone:ro \
  sonic-gnmi-standalone:latest \
  --no-tls
```

### Debug Mode

```bash
# Run with verbose logging
docker run -d --name sonic-gnmi \
  --network host \
  sonic-gnmi-standalone:latest \
  -v 2
```

## Notes

- The default port is **50055** (not 50051)
- TLS is enabled by default unless `--no-tls` is specified
- The binary name is `sonic-gnmi-standalone` (not `opsd-server`)
- This is a minimal gRPC server foundation for adding SONiC services