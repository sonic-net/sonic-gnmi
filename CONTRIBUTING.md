# Contributing to sonic-gnmi

Thank you for contributing! This guide covers how to set up a local development environment, run tests, and submit a pull request.

## Table of Contents

- [Development Environment](#development-environment)
  - [Prerequisites](#prerequisites)
  - [Quick Start](#quick-start)
- [Building](#building)
- [Running Tests](#running-tests)
- [Code Structure](#code-structure)
- [Submitting a Pull Request](#submitting-a-pull-request)

---

## Development Environment

sonic-gnmi depends on SONiC-specific packages (libyang, swsscommon, sonic_yang_models) that are not available in standard package registries. The recommended way to develop locally is via the **dev container**, which has all dependencies pre-installed.

### Prerequisites

- Docker (any recent version)
- `make`
- No Go installation needed on the host — it's inside the container

### Quick Start

**1. Build the dev image**

The dev image is not yet published to a public registry. Build it locally first (one-time setup, ~10 min):

```bash
make -f dev.mk dev-image
```

**2. Start the dev container**

```bash
make -f dev.mk dev-up
```

This starts a container using the locally built image, with your repo mounted at `/workspace/sonic-gnmi`.

**2. Run a test**

```bash
make -f dev.mk test PKG=sonic_db_config
```

**3. Stop the container when done**

```bash
make -f dev.mk dev-down
```

---

## Building

Build the sonic-gnmi `.deb` package inside the container:

```bash
make -f dev.mk build
```

The output package is written to the `build/` directory.

---

## Running Tests

### Run all tests in a package

```bash
make -f dev.mk test PKG=<package>
```

Available packages: `gnmi_server`, `telemetry`, `sonic_db_config`, `dialout`, `sonic_data_client`, `transl_utils`, `sonic_service_client`, and others under `pkg/`.

### Run a single test by name

```bash
make -f dev.mk test PKG=gnmi_server TEST=TestServerUnixSocket
```

`TEST` is passed to `go test -run`, so it supports regex patterns.

### Run the full integration suite

```bash
make -f dev.mk test-all
```

> ⚠️ The full suite can take 30+ minutes. For day-to-day iteration, prefer `test PKG=...` targeting the package you changed.

### Run fast pure-Go tests (no Redis, no CGO)

```bash
make -f dev.mk test-pure
```

These run without starting Redis and finish in seconds — good for quick sanity checks on packages that don't need SONiC daemons.

### Interactive shell

If you want to run arbitrary commands inside the container:

```bash
make -f dev.mk shell
```

---

## Building the Dev Image Locally

The CI publishes a dev image on each merge to master. If you need to build it yourself (e.g. to test a Dockerfile change):

```bash
make -f dev.mk dev-image
```

Or target a specific branch's artifacts:

```bash
make -f dev.mk dev-image BUILD_BRANCH=202412
```

---

## Code Structure

```
sonic-gnmi/
├── gnmi_server/        # gNMI server (Subscribe, Get, Set)
├── telemetry/          # Telemetry service entry point
├── dialout/            # Dial-out client
├── sonic_data_client/  # SONiC DB data client (Redis-backed)
├── sonic_db_config/    # DB config parsing
├── transl_utils/       # YANG translation utilities
├── sonic_service_client/ # Service/process management via DBus
├── pkg/                # Shared internal libraries
├── proto/              # Protobuf definitions
├── doc/                # Design docs and usage guides
├── Dockerfile.dev      # Dev container image definition
└── dev.mk              # Dev workflow Makefile
```

---

## Submitting a Pull Request

1. **Fork** the repository and create a branch from `master`.

2. **One concept per PR.** Don't mix unrelated changes — reviewers will ask you to split them.

3. **Test your change** before pushing:
   ```bash
   make -f dev.mk test PKG=<affected_package>
   ```

4. **Sign off your commits** (required — SONiC uses the [DCO](https://developercertificate.org/)):
   ```bash
   git commit -s -m "component: description of change"
   ```

5. **Commit message format:**
   ```
   [component]: Short description of intent

   - Detail 1
   - Detail 2

   Signed-off-by: Your Name <your@email.com>
   ```

6. Open a pull request against `sonic-net/sonic-gnmi:master`. The CI pipeline will run unit tests and build the dev container image automatically.

### CI Pipeline

The Azure DevOps pipeline runs two stages in parallel:

- **Build** — compiles and runs the full test suite inside a SONiC slave container
- **DevContainer** — builds the dev image and publishes it as a pipeline artifact

If the `Build` stage fails due to a flaky test (common with timing-sensitive tests), you can re-run just that job. Flakiness in the full suite does not indicate a problem with your change if the affected test is unrelated.

---

## Need Help?

- Open a [GitHub Issue](https://github.com/sonic-net/sonic-gnmi/issues) for bugs or feature requests
- For general SONiC questions: [sonicproject on Google Groups](https://groups.google.com/d/forum/sonicproject)
