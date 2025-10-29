# Building sonic-gnmi Locally

This guide describes how to build `sonic-gnmi` Debian package in a lightweight containerized environment without needing the full sonic-buildimage build system.

## Overview

The build process uses the official SONiC build container (`sonic-slave-bookworm`) and pre-built dependencies from Azure pipeline artifacts. This approach is faster and requires less disk space than a full sonic-buildimage build.

## Prerequisites

- Docker installed and running
- Access to SONiC Azure pipeline artifacts (or pre-built dependencies)
- ~20GB free disk space
- Network access to download dependencies

## Directory Structure

```
$WORKSPACE/
├── sonic-gnmi/              # This repository
├── sonic-mgmt-common/       # Checked out during build
├── sonic-swss-common/       # Checked out during build
└── target/                  # Dependencies from sonic-buildimage artifact
    ├── debs/bookworm/
    └── python-wheels/bookworm/
```

## Step 1: Create Build Container

Create a Docker container using the sonic-slave-bookworm image with your workspace mounted:

```bash
export WORKSPACE=/path/to/your/workspace

docker run -dit \
  --name sonic-gnmi-build \
  --mount type=bind,source=$WORKSPACE,target=/workspace \
  -w /workspace/sonic-gnmi \
  sonicdev-microsoft.azurecr.io:443/sonic-slave-bookworm:latest \
  bash
```

**Note:** Replace `/path/to/your/workspace` with your actual workspace path.

## Step 2: Download Dependencies from Azure Pipelines

sonic-gnmi requires pre-built Debian packages from sonic-buildimage. These are available from Azure Pipelines.

### Finding the Azure Pipeline

1. Go to [sonic-buildimage GitHub](https://github.com/sonic-net/sonic-buildimage)
2. Look for the "Azure Pipelines" build status badge on the front page
3. Click on it to access the pipeline runs
4. Alternatively, directly access: [Azure build pipeline](https://dev.azure.com/mssonic/build/_build?definitionId=142)

### Download the Artifact

You need the `sonic-buildimage.mellanox` (or `sonic-buildimage.vs`) artifact from a successful build:

```bash
cd $WORKSPACE

# Download the artifact (replace URL with actual artifact download URL from Azure)
# Size: ~16GB compressed
wget -O sonic-buildimage.mellanox.tar.gz "ARTIFACT_URL"

# Extract
tar -xzf sonic-buildimage.mellanox.tar.gz
```

**IMPORTANT NOTE:** Most of these packages are **NOT** installed in the build container. They are only extracted so that sonic-mgmt-common and sonic-gnmi build processes can reference them. Only a subset will be installed in Step 3.

### Required files from artifact:

**For sonic-mgmt-common build:**
- `target/debs/bookworm/libyang_1.0.73_amd64.deb`
- `target/debs/bookworm/libyang-dev_1.0.73_amd64.deb`
- `target/debs/bookworm/libyang-cpp_1.0.73_amd64.deb`
- `target/debs/bookworm/libnl-3-200_3.7.0-0.2+b1sonic1_amd64.deb`
- `target/debs/bookworm/libnl-3-dev_3.7.0-0.2+b1sonic1_amd64.deb`
- `target/debs/bookworm/libnl-genl-3-200_3.7.0-0.2+b1sonic1_amd64.deb`
- `target/debs/bookworm/libnl-genl-3-dev_3.7.0-0.2+b1sonic1_amd64.deb`
- `target/debs/bookworm/libnl-route-3-200_3.7.0-0.2+b1sonic1_amd64.deb`
- `target/debs/bookworm/libnl-route-3-dev_3.7.0-0.2+b1sonic1_amd64.deb`
- `target/python-wheels/bookworm/sonic_yang_models-1.0-py3-none-any.whl`

**For sonic-gnmi build:**
- `target/debs/bookworm/libnl-nf-3-200_3.7.0-0.2+b1sonic1_amd64.deb`
- `target/debs/bookworm/libswsscommon_1.0.0_amd64.deb`
- `target/debs/bookworm/libswsscommon-dev_1.0.0_amd64.deb`

### Create symlink for easier access:

```bash
cd $WORKSPACE
ln -s sonic-buildimage.mellanox/target target
```

This creates a `target/` directory that both sonic-mgmt-common and sonic-gnmi Makefiles expect to find.

## Step 3: Install Dependencies in Container

### 3.1 Install System Packages

```bash
docker exec sonic-gnmi-build bash -c "apt-get update && apt-get install -y redis-server protobuf-compiler"
```

### 3.2 Install libyang

```bash
docker exec sonic-gnmi-build bash -c "dpkg -i \
  /workspace/target/debs/bookworm/libyang_1.0.73_amd64.deb \
  /workspace/target/debs/bookworm/libyang-dev_1.0.73_amd64.deb \
  /workspace/target/debs/bookworm/libyang-cpp_1.0.73_amd64.deb"
```

### 3.3 Install libnl

```bash
docker exec sonic-gnmi-build bash -c "dpkg -i \
  /workspace/target/debs/bookworm/libnl-3-200_3.7.0-0.2+b1sonic1_amd64.deb \
  /workspace/target/debs/bookworm/libnl-3-dev_3.7.0-0.2+b1sonic1_amd64.deb \
  /workspace/target/debs/bookworm/libnl-genl-3-200_3.7.0-0.2+b1sonic1_amd64.deb \
  /workspace/target/debs/bookworm/libnl-genl-3-dev_3.7.0-0.2+b1sonic1_amd64.deb \
  /workspace/target/debs/bookworm/libnl-route-3-200_3.7.0-0.2+b1sonic1_amd64.deb \
  /workspace/target/debs/bookworm/libnl-route-3-dev_3.7.0-0.2+b1sonic1_amd64.deb"
```

### 3.4 Install sonic_yang_models

```bash
docker exec sonic-gnmi-build bash -c "pip3 install /workspace/target/python-wheels/bookworm/sonic_yang_models-1.0-py3-none-any.whl"
```

### 3.5 Install libswsscommon

```bash
docker exec sonic-gnmi-build bash -c "dpkg -i \
  /workspace/target/debs/bookworm/libnl-nf-3-200_3.7.0-0.2+b1sonic1_amd64.deb && \
  dpkg -i \
  /workspace/target/debs/bookworm/libswsscommon_1.0.0_amd64.deb \
  /workspace/target/debs/bookworm/libswsscommon-dev_1.0.0_amd64.deb"
```

## Step 4: Clone and Build sonic-mgmt-common

```bash
# Clone sonic-mgmt-common (if not already present)
cd $WORKSPACE
git clone https://github.com/sonic-net/sonic-mgmt-common.git --depth 1 --branch master

# Build sonic-mgmt-common
docker exec -w /workspace/sonic-mgmt-common sonic-gnmi-build bash -c \
  "NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc"

# Install sonic-mgmt-common packages
docker exec sonic-gnmi-build bash -c "dpkg -i \
  /workspace/sonic-mgmt-common_1.0.0_amd64.deb \
  /workspace/sonic-mgmt-common-codegen_1.0.0_amd64.deb"
```

**Note:** Ignore the postinst warning about `debian/sonic-mgmt-common-codegen.install` - the packages install correctly.

## Step 5: Clone sonic-swss-common

**IMPORTANT:** sonic-gnmi has CGO code (`sonic_data_client/events_client.go`) that references `../../sonic-swss-common/common` for header files. You must clone sonic-swss-common at the same level as sonic-gnmi.

```bash
cd $WORKSPACE
git clone https://github.com/sonic-net/sonic-swss-common.git --depth 1 --branch master
```

## Step 6: Build sonic-gnmi

```bash
docker exec -w /workspace/sonic-gnmi sonic-gnmi-build bash -c \
  "ENABLE_TRANSLIB_WRITE=y GOFLAGS=-buildvcs=false dpkg-buildpackage -rfakeroot -us -uc -b -j$(nproc)"
```

**Build flags:**
- `ENABLE_TRANSLIB_WRITE=y` - Enables translib write support
- `GOFLAGS=-buildvcs=false` - Disables VCS stamping (required when building outside git repository root)
- `-j$(nproc)` - Parallel build using all CPU cores

**Build time:** ~5-10 minutes depending on system

## Step 7: Verify Build Output

The built package will be in the workspace root:

```bash
ls -lh $WORKSPACE/sonic-gnmi_0.1_amd64.deb
```

Expected size: ~30MB

## Troubleshooting

### Error: `events_wrap.h: No such file or directory`

**Cause:** sonic-swss-common is not cloned or not at the correct location.

**Solution:** Ensure sonic-swss-common is cloned at the same level as sonic-gnmi:
```bash
cd $WORKSPACE
git clone https://github.com/sonic-net/sonic-swss-common.git --depth 1
```

### Error: `cannot execute binary file: Exec format error`

**Cause:** Trying to run x86_64 binaries on ARM architecture (or vice versa).

**Solution:** Use the sonic-slave-bookworm container which has the correct architecture and tools.

### Error: `error obtaining VCS status`

**Cause:** Building with VCS stamping enabled outside a git repository.

**Solution:** Add `GOFLAGS=-buildvcs=false` to the build command.

### Missing dependencies

**Cause:** Not all required .deb files or wheels were extracted from the artifact.

**Solution:** Verify all files listed in Step 2 are present in the target directory.

## Deploying to SONiC Device

### Copy to device:

```bash
scp $WORKSPACE/sonic-gnmi_0.1_amd64.deb admin@<sonic-device>:/tmp/
```

### Install in gnmi container:

```bash
ssh admin@<sonic-device>
docker cp /tmp/sonic-gnmi_0.1_amd64.deb gnmi:/root/
docker exec gnmi dpkg -i /root/sonic-gnmi_0.1_amd64.deb
docker exec gnmi supervisorctl restart gnmi-native
```

## Cleaning Up

Remove the build container when done:

```bash
docker stop sonic-gnmi-build
docker rm sonic-gnmi-build
```

To clean build artifacts and rebuild from scratch:

```bash
docker exec -w /workspace/sonic-gnmi sonic-gnmi-build make clean
```

## Dependencies Summary

### Build-time Dependencies:
- Docker
- sonic-slave-bookworm container image
- libyang 1.0.73
- libnl 3.7.0
- libswsscommon 1.0.0
- sonic_yang_models 1.0
- protobuf-compiler
- redis-server
- Go 1.21+ (included in sonic-slave-bookworm)
- SWIG (included in sonic-slave-bookworm)

### Source Dependencies:
- sonic-gnmi (this repository)
- sonic-mgmt-common
- sonic-swss-common

### Runtime Dependencies (on SONiC device):
- All the above library packages must be installed in the SONiC image

## References

- [sonic-gnmi GitHub](https://github.com/sonic-net/sonic-gnmi)
- [sonic-mgmt-common GitHub](https://github.com/sonic-net/sonic-mgmt-common)
- [sonic-swss-common GitHub](https://github.com/sonic-net/sonic-swss-common)
- [Azure Pipeline (build/142)](https://dev.azure.com/mssonic/build/_build?definitionId=142)
