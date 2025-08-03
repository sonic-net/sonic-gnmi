# Docker Support

Build and run sonic-gnmi-standalone as a Docker container.

## Quick Start

```bash
# Build
make docker-build

# Run
docker run -d --name sonic-gnmi --network host sonic-gnmi-standalone:latest

# Run without TLS (testing)
docker run -d --name sonic-gnmi --network host sonic-gnmi-standalone:latest --no-tls
```

## Deploy to SONiC

```bash
# Option 1: Use deployment script
./docker/build_deploy_testonly.sh -t admin@sonic-device

# Option 2: Manual
docker save sonic-gnmi-standalone:latest | ssh admin@sonic-device 'docker load'
ssh admin@sonic-device 'docker run -d --name sonic-gnmi --network host --restart=always sonic-gnmi-standalone:latest'
```

## Configuration

Pass command-line arguments directly:

```bash
docker run -d --name sonic-gnmi --network host sonic-gnmi-standalone:latest --addr=:8080 --no-tls
```

Mount certificates for TLS:

```bash
docker run -d --name sonic-gnmi --network host \
  -v /path/to/certs:/certs:ro \
  sonic-gnmi-standalone:latest \
  --tls-cert=/certs/server.crt \
  --tls-key=/certs/server.key
```

## Notes

- Default port: 50055
- TLS enabled by default (use `--no-tls` to disable)
- Use `--network host` for SONiC deployments
- See main README for all command-line options