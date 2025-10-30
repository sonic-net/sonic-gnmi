# SONiC Upgrade Agent Demo

This demo shows the complete upgrade-agent workflow including download, activate, and reboot steps using gNOI APIs.

## Prerequisites

- KVM virtual switch running SONiC with gNOI server at IP 10.250.0.101
- Linux host with Go toolchain and network connectivity to 10.250.0.101
- Admin access to the KVM (password: `password`)

## Demo Steps

### 1. Checkout Demo Branch

```bash
git checkout demo/reboot-feature
```

### 2. Download SONiC VS Image

Create a directory for SONiC images and download the latest VS build:

```bash
mkdir -p ~/sonic-images
cd ~/sonic-images
wget "https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target=target/sonic-vs.bin" -O sonic-vs.bin
```

Verify the download:
```bash
ls -lh sonic-vs.bin
md5sum sonic-vs.bin  # Note the MD5 for later use
```

### 3. Setup Local HTTP Server

Use a simple container to serve the SONiC image:

```bash
# Run HTTP server container serving the sonic-images directory
docker run -d --name sonic-firmware-server \
  -p 8081:80 \
  -v ~/sonic-images:/usr/share/nginx/html:ro \
  nginx:alpine

# Verify container is running
docker ps | grep sonic-firmware-server
```

Verify the server:
```bash
curl -I http://localhost:8081/sonic-vs.bin
```

To stop the server when done:
```bash
docker stop sonic-firmware-server
docker rm sonic-firmware-server
```

### 4. Prepare KVM Environment

#### Check VM Status
```bash
ping -c 2 10.250.0.101
# Should respond from KVM
```

#### Resize /tmp (if needed)
On your KVM (10.250.0.101), check /tmp space:
```bash
df -h /tmp

# If /tmp is separate tmpfs with insufficient space:
sudo mount -o remount,size=2G /tmp
```

#### Verify gNOI Server
```bash
# Check gNOI services are available
grpcurl -plaintext 10.250.0.101:8080 list

# Should show services including:
# - gnoi.system.System
# - gnoi.os.OS
```

#### Check Current OS State
On your KVM (10.250.0.101):
```bash
sudo sonic-installer list
```

### 5. Build upgrade-agent

```bash
cd sonic-gnmi-standalone
make build
```

Verify the binary:
```bash
./bin/upgrade-agent --help
```

### 6. Update Workflow Configuration

Update the MD5 checksum in workflow files to match your downloaded image:

```bash
# Get MD5 of your image
MD5_CHECKSUM=$(md5sum ~/sonic-images/sonic-vs.bin | cut -d' ' -f1)

# Update upgrade.yaml with correct MD5
sed -i "s/md5: \".*\"/md5: \"$MD5_CHECKSUM\"/" tests/examples/upgrade.yaml
```

### 7. Demo Workflows

#### Test Individual Steps

**Download Only (activate: false):**
```bash
./bin/upgrade-agent apply tests/examples/download-to-host.yaml --server 10.250.0.101:8080
```

**Activate Existing Version:**
```bash
./bin/upgrade-agent apply tests/examples/activate-only.yaml --server 10.250.0.101:8080
```

**Reboot Only:**
```bash
./bin/upgrade-agent apply tests/examples/reboot-only.yaml --server 10.250.0.101:8080
```

#### Full Upgrade Workflow

**Option 1: Download with activate=true + separate reboot**
```bash
# First: Download and install (activate=true)
./bin/upgrade-agent apply tests/examples/redownload-sonic-vs.yaml --server 10.250.0.101:8080 --timeout 10m

# Check that image appears in sonic-installer list (on KVM):
# sudo sonic-installer list

# Then: Reboot to new image
./bin/upgrade-agent apply tests/examples/reboot-immediate.yaml --server 10.250.0.101:8080
```

**Option 2: Complete upgrade workflow**
```bash
# Clean up space first (if needed) - on KVM run:
# sudo sonic-installer remove <old-version> -y

# Run complete upgrade: download + activate + reboot
./bin/upgrade-agent apply tests/examples/upgrade.yaml --server 10.250.0.101:8080 --timeout 10m
```

### 8. Verify Results

After reboot, wait for the VM to come back up and verify the upgrade:

```bash
# Wait for VM to boot (may take 1-2 minutes)
sleep 60

# Verify new version is running (on KVM):
# sudo sonic-installer list
# Should show the new version as "Current"
```

## Workflow Files

- `tests/examples/upgrade.yaml` - Complete upgrade workflow (download + activate + reboot)
- `tests/examples/download-to-host.yaml` - Download only with activate=false
- `tests/examples/redownload-sonic-vs.yaml` - Download with activate=true
- `tests/examples/activate-only.yaml` - Activate existing version
- `tests/examples/reboot-only.yaml` - Reboot with delay
- `tests/examples/reboot-immediate.yaml` - Immediate reboot

## Key Features Demonstrated

1. **gNOI System.SetPackage** - Download SONiC images with HTTP and MD5 verification
2. **gNOI OS.Activate** - Activate specific OS versions
3. **gNOI System.Reboot** - Trigger system reboots with various methods
4. **Workflow orchestration** - Multi-step upgrade procedures
5. **Error handling** - Proper error reporting and recovery

## Troubleshooting

**gNOI server not responding:**
On your KVM (10.250.0.101):
```bash
docker restart gnmi
```

**Insufficient disk space:**
On your KVM (10.250.0.101):
```bash
sudo sonic-installer remove <old-version> -y
```

**Connection reset during activate=true:**
This indicates the gNOI server may have crashed during image installation. Restart the container and try again.

**Reboot fails with "not immediate":**
Use `delay: 0` for immediate reboot instead of scheduled reboot.

## Architecture

The upgrade-agent demonstrates the evolution from legacy bash-script based upgrades to modern gRPC-based orchestration:

- **Legacy**: HardwareProxy + Telnet + Bash scripts
- **Modern**: upgrade-agent + gRPC + YAML workflows

This provides better error handling, retry capabilities, and testability compared to the legacy approach.