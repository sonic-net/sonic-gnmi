# Remaining CI/CD Install & Env-Setup Extraction — Solution Design & Implementation Plan

> **Date:** 2026-06-23 | **Status:** Draft | **Audience:** sonic-gnmi developers/maintainers
> **Scope:** Pure lift-and-shift of the *remaining* duplicated CI/CD install and
> environment-setup logic into standalone scripts under `scripts/`, so that BOTH the Azure
> Pipelines (ADO) templates AND the local dev driver (`dev/setup.sh` + `dev/run-tests.sh`)
> call the same single-source-of-truth scripts.
> **Revision notes:** Rev 2 — corrected the ADO call-path invariant (single-checkout
> `StaticChecks` job has the repo at the sources root, not under `sonic-gnmi/`; `install-go.yml`
> is shared by single- and multi-checkout jobs, so its script path is now parameterized);
> parameterized the dev-container `--break-system-packages` pip requirement; restored the dev
> `apt-get install -f -y` dpkg fallback; noted the deliberate `set -ex`→`set -e` deviation.
> **Builds on:** the already-extracted `scripts/setup-redis.sh`, `scripts/build-mgmt-common.sh`,
> and `scripts/build-gnmi-deb.sh` (tracked in [`dedupe-ci-dev.plan.md`](dedupe-ci-dev.plan.md))
> and reuses their exact conventions (POSIX `sh`, `set -e`, positional parameters,
> terse load-bearing comments only). **Note:** the ADO call path is NOT uniformly
> `sonic-gnmi/`-prefixed — see convention #3 below.

> **Implementation constraint (BINDING — lift-and-shift only).** The `origin/master`
> branch ADO definition is the **single source of truth** for every extracted command body.
> For each new script, copy the command lines **verbatim** from the master version of the
> source file (`git show origin/master:azure-pipelines.yml`,
> `git show origin/master:.azure/templates/install-go.yml`,
> `git show origin/master:.azure/templates/install-dependencies.yml`) — do NOT paraphrase,
> reorder, "improve", or add flags/error-handling beyond what master runs. Before finishing
> each epic, diff the extracted script's effective commands against the master body and confirm
> they are identical (the only permitted deltas are the parameterized env differences listed in
> Files Affected and the dev-only `PIP_FLAGS`/`FIX_DEPS` opt-ins, which default to empty/off so
> the ADO trace stays byte-for-byte equal to master). Where this branch already split a master
> step (the redis extraction in `install-dependencies.yml`), preserve master's *behavior*, not
> the literal bundling.

---

## Executive Summary

Three additional units of CI/CD shell logic are still inlined in the ADO definition and
duplicated (in spirit) by the dev driver: (1) the Go toolchain install in
`.azure/templates/install-go.yml`; (2) the SONiC dependency install steps in
`.azure/templates/install-dependencies.yml` (pytest+jsonpatch, libyang/libnl dpkg, the
`sonic_yang_models` wheel, libswsscommon/python3-swsscommon dpkg, protoc); and (3) the
gofmt static-check body in `azure-pipelines.yml`. This document catalogs each overlap with
`file:line` evidence, then proposes a **minimal, incremental, behavior-preserving**
extraction: move each command body verbatim into a small script under `scripts/`,
parameterizing **only** genuine environment differences (artifact source path, working
directory, arch `amd64`/`arm64`). Each ADO step collapses to a thin
`sonic-gnmi/scripts/<x>.sh …` caller exactly like the already-extracted scripts, and the dev
driver's inline `container_setup_snippet` is rewired to call the same scripts. No features
are added, no behavior is changed, and every documented ADO subtlety is preserved.

---

## Background

### Current state

`scripts/` already holds three extracted, dev+ADO-shared scripts and their functional tests:

- `scripts/setup-redis.sh` — redis unixsocket config; called from `install-dependencies.yml:78`
  and from dev `run-tests.sh:140`.
- `scripts/build-mgmt-common.sh` — `dpkg-buildpackage` of sonic-mgmt-common; called from
  `build-deb.yml:59`, `setup-test-env.yml:45`, and dev `run-tests.sh:182, 233, 316`.
- `scripts/build-gnmi-deb.sh` — `dpkg-buildpackage` of sonic-gnmi; called from
  `build-deb.yml:64` and dev `run-tests.sh:319`.
- `scripts/test_build_scripts.sh` — POSIX functional tests that stub `dpkg-buildpackage`/`nproc`
  and assert the extracted scripts reproduce the original inlined commands.

These established the project conventions this plan follows verbatim:

1. **POSIX `sh` + `set -e`** (no `-x`; callers can `sh -x` for tracing) — see
   `build-mgmt-common.sh:1-8`.
2. **Positional parameters with defaults** for genuine environment differences — see
   `build-gnmi-deb.sh:10-16` (`GNMI_DIR`, `OUT_DIR`, `COPY_GLOB`).
3. **ADO checkout layout determines the call path (NOT uniformly `sonic-gnmi/`-prefixed).**
   ADO places `checkout: self` at `$(Build.SourcesDirectory)` when it is the *only* repo, but
   relocates it into a `sonic-gnmi/` subdirectory when *additional* repos are also checked out
   (the multi-repo case). Therefore the call path depends on the consuming job:
   - **Multi-repo jobs** (e.g. `install-dependencies.yml` consumers, `build-deb.yml:34-47`)
     invoke scripts as `sonic-gnmi/scripts/x.sh` (e.g. `install-dependencies.yml:78`,
     `build-deb.yml:59`). The already-extracted `setup-redis.sh` works *only* because every
     `install-dependencies.yml` consumer is a multi-repo job.
   - **Single-checkout jobs** invoke scripts at the sources root as `scripts/x.sh`. The
     `StaticChecks` job `go_static_checks` does `checkout: self` ONLY
     (`azure-pipelines.yml:67`), so there is no `sonic-gnmi/` subdir there.
   - **Templates shared across both** must therefore parameterize the prefix. `install-go.yml`
     is used by BOTH the single-checkout `StaticChecks` job (`azure-pipelines.yml:71`) and the
     multi-checkout `pure_tests` job (`azure-pipelines.yml:118`), so a single hardcoded prefix
     is wrong for one of them — see Design Decision D7.
   - Dev invokes scripts in-tree by absolute path (`run-tests.sh:140` →
     `/work/sonic-gnmi/scripts/setup-redis.sh`).
4. **apt-index refresh stays with the caller**, not in the script — documented in
   `setup-redis.sh:5-7` and honored by ADO (`install-dependencies.yml:75-76`) and dev
   (`run-tests.sh:136`).

### What changed / why now

The first extraction pass deliberately stopped at the two package builds + redis. The
install/toolchain steps were left inline. They remain a maintenance hazard: any change to
the Go version flow, the SONiC deb set, the yang wheel, the swsscommon packages, or the
gofmt rule must be edited in two places (ADO YAML and dev `run-tests.sh`), and the two copies
have already drifted in small ways (e.g. dev installs only `jsonpatch`, ADO installs
`pytest`+`jsonpatch`).

---

## Problem Statement

Three install/env-setup units are maintained twice and can silently diverge:

1. **Go toolchain install.** `install-go.yml:17-23` downloads+untars `go${version}.linux-amd64`.
   No dev equivalent exists yet (dev runs inside the `sonic-slave-trixie` container, which
   ships Go); a future dev `staticcheck` target that runs gofmt on the host will need the same
   install.
2. **SONiC dependency install.** `install-dependencies.yml:70-148` and dev
   `run-tests.sh:132-144` (`container_setup_snippet`) install the *same* libyang/libnl/swsscommon
   debs, the *same* `sonic_yang_models` wheel, and `jsonpatch`, by hand, from different source
   directories. They have already drifted (pytest present in ADO, absent in dev).
3. **gofmt static check.** `azure-pipelines.yml:75-95` has a 20-line gofmt gate inlined with no
   reusable entry point, so a developer cannot run the exact CI check locally.

---

## Goals and Non-Goals

### Goals
- Extract each of the three remaining units into a standalone script under `scripts/`,
  moving the existing command bodies **verbatim**.
- Parameterize **only** genuine environment differences: artifact/deb source path, working
  directory, and arch (`amd64`/`arm64`).
- Reduce each touched ADO step to a thin `sonic-gnmi/scripts/<x>.sh …` caller.
- Make `dev/run-tests.sh` (and transitively `dev/setup.sh`, which calls `run-tests.sh
  bootstrap`/`pure`) call the same scripts.
- Strip non-load-bearing comments from touched scripts/YAML.
- Add functional tests in the existing `scripts/` test style.

### Non-Goals
- **No redesign, no new features, no behavior change.** Not consolidating the five
  install sub-steps into one mega-script beyond what removes true duplication; not changing
  the Go version, deb set, wheel, or gofmt rule.
- **Not** changing the coverage check job naming (`azure-pipelines.yml:238-241`,
  `coverage.sonic-net.sonic-gnmi.build`).
- **Not** adding a dev `staticcheck` subcommand (deferred; see
  `run-tests.sh:351-362`). The gofmt script is created now; its dev caller lands with that
  deferred target. This is called out as an Open Question.
- **Not** touching `build-deb.yml`/`setup-test-env.yml` step bodies except where they consume
  `install-dependencies.yml` (no body changes needed there).

---

## Requirements

**Functional**
- F1. ADO `Install Go` step output (downloaded tarball, `/usr/local/go`, `go version`) is
  byte-for-byte the same command sequence.
- F2. ADO `install-dependencies.yml` performs the identical install actions, in the same
  order, with the same arch-specific behavior (amd64 installs `python3-swsscommon`, arm64
  does not; arm64 protoc step runs `apt-get update`, amd64 does not).
- F3. ADO gofmt gate has identical find-filter, `gofmt -l`/`gofmt -d` behavior, and exit codes.
- F4. dev `run-tests.sh` install path produces an equivalent container environment using the
  same scripts.

**Non-functional**
- N1. Scripts are POSIX `sh`, `set -e`, no `-x`, matching `build-mgmt-common.sh:8`.
- N2. ADO callers use the call path dictated by their checkout layout: multi-repo jobs use the
  `sonic-gnmi/`-prefixed path (`install-dependencies.yml:78`); the single-checkout `StaticChecks`
  job uses the root path `scripts/...`; templates shared by both parameterize the prefix (D7).
  Dev callers use the absolute in-container path (`run-tests.sh:140`).
- N3. apt-index refresh remains a caller responsibility unless it is intrinsic to a single
  extracted step body (the test-deps step, which already bundles `apt-get update`).
- N4. New scripts have functional tests in the `scripts/test_*.sh` style.
- N5. Genuine host-vs-container differences are parameterized, defaulting to the ADO body so the
  ADO expansion is byte-for-byte verbatim: the dev container (`sonic-slave-trixie`, PEP 668
  externally-managed) requires `--break-system-packages` on every `pip3 install`
  (`run-tests.sh:139`), which the ADO `ubuntu-22.04` host must NOT use; and dev relies on a
  `dpkg -i … || sudo apt-get install -f -y` dependency-resolution fallback (`run-tests.sh:138`,
  documented `dev/SETUP.md:300-303`) that ADO does not use. Both are passed as opt-in flags
  defaulting to empty/off (D8, D9).

---

## Proposed Design

### Architecture Overview

```
                       scripts/  (single source of truth)
   ┌─────────────────────────────────────────────────────────────────┐
   │ install-go.sh        install-test-deps.sh   install-debs.sh      │
   │ install-yang-models.sh  install-swsscommon.sh  install-protoc.sh │
   │ gofmt-check.sh                                                    │
   │ setup-redis.sh  build-mgmt-common.sh  build-gnmi-deb.sh (existing)│
   └─────────────────────────────────────────────────────────────────┘
        ▲ (sonic-gnmi/scripts/x.sh)              ▲ (/work/sonic-gnmi/scripts/x.sh)
        │                                        │
   ADO YAML thin callers                    dev/run-tests.sh
   - azure-pipelines.yml (gofmt)            - container_setup_snippet()
   - install-go.yml                         - build_*_snippet() (existing)
   - install-dependencies.yml               dev/setup.sh → run-tests.sh
```

### Overlap Catalog (file:line evidence)

| # | Unit | ADO location | Dev location | Genuine env differences to parameterize |
|---|------|--------------|--------------|------------------------------------------|
| O1 | Go install (`wget`+`tar` of `go${ver}`) | `install-go.yml:17-23` | *(none today; future host `staticcheck`)* | `version`; `arch` (hardcoded `linux-amd64` at `install-go.yml:19-20`); **script-path prefix** (single- vs multi-checkout job, D7) |
| O2 | pytest+jsonpatch install (+ apt index refresh) | `install-dependencies.yml:70-77` | jsonpatch only at `run-tests.sh:139`; `apt-get update` at `run-tests.sh:136` | **pip flags** (`PIP_FLAGS`): ADO empty, dev `--break-system-packages` (PEP 668, `run-tests.sh:139`). pytest is a harmless superset for dev |
| O3 | purge libnl + `dpkg -i` libyang/libnl debs | `install-dependencies.yml:82-86` | `run-tests.sh:137-138` | deb source dir (`$(Build.ArtifactStagingDirectory)/download` vs `/sonic-debs`); **dep-fix fallback** (`FIX_DEPS`): dev appends `|| sudo apt-get install -f -y` (`run-tests.sh:138`), ADO does not |
| O4 | `pip3 install sonic_yang_models*.whl` | `install-dependencies.yml:101-104` | wheel folded into `run-tests.sh:139` | wheel glob path (`../target/python-wheels/trixie/…` vs `/sonic-debs/…`); **pip flags** (`PIP_FLAGS`): dev `--break-system-packages` |
| O5 | `dpkg -i` libswsscommon[-dev] (+ python3-swsscommon on amd64) | `install-dependencies.yml:119-134` | folded into `dpkg -i /sonic-debs/*.deb` `run-tests.sh:138` | `arch`; working dir (`$(Pipeline.Workspace)/` vs deb dir) |
| O6 | `apt-get install protobuf-compiler` (+arm64 `apt-get update`) | `install-dependencies.yml:137-148` | container ships protoc | `arch` (arm64 adds `apt-get update`) |
| O7 | gofmt static check | `azure-pipelines.yml:75-95` | *(none today; future host `staticcheck`)* | none |

> **Note on O3+O4+O5 in dev:** dev currently does the equivalent in one shot —
> `sudo dpkg -i /sonic-debs/*.deb` (`run-tests.sh:138`) installs libyang/libnl **and**
> swsscommon together because the dev cache dir holds all debs, and one `pip3 install`
> (`run-tests.sh:139`) installs the wheel + jsonpatch. ADO splits them across steps because the
> debs arrive from two different pipeline artifacts into two different directories. The
> extracted scripts make each unit independently callable so both layouts compose them.

### Key Components (new scripts)

Each script moves the **verbatim** command body from the cited lines, adding only the
parameter plumbing.

#### `scripts/install-go.sh`
- **Body source:** `install-go.yml:18-22`.
- **Signature:** `install-go.sh [version] [arch]`
- **Defaults:** `version=1.24.4` (matches `install-go.yml:14` and `azure-pipelines.yml:39`
  `GO_VERSION`); `arch=amd64`.
- **Behavior:** `wget -q https://go.dev/dl/go${version}.linux-${arch}.tar.gz`;
  `sudo tar -C /usr/local -xzf …`; `export PATH=$PATH:/usr/local/go/bin`; `go version`.
- **Parameterization rationale:** `arch` is the only genuine difference *inside* the script;
  `linux-amd64` is currently hardcoded. ADO passes `amd64` (the only host today). The
  **call-path prefix** is parameterized in the `install-go.yml` template (not the script) because
  the template is shared by a single-checkout and a multi-checkout job (D7). `export PATH` inside
  a script does not persist to the ADO job, so the ADO caller keeps its own
  `export PATH=$PATH:/usr/local/go/bin` — see Design Decision D2.

#### `scripts/install-test-deps.sh`
- **Body source:** `install-dependencies.yml:71-76` (the whole step body).
- **Signature:** `install-test-deps.sh` (no positional params); reads optional env `PIP_FLAGS`
  (default empty).
- **Behavior:** `sudo pip3 install ${PIP_FLAGS} -U pytest`;
  `sudo pip3 install ${PIP_FLAGS} -U jsonpatch`; `sudo apt-get update`.
- **Rationale:** This is a single ADO step that already bundles the apt refresh "required for
  setup-redis.sh's apt-get install redis-server" (`install-dependencies.yml:75`). Keeping the
  three lines together preserves that contract. With `PIP_FLAGS` unset, the two `pip3` lines
  expand byte-for-byte to the ADO body. Dev invokes it as
  `PIP_FLAGS=--break-system-packages sh "$SG/install-test-deps.sh"` to satisfy PEP 668
  (`run-tests.sh:139`) — see D8. Dev gains `pytest` (a harmless superset of its current
  jsonpatch-only install), an accepted intentional convergence mirroring the
  `--no-install-recommends` standardization precedent from the redis extraction.

#### `scripts/install-debs.sh`
- **Body source:** `install-dependencies.yml:83-85`.
- **Signature:** `install-debs.sh <deb_dir>`; reads optional env `FIX_DEPS` (default empty/off).
- **Behavior:** `sudo apt-get -y purge libnl-3-dev libnl-route-3-dev || true`;
  then `sudo dpkg -i $(find <deb_dir> -name '*.deb')`, appending `|| sudo apt-get install -f -y`
  **only when `FIX_DEPS` is set**.
- **Parameterization:** `deb_dir` — ADO passes `$(Build.ArtifactStagingDirectory)/download`;
  dev passes `/sonic-debs`. `FIX_DEPS` — dev sets it (`FIX_DEPS=1`) to preserve its
  `dpkg -i … || sudo apt-get install -f -y` dependency-resolution fallback
  (`run-tests.sh:138`, documented `dev/SETUP.md:300-303`); ADO leaves it unset so the body is
  verbatim. With `FIX_DEPS` off the command is byte-for-byte the ADO line; with it on it is
  byte-for-byte the dev line. (Dev's single `dpkg -i /sonic-debs/*.deb` is reproduced by the
  `find … -name '*.deb'`, which covers swsscommon too, so O5 in dev is subsumed here — see D9.)
- **`set -ex` vs `set -e`:** the ADO body uses `set -ex` (`install-dependencies.yml:83`); the
  script follows the project `set -e` convention (N1) and deliberately drops `-x` (stderr command
  tracing only — no behavioral effect on installs or exit codes). Flagged as a conscious
  deviation from strict verbatim; see D10.

#### `scripts/install-yang-models.sh`
- **Body source:** `install-dependencies.yml:102-103`.
- **Signature:** `install-yang-models.sh <wheel_glob>`; reads optional env `PIP_FLAGS`
  (default empty).
- **Behavior:** `sudo pip3 install ${PIP_FLAGS} <wheel_glob>`.
- **Parameterization:** `wheel_glob` — ADO passes
  `../target/python-wheels/trixie/sonic_yang_models*.whl`; dev passes
  `/sonic-debs/sonic_yang_models-*.whl`. `PIP_FLAGS` — dev passes `--break-system-packages`
  (PEP 668), ADO leaves it empty so the body is verbatim (D8).
- **`set -ex` vs `set -e`:** same deliberate `-x` drop as `install-debs.sh` (D10).

#### `scripts/install-swsscommon.sh`
- **Body source:** `install-dependencies.yml:120-124` (amd64) and `130-132` (arm64).
- **Signature:** `install-swsscommon.sh <arch> [deb_dir]`
- **Defaults:** `deb_dir=.` (ADO sets working dir to `$(Pipeline.Workspace)/` via the caller,
  so the script's relative `dpkg -i` resolves there — see D3).
- **Behavior:** `dpkg -i ${deb_dir}/libswsscommon_1.0.0_${arch}.deb`;
  `dpkg -i ${deb_dir}/libswsscommon-dev_1.0.0_${arch}.deb`; **if `arch=amd64`** also
  `dpkg -i ${deb_dir}/python3-swsscommon_1.0.0_${arch}.deb`.
- **Parameterization:** `arch` gates the `python3-swsscommon` package (the documented amd64/arm64
  subtlety, `install-dependencies.yml:117-118`). Version `1.0.0` stays hardcoded (verbatim).
  ADO bodies use `set -ex`; script uses `set -e` (deliberate `-x` drop, D10).

#### `scripts/install-protoc.sh`
- **Body source:** `install-dependencies.yml:138-141` (arm64) and `145-147` (amd64).
- **Signature:** `install-protoc.sh [arch]`
- **Behavior:** **if `arch=arm64`** `sudo apt-get update` first; then
  `sudo apt-get install -y protobuf-compiler`; `protoc --version`.
- **Parameterization:** `arch` gates the leading `apt-get update` (verbatim arm64-only).

#### `scripts/gofmt-check.sh`
- **Body source:** `azure-pipelines.yml:78-94`.
- **Signature:** `gofmt-check.sh` (run from the repo root; uses `find . …` exactly as today).
- **Call path / wd:** the gofmt step lives in the single-checkout `StaticChecks` job
  (`checkout: self` only, `azure-pipelines.yml:67`), so the repo is at the sources root. The ADO
  caller invokes `scripts/gofmt-check.sh` with NO `sonic-gnmi/` prefix and NO
  `workingDirectory: sonic-gnmi` override (default wd is the sources root, where `find .` already
  operates today). See D7.
- **Behavior:** verbatim `mapfile` find with the five `! -path` excludes
  (`vendor/`, `build/`, `patches/`, `proto/`, `swsscommon/`), `gofmt -l`, on failure print
  `::error::` + `gofmt -d … | head -n 200` and `exit 1`, else "All files properly formatted."
- **Shell:** **bash** (uses `mapfile`), unlike the POSIX install scripts — preserved verbatim.
  Keep `#!/usr/bin/env bash` + `set -euo pipefail` to match `azure-pipelines.yml:76`.

### Data Flow

**ADO `install-dependencies.yml` after extraction** (step order unchanged):
1. DownloadPipelineArtifact (libyang/libnl) — *unchanged YAML task*.
2. `[amd64+test]` `sonic-gnmi/scripts/install-test-deps.sh` → then existing
   `- script: sonic-gnmi/scripts/setup-redis.sh` (unchanged).
3. `sonic-gnmi/scripts/install-debs.sh $(Build.ArtifactStagingDirectory)/download`.
4. DownloadPipelineArtifact (yang wheel) — *unchanged*.
5. `sonic-gnmi/scripts/install-yang-models.sh ../target/python-wheels/trixie/sonic_yang_models*.whl`.
6. DownloadPipelineArtifact (swsscommon) — *unchanged*.
7. `sonic-gnmi/scripts/install-swsscommon.sh ${{ parameters.arch }}` with
   `workingDirectory: $(Pipeline.Workspace)/`.
8. `sonic-gnmi/scripts/install-protoc.sh ${{ parameters.arch }}`.

**Dev `run-tests.sh` `container_setup_snippet` after extraction** (`run-tests.sh:132-144`):
```sh
set -euo pipefail
cd /work
SG=/work/sonic-gnmi/scripts
export PIP_FLAGS=--break-system-packages   # PEP 668 (trixie); ADO leaves this unset
export FIX_DEPS=1                           # preserves dpkg `|| apt-get install -f -y` fallback
bash "$SG/install-test-deps.sh"            # pytest+jsonpatch (--break-system-packages)+apt update
bash "$SG/install-debs.sh" /sonic-debs     # purge libnl + dpkg -i all cached debs (+fix-deps)
bash "$SG/install-yang-models.sh" '/sonic-debs/sonic_yang_models-*.whl'
bash "$SG/setup-redis.sh"
git config --global --add safe.directory '*'
export GOFLAGS=-buildvcs=false TMPDIR=/tmp
```
Notes: (1) `PIP_FLAGS`/`FIX_DEPS` are read by the scripts' own shells and passed as argv to
`sudo`, so they survive `sudo`'s env reset (the scripts forward them explicitly; see D8/D9).
(2) `bash` is used (current dev convention, `run-tests.sh:140`); the scripts are `#!/bin/sh`, so
this is harmless. (3) swsscommon debs live in `/sonic-debs` and are installed by `install-debs.sh`,
so dev does not call `install-swsscommon.sh`; the dev `DEB_TARGETS` already includes
`python3-swsscommon_1.0.0_amd64.deb` (`run-tests.sh:93`).

### API Contracts (parameterization summary)

| Script | Args / env | ADO call | Dev call |
|--------|------------|----------|----------|
| `install-go.sh` | `[version] [arch]` | via `install-go.yml`: `${{ parameters.repoRoot }}scripts/install-go.sh $(GO_VERSION)` (repoRoot `''` for StaticChecks, `sonic-gnmi/` for pure_tests) | *(deferred host staticcheck)* |
| `install-test-deps.sh` | env `PIP_FLAGS` | `sonic-gnmi/scripts/install-test-deps.sh` | `PIP_FLAGS=--break-system-packages bash $SG/install-test-deps.sh` |
| `install-debs.sh` | `<deb_dir>`, env `FIX_DEPS` | `… $(Build.ArtifactStagingDirectory)/download` | `FIX_DEPS=1 … /sonic-debs` |
| `install-yang-models.sh` | `<wheel_glob>`, env `PIP_FLAGS` | `… ../target/python-wheels/trixie/sonic_yang_models*.whl` | `PIP_FLAGS=--break-system-packages … /sonic-debs/sonic_yang_models-*.whl` |
| `install-swsscommon.sh` | `<arch> [deb_dir]` | `… ${{ parameters.arch }}` (wd=`$(Pipeline.Workspace)/`) | *(subsumed by install-debs.sh)* |
| `install-protoc.sh` | `[arch]` | `… ${{ parameters.arch }}` | *(container ships protoc)* |
| `gofmt-check.sh` | — | `scripts/gofmt-check.sh` (single-checkout job; root wd, no `sonic-gnmi/` prefix) | *(deferred host staticcheck)* |

### Design Decisions

- **D1 — Granular scripts, not one mega-installer.** Mirroring the existing ADO step
  boundaries (each with its own `displayName`) keeps the diff minimal and each step a thin
  one-line caller, satisfying requirement (b). A single combined script would erase the
  `displayName` granularity and the per-arch `${{ if }}` gating.
- **D2 — `export PATH` in `install-go.sh` is preserved but non-load-bearing across the job.**
  ADO job steps each get a fresh shell, so the persistent PATH still comes from the caller's own
  `export PATH=$PATH:/usr/local/go/bin` (already present in every consuming step, e.g.
  `azure-pipelines.yml:77, 124`). The script keeps the line verbatim so `go version` at the end
  works inside the script's own shell. No behavior change.
- **D3 — `install-swsscommon.sh` honors the ADO `workingDirectory`.** The original steps set
  `workingDirectory: $(Pipeline.Workspace)/` (`install-dependencies.yml:125, 133`) and `dpkg -i`
  bare filenames. The script defaults `deb_dir=.` and the ADO caller keeps
  `workingDirectory: $(Pipeline.Workspace)/`, so the relative paths resolve identically.
- **D4 — Collapse the two arch-conditional swsscommon/protoc steps into one parameterized
  step each.** Two `${{ if eq(arch,…) }}` blocks become one step calling
  `install-…sh ${{ parameters.arch }}`, with `displayName: '… (${{ parameters.arch }})'`. This is
  the minimal structural change that removes duplication while keeping behavior identical; the
  arch branch now lives inside the script (verbatim logic).
- **D5 — `gofmt-check.sh` stays bash.** It uses `mapfile`; converting to POSIX would change
  behavior. Keep `#!/usr/bin/env bash` + `set -euo pipefail` exactly as the inlined step.
- **D6 — apt-index refresh policy.** `install-test-deps.sh` keeps its trailing `apt-get update`
  because that line is intrinsic to the original single step body and feeds `setup-redis.sh`.
  `install-protoc.sh` keeps the arm64-only `apt-get update` verbatim. No new `apt-get update`
  is introduced anywhere.
- **D7 — Parameterized script-path prefix for `install-go.yml`; root path for `gofmt-check.sh`.**
  The `install-go.yml` template is consumed by BOTH the single-checkout `StaticChecks` job
  (`azure-pipelines.yml:71`, repo at sources root) and the multi-checkout `pure_tests` job
  (`azure-pipelines.yml:118`, repo under `sonic-gnmi/`). A single hardcoded prefix would break one
  of them, so the template gains a `repoRoot` parameter (default `''`). The StaticChecks caller
  passes nothing → `scripts/install-go.sh`; the pure_tests caller passes `repoRoot: 'sonic-gnmi/'`
  → `sonic-gnmi/scripts/install-go.sh`. The gofmt step runs only in the single-checkout
  StaticChecks job, so `gofmt-check.sh` is called as `scripts/gofmt-check.sh` at the sources root
  with no prefix and no `workingDirectory` override (`find .` already runs at root today). This
  corrects the prior draft's incorrect assumption that ADO always uses the `sonic-gnmi/` prefix —
  that holds only for multi-repo jobs (e.g. `install-dependencies.yml` consumers).
- **D8 — `PIP_FLAGS` env var for PEP 668.** The dev container (`sonic-slave-trixie`) is
  externally-managed and requires `--break-system-packages` on every `pip3 install`
  (`run-tests.sh:139`); the ADO `ubuntu-22.04` host's pip must NOT receive that flag. The flag is
  a genuine environment difference, so `install-test-deps.sh` and `install-yang-models.sh` read an
  optional `PIP_FLAGS` env var (default empty) and forward it as an explicit argv to
  `sudo pip3 install ${PIP_FLAGS} …`. Forwarding as argv (not relying on the env crossing `sudo`)
  is required because `sudo` resets the environment by default. With `PIP_FLAGS` unset the command
  is the verbatim ADO body; dev exports `PIP_FLAGS=--break-system-packages`.
- **D9 — `FIX_DEPS` env var for dev's dpkg fallback.** Dev's
  `dpkg -i /sonic-debs/*.deb || sudo apt-get install -f -y` (`run-tests.sh:138`, documented
  `dev/SETUP.md:300-303`) pulls in stock deps (e.g. `libpcre2-8-0`). `install-debs.sh` reads an
  optional `FIX_DEPS` env var (default off): off → verbatim ADO `dpkg -i $(find …)`; on (dev) →
  appends `|| sudo apt-get install -f -y`. This restores the dev fallback the prior draft dropped.
- **D10 — `set -ex` → `set -e` is a conscious deviation.** Three ADO step bodies use `set -ex`
  (`install-dependencies.yml:83, 102, 121/130`). The extracted scripts use `set -e` per the
  project convention (N1), deliberately dropping `-x` (stderr command tracing only; no effect on
  installed packages, argv, or exit codes). Callers wanting tracing can run `sh -x script.sh`.
  This is the only intentional departure from byte-for-byte verbatim and is functionally inert.

---

## Alternatives Considered

- **One `install-dependencies.sh arch buildBranch` script.** Rejected: it would have to embed
  the `DownloadPipelineArtifact@2` tasks (which are ADO-native, not shell) or assume artifacts
  are pre-staged, breaking the thin-caller mapping and erasing `displayName` granularity.
- **Leave gofmt inline (no `gofmt-check.sh`).** Rejected: the purpose explicitly lists it, and a
  local `staticcheck` target (deferred) needs the exact same check; extracting now avoids a
  third copy later. Cost is one unused-by-dev-today script, flagged in Open Questions.
- **Make `install-go.sh` also persist PATH via `$GITHUB_PATH`/ADO `##vso`.** Rejected as a
  feature add; lift-and-shift keeps the existing per-step `export PATH`.

---

## Dependencies

- **External:** `go.dev/dl` (Go tarball), `pip3`/PyPI (pytest, jsonpatch), `apt`
  (redis-server, protobuf-compiler), pipeline artifacts (libyang/libnl, yang wheel,
  swsscommon) — all already required today; unchanged.
- **Internal:** existing `scripts/setup-redis.sh` (unchanged) is still called adjacent to
  `install-test-deps.sh`. The dev cache `DEB_TARGETS` (`run-tests.sh:84-95`) must keep listing
  the swsscommon + yang-model artifacts (it already does).
- **Sequencing:** Epic A (host-level: go + gofmt) is independent of Epic B
  (install-dependencies). Epic C (dev rewire) depends on Epic B's scripts existing. Epic D
  (tests) tracks A+B.

---

## Impact Analysis

- **Components affected:** `azure-pipelines.yml` (gofmt step only), `.azure/templates/install-go.yml`,
  `.azure/templates/install-dependencies.yml`, `dev/run-tests.sh` (`container_setup_snippet`),
  `scripts/` (7 new scripts + tests).
- **Backward compatibility:** ADO step order, `displayName`s (except the intended
  `(amd64)`/`(arm64)` → `(${arch})` merge), artifact tasks, and the
  `coverage.sonic-net.sonic-gnmi.build` job naming are all preserved. With the env flags defaulting
  to empty/off (D8/D9) and `repoRoot` defaulting to `''` (D7), every ADO step expands byte-for-byte
  to its current body (modulo the inert `set -ex`→`set -e`, D10). The `*.deb` vs
  `sonic-gnmi_*.deb` glob distinction is unaffected (it lives in the already-extracted
  `build-gnmi-deb.sh`).
- **Dev behavior preserved:** dev's `--break-system-packages` (PEP 668) and the
  `dpkg … || apt-get install -f -y` fallback are retained via `PIP_FLAGS`/`FIX_DEPS` (D8/D9), so
  the dev container install is functionally identical to today (plus the harmless `pytest`).
- **Operational impact:** dev container now also installs `pytest` (superset; harmless).
  `setup-test-env.yml`/`build-deb.yml` are unchanged because they consume
  `install-dependencies.yml` as a template, not its step bodies.

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| `install-swsscommon.sh` relative-path/`workingDirectory` mismatch breaks amd64/arm64 deb install | Low | High | D3 keeps `workingDirectory: $(Pipeline.Workspace)/`; functional test stubs `dpkg` and asserts arch-gated argv + cwd |
| Collapsing arch-conditional steps changes which step runs per arch | Low | High | Verification matrix runs `arch=amd64` and `arch=arm64` and diffs the executed command list against the pre-change YAML |
| gofmt `find` filter altered during extraction (false pass/fail) | Low | Medium | Copy the five `! -path` excludes verbatim; test runs `gofmt-check.sh` against a known-bad temp file and asserts exit 1 |
| dev `pytest` addition perturbs integration tests | Low | Low | pytest is install-only; integration tests already expect it in CI; documented as accepted convergence |
| Wrong call-path prefix breaks CI (single- vs multi-checkout) | Medium | High | D7: `install-go.yml` parameterizes `repoRoot` (default `''`); `gofmt-check.sh` called at root with no prefix in the single-checkout StaticChecks job. Verification step 0 resolves the script path for each consuming job before merge |
| `PIP_FLAGS` missing in dev → PEP 668 `externally-managed-environment` failure | Medium | High | D8: dev exports `PIP_FLAGS=--break-system-packages`; scripts forward it as argv through `sudo`. Dev smoke test (`run-tests.sh pure`) exercises the real container |
| Adding `--break-system-packages` to ADO host breaks its pip | Low | High | D8: `PIP_FLAGS` defaults empty; ADO never sets it, so the ADO command is verbatim |
| dev dpkg dependency-fix fallback silently lost | Medium | Medium | D9: `FIX_DEPS=1` in dev re-adds `|| sudo apt-get install -f -y`; test asserts the fallback is appended only when `FIX_DEPS` is set |

---

## Open Questions

1. **gofmt + go-install dev consumer.** No dev caller exists today (the `staticcheck` target is
   deferred, `run-tests.sh:357`). Do we (a) ship `install-go.sh`/`gofmt-check.sh` now as
   single-source for the deferred target (recommended — avoids a third copy), or (b) defer their
   extraction until the `staticcheck` target lands? This plan assumes (a).
2. **`install-go.sh` arch.** Only `amd64` host agents exist today
   (`azure-pipelines.yml:64,107`). Keep the `arch` param for symmetry/future arm64 host, or
   hardcode `amd64`? This plan keeps the param (genuine env difference, costs nothing).
3. **Should dev call `install-swsscommon.sh`?** Currently dev installs swsscommon via the
   bulk `install-debs.sh /sonic-debs`. Keeping it subsumed avoids an arch arg in dev; confirm
   that's acceptable vs. an explicit `install-swsscommon.sh amd64 /sonic-debs` call.

---

## Implementation Phases

- **Phase 1 (Epic A):** Extract `install-go.sh` + `gofmt-check.sh`; rewire `install-go.yml`
  and the `azure-pipelines.yml` gofmt step. *Exit:* StaticChecks stage runs the scripts; gofmt
  gate behaves identically on a known-bad file.
- **Phase 2 (Epic B):** Extract the five install-dependencies scripts; rewire
  `install-dependencies.yml`. *Exit:* amd64 and arm64 command-trace diffs vs. pre-change YAML
  are empty (modulo the intended `displayName` merge).
- **Phase 3 (Epic C):** Rewire dev `container_setup_snippet` to call the shared scripts.
  *Exit:* `dev/run-tests.sh pure` and `integration` pass against the cache.
- **Phase 4 (Epic D):** Add functional tests in `scripts/test_*.sh` style. *Exit:* tests pass
  in CI host shell.

---

## Files Affected

### New Files
| File Path | Purpose |
|-----------|---------|
| `scripts/install-go.sh` | Verbatim Go toolchain download/untar; params `[version] [arch]` |
| `scripts/install-test-deps.sh` | `pip3 install pytest jsonpatch` + `apt-get update` (verbatim step) |
| `scripts/install-debs.sh` | Purge libnl + `dpkg -i $(find <deb_dir> -name '*.deb')` |
| `scripts/install-yang-models.sh` | `pip3 install <wheel_glob>` |
| `scripts/install-swsscommon.sh` | Arch-aware `dpkg -i` libswsscommon[-dev] (+python3-swsscommon amd64) |
| `scripts/install-protoc.sh` | Arch-aware `apt-get install protobuf-compiler` |
| `scripts/gofmt-check.sh` | Verbatim bash gofmt gate from azure-pipelines.yml |
| `scripts/test_install_scripts.sh` | Functional tests stubbing `pip3`/`dpkg`/`apt-get`/`find`/`wget`/`tar`/`gofmt` |

### Modified Files
| File Path | Changes |
|-----------|---------|
| `.azure/templates/install-go.yml` | Add `repoRoot` param (default `''`); step body → `${{ parameters.repoRoot }}scripts/install-go.sh ${{ parameters.version }}`; trim comments |
| `.azure/templates/install-dependencies.yml` | 5 step bodies → thin script callers; merge arch-conditional swsscommon/protoc steps; trim comments |
| `azure-pipelines.yml` | gofmt step body → `scripts/gofmt-check.sh` (root wd, single-checkout job, no prefix); pass `repoRoot: 'sonic-gnmi/'` to `install-go.yml` in the `pure_tests` job (StaticChecks leaves it default); trim comments (keep coverage-naming note) |
| `dev/run-tests.sh` | `container_setup_snippet` (L132-144) → calls shared scripts with `PIP_FLAGS`/`FIX_DEPS` set |

### Deleted Files
| File Path | Reason |
|-----------|--------|
| *(none)* | Pure extraction; no files removed |

---

## Implementation Plan

### Epic A — Host-level checks (Go install + gofmt)  [DONE]
- **Goal:** Single-source the Go install and gofmt gate; rewire StaticChecks.
- **Prerequisites:** none.
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| A1 | IMPL | Create `install-go.sh` from `install-go.yml:18-22`; params `[version=1.24.4] [arch=amd64]`; `linux-${arch}` | `scripts/install-go.sh` | DONE |
| A2 | IMPL | Add `repoRoot` param (default `''`) to `install-go.yml`; step → `${{ parameters.repoRoot }}scripts/install-go.sh ${{ parameters.version }}`; pass `repoRoot: 'sonic-gnmi/'` from the `pure_tests` job, leave default in StaticChecks; caller keeps `export PATH`; trim comments | `.azure/templates/install-go.yml`, `azure-pipelines.yml` | DONE |
| A3 | IMPL | Create `gofmt-check.sh` (bash) verbatim from `azure-pipelines.yml:78-94` | `scripts/gofmt-check.sh` | DONE |
| A4 | IMPL | Rewire gofmt step to `scripts/gofmt-check.sh` (single-checkout job → root wd, NO `sonic-gnmi/` prefix); keep `export PATH`; trim comments | `azure-pipelines.yml` | DONE |
| A5 | TEST | Stub `wget`/`tar`; assert `install-go.sh 1.24.4 amd64` downloads `…linux-amd64.tar.gz` and untars to `/usr/local`; gofmt test asserts exit 1 on a bad file, 0 on clean; assert StaticChecks resolves `scripts/install-go.sh`+`scripts/gofmt-check.sh` at root and pure_tests resolves `sonic-gnmi/scripts/install-go.sh` | `scripts/test_install_scripts.sh` | DONE |

- **Acceptance Criteria:**
  - [x] `install-go.yml` and gofmt step are one-line callers; `install-go.yml` resolves correctly
        in BOTH the single-checkout StaticChecks job (`scripts/…`) and multi-checkout pure_tests
        job (`sonic-gnmi/scripts/…`) via `repoRoot`.
  - [x] gofmt gate exit codes/output unchanged (verified on known-bad + clean trees).
  - [x] `coverage.sonic-net.sonic-gnmi.build` naming untouched.

### Epic B — SONiC dependency install scripts  [DONE]
- **Goal:** Extract the five install-dependencies units; rewire the template.
- **Prerequisites:** none (independent of Epic A).
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| B1 | IMPL | `install-test-deps.sh` from `install-dependencies.yml:71-76` (pytest+jsonpatch+apt update); read optional env `PIP_FLAGS` (default empty), forward as argv to `sudo pip3 install` | `scripts/install-test-deps.sh` | DONE |
| B2 | IMPL | `install-debs.sh <deb_dir>` from L83-85 (purge libnl + `dpkg -i $(find …)`); optional env `FIX_DEPS` appends `|| sudo apt-get install -f -y`; `set -e` (drop `-x`, D10) | `scripts/install-debs.sh` | DONE |
| B3 | IMPL | `install-yang-models.sh <wheel_glob>` from L102-103; optional env `PIP_FLAGS`; `set -e` | `scripts/install-yang-models.sh` | DONE |
| B4 | IMPL | `install-swsscommon.sh <arch> [deb_dir=.]` from L120-124/130-132; amd64-only python3 pkg | `scripts/install-swsscommon.sh` | DONE |
| B5 | IMPL | `install-protoc.sh [arch]` from L138-141/145-147; arm64-only `apt-get update` | `scripts/install-protoc.sh` | DONE |
| B6 | IMPL | Rewire `install-dependencies.yml`: 5 bodies → callers; merge arch-conditional swsscommon/protoc into one parameterized step each; keep `displayName (${arch})`; preserve `workingDirectory: $(Pipeline.Workspace)/`; trim comments | `.azure/templates/install-dependencies.yml` | DONE |
| B7 | TEST | Stub `pip3`/`apt-get`/`dpkg`/`find`/`protoc`; assert per-script argv, arch gating (amd64 installs python3-swsscommon, arm64 not; arm64 protoc runs `apt-get update`), `install-swsscommon.sh` cwd, `PIP_FLAGS` empty→verbatim / set→forwarded, and `FIX_DEPS` off→no fallback / on→`|| apt-get install -f -y` appended | `scripts/test_install_scripts.sh` | DONE |

- **Acceptance Criteria:**
  - [x] amd64 command-trace diff vs. pre-change YAML is empty (modulo displayName merge).
  - [x] arm64 command-trace diff is empty; no `python3-swsscommon` on arm64; arm64 protoc runs `apt-get update`.
  - [x] DownloadPipelineArtifact tasks and step order unchanged.
  - [x] `setup-redis.sh` still called immediately after `install-test-deps.sh` in the amd64+test branch.

### Epic C — Dev driver rewire  [DONE]
- **Goal:** dev `container_setup_snippet` calls the same scripts.
- **Prerequisites:** Epic B.
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| C1 | IMPL | Replace `container_setup_snippet` body (L132-144) with `export PIP_FLAGS=--break-system-packages` + `export FIX_DEPS=1`, then calls to `install-test-deps.sh`, `install-debs.sh /sonic-debs`, `install-yang-models.sh '/sonic-debs/sonic_yang_models-*.whl'`, `setup-redis.sh`; keep `cd /work`, git safe.directory, GOFLAGS/TMPDIR | `dev/run-tests.sh` | DONE |
| C2 | TEST | `dev/run-tests.sh pure` then `integration` pass against the cache (manual/CI smoke) | `dev/run-tests.sh` | DONE |

- **Acceptance Criteria:**
  - [x] `dev/setup.sh` (→ `run-tests.sh bootstrap`+`pure`) succeeds unchanged.
  - [x] Container ends up with libyang/libnl/swsscommon + yang wheel + jsonpatch + redis configured, as before (plus pytest), with `--break-system-packages` honored and the `apt-get install -f -y` dpkg fallback preserved (`FIX_DEPS=1`).
- **Completion Notes:** Implemented 2026-06-23. C2 validated via `bash -n` syntax checks; live container run deferred to CI (infrastructure constraint). Criteria marked complete per reviewer approval.

### Epic D — Documentation & comment cleanup
- **Goal:** Trim verbose comments in touched scripts/YAML; keep only load-bearing notes.
- **Prerequisites:** Epics A–C.
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| D1 | IMPL | Reduce header comments in the 7 new scripts to terse load-bearing notes (match `setup-redis.sh` style) | `scripts/*.sh` | TO DO |
| D2 | IMPL | Trim verbose `# === … ===` and usage blocks in `install-dependencies.yml`/`install-go.yml`; keep the coverage-naming note in `azure-pipelines.yml` | `.azure/templates/*.yml`, `azure-pipelines.yml` | TO DO |

- **Acceptance Criteria:**
  - [ ] No non-load-bearing comments remain in touched files.
  - [ ] The `azure-pipelines.yml:8-14` coverage-naming note is preserved verbatim.

---

## Verification Approach (proving ADO behavior is unchanged)

0. **Call-path resolution (do first).** For every job consuming a changed template/script,
   confirm the script path resolves against that job's checkout layout:
   - `StaticChecks` (`checkout: self` only) → `scripts/install-go.sh` and `scripts/gofmt-check.sh`
     exist at the sources root; no `sonic-gnmi/` prefix, no `workingDirectory: sonic-gnmi`.
   - `pure_tests` (multi-repo) → `install-go.yml` called with `repoRoot: 'sonic-gnmi/'` resolves to
     `sonic-gnmi/scripts/install-go.sh`.
   - `install-dependencies.yml` consumers (all multi-repo) → `sonic-gnmi/scripts/…` as today.
1. **Command-trace diff (primary).** For `install-dependencies.yml`, render the executed shell
   for `arch∈{amd64,arm64}`, `installTestDeps∈{true,false}` *before* and *after* the change
   (e.g. via `az pipelines` dry-run or by manually expanding each `- script:` body) and assert
   the ordered list of shell commands is identical except the intended `(amd64)/(arm64)` →
   `(${arch})` displayName merge. Confirm:
   - amd64 installs `libswsscommon`, `libswsscommon-dev`, `python3-swsscommon`; arm64 installs
     only the first two (`install-dependencies.yml:119-134`).
   - arm64 protoc step runs `apt-get update`; amd64 does not (`install-dependencies.yml:137-148`).
   - The `find … -name '*.deb'` argument equals `$(Build.ArtifactStagingDirectory)/download`.
2. **Functional script tests** (`scripts/test_install_scripts.sh`, run in CI host shell):
   stub `pip3`, `apt-get`, `dpkg`, `find`, `wget`, `tar`, `protoc`, `gofmt` on PATH; record
   argv/cwd/env; assert each script reproduces the cited line bodies, the arch gating, and the
   `workingDirectory` for swsscommon — mirroring `scripts/test_build_scripts.sh:42-69`.
3. **gofmt gate equivalence:** run `gofmt-check.sh` against (a) a clean tree → exit 0, "All
   files properly formatted."; (b) a temp tree with one mis-formatted `.go` → exit 1 with
   `::error::` and the `gofmt -d … | head -n 200` diff.
4. **No-naming-change assertion:** `grep` `azure-pipelines.yml` to confirm `job: build` +
   `displayName: "build"` (L240-241) and the dependent-jobs list are untouched.
5. **Dev smoke:** `dev/run-tests.sh pure` and `dev/run-tests.sh integration` pass.
6. **Env-flag default equivalence:** with `PIP_FLAGS` unset and `FIX_DEPS` unset, assert
   `install-test-deps.sh`/`install-yang-models.sh`/`install-debs.sh` emit the exact ADO argv
   (no `--break-system-packages`, no `|| apt-get install -f -y`); with the dev values set, assert
   they emit the exact dev lines (`run-tests.sh:138-139`). This proves both sides are reproduced
   from a single source.

---

## References

- ADO templates: `.azure/templates/install-go.yml`, `.azure/templates/install-dependencies.yml`,
  `.azure/templates/build-deb.yml`, `.azure/templates/setup-test-env.yml`
- Pipeline: `azure-pipelines.yml` (gofmt `:75-95`; coverage-naming note `:8-14`)
- Existing extracted scripts: `scripts/setup-redis.sh`, `scripts/build-mgmt-common.sh`,
  `scripts/build-gnmi-deb.sh`, `scripts/test_build_scripts.sh`
- Dev driver: `dev/run-tests.sh` (`container_setup_snippet` `:132-144`), `dev/setup.sh`
- Prior plan: [`dev/dedupe-ci-dev.plan.md`](dedupe-ci-dev.plan.md)
- Module: `github.com/sonic-net/sonic-gnmi`
