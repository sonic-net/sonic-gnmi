# mopd (Method of Procedure daemon) Docker Container

This directory contains files for building and deploying the mopd service as a Docker container on SONiC devices.

## Files

- `Dockerfile`: Defines the container image for mopd server.
- `build_deploy.sh`: Script to build and deploy the container

## Building and Deploying

### Using Makefile

To build the Docker image:

```bash
# Build the Docker image
make docker-build
```

### Using build_deploy.sh

For a one-step build and deploy process, use the build_deploy.sh script:

```bash
# Build and deploy to a specific SONiC device
./build_deploy.sh -t admin@vlab-01

# Optionally specify a custom image tag
./build_deploy.sh -t admin@vlab-01 -i v1.0

# Deploy with a custom server address
./build_deploy.sh -t admin@vlab-01 -a ":8080"
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
   docker save docker-mopd:latest | ssh admin@<sonic-device> 'docker load'
   ```

3. Run the container on the SONiC device:
   ```bash
   # Default configuration (port 50051)
   ssh admin@<sonic-device> 'docker run -d --name mopd --network host docker-mopd:latest'

   # With custom address and port mapping
   ssh admin@<sonic-device> 'docker run -d --name mopd -p 8080:8080 -e MOPD_ADDR=":8080" docker-mopd:latest'

   # With host networking (recommended for SONiC)
   ssh admin@<sonic-device> 'docker run -d --name mopd --network host -e MOPD_ADDR=":8080" docker-mopd:latest'
   ```

### Deploying to SONiC

#### Method 1: Manual Deployment

1. Transfer the Docker image to the SONiC device:
   ```bash
   docker save docker-mopd:latest | ssh admin@<sonic-device> 'docker load'
   ```

2. Run the container on the SONiC device:
   ```bash
   # Default configuration (port 50051)
   ssh admin@<sonic-device> 'docker run -d --name mopd --network host docker-mopd:latest'

   # With custom address and port mapping
   ssh admin@<sonic-device> 'docker run -d --name mopd -p 8080:8080 -e MOPD_ADDR=":8080" docker-mopd:latest'

   # With host networking (recommended for SONiC)
   ssh admin@<sonic-device> 'docker run -d --name mopd --network host -e MOPD_ADDR=":8080" docker-mopd:latest'
   ```

## Configuration

The container supports the following environment variables:
- `MOPD_ADDR`: The address to listen on (default: ":50051")
- `MOPD_SHUTDOWN_TIMEOUT`: Maximum time to wait for graceful shutdown (default: "10s")

The container mounts the following directories from the SONiC host:
- `/etc/sonic` (read-only): For accessing SONiC configuration
- `/host` (read-only): For accessing the host file system

## Ports

The gRPC server's listening port is configurable via the `MOPD_ADDR` environment variable (default: ":50051").

**Important**: When using custom ports, ensure you either:
- Use `--network host` for direct host networking (recommended for SONiC), OR
- Use `-p <host-port>:<container-port>` to map ports when using custom addresses

Examples:
```bash
# Host networking (port determined by MOPD_ADDR)
docker run -d --name mopd --network host -e MOPD_ADDR=":8080" docker-mopd:latest

# Port mapping (must match MOPD_ADDR port)
docker run -d --name mopd -p 8080:8080 -e MOPD_ADDR=":8080" docker-mopd:latest
```
