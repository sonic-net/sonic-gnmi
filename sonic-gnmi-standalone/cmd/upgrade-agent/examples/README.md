# Upgrade Agent Examples

This directory contains example YAML configuration files for the upgrade-agent tool.

## Usage

### Using YAML configuration file (recommended)

```bash
# Build the upgrade-agent
make build

# Apply a package upgrade from YAML config
./bin/upgrade-agent apply examples/httpbin-example.yaml
```

### Using command-line flags

```bash
# Download and install package directly
./bin/upgrade-agent download \
  --server localhost:50055 \
  --url http://httpbin.org/bytes/1048576 \
  --file /tmp/test-package.bin \
  --md5 d41d8cd98f00b204e9800998ecf8427e \
  --version 1.0.0-test
```

## Configuration Examples

- **stable-file-example.yaml**: Downloads robots.txt with a known, stable MD5 checksum (recommended for testing)
- **httpbin-example.yaml**: Downloads random bytes - will ALWAYS fail MD5 check (only for testing download process)
- **httpbin-json-example.yaml**: Downloads a small JSON response (MD5 may vary)
- **no-md5-example.yaml**: Example without MD5 field - will fail validation (demonstrates MD5 is required)

## Notes

1. Replace the `server.address` with your actual SONiC device gNOI server address
2. For real packages, you'll need to calculate the actual MD5 checksum:
   ```bash
   curl -s http://httpbin.org/bytes/1048576 | md5sum
   ```
3. The `filename` path must be absolute and writable on the target device
4. Set `activate: true` if you want to activate the package after installation