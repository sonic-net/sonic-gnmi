# Upgrade Agent Examples

This directory contains example YAML workflow configuration files for the upgrade-agent tool.

## Usage

### Using YAML configuration file

```bash
# Build the upgrade-agent (from project root)
make build

# Apply a workflow configuration from YAML file (server specified via flags)
./bin/upgrade-agent apply tests/examples/workflow-example.yaml --server localhost:50055

# Apply a multi-step workflow
./bin/upgrade-agent apply tests/examples/multi-step-workflow.yaml --server localhost:50055

# With TLS enabled
./bin/upgrade-agent apply tests/examples/workflow-example.yaml --server localhost:50055 --tls
```

## Configuration Examples

### Workflow Configuration
- **workflow-example.yaml**: Single-step workflow example
- **multi-step-workflow.yaml**: Multiple download steps in sequence

For the YAML configuration structure and field descriptions, see the inline documentation in `pkg/workflow/workflow.go` and `pkg/workflow/steps/download.go`.