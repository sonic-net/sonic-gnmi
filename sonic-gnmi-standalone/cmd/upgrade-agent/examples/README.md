# Upgrade Agent Examples

This directory contains example YAML configuration files for the upgrade-agent tool.

## Usage

### Using YAML configuration file (recommended)

```bash
# Build the upgrade-agent
make build

# Apply a workflow configuration from YAML file (server specified via flags)
./bin/upgrade-agent apply examples/workflow-example.yaml --server localhost:50055

# Apply a multi-step workflow
./bin/upgrade-agent apply examples/multi-step-workflow.yaml --server localhost:50055

# With TLS enabled
./bin/upgrade-agent apply examples/workflow-example.yaml --server localhost:50055 --tls
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

### Workflow Configuration
- **workflow-example.yaml**: Single-step workflow example
- **multi-step-workflow.yaml**: Multiple download steps in sequence

For the YAML configuration structure and field descriptions, see the inline documentation in `cmd/upgrade-agent/config.go`.