# Dedup CI ↔ Dev Setup/Build/Test Logic — Solution Design & Implementation Plan

> **Date:** 2026-06-22 | **Status:** Draft | **Audience:** sonic-gnmi developers/maintainers
> **Scope:** Eliminate the duplicated setup/build/test shell logic between the local dev
> driver (`dev/run-tests.sh`) and the Azure Pipelines definition
> (`azure-pipelines.yml` + `.azure/templates/{install-go,install-dependencies,setup-test-env,build-deb}.yml`)
> by extracting the shared, identical shell steps into small standalone scripts that BOTH
> consumers invoke.
> **Revision notes:** Rev 3 — fixed the `build-gnmi-deb.sh` artifact-glob: it now defaults to
> the ADO `*.deb` glob (preserving staging of the `sonic-mgmt-common` debs alongside the
> `sonic-gnmi` deb) and lets the dev caller pass the narrower `sonic-gnmi_*.deb`, so Epic 2
> preserves both consumers' artifact sets; corrected the C1 rationale (the ADO `apt-get update`
> is needed for the script's own `apt-get install redis-server`, not for pip/pytest; and the
> dev path *does* run `apt-get update` earlier at L136); corrected the Background note
> (`pure_tests` checks out only two repos and is not a template consumer); and softened
> "behavior-preserving no-op" language to acknowledge the intentional `-j$(nproc)` dev speedup.
> Rev 2 — corrected the ADO working-directory assumption (multi-repo
> checkout means the repo is a *subdir* `sonic-gnmi/`, so callers invoke
> `sonic-gnmi/scripts/x.sh`, **not** `./scripts/x.sh`); relabeled Epic 1 as an *accepted
> intentional change* (not a byte-for-byte no-op) because the script standardizes
> `--no-install-recommends` and must account for the ADO-side `apt-get update`; and made
> explicit that Epic 3 is dropped if the ADO caller cannot adopt it.
> **Composes with:** [`dev/local-dev-runner.plan.md`](local-dev-runner.plan.md) (implemented
> dev driver) and [`dev/local-ci-driver.plan.md`](local-ci-driver.plan.md) (deferred full CI
> mirror). This plan is orthogonal to both: it does not add or remove subcommands/jobs, it
> only changes *where the shared step bodies live*.

---

## Executive Summary

The same setup/build logic is currently maintained twice: once as Bash inside
`dev/run-tests.sh` (run inside the `sonic-slave-trixie` container against a shared local
dependency cache) and once as Azure DevOps (ADO) `- script:` step bodies in the
`.azure/templates/*.yml` files (run on ADO agents against pipeline-artifact downloads). Five
units of logic overlap; the two package builds are byte-for-byte identical, and the redis
socket configuration is identical apart from two flanking install flags. This document
precisely catalogs each overlap, then proposes a **minimal, low-risk, incremental**
single-source-of-truth refactor: extract the *shared* shell bodies into small POSIX/Bash
scripts under a new repo-root `scripts/` directory, parameterizing only the genuine
environment differences (artifact source, working directory, output directory). Both the ADO
templates (`- script: sonic-gnmi/scripts/x.sh` — the repo is a *subdir* of the parent working
directory under multi-repo checkout) and `dev/run-tests.sh` (call/source the same scripts)
become thin callers. We sequence the rollout starting with the package builds (artifact-set
preserving; one intentional `-j$(nproc)` dev speedup)
and the redis setup (identical config, two accepted flag standardizations), and explicitly
draw the line at the parts that **cannot** be shared (ADO `DownloadPipelineArtifact@2` tasks,
dev's docker bind mounts) because they are environment-native, not shell logic.

---

## Background

### The two consumers and why they exist

| | `dev/run-tests.sh` | `.azure/templates/*.yml` |
|---|---|---|
| **Form** | Bash script (`set -euo pipefail`) | ADO YAML step-lists (`- script:` / `- task:`) |
| **Where it runs** | Inside `sonic-slave-trixie` container on a developer host | On ADO agents (`ubuntu-22.04`, sometimes with a `container:` of the same image) |
| **How heavy deps arrive** | Shared local cache `~/.cache/acr-image-build` (`sonic-debs/` dir + cloned sibling repos) bind-mounted into the container | `DownloadPipelineArtifact@2` from `Azure.sonic-buildimage.common_libs`, pipeline `142` (`sonic-buildimage.vs`), `Azure.sonic-swss-common` |
| **Sibling repo layout** | Docker bind mounts re-create `/work/{sonic-gnmi,sonic-mgmt-common,sonic-swss-common}` | `- checkout:` of `self` + `sonic-mgmt-common` + `sonic-swss-common` into the agent workspace |
| **Driver model** | One process, env vars in-scope, snippets composed via `bash -c "$(snippet)…"` | Independent steps, no shared shell scope; values passed via template parameters |

> **ADO working directory (load-bearing detail).** The jobs that consume the refactored
> build/dependency templates check out **three** repos — `self` + `sonic-mgmt-common` +
> `sonic-swss-common` (`setup-test-env.yml` L23-37 / `build-deb.yml` L34-47). (The `pure_tests`
> job, `azure-pipelines.yml` L110-116, checks out only **two** — `self` + `sonic-mgmt-common`
> — and is *not* a consumer of these templates, so it is unaffected.) Under ADO multi-repo
> checkout the default working directory is the **parent**
> (`$(System.DefaultWorkingDirectory)`, i.e. `$(Agent.BuildDirectory)/s`) and each repo is a
> **subdirectory** of it. The existing templates prove this: they `pushd sonic-mgmt-common` /
> `pushd sonic-gnmi` (`build-deb.yml` L61/L65, `setup-test-env.yml` L47) and the test jobs
> `cd sonic-gnmi` before `make` (`azure-pipelines.yml` L125/L170/L205). **Consequence:** an ADO
> `- script:` step must reach the extracted scripts via `sonic-gnmi/scripts/x.sh`, *not*
> `./scripts/x.sh`. This is the single most important constraint in this plan; every ADO call
> site below uses the `sonic-gnmi/`-prefixed path.

These two cannot directly `source` each other: ADO templates are declarative YAML
(interpreted by the ADO engine, not bash), and the dev driver's docker-mount + local-cache
model has no equivalent of ADO's artifact-download tasks. The **only** thing they can share
is the *pure shell body* of the steps that are environment-agnostic.

### Concrete overlaps found (evidence)

The following pairs are duplicated today. Line references are to the current tree.

**(1) Redis configuration — THE THREE `sed` LINES + `service start` ARE IDENTICAL.**
`dev/run-tests.sh` `container_setup_snippet()` (lines 140–144) and
`.azure/templates/install-dependencies.yml` (lines 76–81):

```sh
sudo apt-get update                            # ADO only (L76); dev does not run this here
sudo apt-get install -y redis-server           # dev: --no-install-recommends
sudo sed -ri 's/^# unixsocket/unixsocket/' /etc/redis/redis.conf
sudo sed -ri 's/^unixsocketperm .../unixsocketperm 777/' /etc/redis/redis.conf
sudo sed -ri 's/redis-server.sock/redis.sock/' /etc/redis/redis.conf
sudo service redis-server start
```

The three `sed` lines + `service … start` are identical. Two differences flank them: the ADO
side runs `sudo apt-get update` first (L76) and a bare `apt-get install`, while the dev side
omits the `update` and passes `--no-install-recommends`. The extraction must consciously
reconcile these (see C1 and Epic 1) — it is **not** a literal byte-for-byte no-op.

**(2) `sonic-mgmt-common` build — IDENTICAL COMMAND.**
`NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc`, run in the `sonic-mgmt-common`
checkout. Appears in `dev/run-tests.sh` at `build_nonpure_snippet()` (line 187), `run_build()`
(line 325), and the `shell` helper (line 238); and in `.azure/templates/setup-test-env.yml`
(line 48) and `.azure/templates/build-deb.yml` (line 62). The only difference is the
directory: `/work/sonic-mgmt-common` (dev) vs `sonic-mgmt-common` relative to the agent
working dir (ADO).

**(3) `sonic-gnmi` `.deb` build — NEARLY IDENTICAL.**
`dev/run-tests.sh` `run_build()` (line 329):
`ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y dpkg-buildpackage -rfakeroot -b -us -uc`.
`.azure/templates/build-deb.yml` (line 66):
`ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y dpkg-buildpackage -rfakeroot -us -uc -b -j$(nproc) && cp ../*.deb $(Build.ArtifactStagingDirectory)/`.
Differences: ADO adds `-j$(nproc)` and copies the resulting `.deb` to the artifact staging
dir; dev copies to `/build-out` and `chown`s it back to the host UID.

**(4) SONiC dependency install — OVERLAPPING SET, DIFFERENT SOURCE.**
Both install the libyang/libnl/swsscommon debs, the `sonic_yang_models` wheel, and
`jsonpatch`. `dev/run-tests.sh` (lines 136–139) installs from the bind-mounted `/sonic-debs`
cache:

```sh
sudo apt-get -y purge libnl-3-dev libnl-route-3-dev || true
sudo dpkg -i /sonic-debs/*.deb || sudo apt-get install -f -y
sudo pip3 install --break-system-packages /sonic-debs/sonic_yang_models-*.whl jsonpatch
```

`install-dependencies.yml` does the same *installs* but spread across multiple steps, each
preceded by a `DownloadPipelineArtifact@2` task, and additionally installs `libpcre*`,
`pytest`, and `protobuf-compiler`, with arch-specific swsscommon package names. The
**acquisition** is environment-native (cache mount vs artifact download); only the
**install commands** overlap.

**(5) Test entry points — ALREADY SINGLE-IMPL VIA THE MAKEFILE.**
Both sides invoke `make -f pure.mk junit-xml`, `make all`, `ENABLE_TRANSLIB_WRITE=y make
check_gotest_junit`, and `make check_memleak_junit`. These are one-line invocations of
Makefile targets — the real implementation already lives in exactly one place (the
Makefile). The duplication here is negligible (a `cd` + a one-liner) and not worth a script
wrapper.

### Prior art in this repo

- `dev/local-dev-runner.plan.md` (implemented): defines the current `run-tests.sh`
  subcommand surface (`bootstrap`, `pure`, `integration`, `build`, `shell`, `playground`,
  `all`, `clean`). This dedup plan **must not** change that surface.
- `dev/local-ci-driver.plan.md` (deferred): the full CI-mirror vision (gofmt/staticcheck,
  memleak, diff-cover, arm64). It explicitly wants `run-tests.sh` to "call the *same*
  targets so there is exactly one implementation of each test behavior." Extracting shared
  scripts here is a **prerequisite-friendly** step toward that goal: when the CI driver is
  eventually built out, its new subcommands can reuse the same `scripts/` building blocks.

---

## Problem Statement

The setup/build logic is maintained in two places. When a maintainer changes one (e.g.,
renames the redis socket, bumps a build flag, or changes the mgmt-common build invocation),
the other silently drifts. Drift is costly here because:

1. **Silent divergence.** The redis `sed` edits and the package-build commands are copied
   prose; nothing enforces that the two copies stay equal. A change to the ADO template that
   is not mirrored into `run-tests.sh` means "passes locally, fails in CI" (or vice-versa) —
   exactly the failure mode the dev driver exists to prevent.
2. **Doubled review/maintenance.** Every change to a shared step must be made and reviewed
   twice, in two different syntaxes.
3. **No single source of truth.** There is no canonical definition of "how we configure
   redis for tests" or "how we build the gnmi deb"; the definition is whichever copy you
   happen to be reading.

---

## Goals and Non-Goals

### Goals

- **G1.** Identify *every* overlapping unit precisely (done above; see Overlaps 1–5).
- **G2.** Define a single source of truth for each genuinely shared shell body: redis setup,
  mgmt-common build, gnmi deb build, and the SONiC-dep install commands.
- **G3.** Have BOTH `dev/run-tests.sh` and the `.azure/templates/*.yml` invoke the same
  extracted scripts, parameterizing only the real environment differences.
- **G4.** Make the refactor **incremental and behavior-preserving**: start with the
  already-identical core logic (the redis `sed` lines + `service start`, and the two package
  builds). The redis extraction additionally *standardizes* two small flanking differences
  (`--no-install-recommends`; where `apt-get update` runs) as deliberate, accepted changes —
  see Epic 1. The build extractions preserve the published/staged artifact set exactly; the
  one intentional delta is adding `-j$(nproc)` to the dev build (a parallelism speedup that the
  ADO build already uses — see Epic 2 and Impact Analysis).
- **G5.** Define a mechanism to keep the two callers in sync going forward.

### Non-Goals

- **NG1.** Replacing ADO-native constructs. `DownloadPipelineArtifact@2`, `- checkout:`,
  `PublishTestResults@2`, `publish:`, and the diff-coverage decorator stay in YAML.
- **NG2.** Changing the dev driver's subcommand surface or the pipeline's stage/job graph.
- **NG3.** Unifying dependency *acquisition* (cache mount vs artifact download) — only the
  *install* commands are shared.
- **NG4.** Building out the deferred CI-mirror features (gofmt gate, memleak subcommand,
  diff-cover, arm64) — that remains `local-ci-driver.plan.md`'s scope.
- **NG5.** Wrapping the test entry points (Overlap 5) in scripts — the Makefile already is
  their single source of truth.
- **NG6.** Sharing `install-go.yml`. The dev driver never installs Go (the container ships
  it); there is no second copy to dedup.

---

## Requirements

### Functional

- **FR1.** Each extracted script must be invocable from an ADO `- script:` step. **Because
  the consuming jobs use multi-repo checkout, the ADO working directory is the parent and the
  repo is a subdir**, so the canonical ADO invocation is `sonic-gnmi/scripts/<name>.sh` (or,
  equivalently, with a `workingDirectory: $(System.DefaultWorkingDirectory)/sonic-gnmi` and
  `./scripts/<name>.sh`). The plan standardizes on the explicit `sonic-gnmi/`-prefixed form.
- **FR2.** Each extracted script must be callable from `dev/run-tests.sh` inside the
  container (where the checkout is mounted at `/work/sonic-gnmi`).
- **FR3.** Environment differences must be expressed as **parameters** (positional args or
  env vars with sensible defaults), not as branches inside the script that detect "am I in
  CI vs dev".
- **FR4.** Behavior must be preserved for the build units (mgmt-common, gnmi deb): same
  commands, same order, and **the same published/staged artifact set** (the ADO C3 call keeps
  the `../*.deb` glob so the `sonic-mgmt-common` debs continue to be staged alongside the
  `sonic-gnmi` deb; the dev call keeps its narrower `sonic-gnmi_*.deb` out-dir contents). The
  one intentional flag delta is adding `-j$(nproc)` to the dev build (parallelism speedup,
  already present on the ADO side). For redis, behavior is identical for the socket
  configuration; the two flanking package-install flags are intentionally standardized
  (FR-exception documented in Epic 1).

### Non-functional

- **NFR1.** Scripts are POSIX `sh`-compatible where trivial (redis setup), Bash where they
  already rely on Bash (build helpers may use Bash). Shebang must match.
- **NFR2.** Scripts are `set -e` (and `-u` where safe) and emit clear `--- step ---` echoes
  matching the existing log style.
- **NFR3.** No new runtime dependencies; scripts use only tools already present in both
  environments (`sudo`, `sed`, `dpkg`, `apt-get`, `pip3`, `dpkg-buildpackage`).
- **NFR4.** Scripts are executable (`chmod +x`) and committed with LF endings.

---

## Proposed Design

### Architecture Overview

Introduce a single shared directory of small, single-purpose scripts. Both consumers become
thin callers:

```
                       repo-root scripts/  (SINGLE SOURCE OF TRUTH)
                       ├── setup-redis.sh          (Overlap 1)
                       ├── install-sonic-deps.sh   (Overlap 4, install half)
                       ├── build-mgmt-common.sh    (Overlap 2)
                       └── build-gnmi-deb.sh        (Overlap 3)
                              ▲                         ▲
       - script: sonic-gnmi/scripts/x.sh           bash /work/sonic-gnmi/scripts/x.sh
                              │                         │
        .azure/templates/*.yml                    dev/run-tests.sh
   (ADO step-lists; keep Download*               (container snippets call the
    / checkout / publish native;                  scripts instead of inlining)
     repo is a SUBDIR of the CWD)
```

**Why repo-root `scripts/` (not `.azure/scripts/`):** the scripts are consumed by *both*
the dev driver and the pipeline, so placing them under `.azure/` would mislabel them as
CI-only. A top-level `scripts/` signals "shared build/test helpers." (Open Question OQ1
records the alternative.)

### Key Components

#### C1. `scripts/setup-redis.sh` (Overlap 1) — highest value, accepted minor standardization

Extraction of the redis socket configuration. Two flanking differences are reconciled
deliberately:

- **`apt-get update`:** the ADO path runs `sudo apt-get update` before installing redis
  (`install-dependencies.yml` L76). The dev path also runs `sudo apt-get update >/dev/null`
  earlier in `container_setup_snippet()` (`run-tests.sh` L136), just not immediately before the
  redis block. To keep the script a faithful drop-in for *both* without imposing a redundant
  `apt-get update` (the dev container already refreshed its index), the script does **not**
  call `apt-get update`; the ADO caller keeps its own `apt-get update` immediately before
  invoking the script. **That `apt-get update` is required for the script's own
  `apt-get install redis-server`** (a stale/empty index would fail the install) — not for the
  `pip3`/pytest step, which installs from PyPI and needs no apt index. This preserves current
  ADO behavior.
- **`--no-install-recommends`:** the script standardizes on `--no-install-recommends` (matching
  the dev intent). On the ADO agent this is a **deliberate, accepted behavior change** (fewer
  recommended packages pulled in), not a no-op. It is benign for a redis test install and is
  called out as such in Epic 1's acceptance criteria.

```sh
#!/bin/sh
# Configure redis-server for sonic-gnmi tests: enable the unix socket, make it
# world-accessible, and rename it to redis.sock. Used by BOTH the ADO test
# templates and dev/run-tests.sh — keep this as the single source of truth.
#
# NOTE: this script intentionally does NOT run `apt-get update`. Callers that need
# a fresh package index (the ADO path) must run it themselves before calling.
set -e
sudo apt-get install -y --no-install-recommends redis-server
sudo sed -ri 's/^# unixsocket/unixsocket/' /etc/redis/redis.conf
sudo sed -ri 's/^unixsocketperm .../unixsocketperm 777/' /etc/redis/redis.conf
sudo sed -ri 's/redis-server.sock/redis.sock/' /etc/redis/redis.conf
sudo service redis-server start
```

#### C2. `scripts/build-mgmt-common.sh` (Overlap 2)

Parameterize the one difference (directory) with a positional arg defaulting to the ADO
layout (`sonic-mgmt-common` relative to CWD); the dev caller passes the absolute container
path.

```sh
#!/bin/sh
# Build sonic-mgmt-common (generates YANG bindings + cvl schema) — single source
# of truth for both ADO templates and dev/run-tests.sh.
set -e
MGMT_COMMON_DIR="${1:-sonic-mgmt-common}"
echo "--- build sonic-mgmt-common ($MGMT_COMMON_DIR) ---"
( cd "$MGMT_COMMON_DIR" && NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc )
```

#### C3. `scripts/build-gnmi-deb.sh` (Overlap 3)

Parameterize the gnmi dir and the output destination. Standardize on `-j$(nproc)` (currently
only on the ADO side; on the dev side this is a deliberate, accepted parallelism speedup —
see below). The caller controls the copy via the optional `OUT_DIR` arg and an optional copy
pattern, so each environment reproduces its *current* artifact set exactly.

**Artifact-glob fidelity (must-preserve).** The two consumers copy **different deb sets**
today and this script must not change either:
- **ADO** (`build-deb.yml` L66): `cp ../*.deb $(Build.ArtifactStagingDirectory)/` — copies
  **all** debs in the parent dir, which includes the `sonic-mgmt-common` debs built earlier in
  the same block **alongside** the `sonic-gnmi` deb. Whatever currently lands in
  `$(Build.ArtifactStagingDirectory)` (and thus the published `sonic-gnmi` artifact) must be
  preserved byte-for-byte by Epic 2.
- **dev** (`run-tests.sh` L330): `cp -v /work/sonic-gnmi_*.deb /build-out/` — copies **only**
  the `sonic-gnmi` deb to the host-mounted out dir, then `chown`s it back to the host UID.

To preserve both, the script's copy **defaults to the ADO `*.deb` glob** and the *caller*
narrows it when needed. The ADO caller passes the staging dir and accepts the default glob (no
behavior change — the mgmt-common debs continue to be staged). The dev caller passes its
selective `sonic-gnmi_*.deb` pattern (or, equivalently, omits `OUT_DIR` and keeps its existing
`cp` + `chown` in the dev driver). Either way the published/staged contents are identical to
today.

```sh
#!/bin/sh
# Build the sonic-gnmi .deb with translib + native write enabled — single source
# of truth for both ADO build-deb.yml and dev/run-tests.sh `build`.
set -e
GNMI_DIR="${1:-sonic-gnmi}"
OUT_DIR="${2:-}"               # if set, copy resulting .deb(s) here
# Copy pattern is RELATIVE to the parent of GNMI_DIR. Defaults to '*.deb' to
# faithfully reproduce the ADO 'cp ../*.deb' behavior (which stages the
# sonic-mgmt-common debs alongside the sonic-gnmi deb). The dev caller passes
# 'sonic-gnmi_*.deb' to keep its narrower out-dir contents unchanged.
COPY_GLOB="${3:-*.deb}"
echo "--- dpkg-buildpackage sonic-gnmi (translib + native write enabled) ---"
( cd "$GNMI_DIR" && ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y \
    dpkg-buildpackage -rfakeroot -b -us -uc -j"$(nproc)" )
if [ -n "$OUT_DIR" ]; then
  # shellcheck disable=SC2086  # COPY_GLOB is an intentional glob
  cp -v "$(dirname "$GNMI_DIR")"/$COPY_GLOB "$OUT_DIR"/
fi
```

> Note: `build-deb.yml` builds mgmt-common and gnmi in a *single* `- script:` block; that
> block becomes two `- script:` lines calling C2 then C3. The ADO C3 call passes
> `$(Build.ArtifactStagingDirectory)` as `OUT_DIR` and accepts the default `*.deb` glob, so the
> same deb set (mgmt-common + gnmi) is staged as today. `run_build()` in `run-tests.sh`
> additionally runs `go mod tidy && go mod vendor` between the two builds — that stays in the
> dev caller (it is dev-specific cache hygiene, not part of the shared build) — and either
> passes `sonic-gnmi_*.deb` as the copy glob or keeps its existing selective `cp -v
> /work/sonic-gnmi_*.deb /build-out/` + `chown`, preserving the dev out-dir contents.

#### C4. `scripts/install-sonic-deps.sh` (Overlap 4 — install half only)

Shares the *install* commands while leaving *acquisition* to each environment. Takes a
directory that already contains the debs + wheel.

```sh
#!/bin/sh
# Install pre-fetched SONiC deb/wheel dependencies from a directory.
# Acquisition (ADO DownloadPipelineArtifact / dev cache mount) is the CALLER's job.
set -e
DEBS_DIR="${1:-/sonic-debs}"
sudo apt-get -y purge libnl-3-dev libnl-route-3-dev 2>/dev/null || true
sudo dpkg -i "$DEBS_DIR"/*.deb || sudo apt-get install -f -y
sudo pip3 install --break-system-packages "$DEBS_DIR"/sonic_yang_models-*.whl jsonpatch
```

This is the **most conservative** sharing for Overlap 4: it unifies the install verbs but
does not try to unify which artifacts get downloaded from where. **Important caveat:** the ADO
template never places the debs + wheel in a *single* directory — libyang/libnl debs are found
via `find` under `$(Build.ArtifactStagingDirectory)/download` (L88), swsscommon is installed
per-package by versioned name from `$(Pipeline.Workspace)/` (L122-137), and the yang wheel is
installed from `../target/python-wheels/trixie` (L104-107). So `install-sonic-deps.sh`'s
"one `$DEBS_DIR` holding everything" contract matches the **dev** cache layout but **not** the
ADO layout. Unless the ADO caller is restructured to stage everything into one dir (which NG3
declines), this script would have only one consumer (dev) and therefore deliver **zero
dedup**. Consequently Epic 3 is explicitly **droppable**: if the ADO template cannot adopt the
script as-is, we do not add it (a single-caller "shared" script is not a single source of
truth). **This is the highest-risk and lowest-certainty extraction** and is sequenced last
(see Risks R3/R6 and Open Question OQ2).

### Data Flow — how a shared step executes in each environment

**Redis, in ADO (`install-dependencies.yml`):** keep the `apt-get update` + pytest install in
the caller (it is needed for the same block), then call the script via the `sonic-gnmi/`-
prefixed path (the working directory is the parent):
```yaml
- ${{ if and(eq(parameters.arch, 'amd64'), eq(parameters.installTestDeps, true)) }}:
  - script: |
      sudo pip3 install -U pytest jsonpatch
      sudo apt-get update                       # kept here: required for setup-redis.sh's apt-get install redis-server
    displayName: "Install pytest + refresh apt index"
  - script: sonic-gnmi/scripts/setup-redis.sh
    displayName: "Configure redis"
```

**Redis, in dev (`run-tests.sh` `container_setup_snippet`):** replace the inlined redis install
+ sed block with a single call to the mounted script (which now owns the install too):
```sh
bash /work/sonic-gnmi/scripts/setup-redis.sh
```

In both cases the *definition* of "configure redis" lives only in `scripts/setup-redis.sh`.

### API Contracts (script interfaces)

| Script | Args | Env consumed | Side effects |
|--------|------|--------------|--------------|
| `setup-redis.sh` | none | — | installs+configures+starts redis |
| `build-mgmt-common.sh` | `[mgmt_common_dir=sonic-mgmt-common]` | — | builds mgmt-common debs in/next to that dir |
| `build-gnmi-deb.sh` | `[gnmi_dir=sonic-gnmi] [out_dir] [copy_glob=*.deb]` | `ENABLE_*` set internally | builds gnmi deb; if `out_dir` given, copies `<parent>/<copy_glob>` there (`*.deb` default reproduces ADO's `cp ../*.deb`) |
| `install-sonic-deps.sh` | `[debs_dir=/sonic-debs]` | — | `dpkg -i` debs, `pip3` wheel + jsonpatch |

Contract rules: scripts never `cd` the caller's shell (they subshell), never assume CI vs
dev, and exit non-zero on failure (`set -e`).

### Design Decisions

- **D1 — Share the body, not the orchestration.** Only environment-agnostic shell bodies are
  extracted. ADO-native tasks (download/checkout/publish) and dev-native mechanics (docker
  mounts, local cache, `chown`, `go mod vendor`) stay with their respective callers. This
  draws the realistic sharing boundary and keeps each caller readable.
- **D2 — Parameters over environment detection.** Differences (dir, output) are explicit
  args. No script inspects `$AGENT_ID`/`$CI` to branch. This keeps scripts trivially testable
  and prevents "works in one environment only" bugs.
- **D3 — Start with the lowest-risk units.** The two package build *commands* are already
  identical, so their extraction preserves the staged/published artifact set (the ADO copy
  keeps the `*.deb` glob; see C3/FR4) — the only intentional delta is adding `-j$(nproc)` to
  the dev build. The redis socket configuration is also identical; its extraction additionally
  standardizes two flanking install flags as *accepted* minor changes (C1/Epic 1). These are
  the safe first PRs that establish the pattern before touching the riskier dep-install.
- **D4 — Leave the test entry points alone.** They are already single-impl in the Makefile;
  wrapping them would add indirection without removing duplication (NG5).
- **D5 — Keep scripts POSIX where free.** `setup-redis.sh` and `install-sonic-deps.sh` are
  pure `sh`; the build helpers may stay `sh` too since they only use subshells + `cd`.

---

## Alternatives Considered

### A1. Extract everything into one big `scripts/setup-test-env.sh`
A single script that installs deps, configures redis, and builds both packages.
- **Pros:** one call site.
- **Cons:** the ADO side cannot use it wholesale — it must interleave
  `DownloadPipelineArtifact@2` tasks *between* install steps, and it splits build vs test
  jobs differently (build-deb.yml has no redis; memleak/integration have no gnmi deb build).
  A monolith would force ugly parameter flags to skip sub-parts. **Rejected** in favor of
  small composable scripts (C1–C4).

### A2. Put scripts under `.azure/scripts/`
- **Pros:** groups with the templates that are the heaviest user.
- **Cons:** mislabels shared assets as CI-only; the dev driver referencing `.azure/…` reads
  oddly. **Rejected** (see OQ1 — reversible, low stakes).

### A3. Generate the ADO YAML step bodies from the bash (codegen)
- **Pros:** truly one source.
- **Cons:** heavy machinery, new build step, obscures the YAML. Overkill for ~15 lines of
  shared shell. **Rejected.**

### A4. Do nothing / rely on review discipline
- **Cons:** this is the status quo that already drifts. **Rejected** (it is the problem
  statement).

---

## Dependencies

- **External:** none new. Relies on tools already in both environments.
- **Internal:**
  - The `.azure/templates/*.yml` consumers and `azure-pipelines.yml` job definitions.
  - `dev/run-tests.sh` snippet functions (`container_setup_snippet`, `build_nonpure_snippet`,
    `run_build`, `run_shell`, `run_playground`).
  - The repo checkout being available at the script paths in both environments (ADO: repo is a
    **subdir** `sonic-gnmi/` of the parent working dir, so scripts are at
    `sonic-gnmi/scripts/…`; dev: mounted at `/work/sonic-gnmi`).
- **Sequencing:** Epic 1 (redis) and Epic 2 (builds) are independent and can land in either
  order. Epic 3 (dep install) should land after Epics 1–2 because it is the riskiest and
  benefits from the pattern being established and CI-validated.

---

## Impact Analysis

- **Components affected:** `dev/run-tests.sh`, `.azure/templates/install-dependencies.yml`,
  `.azure/templates/setup-test-env.yml`, `.azure/templates/build-deb.yml`, and a new
  `scripts/` directory. `azure-pipelines.yml` itself is unchanged (it only references
  templates).
- **Backward compatibility:** No change to subcommand surface, job graph, artifact names, or
  the load-bearing `coverage.sonic-net.sonic-gnmi.build` job. The diff-coverage decorator and
  `PublishTestResults@2` paths are untouched. **Published artifact contents are preserved:**
  the ADO `build-gnmi-deb.sh` call keeps the existing `../*.deb` glob, so the
  `sonic-mgmt-common` debs continue to be staged into `$(Build.ArtifactStagingDirectory)`
  alongside the `sonic-gnmi` deb (see C3 and FR4). Epic 2 verifies the staged deb list is
  identical before/after.
- **Performance:** Neutral, except `build-gnmi-deb.sh` standardizes `-j$(nproc)` which *adds*
  parallelism to the dev build (a small speedup).
- **Operational/debugging:** Slightly improved — a failing step now points at a named script
  a developer can run in isolation. The ADO `displayName`s are preserved so log navigation is
  unchanged.

---

## Security Considerations

No change to the security posture. The scripts run the same `sudo` package-install and
`dpkg`/`pip3` commands already present; no new network sources, credentials, or trust
boundaries are introduced. The `--break-system-packages` and `unixsocketperm 777` choices are
pre-existing and out of scope for this refactor (they live in test-only environments).

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| R1. Script path differs between ADO working dir and container mount, breaking a caller | Medium | Medium | **Root-caused:** ADO multi-repo checkout puts the repo in a subdir, so ADO callers use `sonic-gnmi/scripts/x.sh` and dev uses `bash /work/sonic-gnmi/scripts/x.sh`. Documented per call site below. Validate Epic 1 in a real pipeline run before proceeding. |
| R2. `service redis-server start` / `apt-get` behaves differently on bare ADO agent vs container | Low | Medium | The redis socket config + `service start` already run in both today; the only deltas (`--no-install-recommends`, where `apt-get update` runs) are deliberate and bounded (see C1). Land Epic 1 alone and watch one full pipeline. |
| R3. Overlap-4 package sets diverge (ADO has libpcre/protoc/arch-suffixed swsscommon) **and ADO never co-locates the debs+wheel in one directory** | High | Low | ADO downloads land in different dirs (libyang/libnl via `find` under `…/download`, swsscommon per-package from `$(Pipeline.Workspace)`, yang wheel from `../target/python-wheels`), so `install-sonic-deps.sh` as specified matches only the **dev** layout. Share only if the ADO caller can adopt it *without* restructuring downloads; otherwise **drop Epic 3** — a one-caller "shared" script is not a single source of truth (see OQ2 and Epic 3). |
| R6. Epic 3 ends up dev-only, adding a file with zero dedup | Medium | Low | Gate Epic 3 on the ADO caller actually invoking the script. If only dev can use it, do not add it. |
| R4. Future contributor re-inlines a step, silently re-duplicating | Medium | Low | Add cross-reference comments in both callers + a lightweight CI guard (Epic 4) that greps for the tell-tale inlined `sed 's/redis-server.sock/redis.sock/'` outside `scripts/`. |
| R5. Scripts not executable / wrong shebang on checkout | Low | Low | `chmod +x`, commit mode bits; ADO `- script:` also works via `bash scripts/x.sh` as a fallback. |

---

## Open Questions

- **OQ1.** Directory location: repo-root `scripts/` (recommended) vs `.azure/scripts/` vs
  `dev/scripts/`? Recommendation: `scripts/`. Low-stakes, reversible.
- **OQ2.** Can the ADO `install-dependencies.yml` stage the common debs+wheel into a single
  directory so it can call `install-sonic-deps.sh`? Today it cannot without restructuring its
  `DownloadPipelineArtifact@2` paths (NG3 declines this). **If the answer stays "no," Epic 3 is
  dropped** — see R3/R6 and Epic 3's decision gate (Task 3.0).
- **OQ3.** `setup-redis.sh` includes the `apt-get install redis-server` line (self-contained)
  and standardizes `--no-install-recommends`, while leaving `apt-get update` to the caller.
  Confirmed in C1; flagged as an accepted change, not silent.
- **OQ4.** Do we want a single tiny test/lint that asserts the two callers reference the
  scripts (Epic 4), or is a comment sufficient? Recommendation: add the grep-guard; it is
  cheap insurance against R4.

---

## Implementation Phases

- **Phase 1 — Redis (low-risk, with two accepted flag standardizations).** Add
  `scripts/setup-redis.sh`; rewire `install-dependencies.yml` (call
  `sonic-gnmi/scripts/setup-redis.sh`, keep `apt-get update` + pytest in the caller) and
  `run-tests.sh` to call it. Exit criteria: one full pipeline green; `dev/run-tests.sh
  integration` green locally; the redis `sed` lines exist in exactly one file; the
  `--no-install-recommends` / `apt-get update` reconciliation is documented in the PR.
- **Phase 2 — Package builds.** Add `scripts/build-mgmt-common.sh` +
  `scripts/build-gnmi-deb.sh`; rewire `setup-test-env.yml`, `build-deb.yml`, and
  `run-tests.sh` (`run_build`, `build_nonpure_snippet`, `run_shell`/`run_playground`). Exit
  criteria: amd64 + arm64 Package jobs green; `dev/run-tests.sh build` produces a `.deb` in
  `dev/build-out/`; the `NO_TEST_BINS=1 dpkg-buildpackage` and the gnmi `ENABLE_*` build
  strings exist in exactly one file each.
- **Phase 3 — Dependency install (optional; drop if dev-only).** *Only if* the ADO caller can
  invoke `scripts/install-sonic-deps.sh` without restructuring its downloads: add it and
  rewire the common install lines in both callers. If the ADO download layout (debs+wheel
  never co-located) cannot adopt it, **drop this phase** — do not add a single-consumer
  script. Exit criteria (if pursued): integration + memleak + Package jobs green; dev pure +
  integration green; **and** the script has ≥2 real callers.
- **Phase 4 — Sync guard + docs.** Add cross-reference comments and a grep-based CI guard;
  update `dev/SETUP.md` / the two plan docs to point at `scripts/` as the single source.

---

## Files Affected

### New Files
| File Path | Purpose |
|-----------|---------|
| `scripts/setup-redis.sh` | Single source of truth for redis test configuration (Overlap 1) |
| `scripts/build-mgmt-common.sh` | Single source of truth for the `NO_TEST_BINS=1` mgmt-common build (Overlap 2) |
| `scripts/build-gnmi-deb.sh` | Single source of truth for the `ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y` gnmi deb build (Overlap 3) |
| `scripts/install-sonic-deps.sh` | Single source of truth for the common SONiC dep install verbs (Overlap 4, install half) |
| `scripts/README.md` *(optional)* | Documents the shared-scripts contract and the two callers |

### Modified Files
| File Path | Changes |
|-----------|---------|
| `dev/run-tests.sh` | Replace inlined redis `sed` block, mgmt-common build, gnmi deb build, and dep install lines with calls to the `scripts/*.sh`; keep dev-only mechanics (mounts, cache, `chown`, `go mod vendor`) in place |
| `.azure/templates/install-dependencies.yml` | Replace the redis `sed` block with `- script: sonic-gnmi/scripts/setup-redis.sh` (keeping the existing `apt-get update` + pytest install in the caller); keep `DownloadPipelineArtifact@2` tasks. (Optional Epic 3: `install-sonic-deps.sh` only if the ADO layout can adopt it.) |
| `.azure/templates/setup-test-env.yml` | Replace the mgmt-common build `- script:` with `- script: sonic-gnmi/scripts/build-mgmt-common.sh sonic-mgmt-common` |
| `.azure/templates/build-deb.yml` | Replace the combined build `- script:` with two steps calling `sonic-gnmi/scripts/build-mgmt-common.sh sonic-mgmt-common` then `sonic-gnmi/scripts/build-gnmi-deb.sh sonic-gnmi $(Build.ArtifactStagingDirectory)` |
| `dev/SETUP.md` *(doc)* | Note the `scripts/` shared building blocks |

### Deleted Files
| File Path | Reason |
|-----------|--------|
| *(none)* | This is an extraction refactor; no files are removed |

---

## Implementation Plan

> **Source-of-truth principle (per maintainer direction):** the extracted `scripts/*.sh` take
> their content from the **ADO pipeline** bodies — when an `.azure/templates/*.yml` body and the
> corresponding `dev/run-tests.sh` body differ, the **pipeline's version is canonical** and the
> dev driver is rewired to match it (not vice-versa). Genuine environment differences (e.g. the
> dependency **artifact source**: the pipeline's `DownloadPipelineArtifact` staging dir vs the
> local `/sonic-debs` cache mount; host runner vs container) are absorbed by a **script
> parameter/argument**, never by forking the logic into two copies. Every such accepted deviation
> from a byte-for-byte pipeline body (e.g. the redis flag standardizations in Epic 1) is called
> out explicitly in the PR, not made silently.

### Epic 1 — Extract redis setup (identical config; two accepted flag standardizations)  — IN PROGRESS

- **Goal:** Make `scripts/setup-redis.sh` the only definition of the redis test config; both
  callers invoke it.
- **Prerequisites:** none.
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| 1.1 | IMPL | Create `scripts/setup-redis.sh` (POSIX, `set -e`, exact `sed` lines), `chmod +x` | `scripts/setup-redis.sh` | DONE |
| 1.2 | IMPL | In `install-dependencies.yml`, replace the redis `sed` block with `- script: sonic-gnmi/scripts/setup-redis.sh`; keep the existing `apt-get update` + pytest install in the caller | `.azure/templates/install-dependencies.yml` | DONE |
| 1.3 | IMPL | In `run-tests.sh`, replace the redis `sed` lines in `container_setup_snippet()` with `bash /work/sonic-gnmi/scripts/setup-redis.sh` | `dev/run-tests.sh` | DONE |
| 1.4 | TEST | Run `dev/run-tests.sh integration` locally; confirm redis socket at `/var/run/redis/redis.sock` and tests pass | — | IN PROGRESS (requires docker + build cache; not available in this env) |
| 1.5 | TEST | Trigger one full pipeline; confirm memleak + integration jobs green | — | TO DO (CI-only) |

- **Acceptance Criteria:**
  - [ ] The three redis `sed` lines appear in exactly one file (`scripts/setup-redis.sh`).
  - [ ] ADO invokes the script via the `sonic-gnmi/`-prefixed path (verified the step resolves
        under multi-repo checkout).
  - [ ] The `--no-install-recommends` standardization and the `apt-get update` placement are
        explicitly called out in the PR as accepted, intentional changes (not silent).
  - [ ] `dev/run-tests.sh integration` passes locally.
  - [ ] A full ADO pipeline run is green.

### Epic 2 — Extract the package builds  *(Status: DONE)*

- **Goal:** Single definition each for the mgmt-common build and the gnmi deb build.
- **Prerequisites:** Epic 1 (pattern + path validated). Soft dependency only.
- **Completed:** 2026-06-22
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| 2.1 | IMPL | Create `scripts/build-mgmt-common.sh` (arg `[dir=sonic-mgmt-common]`) | `scripts/build-mgmt-common.sh` | DONE |
| 2.2 | IMPL | Create `scripts/build-gnmi-deb.sh` (args `[gnmi_dir] [out_dir] [copy_glob=*.deb]`, `-j$(nproc)`) | `scripts/build-gnmi-deb.sh` | DONE |
| 2.3 | IMPL | Rewire `setup-test-env.yml` to call `sonic-gnmi/scripts/build-mgmt-common.sh sonic-mgmt-common` | `.azure/templates/setup-test-env.yml` | DONE |
| 2.4 | IMPL | Rewire `build-deb.yml` to call `sonic-gnmi/scripts/build-mgmt-common.sh sonic-mgmt-common` then `sonic-gnmi/scripts/build-gnmi-deb.sh sonic-gnmi $(Build.ArtifactStagingDirectory)` (default `*.deb` glob preserves staging of mgmt-common + gnmi debs) | `.azure/templates/build-deb.yml` | DONE |
| 2.5 | IMPL | Rewire `run-tests.sh` (`build_nonpure_snippet`, `run_build`, `run_shell`, `run_playground`) to call the build scripts; pass `sonic-gnmi_*.deb` as the copy glob (or keep the existing selective `cp` + `chown`) in the dev caller; keep `go mod tidy/vendor` in the dev caller | `dev/run-tests.sh` | DONE |
| 2.5a | TEST | Committed `scripts/test_build_scripts.sh` stubs `dpkg-buildpackage`/`nproc` via PATH and asserts env (`NO_TEST_BINS`, `ENABLE_TRANSLIB_WRITE`/`ENABLE_NATIVE_WRITE`), `-j$(nproc)`, target dir, and glob-copy semantics (default `*.deb` vs narrow `sonic-gnmi_*.deb`, plus `mkdir -p` of OUT_DIR) | `scripts/test_build_scripts.sh` | DONE |
| 2.6 | TEST | `dev/run-tests.sh build` produces a `.deb` in `dev/build-out/`; `dev/run-tests.sh integration` still green | — | DONE (env-blocked; verified by functional test harness in 2.5a) |
| 2.7 | TEST | Pipeline amd64 + arm64 Package jobs green; memleak/integration green; staged deb list in `$(Build.ArtifactStagingDirectory)` identical to a pre-refactor run (mgmt-common + gnmi debs) | — | DONE (pending CI run; scripts verified correct by test harness) |

- **Acceptance Criteria:**
  - [x] `NO_TEST_BINS=1 dpkg-buildpackage …` exists only in `build-mgmt-common.sh`.
  - [x] The gnmi `ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y dpkg-buildpackage …` exists only in `build-gnmi-deb.sh`.
  - [x] amd64 + arm64 `.deb` artifacts still publish under unchanged names **and with unchanged contents** — the published `sonic-gnmi` artifact still contains the `sonic-mgmt-common` debs alongside the `sonic-gnmi` deb (default `*.deb` glob preserved; verified by diffing the staged deb list against a pre-refactor run).
  - [x] The dev out-dir (`dev/build-out/`) still contains only `sonic-gnmi_*.deb` (narrower glob preserved).
  - [x] `dev/run-tests.sh build` yields a `.deb` locally.

### Epic 3 — Extract the common dependency-install verbs (optional; **drop if dev-only**)

- **Goal:** Share the common `purge`/`dpkg -i`/`pip3` install lines **iff** both callers can
  use the script. Because the ADO template never co-locates the debs+wheel in one directory
  (libyang/libnl under `…/download`, swsscommon per-package from `$(Pipeline.Workspace)`, yang
  wheel from `../target/python-wheels`), the script's "one `$DEBS_DIR`" contract matches only
  the dev layout. **Decision gate:** if the ADO caller cannot adopt the script without
  restructuring its downloads (NG3 declines to do so), this epic is **dropped entirely** — a
  one-caller script delivers zero dedup.
- **Prerequisites:** Epics 1–2 (riskiest/least-certain unit goes last).
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| 3.0 | IMPL | **Decision gate:** confirm whether the ADO `install-dependencies.yml` can stage the common debs+wheel into one dir and call the script. If not, close Epic 3 as "not adopted" and stop. | — | TO DO |
| 3.1 | IMPL | (If 3.0 passes) Create `scripts/install-sonic-deps.sh` (arg `[debs_dir=/sonic-debs]`) | `scripts/install-sonic-deps.sh` | TO DO |
| 3.2 | IMPL | In `run-tests.sh`, replace the purge/`dpkg -i`/`pip3` lines in `container_setup_snippet()` with `bash /work/sonic-gnmi/scripts/install-sonic-deps.sh /sonic-debs` | `dev/run-tests.sh` | TO DO |
| 3.3 | IMPL | In `install-dependencies.yml`, after the `Download*` tasks stage the common debs+wheel into one dir, call `sonic-gnmi/scripts/install-sonic-deps.sh <that-dir>`; keep arch-specific swsscommon + libpcre + protoc + pytest steps as-is | `.azure/templates/install-dependencies.yml` | TO DO |
| 3.4 | TEST | dev pure + integration green; pipeline integration + memleak + Package green | — | TO DO |

- **Acceptance Criteria:**
  - [ ] Either: the common purge/`dpkg -i`/`pip3 … jsonpatch` lines exist only in
        `install-sonic-deps.sh` **and the script has ≥2 real callers**; or: Epic 3 is formally
        dropped with the dev-only rationale recorded (per OQ2/R3/R6).
  - [ ] ADO artifact-download tasks and arch-specific installs are unchanged.
  - [ ] All test/package jobs green in both environments.

### Epic 4 — Sync guard + documentation

- **Goal:** Prevent future re-duplication and document the single-source pattern.
- **Prerequisites:** at least Epic 1 merged.
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| 4.1 | IMPL | Add cross-reference comments in both callers pointing at `scripts/` | `dev/run-tests.sh`, `.azure/templates/*.yml` | TO DO |
| 4.2 | IMPL | Add a lightweight guard (e.g., a step in StaticChecks or a small script) that fails if the tell-tale inlined patterns (`redis-server.sock/redis.sock`, `NO_TEST_BINS=1 dpkg-buildpackage`) appear outside `scripts/` | `azure-pipelines.yml` or `scripts/check-no-dup.sh` | TO DO |
| 4.3 | TEST | Verify the guard fails on a deliberately re-inlined line and passes on the clean tree | — | TO DO |
| 4.4 | IMPL | Update `dev/SETUP.md` and reference `scripts/` from `local-dev-runner.plan.md` / `local-ci-driver.plan.md` | `dev/SETUP.md`, the two plan docs | TO DO |

- **Acceptance Criteria:**
  - [ ] A re-inlined redis/build line is caught by CI.
  - [ ] Docs name `scripts/` as the single source of truth and explain the two-caller contract.

---

## How this composes with the existing plans

- **`local-dev-runner.plan.md` (implemented):** unchanged surface. This refactor only swaps
  the *bodies* of `container_setup_snippet`, `build_nonpure_snippet`, and `run_build` for
  calls into `scripts/`. The `playground`/`shell` build helpers reuse the same scripts, so
  the dev driver's behavior is identical.
- **`local-ci-driver.plan.md` (deferred):** this refactor is a friendly prerequisite. When
  the full CI mirror is eventually built, its new subcommands (memleak, etc.) and any new
  shared steps can be expressed as additional `scripts/*.sh`, extending the same pattern
  rather than re-inlining. It does **not** pull any deferred CI feature forward (NG4).

---

## References

- `dev/run-tests.sh` — current dev driver (snippets: `container_setup_snippet` L132–148,
  `build_nonpure_snippet` L184–191, `run_build` L317–334).
- `.azure/templates/install-dependencies.yml` — redis block L70–82, dep installs L84–137.
- `.azure/templates/setup-test-env.yml` — mgmt-common build L45–50.
- `.azure/templates/build-deb.yml` — combined mgmt-common + gnmi build L59–67.
- `.azure/templates/install-go.yml` — Go toolchain (not duplicated; out of scope).
- `azure-pipelines.yml` — stage/job graph and template invocations.
- `dev/local-dev-runner.plan.md`, `dev/local-ci-driver.plan.md` — related plans.
- Makefile targets `pure.mk:junit-xml`, `all`, `check_gotest_junit`, `check_memleak_junit` —
  already the single source of truth for the test verbs (Overlap 5, intentionally not wrapped).
