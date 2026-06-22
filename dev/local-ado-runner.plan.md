# Local ADO-YAML Runner for sonic-gnmi — Solution Design & Implementation Plan

> **Date:** 2026-06-22 | **Status:** Draft | **Audience:** sonic-gnmi developers/maintainers
> **Revision notes:** Rev 2 — corrected the host-vs-container capability gap for the bare-image
> jobs (`pure_tests`/`go_static_checks`, `pkg/exec`/nsenter), corrected the `${{ }}` expression
> scope (compound `and()` at `install-dependencies.yml` L69), stated the "single source of truth"
> limit up front, gated Epics 2–3 behind OQ4, and fixed line/displayName nits.
> Rev 3 — per maintainer direction, **env setup is now sourced from `azure-pipelines.yml`/
> `install-dependencies.yml`** as the **primary Epic 1 deliverable** (OQ2 resolved to rewrite the
> artifact path → `/sonic-debs`; OQ4 = yes, Epics ungated); `playground` and other `run-tests.sh`-only
> features remain pass-through parameters and out of scope.
> **Scope:** Investigate and plan a way to run the sonic-gnmi Azure DevOps (ADO) pipeline
> **locally** by **reading** the existing `azure-pipelines.yml` (and its `.azure/templates/*.yml`)
> and executing its step bodies on a developer's machine — **without modifying** the pipeline
> YAML or templates **at all**.
> **Relationship to other plans:** This is a **BACKUP / alternative** direction to the
> dedup-by-refactoring approach tracked in [`dev/dedupe-ci-dev.plan.md`](dedupe-ci-dev.plan.md).
> It **must not** modify `azure-pipelines.yml`, the `.azure/templates/`, or that other plan.
> It **composes with** the existing dockerized driver [`dev/run-tests.sh`](run-tests.sh) and the
> two prior designs [`dev/local-dev-runner.plan.md`](local-dev-runner.plan.md) (implemented inner
> loop) and [`dev/local-ci-driver.plan.md`](local-ci-driver.plan.md) (deferred full CI mirror).

---

## Executive Summary

The sonic-gnmi CI lives in `azure-pipelines.yml` plus four templates under `.azure/templates/`.
Today the local inner loop (`dev/run-tests.sh`) **re-implements** the same setup/build/test shell
by hand, so the two drift (the dedupe plan addresses that by *refactoring both* to share extracted
scripts). This document explores the opposite, **read-only** idea: leave the pipeline 100%
untouched and add a thin local **driver** that *parses* the unmodified ADO YAML, extracts the
executable `bash:`/`script:` step bodies for the jobs a developer cares about
(`go_static_checks`/gofmt, `pure_tests`, `integration_tests`, `memleak_tests`, the `Package`
builds), resolves the handful of template parameters/variables those bodies reference, maps ADO
concepts to their existing local equivalents (sibling repos from `bootstrap`, debs/wheel from the
`~/.cache/acr-image-build` cache, the same `sonic-slave-trixie` container, `DownloadPipelineArtifact`
→ cached debs, `PublishTestResults`/`PublishCodeCoverage` → no-ops), and runs the bodies in the
container.

**The key question — is there an off-the-shelf tool that runs `azure-pipelines.yml` locally
(an ADO analogue of `nektos/act`)? — is answered honestly up front: no, not usefully.** There is
no official Microsoft local runner; a self-hosted `azure-pipelines-agent` still requires an Azure
DevOps org/server to orchestrate (so it is not offline); and no community tool robustly evaluates
ADO template expansion + `${{ }}` compile-time expressions + `$( )` runtime macros + `condition:`
+ container jobs + `DownloadPipelineArtifact`. **Recommendation: do *not* attempt a faithful
generic ADO interpreter.** Build, *if anything*, a deliberately **narrow, sonic-gnmi-specific
"step extractor"** that pulls only the named jobs' inline `bash`/`script` bodies and runs them
through the existing `dev/run-tests.sh` container plumbing — and weigh even that against simply
**keeping the hardcoded commands** in `run-tests.sh` (the status quo, which the dedupe plan already
proposes to de-duplicate properly). The honest conclusion (see [Recommendation](#recommendation))
is that the **dedupe refactor is the right long-term answer** and this read-only runner is worth
building only as a **thin, best-effort drift-detector / extractor**, not as a primary execution path.

**Honest framing of the "single source of truth" claim up front.** The stated purpose is to avoid
duplicated logic by *sourcing it from the YAML*. In practice this design can only source roughly
**four to five make/`dpkg-buildpackage` invocations** from the YAML (gofmt gate, `make -f pure.mk
junit-xml`, `make all`, `make check_gotest_junit`/`check_memleak_junit`, `dpkg-buildpackage`). The
**bulk of the real setup** — dependency `dpkg -i`, redis/pytest/protoc install, the mgmt-common build
path choice — still comes from `run-tests.sh`'s `container_setup_snippet`/`build_nonpure_snippet`,
because those YAML bodies depend on `DownloadPipelineArtifact` paths. **Rev 3 maintainer direction:**
OQ2 is resolved to **rewrite that staging path to the cached `/sonic-debs` mount** so the deb-install
bodies (and the redis/pytest/protoc install bodies) **are** sourced from `install-dependencies.yml` —
making **env setup sourced from the pipeline the primary Epic 1 deliverable**, kept in sync in one
place. The remaining unsourced bits are only the host Go-install `wget` (FG2) and the artifact
*download* itself (mapped to the cache, FG3-residual). `playground` and other `run-tests.sh`-only
features stay pass-through and out of scope. This is stated here, not buried, so the scope is clear.

---

## Background

### Current state — the pipeline (unmodified, read-only target)

`azure-pipelines.yml` (304 lines) defines three stages:

| Stage | Job (key / displayName) | Runs on | Step bodies of interest |
|-------|-------------------------|---------|--------------------------|
| **StaticChecks** | `go_static_checks` / "Go static checks" | bare `ubuntu-22.04` + `install-go.yml` | inline `bash:` **gofmt** gate (L75-95) |
| **Test** | `pure_tests` / "Pure package tests" | bare `ubuntu-22.04` + `install-go.yml` | inline `bash:` `go mod tidy` + `make -f pure.mk junit-xml` (L122-128) |
| **Test** | `memleak_tests` / "Memory-leak tests" | `container: sonic-slave-trixie:latest` + `setup-test-env.yml` | inline `bash:` `make all` + `$(UNIT_TEST_FLAG) make check_memleak_junit` (L168-173) |
| **Test** | `integration_tests` / "Integration tests" | `container: sonic-slave-trixie:latest` + `setup-test-env.yml` | inline `bash:` `make all` + `$(UNIT_TEST_FLAG) make check_gotest_junit` (L203-208) |
| **Test** | `build` / "build" (coverage aggregator) | bare `ubuntu-22.04`, PR-only | only `download:` artifacts; decorator does the work |
| **Package** | `amd64` / "amd64 deb build" / `arm64` / "arm64 deb build" | `container: sonic-slave-trixie` (arm64: `$(BUILD_BRANCH)-arm64`) | `build-deb.yml` (`dpkg-buildpackage`) |

> **Critical environment caveat for the two bare-image jobs.** `go_static_checks` and `pure_tests`
> run in CI on a **bare `ubuntu-22.04` host**, not in `sonic-slave-trixie`. `pure_tests`' verbatim
> body is `make -f pure.mk junit-xml`, whose default `PURE_PACKAGES` set **includes `pkg/exec`**
> (`pure.mk` L27). `pkg/exec`'s tests call `nsenter`, which needs `CAP_SYS_ADMIN` + a relaxed seccomp
> profile; inside `sonic-slave-trixie` they fail spuriously ("Operation not permitted") — which is
> exactly why `run-tests.sh run_pure` **overrides `PACKAGES` to exclude `pkg/exec`** (L166-170).
> Consequence: a faithful in-container replay of the YAML body will **not** match `run-tests.sh pure`
> (different package set) **and may fail on the capability gap**. This is a hard host-vs-container
> fidelity gap, tracked below (FG1) and reflected in the revised acceptance criteria.

The four templates:

- **`install-go.yml`** — `wget` + untar Go `${{ parameters.version }}` into `/usr/local/go` (L17-23).
- **`setup-test-env.yml`** — checkout self + `sonic-mgmt-common` + `sonic-swss-common`, then
  `install-dependencies.yml`, then build `sonic-mgmt-common` (`NO_TEST_BINS=1 dpkg-buildpackage …`, L45-50).
- **`install-dependencies.yml`** — `DownloadPipelineArtifact@2` ×3 (common_libs libyang/libnl debs,
  pipeline `142` `sonic_yang_models*.whl`, `Azure.sonic-swss-common` debs), `dpkg -i` them, install
  redis + pytest (`${{ if and(eq(parameters.arch,'amd64'), eq(parameters.installTestDeps,true)) }}`,
  L69), install protoc. Heavy use of `${{ if eq(parameters.arch,…) }}` (L122,131,140,147) plus the one
  **compound** `and()` guard at L69.
- **`build-deb.yml`** — checkout ×3, `install-dependencies.yml`, build mgmt-common + `sonic-gnmi`
  `dpkg-buildpackage`, publish artifacts.

### Current state — the local driver (`dev/run-tests.sh`)

A single `set -euo pipefail` Bash script (386 lines). It already maps every ADO concept this plan
needs to a local equivalent — which is precisely why a read-only runner is even feasible:

| ADO concept (in YAML) | Local equivalent already in `run-tests.sh` |
|-----------------------|--------------------------------------------|
| `resources.repositories: sonic-mgmt-common, sonic-swss-common` | `bootstrap()` clones both into `$CACHE_DIR` (L107-122); bind-mounted at `/work/sonic-{mgmt,swss}-common` (L155-156) |
| `container: sonic-slave-trixie:latest` | `IMAGE` constant (L48); `docker_run()` (L150-161) |
| `DownloadPipelineArtifact@2` (libyang/libnl/swsscommon debs + yang wheel) | `bootstrap()` curls `DEB_TARGETS` into `$DEBS_DIR` (L84-95, L111-118); mounted RO at `/sonic-debs` (L157); installed by `container_setup_snippet()` (L138-139) |
| redis + pytest install (`install-dependencies.yml` L70-81) | `container_setup_snippet()` (L140-144) |
| build `sonic-mgmt-common` (`setup-test-env.yml` L48) | `build_nonpure_snippet()` (L187) |
| `make all` (memleak/integration jobs) | `build_nonpure_snippet()` (L189) |
| `make check_gotest_junit` (integration) | `run_integration()` (L220) |
| `PublishTestResults@2` / `PublishCodeCoverageResults@2` | not implemented (JUnit XML emitted to `test-results/`; no publish step needed locally) |
| gofmt, `check_memleak_junit`, diff-cover, arm64 | **not implemented** (deferred to `local-ci-driver.plan.md`) |

### Prior art in this repo

- **`dev/dedupe-ci-dev.plan.md`** — the *primary* direction: extract the shared shell bodies into
  `scripts/*.sh` that **both** the ADO templates **and** `run-tests.sh` call. It explicitly
  catalogs the overlaps (redis config, mgmt-common build, gnmi deb build) and notes the parts that
  **cannot** be shared (`DownloadPipelineArtifact@2`, docker bind mounts) because they are
  environment-native, not shell logic. **This read-only runner is the inverse trade-off:** instead
  of moving logic *out* of the YAML into shared scripts, it *reads the logic from the YAML in place*.
- **`dev/local-ci-driver.plan.md`** — the deferred full CI mirror that adds gofmt/memleak/diff-cover/arm64
  subcommands to `run-tests.sh` by calling the same Make targets.
- **`dev/local-dev-runner.plan.md`** — the implemented lightweight inner loop.

---

## Problem Statement

1. **Two copies of the same logic.** The setup/build/test shell exists once in `azure-pipelines.yml`
   + templates and again in `dev/run-tests.sh`. When CI changes (e.g. a new gofmt exclude path, a
   bumped Go version, a new make target), the local driver silently drifts and a developer can pass
   locally yet fail CI.
2. **The dedupe plan changes the pipeline.** `dedupe-ci-dev.plan.md` solves drift by editing the
   templates to call shared scripts. Some teams/maintainers may want a path that **touches nothing**
   in the pipeline (review-risk, ownership, or release-train reasons). This plan is that backup.
3. **No obvious off-the-shelf solution.** Unlike GitHub Actions (which has `nektos/act`), there is no
   established way to execute an `azure-pipelines.yml` locally, so "just run the pipeline file" is not
   a turnkey option — the feasibility itself must be established before any plan is committed to.

---

## Goals and Non-Goals

### Goals
- **G1.** Answer definitively whether an off-the-shelf tool can run `azure-pipelines.yml` locally,
  with honest assessment of official, self-hosted-agent, and community options.
- **G2.** If no off-the-shelf tool suffices, design a **narrow, read-only** local driver that sources
  the executable step bodies from the **unmodified** `azure-pipelines.yml` + `.azure/templates/` and
  runs them via the existing `dev/run-tests.sh` container plumbing.
- **G3.** Map every ADO concept the targeted jobs use to its existing local equivalent, and be
  explicit about what is faithfully honored vs. approximated vs. ignored.
- **G4.** Deliver a concrete **build-vs-keep-hardcoding** recommendation with an incremental plan if
  "build" is chosen.

### Non-Goals
- **N1.** Modifying `azure-pipelines.yml`, any `.azure/templates/*.yml`, or `dedupe-ci-dev.plan.md`.
  (Hard constraint.)
- **N2.** A faithful, general-purpose ADO YAML interpreter (full `${{ }}` template-expression engine,
  `condition:` evaluation, matrix/strategy, service containers, `DownloadPipelineArtifact` against a
  live org). Explicitly out of scope — see [Alternatives](#alternatives-considered).
- **N3.** Running the `build`/coverage-aggregator job (it is a pure decorator + artifact download with
  no portable step body) or the arm64 cross-build on an amd64 dev box.
- **N4.** Replacing `run-tests.sh`. This driver *reuses* its container/cache functions. The
  `playground` (and other `run-tests.sh`-only conveniences) remain **pass-through parameters of
  `run-tests.sh`** and are out of scope for `ado-local.py` — it never sources, wraps, or runs them.
- **N5.** Offline execution of `DownloadPipelineArtifact@2` against Azure (the cache already substitutes).

---

## Requirements

### Functional
- **FR1.** Parse `azure-pipelines.yml` and the four templates with a real YAML parser (not regex),
  read-only.
- **FR2.** Select a job by its ADO key (`go_static_checks`, `pure_tests`, `integration_tests`,
  `memleak_tests`, `amd64`) and assemble its **ordered** step list, expanding `- template:` includes.
- **FR3.** Extract the literal body of each `- bash:` / `- script:` step; skip `- task:`,
  `- checkout:`, `- publish:`, `- download:` (map them to local equivalents or no-ops).
- **FR4.** Resolve the minimal variable/parameter set the bodies actually reference:
  `$(UNIT_TEST_FLAG)`, `$(GO_VERSION)`, `$(BUILD_BRANCH)`, `${{ parameters.version }}`,
  `${{ parameters.arch }}`, `${{ parameters.installTestDeps }}`, `$(System.DefaultWorkingDirectory)`.
- **FR5.** Run the assembled bodies inside `sonic-slave-trixie` using `run-tests.sh`'s `docker_run`
  + `container_setup_snippet` (cache-backed), so the bodies see the same sibling layout and deps.
- **FR6.** Emit the same artifacts the jobs produce locally (`test-results/*.xml`, `coverage.xml`)
  and treat publish/download tasks as no-ops.
- **FR7.** Fail loudly and clearly when the YAML contains a construct the extractor does not support
  (unknown `task`, an `${{ if }}` it cannot evaluate, a referenced variable it cannot resolve) —
  never silently skip pipeline logic.

### Non-Functional
- **NFR1.** **Read-only** w.r.t. the pipeline (enforced; the driver only opens the YAML for reading).
- **NFR2.** No new heavyweight runtime dep beyond what's already present. Python 3 + `PyYAML` is the
  pragmatic choice (common in SONiC Python tooling, though not yet verified as installed in this repo's
  dev environment — Epic 1 must confirm or vendor it); a pure-Bash+`yq` variant is the fallback.
- **NFR3.** Single-maintainer-debuggable: the extractor must be small (target < ~400 LOC) and print
  exactly what it will run before running it (`--dry-run`).
- **NFR4.** Honest fidelity reporting: a `--explain` mode that lists, per job, which steps were run,
  which were mapped to local equivalents, and which were skipped/unsupported.

---

## Research: can an off-the-shelf tool run `azure-pipelines.yml` locally?

This is **G1**, answered before any design commitment.

### (a) Official Microsoft position — **no local runner exists**
Microsoft does not ship a local/offline runner for Azure Pipelines. The long-standing Developer
Community request "Run Azure DevOps Pipeline (YAML) locally" remains unimplemented; the only
first-party "local" affordances are **validation** (the *Azure Pipelines* VS Code extension and the
`/_apis/pipelines/{id}/preview` "what-if" run, which *parses/expands* YAML but does not execute it).
**Conclusion:** there is no `nektos/act` equivalent blessed by Microsoft.

### (b) Self-hosted agent (`microsoft/azure-pipelines-agent`) — **not truly local/offline**
You *can* run the agent on your own machine, but per the official self-hosted-agent docs the agent
must be **configured/registered against an Azure DevOps organization (or Azure DevOps Server) and an
agent pool**, using a **PAT** (the docs note "the user configuring the agent needs pool admin
permissions"; PAT scope and pool registration are mandatory). The agent then **polls the server for
jobs** — the *server* compiles the YAML, expands templates/expressions, resolves variables/artifacts,
and dispatches step batches. So:
- It needs a reachable ADO org/server → **not offline**.
- It needs the pipeline to actually run there → does not let you iterate on a local YAML edit without
  pushing.
**Conclusion:** the self-hosted agent solves "run on my hardware," not "run my YAML locally/offline."
It does **not** satisfy this plan's intent.

### (c) Community / third-party tools — **immature; break on the hard parts**
There is no widely adopted, maintained ADO analogue of `nektos/act`. The constructs that any such
tool must handle are exactly the ones the sonic-gnmi pipeline leans on, and they are where community
attempts fail:

| ADO construct used by sonic-gnmi | Where | Why a generic local runner struggles |
|----------------------------------|-------|--------------------------------------|
| `- template:` includes with `parameters:` | everywhere | Requires a template-expansion engine + parameter typing/defaults |
| `${{ if eq(parameters.arch,'amd64') }}` compile-time expressions | `install-dependencies.yml` L122,131,140,147 (plain `eq`); **L69 is a compound `and(eq(arch,'amd64'), eq(installTestDeps,true))`**; root L32 (`eq(Build.Reason,'PullRequest')`) | Requires implementing the ADO **expression language** (functions `eq`/`and`, boolean params, contexts) at *compile* time |
| `$(VAR)` runtime macros | `$(UNIT_TEST_FLAG)`, `$(GO_VERSION)`, `$(BUILD_BRANCH)`, `$(System.DefaultWorkingDirectory)` | Two-phase substitution (compile `${{ }}` vs runtime `$( )`) is subtle and order-dependent |
| `condition: and(succeeded(), eq(variables['Build.Reason'],'PullRequest'))` | `build` job L243 | Needs job-graph + status model + variables context |
| `container:` jobs | memleak/integration/package | Must launch the image and run steps inside it |
| `task: DownloadPipelineArtifact@2` | `install-dependencies.yml` | Needs a live ADO org + artifact feed, or a substitute (the very thing the cache provides) |
| `task: PublishTestResults@2` / `PublishCodeCoverageResults@2` | all test jobs | Server-side ingestion; no local meaning |

**Conclusion:** a faithful generic runner would have to re-implement a meaningful slice of the ADO
service. No off-the-shelf tool does this reliably for templated pipelines like ours.

### (d) Pragmatic custom approach — **a narrow, sonic-gnmi-specific extractor**
Because the pipeline is *small and known*, we don't need a generic interpreter. We need to honor a
**fixed, enumerated** set of jobs whose only portable content is a handful of inline `bash`/`script`
bodies that — by inspection — reference **at most seven** variables/parameters and call **make
targets that already exist locally**. Everything heavyweight (`DownloadPipelineArtifact`, `container`,
checkouts, publish) already has a local equivalent inside `run-tests.sh`. This is the only viable
"run the YAML locally" path, and it is viable precisely because it is **not generic**.

---

## Proposed Design

### Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│ dev/ado-local.py   (NEW — read-only ADO step extractor/driver)         │
│                                                                        │
│  1. LOAD    azure-pipelines.yml + .azure/templates/*.yml  (PyYAML, RO) │
│  2. SELECT  job by key (e.g. integration_tests)                        │
│  3. EXPAND  - template: includes  → flat ordered step list             │
│  4. RESOLVE the ~7 known vars/params (static table, not an engine)     │
│  5. CLASSIFY each step:                                                 │
│        bash/script  → keep body (substitute resolved vars)             │
│        checkout     → no-op (cache/bind-mounts already provide repos)  │
│        DownloadPipelineArtifact → no-op (cache already has debs/wheel) │
│        Publish*/publish/download → no-op (or local cp)                 │
│        unknown task / unresolved ${{ }} → ERROR (FR7)                  │
│  6. EMIT    an ordered bash program (the concatenated bodies)          │
│  7. RUN     it via run-tests.sh plumbing  ───────────────┐            │
└──────────────────────────────────────────────────────────┼───────────┘
                                                            │ sources / calls
                                                            ▼
              dev/run-tests.sh  (UNCHANGED public behavior; functions reused)
                docker_run() · container_setup_snippet() · IMAGE · CACHE_DIR
                                                            │
                                                            ▼
              sonic-slave-trixie container  +  ~/.cache/acr-image-build
```

`dev/ado-local.py` is the **only** new artifact. It treats the pipeline as **read-only input data**
and the existing dockerized driver as its **execution backend**. It never writes to the YAML or
templates.

### Key Components

#### C1. YAML loader (read-only)
- Opens `azure-pipelines.yml` and each referenced `.azure/templates/*.yml` with `yaml.safe_load`.
- Resolves `- template: <path>` relative to the including file's directory (root includes are
  `.azure/templates/x.yml`; nested includes inside a template are relative to `.azure/templates/`).
- **Constraint check:** asserts the files are opened in read mode only (NFR1).

#### C2. Job selector + step flattener
- Indexes `stages[].jobs[]` by `job:` key. Exposes the five supported keys
  (`go_static_checks`, `pure_tests`, `memleak_tests`, `integration_tests`, `amd64`).
- Produces the ordered step list, recursively splicing template steps in place with their
  `parameters:` bound (defaults from the template's `parameters:` block, overridden by the call site).

#### C3. Variable/parameter resolver (a **static table**, not an expression engine)
Deliberately *not* a general evaluator. A small dict maps the **enumerated** names the bodies use:

| Name | Source | Local value |
|------|--------|-------------|
| `$(GO_VERSION)` | root `variables` L38-39 | `1.24.4` (read from YAML) |
| `$(UNIT_TEST_FLAG)` | root `variables` L36-37 | `ENABLE_TRANSLIB_WRITE=y` (read from YAML) |
| `$(BUILD_BRANCH)` | root `variables` L31-35 (`${{ if PullRequest }}`) | local default `master` (overridable `--branch`); the `${{ if }}` is **not evaluated** — we pick the non-PR branch by policy and document it |
| `${{ parameters.version }}` | `install-go.yml` | bound from call site (`$(GO_VERSION)` → `1.24.4`) |
| `${{ parameters.arch }}` | `install-dependencies.yml`/`build-deb.yml` | `amd64` (only supported arch locally) |
| `${{ parameters.installTestDeps }}` | `setup-test-env.yml` → `true` | `true` for test jobs |
| `$(System.DefaultWorkingDirectory)` | implicit | `/work` (the container mount root) |

For the `${{ if … }}` guards in `install-dependencies.yml`, the resolver evaluates a **small, closed
set of predicates** against the fixed local context (`arch=amd64`, `installTestDeps=true`):
- the plain `eq(parameters.arch,'amd64')` guards at L122 and L147 → **include** (amd64 branch);
- the plain `eq(parameters.arch,'arm64')` guards at L131 and L140 → **drop** (arm64 not supported locally);
- the **compound** `and(eq(parameters.arch,'amd64'), eq(parameters.installTestDeps,true))` guard at
  **L69** → the resolver must evaluate `and()` over **both** `eq()` operands plus the `installTestDeps`
  boolean parameter, and **include** the branch (both predicates true locally).

So the resolver is **not** "a single `arch==amd64` comparison" — it must support `and()`, `eq()` with
both a string-parameter operand and a boolean-parameter operand, and the `installTestDeps` parameter
default. It implements exactly these forms and **nothing else**: any other `${{ }}` expression (a new
function, `or()`, `ne()`, a `condition:`, an unknown variable context) triggers FR7 (error, do not
guess). This keeps the surface closed and enumerable while still honoring the actual L69 guard.

#### C4. Step classifier → local mapping
| Step kind in YAML | Action |
|-------------------|--------|
| `- bash:` / `- script:` | Keep body; substitute resolved vars (C3); append to the emitted program |
| `- checkout: self/sonic-*` | No-op + log (`run-tests.sh` bind-mounts already provide all three repos) |
| `- task: DownloadPipelineArtifact@2` | No-op + log for the *download* itself (the cache already holds the libyang/libnl/swsscommon debs + `sonic_yang_models*.whl` at `/sonic-debs`); the subsequent `- script:` deb-install bodies **are kept** with their staging path rewritten to `/sonic-debs` (OQ2=ii) so env setup is sourced from the YAML |
| `- task: PublishTestResults@2` | No-op + log (JUnit XML already lands in `test-results/`) |
| `- task: PublishCodeCoverageResults@2` | No-op + log (`coverage.xml` already produced) |
| `- publish:` / `- download:` | No-op (or optional `cp` into `dev/build-out/` for deb artifacts) |
| any other `- task:` | **ERROR** (FR7) |

> **Important fidelity note.** `setup-test-env.yml`/`build-deb.yml` build `sonic-mgmt-common` via a
> `- script:` body (`NO_TEST_BINS=1 dpkg-buildpackage …`). That body **is** extracted and run, so the
> mgmt-common build is honored *from the YAML* rather than from `run-tests.sh`'s
> `build_nonpure_snippet`. The `install-dependencies.yml` deb-install `- script:` bodies (`dpkg -i …`)
> reference `$(Build.ArtifactStagingDirectory)/download` populated by the skipped
> `DownloadPipelineArtifact` task. **Per Rev 3 maintainer direction (OQ2 = option (ii)) these bodies
> are also kept and sourced from the YAML, with that staging path rewritten to the cached
> `/sonic-debs` mount** — so the dependency-install logic is single-sourced from the pipeline, not
> re-hardcoded in the runner. Only the artifact *download* and the host Go-install `wget` remain mapped
> to local equivalents (FG2/FG3-residual).

#### C5. Emitter + executor
- Concatenates the kept bodies into one `set -euo pipefail` program. For the container jobs the
  env-setup prelude is the **YAML-sourced `install-dependencies.yml` bodies** (deb install rewritten to
  `/sonic-debs`, plus redis/pytest/protoc), not a re-hardcoded `container_setup_snippet`, so deps/redis
  are present *and* single-sourced from the pipeline.
- For **container jobs** (`memleak_tests`, `integration_tests`, `amd64`): runs the program via
  `docker_run` from `run-tests.sh` (sourced or re-invoked).
- For **bare-image jobs** (`go_static_checks`, `pure_tests`): CI runs these on a bare host
  `ubuntu-22.04` with Go installed via `install-go.yml`. There is **no faithful local equivalent**
  because of the host-vs-container capability gap (FG1):
  - `go_static_checks` (gofmt) is capability-neutral, so running it in-container is a safe, faithful
    replay of the YAML body (we skip the `install-go.yml` `wget` and use the container's Go; toggle
    `--honor-go-install`).
  - `pure_tests`' verbatim body `make -f pure.mk junit-xml` uses the default `PURE_PACKAGES` set
    **including `pkg/exec`** (`pure.mk` L27), whose `nsenter` tests need `CAP_SYS_ADMIN`/relaxed
    seccomp and **fail spuriously in `sonic-slave-trixie`** (the exact reason `run-tests.sh run_pure`
    excludes `pkg/exec`, L166-170). Therefore the runner **cannot** give command+environment parity
    for `pure_tests` by replaying the body in-container. The default behavior is to run the **verbatim
    YAML body** (default package set) and **surface FG1 prominently in `--explain`/output**, accepting
    that it may fail on the capability gap — rather than silently diverging from CI by dropping
    `pkg/exec`. A `--exclude-pkg-exec` toggle reproduces `run-tests.sh`'s relaxed set for a green run,
    explicitly logged as a *deviation from the CI body*. Honest replay vs. green-run is the developer's
    choice; the tool never hides which one it did.
- `--dry-run` prints the emitted program + the classification table and exits without running.
- `--explain` prints, per step, kept/no-op/error with the source line reference.

### Data Flow (integration_tests example)

1. `dev/ado-local.py run integration_tests --dry-run`
2. Loader reads `azure-pipelines.yml`; finds `integration_tests` → steps =
   `[template setup-test-env.yml, bash "make all + make check_gotest_junit", task PublishTestResults,
   bash "pip install diff-cover", task PublishCodeCoverageResults, publish coverage.xml]`.
3. Flattener expands `setup-test-env.yml` →
   `[checkout self, checkout mgmt-common, checkout swss-common, template install-dependencies.yml,
   script "build sonic-mgmt-common"]`; and expands `install-dependencies.yml` (with `arch=amd64`,
   `installTestDeps=true`) → its download tasks + amd64 install scripts.
4. Classifier: checkouts → no-op; `DownloadPipelineArtifact` *download* → no-op (cache provides
   `/sonic-debs`); amd64 install `- script:` bodies → **kept** with the staging path rewritten to
   `/sonic-debs` (OQ2=ii); `build sonic-mgmt-common` `- script:` → kept;
   `make all + make check_gotest_junit` → kept (with `$(UNIT_TEST_FLAG)`→`ENABLE_TRANSLIB_WRITE=y`);
   Publish*/publish → no-op.
5. Emitter → the **YAML-sourced env-setup prelude** (the `install-dependencies.yml` deb-install bodies
   with paths rewritten to `/sonic-debs`, plus its redis/pytest/protoc install bodies) +
   `cd /work/sonic-mgmt-common && NO_TEST_BINS=1 dpkg-buildpackage …`
   + `cd /work/sonic-gnmi && make all && ENABLE_TRANSLIB_WRITE=y make check_gotest_junit`.
6. Without `--dry-run`, executor runs that via `docker_run "-t" bash -c "<program>"`.
7. JUnit/coverage XML land in `test-results/` and `coverage.xml`; publish tasks are no-ops.

### API Contract (CLI)

```
dev/ado-local.py <command> [job] [flags]

commands:
  list                       list supported jobs and their stage
  run <job>                  extract + run the job's step bodies in the container
  explain <job>              print per-step classification (kept/no-op/error) + source refs

jobs: go_static_checks | pure_tests | memleak_tests | integration_tests | amd64

flags:
  --dry-run                  print the emitted program, do not execute
  --branch <name>            value for $(BUILD_BRANCH)        (default: master)
  --honor-go-install         run install-go.yml's wget body instead of using container Go
  --exclude-pkg-exec         pure_tests only: drop pkg/exec to match run-tests.sh (logged FG1 deviation)
  --explain                  verbose per-step mapping report
  --pipeline <path>          override azure-pipelines.yml path (default: repo-root)
```

### Design Decisions

- **D1. Narrow extractor, not a generic interpreter.** We honor a *fixed enumerated* job set and a
  *fixed enumerated* variable table. Anything outside the table errors (FR7). Rationale: the only way
  to keep this robust is to refuse to guess at ADO semantics.
- **D2. Reuse `run-tests.sh` as the execution backend; do not fork its container/cache logic.**
  Rationale: avoids creating a *third* copy of the docker/cache plumbing; keeps this plan additive.
- **D3. Map `DownloadPipelineArtifact` + checkouts + publish to the existing cache/mounts/no-ops.**
  Rationale: these are environment-native (the dedupe plan reaches the same conclusion — they can't be
  shared shell logic); the cache already substitutes the artifacts.
- **D4. Honor the *test/build command* **and** the *env-setup* bodies from the YAML (deb install via
  the `/sonic-debs` path rewrite, redis/pytest/protoc); accept a gap only on the host Go-install
  `wget` and the artifact download itself.** Rationale (Rev 3): the maintainer goal is a single source
  of truth for env setup, so the dependency-install logic is sourced from `install-dependencies.yml`
  rather than re-hardcoded; the residual gaps (Go `wget`, artifact download) are environment-native and
  logged, never hidden.
- **D5. Python + PyYAML over Bash + `yq`.** Rationale: template expansion, parameter binding, and the
  small closed set of `${{ }}` predicates (incl. the compound `and()` at L69) are far cleaner in
  Python; PyYAML is ubiquitous. (`yq` fallback noted.)

### Fidelity Gaps (explicit)

These are intrinsic limits, surfaced in `--explain` and `dev/SETUP.md` — never hidden:

| ID | Gap | Why it exists | Local handling |
|----|-----|---------------|----------------|
| **FG1** | `pure_tests` cannot achieve command+environment parity | CI runs the body on a **bare host** with the default `PURE_PACKAGES` incl. `pkg/exec` (`pure.mk` L27); in `sonic-slave-trixie` `pkg/exec`'s `nsenter` tests fail spuriously without `CAP_SYS_ADMIN`/relaxed seccomp (`run-tests.sh` L166-170) | Default = run verbatim YAML body in-container and loudly flag possible `pkg/exec` failure; `--exclude-pkg-exec` reproduces `run-tests.sh`'s green set as a logged deviation |
| **FG2** | `go_static_checks` host Go bootstrap not reproduced | CI installs Go via `install-go.yml` `wget`; we use the container's Go | gofmt body itself is honored verbatim (capability-neutral); `--honor-go-install` to replay the `wget` |
| **FG3** | Only the artifact *download* (not the deb-install logic) is unsourced | The `DownloadPipelineArtifact` task needs a live ADO feed | **Rev 3:** the deb-install `- script:` bodies **are** sourced from `install-dependencies.yml` with the staging path rewritten to the cached `/sonic-debs` (OQ2=ii); only the download task itself is a no-op mapped to the cache |
| **FG4** | `${{ }}` not generally evaluated | No full ADO expression engine | Only the enumerated predicates in C3 are evaluated; all else errors (FR7) |
| **FG5** | `build` coverage aggregator + `arm64` not run | Decorator/PR-only and cross-arch | Out of scope (N3) |

---

## Alternatives Considered

### A1. Keep hardcoding the commands in `run-tests.sh` (status quo + dedupe plan)
- **Pros:** zero new parsing surface; already works; the dedupe plan (`dedupe-ci-dev.plan.md`) already
  proposes the *correct* single-source-of-truth fix by extracting shared `scripts/*.sh` that **both**
  the YAML and `run-tests.sh` call — which removes drift **without** fragile YAML parsing.
- **Cons:** the local driver still embeds command knowledge (until the dedupe refactor lands);
  developers must trust that `run-tests.sh` mirrors CI.
- **Assessment:** **This is the recommended long-term answer.** The dedupe refactor achieves
  drift-freedom with far less fragility than parsing ADO YAML/expressions.

### A2. Self-hosted `azure-pipelines-agent` pointed at the real org
- **Pros:** 100% fidelity (it *is* the pipeline).
- **Cons:** requires an ADO org + PAT + pool; **not offline/local**; doesn't help iterate on an unpushed
  YAML edit; heavyweight. Fails the plan's "truly local" intent (research (b)).
- **Assessment:** Rejected for the stated goal.

### A3. Full generic ADO YAML interpreter (an "act for ADO")
- **Pros:** would run *any* pipeline, future-proof.
- **Cons:** must implement template expansion + the `${{ }}` expression language + `$( )` macros +
  `condition:` + job graph + container orchestration + artifact resolution. Enormous surface; brittle;
  no community base to build on. Way beyond a dev convenience.
- **Assessment:** Rejected (N2).

### A4. The narrow read-only extractor (this plan's proposal)
- **Pros:** sources the *test/build commands* from the unmodified YAML, so those specific bodies can't
  drift; honest about gaps; small; reuses `run-tests.sh`.
- **Cons:** still fragile against pipeline restructures (renamed jobs, new `${{ }}`, new tasks);
  honors only a subset of the YAML; partly overlaps with what the dedupe plan does more cleanly.
- **Assessment:** Worth building **only as a thin best-effort extractor / drift-detector**, not as the
  primary execution path. See Recommendation.

---

## Recommendation

**Primary recommendation:** adopt **A1 + the dedupe refactor** (`dedupe-ci-dev.plan.md`) as the real
solution to drift, and treat this read-only runner as a **secondary, optional, best-effort tool**.

**If a read-only runner is still wanted** (e.g. maintainers refuse any pipeline edit), build **A4 — the
narrow extractor — incrementally and defensively**, with these guardrails:
1. Scope to the five enumerated jobs and the seven enumerated variables; **error, never guess** (FR7).
2. Reuse `run-tests.sh` plumbing (D2); add **no** new container/cache logic.
3. Ship `explain`/`--dry-run` first (read-only, zero execution risk) as a **drift-detector**, then —
   since **OQ4 is answered "yes" (Rev 3)** — build the executor, with the **env-setup bodies sourced
   from `install-dependencies.yml`** (path → `/sonic-debs`) as the primary Epic 1 value, followed by
   the container test jobs and packaging.
4. Document every fidelity gap (Go-install body, deb-install path, `${{ if }}` not evaluated,
   `BUILD_BRANCH` policy) in `--explain` output and in `dev/SETUP.md`.

**Honest bottom line on "how much of `azure-pipelines.yml` can be honored unmodified":** the
*test/build command* bodies (gofmt gate, `make all`, `make check_gotest_junit`,
`make check_memleak_junit`, `dpkg-buildpackage`) — roughly the lines that *matter for reproducing a CI
failure* — can be honored verbatim. **`make -f pure.mk junit-xml` is the exception: it can be emitted
verbatim but cannot run faithfully in the container (FG1), so it is honored-but-flagged, not honored
cleanly.** The *environment-bootstrap* portions (`DownloadPipelineArtifact`, checkouts,
redis/pytest/protoc install, Go install, publish/coverage upload, the PR-only `build` aggregator,
arm64) cannot be meaningfully sourced from the YAML and are mapped to local equivalents or skipped. So
the runner faithfully reproduces **most of the commands a developer cares about**, with one documented
capability exception, but is **not** a faithful reproduction of the whole pipeline — and that limit is
intrinsic, not an implementation shortcut.

---

## Dependencies

- **External:** Docker; `sonic-slave-trixie:latest` image; Python 3 + `PyYAML` (or `yq`); network for
  `bootstrap` artifact fetch (one-time, cached).
- **Internal:** `dev/run-tests.sh` (`docker_run`, `container_setup_snippet`, `IMAGE`, `CACHE_DIR`,
  `bootstrap`); the unmodified `azure-pipelines.yml` + `.azure/templates/*.yml` (read-only input);
  existing Make targets (`pure.mk junit-xml`, `all`, `check_gotest_junit`, `check_memleak_junit`).
- **Sequencing:** `dev/run-tests.sh bootstrap` must have populated `~/.cache/acr-image-build` before
  any `run` (the runner calls `require_cache`/`bootstrap` indirectly).

## Impact Analysis

- **Affected:** only the new `dev/ado-local.py` and docs (`dev/SETUP.md`, this plan). **No** change to
  `azure-pipelines.yml`, `.azure/templates/`, `run-tests.sh` behavior, Makefiles, or `dedupe-ci-dev.plan.md`.
- **Backward compatibility:** purely additive; existing `run-tests.sh` subcommands unchanged. If
  `run-tests.sh` is refactored to *source* shared functions (so `ado-local.py` can call them without
  re-invoking the script), that refactor must preserve the existing CLI exactly.
- **Performance:** the parsing/extraction is negligible; runtime is dominated by the same container
  build/test the dev driver already runs.
- **Operational:** one more script to learn; mitigated by `list`/`explain`/`--dry-run`.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Pipeline restructure (renamed job, new `${{ }}`, new task) silently breaks the extractor | High | Medium | FR7 hard-errors on anything unenumerated; CI-independent `explain` mode surfaces drift early; pin to job keys, not displayNames |
| Developers over-trust the runner as "exactly CI" when it skips bootstrap/publish/arm64 | Medium | Medium | `--explain` always lists skipped steps; docs state the fidelity boundary explicitly |
| Two-phase `${{ }}` vs `$( )` substitution bugs produce wrong commands | Medium | High | Keep resolver a static table (no general evaluator); `--dry-run` shows the exact emitted program for review |
| Maintenance overlap with `dedupe-ci-dev.plan.md` causes confusion about the "real" source of truth | Medium | Medium | This plan declares itself the **backup**; recommend dedupe as primary; cross-link both |
| `arch`/`BUILD_BRANCH` `${{ if }}` mis-evaluated | Low | Medium | Resolver implements only the enumerated predicates — plain `eq(arch,…)` (L122/131/140/147) and the compound `and(eq(arch,'amd64'),eq(installTestDeps,true))` (L69); `BUILD_BRANCH` is policy-set + documented; all else errors |
| `pure_tests` fails or diverges in-container due to `pkg/exec`/nsenter (FG1) | High | Medium | Default surfaces FG1 loudly; `--exclude-pkg-exec` reproduces `run-tests.sh`'s green set as a logged deviation; acceptance criterion revised to not claim parity |

## Security Considerations

- The runner only **reads** the pipeline YAML and executes shell that **already exists in the repo's
  pipeline** — it introduces no new privileged operations beyond what `run-tests.sh` already does
  (docker, `sudo` inside the throwaway container). It must **not** add the playground's no-TLS server
  flags or any auth-disabling behavior. No secrets/PATs are involved (unlike the self-hosted agent
  path, which would require a PAT).

## Open Questions

> **All resolved per maintainer direction (Rev 3).**

- **OQ1.** *(open, low-risk implementation choice)* Should `ado-local.py` **source** `run-tests.sh`
  functions (requires making the script sourceable without auto-dispatching `case "${1:-all}"`) or
  **re-invoke** `run-tests.sh shell`-style with a piped program? Sourcing is cleaner but is a
  (behavior-preserving) refactor of `run-tests.sh`. **Default:** re-invoke unless sourcing is trivially
  safe; either is acceptable as long as `run-tests.sh`'s CLI is preserved.
- **OQ2. → RESOLVED: option (ii).** The `install-dependencies.yml` deb-install `- script:` bodies are
  **sourced from the YAML** with the `DownloadPipelineArtifact` staging path **rewritten to
  `/sonic-debs`**, so the dependency-install logic is single-sourced from the pipeline (closes FG3).
- **OQ3. → RESOLVED: no.** Ship the executor, not just the drift-detector — the env-setup execution is
  the primary deliverable.
- **OQ4. → RESOLVED: yes.** Maintainers want this backup; Epics 1–3 are **ungated**.

## Implementation Phases

> **Rev 3:** OQ4 = yes, so all phases are **ungated**. Per maintainer direction the **env-setup
> execution sourced from `install-dependencies.yml`** (OQ2=ii, path → `/sonic-debs`) is pulled into
> Phase 0/1 as the primary deliverable; `playground` and other `run-tests.sh`-only features stay
> pass-through and out of scope.

- **Phase 0 — Spike/validation (read-only, no execution).** Build the loader + flattener + classifier
  and ship `list` + `explain` + `--dry-run` only. Exit criteria: for all five jobs, `explain` prints a
  correct kept/no-op/error breakdown and `--dry-run` emits a program a human agrees matches CI.
  **This phase alone is the recommended deliverable if OQ3=yes.**
- **Phase 1 — Executor for container jobs.** *(Gated behind OQ4=yes.)* Wire the emitted program
  through `run-tests.sh`'s `docker_run`. Exit criteria: `run integration_tests` and `run memleak_tests`
  produce the same `test-results/*.xml` / `coverage.xml` as `run-tests.sh integration`.
- **Phase 2 — Bare-image jobs + polish.** *(Gated behind OQ4=yes.)* `go_static_checks` (gofmt) and
  `pure_tests` in-container; `--honor-go-install` and `--exclude-pkg-exec` toggles; docs in
  `dev/SETUP.md`. Exit criteria: `run go_static_checks` fails on an intentionally mis-formatted file and
  passes on a clean tree; `run pure_tests` emits the JUnit XML from the **verbatim** `pure.mk` body
  **or**, with `--exclude-pkg-exec`, matches `run-tests.sh pure` — with FG1 reported either way. (Note:
  the verbatim run is **not** expected to equal `run-tests.sh pure`; see FG1.)
- **Phase 3 (optional) — `amd64` package build + drift CI hook.** *(Gated behind OQ4=yes.)* `run amd64`
  builds the deb; optionally a `make`/CI check that runs `explain` and fails if an unenumerated
  construct appears.

## Files Affected

### New Files
| File Path | Purpose |
|-----------|---------|
| `dev/ado-local.py` | Read-only ADO-YAML step extractor/driver (loader, flattener, resolver, classifier, emitter, executor) |
| `dev/local-ado-runner.plan.md` | This design document |
| `dev/local-ado-runner.decisions.md` | (optional) decision log mirroring the repo's `*.decisions.md` convention |

### Modified Files
| File Path | Changes |
|-----------|---------|
| `dev/SETUP.md` | Document the new `dev/ado-local.py` tool, its fidelity boundary, and that it never modifies the pipeline |
| `dev/run-tests.sh` | **Only if OQ1=source:** make it sourceable (guard the `case` dispatch behind `BASH_SOURCE`/`$0` check) — behavior-preserving; no CLI change. Otherwise unchanged. |

### Deleted Files
| File Path | Reason |
|-----------|--------|
| _(none)_ | This plan is strictly additive; it deletes nothing and modifies no pipeline files |

## Implementation Plan

> **Maintainer direction (Rev 3):** the environment setup is to be **sourced from
> `azure-pipelines.yml` / `install-dependencies.yml`** so it stays in sync in one place — this is the
> **primary Epic 1 deliverable** (OQ2 resolved to "rewrite path → `/sonic-debs`", OQ4 = yes, all Epics
> ungated). The `playground` and other `run-tests.sh`-only conveniences remain **pass-through
> parameters of `run-tests.sh`** and are **out of scope** for `ado-local.py` (it never sources or runs
> them).

### Epic 1 — Read-only extractor core + env-setup sourced from the YAML *(primary deliverable)* **(DONE)**
- **Goal:** Parse the unmodified pipeline + templates, expand templates, classify steps, emit the
  per-job program (`list`/`explain`/`--dry-run`), **and** execute the **environment-setup** step
  bodies sourced from `install-dependencies.yml` (deb install with the `DownloadPipelineArtifact`
  staging path rewritten to the cached `/sonic-debs` mount, plus the redis/pytest/protoc install
  bodies) inside `sonic-slave-trixie`, so the env bootstrap is single-sourced from the pipeline rather
  than duplicated in `run-tests.sh`.
- **Prerequisites:** `dev/run-tests.sh bootstrap` cache populated (provides `/sonic-debs`); Docker.
- **Tasks:**

  | Task ID | Type | Description | Files | Status |
  |---------|------|-------------|-------|--------|
  | E1-T1 | IMPL | YAML loader (RO) + template-include resolution (C1) | `dev/ado-local.py` | DONE |
  | E1-T2 | IMPL | Job selector + recursive step flattener with parameter binding (C2) | `dev/ado-local.py` | DONE |
  | E1-T3 | IMPL | Static variable/parameter resolver incl. the enumerated `${{ }}` predicates — plain `eq(arch,…)` and the compound `and(eq(arch,'amd64'),eq(installTestDeps,true))` (L69); FR7 errors on anything else (C3) | `dev/ado-local.py` | DONE |
  | E1-T4 | IMPL | Step classifier + emitter (`bash/script`→keep, checkout/`DownloadPipelineArtifact` download/publish→no-op, unknown→error) (C4/C5) | `dev/ado-local.py` | DONE |
  | E1-T5 | IMPL | `list`, `explain`, `--dry-run` CLI surface | `dev/ado-local.py` | DONE |
  | E1-T6 | IMPL | **Env-setup execution sourced from `install-dependencies.yml`**: keep the deb-install `- script:` bodies, rewrite the `DownloadPipelineArtifact` staging path → `/sonic-debs` (OQ2=ii), and run them plus the redis/pytest/protoc install bodies in-container via `run-tests.sh` `docker_run` | `dev/ado-local.py` | DONE |
  | E1-T7 | TEST | Unit tests: for each of the 5 jobs assert the emitted program + classification table (golden), including the rewritten `/sonic-debs` path in the env-setup body | `dev/ado-local.py` (or `dev/tests/`) | DONE |
  | E1-T8 | TEST | Negative tests: an injected unknown `task:`/`${{ }}` raises (FR7) | `dev/tests/` | DONE |

- **Acceptance Criteria:**
  - [x] `dev/ado-local.py list` lists the five jobs with their stages.
  - [x] `dev/ado-local.py explain integration_tests` shows every step as kept/no-op/error with source refs, including the env-setup deb-install body kept with the rewritten `/sonic-debs` path.
  - [x] `--dry-run` for all five jobs emits a program a maintainer confirms matches the CI commands.
  - [x] The env-setup bodies (deb `dpkg -i` via `/sonic-debs`, redis, pytest, protoc) are **sourced from `install-dependencies.yml`** and run successfully in-container — the bootstrap is not re-hardcoded in `ado-local.py`.
  - [x] An unenumerated construct causes a clear non-zero error (no silent skip).
  - [x] The tool never opens any pipeline file for writing (verified by test/inspection).
  - [x] `playground` and other `run-tests.sh`-only features are **not** referenced or invoked by `ado-local.py`.

- **Completion Notes (re-review fix pass, 2026-06-22):**
  - `cmd_run`: moved `print_explain()` before `assert_no_errors()` in `--explain` branch so the full classification table (including ERROR rows + source refs) is printed before exiting non-zero.
  - `_read_pure_packages`: added empty-list guard that raises `UnsupportedConstruct` to prevent silent behavior change if `pure.mk` structure changes.
  - `dev/run-tests.sh`: wrapped `case` dispatch in `BASH_SOURCE` guard so the script is sourceable by `ado-local.py` without executing a subcommand.
  - `test_ado_local.py`: added `test_run_explain_prints_table_before_error` negative test verifying stdout table content and exception propagation.

### Epic 2 — Executor for the container test jobs **(DONE)**
- **Goal:** Run the emitted program for the container test jobs (`integration_tests`, `memleak_tests`)
  end-to-end on top of the Epic 1 YAML-sourced env setup, and reproduce CI artifacts locally.
- **Prerequisites:** Epic 1; `dev/run-tests.sh bootstrap` cache.
- **Tasks:**

  | Task ID | Type | Description | Files | Status |
  |---------|------|-------------|-------|--------|
  | E2-T1 | IMPL | Decide OQ1 and wire executor to `run-tests.sh` (`docker_run`) | `dev/ado-local.py` (+maybe `dev/run-tests.sh` sourcing guard) | DONE — OQ1=source (BASH_SOURCE guard from Epic 1); `cmd_run` sources `run-tests.sh`, calls `require_cache`, then `docker_run -t bash -c <program>` |
  | E2-T2 | IMPL | `run integration_tests` + `run memleak_tests` (container jobs) on the YAML-sourced env setup | `dev/ado-local.py` | DONE — both container jobs extract + emit + execute via the generic `cmd_run` path |
  | E2-T3 | IMPL | Honor the mgmt-common build `- script:` body from the YAML; log the full env→build→test mapping in `--explain` | `dev/ado-local.py` | DONE — build body kept; `print_explain` now tags each kept step with its `env`/`build`/`test` phase and prints an env→build→test mapping |
  | E2-T4 | TEST | Compare `run integration_tests` artifacts vs `run-tests.sh integration` (same `test-results/*.xml`) | `dev/tests/` | DONE |

- **Acceptance Criteria:**
  - [x] `run integration_tests` produces `junit-integration-{basic,env,dialout}.xml` + `coverage.xml`.
  - [x] `run memleak_tests` produces `junit-memleak-standard.xml`.
  - [x] If OQ1=source, `dev/run-tests.sh`'s existing subcommands still behave identically.

### Epic 3 — Bare-image jobs, packaging, docs, drift hook
- **Goal:** Cover `go_static_checks` + `pure_tests` + `amd64`; document; optional drift CI check.
- **Prerequisites:** Epics 1–2.
- **Tasks:**

  | Task ID | Type | Description | Files | Status |
  |---------|------|-------------|-------|--------|
  | E3-T1 | IMPL | `run go_static_checks` (gofmt body) + `run pure_tests` in-container (verbatim `pure.mk` body by default; `--honor-go-install`, `--exclude-pkg-exec` toggles); surface FG1 | `dev/ado-local.py` | TO DO |
  | E3-T2 | IMPL | `run amd64` (deb build body → `dev/build-out/`) | `dev/ado-local.py` | TO DO |
  | E3-T3 | IMPL | Document tool + fidelity boundary in `dev/SETUP.md` | `dev/SETUP.md` | TO DO |
  | E3-T4 | IMPL | (optional) drift check that runs `explain` and fails on unenumerated constructs | `dev/ado-local.py` / CI-local hook | TO DO |
  | E3-T5 | TEST | gofmt gate fails on a dirtied file, passes clean; `pure_tests` verbatim body emits JUnit XML (FG1 reported); `--exclude-pkg-exec` matches `run-tests.sh pure` | `dev/tests/` | TO DO |

- **Acceptance Criteria:**
  - [ ] `run go_static_checks` mirrors the CI gofmt result on clean and dirty trees.
  - [ ] `run pure_tests` (default) runs the **verbatim** `make -f pure.mk junit-xml` body and reports
        FG1; it is **not** required to equal `run-tests.sh pure`. With `--exclude-pkg-exec` it matches
        `run-tests.sh pure`'s package set.
  - [ ] `run amd64` yields a `sonic-gnmi_*.deb` in `dev/build-out/`.
  - [ ] `dev/SETUP.md` states the tool is read-only and lists the honored vs. skipped pipeline parts incl. FG1–FG5.

## References
- `azure-pipelines.yml` (this repo) — stages/jobs/variables/resources.
- `.azure/templates/{install-go,install-dependencies,setup-test-env,build-deb}.yml` (this repo).
- `dev/run-tests.sh` — dockerized cache-backed driver (execution backend).
- `dev/dedupe-ci-dev.plan.md` — primary drift-removal approach (this plan is its backup).
- `dev/local-dev-runner.plan.md`, `dev/local-ci-driver.plan.md` — baseline dev/CI driver designs.
- Microsoft Learn — *Self-hosted agents* (agent must register against an org/pool with a PAT; confirms
  the self-hosted agent is not an offline local runner).
- Microsoft Learn — *Azure Pipelines YAML schema*, template expressions `${{ }}`, runtime macros `$( )`,
  `condition:`, `DownloadPipelineArtifact@2`, `PublishTestResults@2`, `PublishCodeCoverageResults@2`.
- `nektos/act` — GitHub Actions local runner (the analogue that has **no** maintained ADO equivalent).
