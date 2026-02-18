# Scalable DPU Status with upgrade-agent

This document shows how to use the **scalable** `show` commands in `upgrade-agent` to check DPU status via gRPC. The design follows industry-standard CLI patterns with hierarchical subcommands.

## Prerequisites

1. Build the upgrade service:
   ```bash
   make build
   ```

2. Deploy the server to your SONiC device:
   ```bash
   ./docker/build_deploy.sh -t admin@10.250.0.101
   ```

## 🎯 Scalable Command Structure

### Available Commands

```bash
# Show chassis modules midplane status
./bin/upgrade-agent show chassis modules midplane-status [--dpu DPU0]

# Show system health for DPUs
./bin/upgrade-agent show system-health dpu --dpu DPU0
```

### Command Hierarchy

```
upgrade-agent show
├── chassis
│   └── modules
│       └── midplane-status [--dpu DPU0]
└── system-health
    └── dpu --dpu DPU0
```

## Usage Examples

### 1. Chassis Modules Midplane Status

#### Show All DPUs
```bash
./bin/upgrade-agent show chassis modules midplane-status --server 10.250.0.101:50051 --no-tls
```

**Expected Output:**
```
Chassis Modules Midplane Status for All DPUs:
============================================
Found 2 DPU(s):

Name     IP Address      Reachability
----     ----------      ------------
DPU0     169.254.200.1   True
DPU3     169.254.200.4   False

Summary: 1/2 DPUs reachable
```

#### Show Specific DPU
```bash
./bin/upgrade-agent show chassis modules midplane-status --server 10.250.0.101:50051 --no-tls --dpu DPU0
```

**Expected Output:**
```
Chassis Modules Midplane Status for DPU0:
============================================
Found 1 DPU(s):

Name     IP Address      Reachability
----     ----------      ------------
DPU0     169.254.200.1   True

Summary: 1/1 DPUs reachable
```

### 2. System Health DPU Status

#### Show Specific DPU System Health
```bash
./bin/upgrade-agent show system-health dpu --server 10.250.0.101:50051 --no-tls --dpu DPU0
```

**Expected Output:**
```
System Health DPU Status for Specific DPU:
======================================
Found 1 DPU(s):

Name     Oper-Status   State-Detail            State-Value   Time                            Reason
----     -----------   ------------            -----------   ----                            ------
DPU0     Online        dpu_midplane_link_state up            Fri Jul 18 12:45:12 AM UTC 2025
                       dpu_control_plane_state up            Fri Jul 18 12:46:27 AM UTC 2025
                       dpu_data_plane_state    up            Fri Jul 18 12:46:27 AM UTC 2025

Summary: 1/1 DPUs online
```



## Command Line Options

All subcommands support:
- `--server <address>`: Server address (required)
- `--no-tls`: Disable TLS encryption
- `--dpu <name>`: Specific DPU name (optional, shows all if not specified)
- `--verbose`: Show additional debug information
- `--help`: Show help message

## Comparison with Direct CLI

### Traditional Way:
```bash
# SSH into SONiC device
ssh admin@10.250.0.101

# Run show commands directly
admin@str3-msn4280-02:~$ show chassis modules midplane-status DPU0
admin@str3-msn4280-02:~$ show system-health dpu DPU0
```

### With upgrade-agent:
```bash
# Run from any machine with network access
./bin/upgrade-agent show chassis modules midplane-status --server 10.250.0.101:50051 --no-tls --dpu DPU0
./bin/upgrade-agent show system-health dpu --server 10.250.0.101:50051 --no-tls --dpu DPU0
```

## Architecture

```
┌─────────────────┐    gRPC call           ┌─────────────────┐    CLI exec         ┌─────────────────┐
│  upgrade-agent  │ ─── GetShowCommand ──► │  upgrade-service│ ───────────────────► │  show command   │
│     (client)    │     Output()           │    (server)     │                     │   (SONiC CLI)   │
│                 │                        │                 │                     │                 │
│ show chassis... │                        │ ExecuteCommand()│                     │ show chassis... │
│ show sys-health │                        │ (CommandType)   │                     │ show sys-health │
└─────────────────┘                        └─────────────────┘                     └─────────────────┘
```

## Benefits of Scalable Design

1. **🎯 Industry Standard**: Follows Cisco, Juniper, and SONiC CLI patterns
2. **🔧 Extensible**: Easy to add new show commands without breaking existing ones
3. **📚 Self-Documenting**: Clear hierarchy makes discoverability easy
4. **🌐 Remote Access**: No need to SSH into each SONiC device
5. **🤖 Programmatic**: Structured output for automation
6. **🔐 Secure**: Uses gRPC with optional TLS

## Usage Examples

```bash
# Check chassis modules midplane status
./bin/upgrade-agent show chassis modules midplane-status --dpu DPU0

# Check system health for DPUs
./bin/upgrade-agent show system-health dpu --dpu DPU0
```


## Help Discovery

Use the built-in help system to explore available commands:

```bash
./bin/upgrade-agent show --help
./bin/upgrade-agent show chassis --help
./bin/upgrade-agent show chassis modules --help
./bin/upgrade-agent show system-health --help
```