# opsd (Operations daemon) Docker Container

This directory contains files for building and deploying the opsd service as a Docker container on SONiC devices.

## Files

- `Dockerfile`: Defines the container image for opsd server.
- `build_deploy_testonly.sh`: Script to build and deploy the container (test-only)

## Building and Deploying

### Using Makefile

To build the Docker image:

```bash
# Build the Docker image
make docker-build
```

### Using build_deploy_testonly.sh

For a one-step build and deploy process, use the build_deploy_testonly.sh script:

```bash
# Build and deploy to a specific SONiC device
./build_deploy_testonly.sh -t admin@vlab-01

# Optionally specify a custom image tag
./build_deploy_testonly.sh -t admin@vlab-01 -i v1.0

# Deploy with a custom server address
./build_deploy_testonly.sh -t admin@vlab-01 -a ":8080"
```

The script will:
1. Build the Docker image using `make docker-build`
2. Save the image to a temporary file
3. Transfer the image to the specified SONiC device
4. Load the image and start the container on the device
5. Clean up temporary files

## Manual Deployment

If you prefer to deploy manually:

1. Build the Docker image:
   ```bash
   make docker-build
   ```

2. Transfer the image to the SONiC device:
   ```bash
   docker save opsd:latest | ssh admin@<sonic-device> 'docker load'
   ```

3. Run the container on the SONiC device:
   ```bash
   # Default configuration (port 50055)
   ssh admin@<sonic-device> 'docker run -d --name opsd --network host opsd:latest'

   # With custom address and port mapping
   ssh admin@<sonic-device> 'docker run -d --name opsd -p 8080:8080 -e OPSD_ADDR=":8080" opsd:latest'

   # With host networking (recommended for SONiC)
   ssh admin@<sonic-device> 'docker run -d --name opsd --network host -e OPSD_ADDR=":8080" opsd:latest'
   ```

### Deploying to SONiC

#### Method 1: Manual Deployment

1. Transfer the Docker image to the SONiC device:
   ```bash
   docker save opsd:latest | ssh admin@<sonic-device> 'docker load'
   ```

2. Run the container on the SONiC device:
   ```bash
   # Default configuration (port 50055)
   ssh admin@<sonic-device> 'docker run -d --name opsd --network host opsd:latest'

   # With custom address and port mapping
   ssh admin@<sonic-device> 'docker run -d --name opsd -p 8080:8080 -e OPSD_ADDR=":8080" opsd:latest'

   # With host networking (recommended for SONiC)
   ssh admin@<sonic-device> 'docker run -d --name opsd --network host -e OPSD_ADDR=":8080" opsd:latest'
   ```

## Configuration

The container supports the following environment variables:
- `OPSD_ADDR`: The address to listen on (default: ":50055")
- `OPSD_SHUTDOWN_TIMEOUT`: Maximum time to wait for graceful shutdown (default: "10s")

The container mounts the following directories from the SONiC host:
- `/etc/sonic` (read-only): For accessing SONiC configuration
- `/host` (read-only): For accessing the host file system

## Ports

The gRPC server's listening port is configurable via the `OPSD_ADDR` environment variable (default: ":50055").

**Important**: When using custom ports, ensure you either:
- Use `--network host` for direct host networking (recommended for SONiC), OR
- Use `-p <host-port>:<container-port>` to map ports when using custom addresses

Examples:
```bash
# Host networking (port determined by OPSD_ADDR)
docker run -d --name opsd --network host -e OPSD_ADDR=":8080" opsd:latest

# Port mapping (must match OPSD_ADDR port)
docker run -d --name opsd -p 8080:8080 -e OPSD_ADDR=":8080" opsd:latest
```
