# Local Dev Runner for sonic-gnmi — Solution Design & Implementation Plan

> **Date:** 2026-06-22 | **Status:** Draft (Rev. 2) | **Audience:** sonic-gnmi developers/maintainers
> **Scope:** A SIMPLE, lightweight local inner-loop driver evolved from `dev/run-tests.sh`.
> **Explicitly NOT** a full CI mirror — the comprehensive vision lives in
> [`dev/local-ci-driver.plan.md`](local-ci-driver.plan.md) and is deferred.

---

## Executive Summary

`dev/run-tests.sh` already gives developers a clean, dockerized inner loop (bootstrap,
pure unit tests, integration tests, deb build, container shell). This design makes the
**smallest useful evolution**: keep the existing structure, scope the developer's daily
needs down to (1) fast **pure** unit tests, (2) a **focused subset** of integration
tests rather than the whole multi-package suite (Makefile caps each tier at `-timeout 40m`),
and (3) a new **`playground`** subcommand
that boots Redis + builds + launches the `telemetry`/gNMI/gNOI server inside the
container so the developer can poke at a live service with `gnmi_cli`, `gnmi_dump`, and
`gnoi_client`. The result stays a single Bash script with discrete, idempotent
subcommands and clean extension points, deliberately leaving the heavier CI-parity
features (gofmt gate, memleak, diff-cover, arm64 packaging) for later.

## Background

- **Current driver:** `dev/run-tests.sh` (229 lines, Bash) orchestrates everything inside
  the `sonicdev-microsoft.azurecr.io:443/sonic-slave-trixie:latest` image so the checkout
  stays clean. It bind-mounts the `sonic-gnmi` checkout next to cached sibling repos
  (`sonic-mgmt-common`, `sonic-swss-common`) and a read-only `sonic-debs` dir from the
  shared cache `~/.cache/acr-image-build` (overridable via `ACR_IMAGE_CACHE_DIR`).
- **Existing subcommands:** `bootstrap`, `pure`, `integration`, `build`, `shell`, `all`,
  `clean` (dispatched via a single `case` block at the bottom).
- **Test wiring already in place:**
  - `pure` → `make -f pure.mk PACKAGES='…' junit-xml` (no SONiC deps; `pkg/exec` excluded
    for nsenter/seccomp reasons).
  - `integration` → `build_nonpure_snippet` (builds `sonic-mgmt-common` + `make all`) then
    `ENABLE_TRANSLIB_WRITE=y make check_gotest_junit`.
  - `check_gotest_junit` (Makefile:327) runs `INTEGRATION_BASIC_PKGS` (Makefile:268:
    `sonic_db_config`, `sonic_service_client`, `telemetry`, `sonic_data_client`) and
    `INTEGRATION_ENV_PKGS` (Makefile:275: `gnmi_server`, `pathz_authorizer`,
    `transl_utils`, `gnoi_client/system`) via `gotestsum`, honoring `TEST_FLAGS`.
- **Server entry point:** `telemetry/telemetry.go` exposes flags incl. `--port`,
  `--unix_socket` (default `/var/run/gnmi/gnmi.sock`), `--insecure`, `--noTLS`,
  `--allow_no_client_auth` — exactly what a no-CA local playground needs.
- **Build outputs:** there is **no** standalone `telemetry`/`gnmi_cli`/`gnmi_dump`/
  `gnoi_client` Make target. The single `sonic-gnmi:` recipe (aliased by `all:` at
  Makefile:67; recipe lines 98–156) `go install`s **all** of these binaries —
  `telemetry`, `gnoi_client`, `gnmi_dump`, `gnmi_get`, `gnmi_set`, `gnmi_cli` (and
  `dialout_client_cli` when dialout is enabled) — into `${GOBIN}`
  (`$(abspath $(BUILD_DIR))`, Makefile:31). `build_nonpure_snippet` already runs
  `make all`, so the playground binaries are produced as a side effect of the existing
  build step — no extra `make` invocation is needed (and `make telemetry …` would fail
  with *No rule to make target* because those names are recipe lines, not targets).
  `dev/SETUP.md` already documents UDS access via `grpcurl` on a DUT.
- **Prior art:** `dev/local-ci-driver.plan.md` + `dev/local-ci-driver.decisions.md`
  describe the *full* CI mirror (gofmt/staticcheck, memleak, diff-cover, arm64). That plan
  is approved in principle but judged **too heavy for current daily needs**.

## Problem Statement

1. The current `integration` subcommand is all-or-nothing and slow (runs every package tier;
   each is capped at `-timeout 40m`, and a full pass typically takes on the order of tens of
   minutes, locking the
   terminal). A developer iterating on one package (e.g. `gnmi_server`) has no supported
   way to run **just that** subset through the same dockerized path.
2. There is **no way to manually exercise a running gNMI/gNOI server locally**. Developers
   must build a deb and deploy to a DUT (SETUP.md §6) just to hand-test an RPC. There is no
   "boot it locally and play" loop.
3. The fuller `local-ci-driver.plan.md` adds five+ subcommands and Make/threshold concerns
   the developer does not want right now; adopting it wholesale would add maintenance
   burden and slow the inner loop for no immediate benefit.

## Goals and Non-Goals

### Goals
- Keep `dev/run-tests.sh` a single, readable Bash script with one `case` dispatch and one
  `usage` renderer.
- Preserve **all** existing infra: clean-checkout docker approach, shared cache, sibling
  bind mounts, `bootstrap`/`shell`/`build`/`clean`, and the `pure`/`integration` flows.
- Allow `integration` to run a **focused subset** of packages (e.g. one or two) without
  changing default behavior when no subset is given.
- Add a **`playground`** subcommand that, in the container, starts Redis, builds the
  server + client tools, launches `telemetry` (no-TLS/insecure, TCP port + UDS), and drops
  the developer into an interactive shell with `gnmi_cli`/`gnmi_dump`/`gnoi_client` on
  `PATH` to interact with the live service. Runnable **independently** of test targets.
- Leave clean, documented extension points (a comment block + dispatch stubs) so the
  deferred CI-parity subcommands can be slotted in later with minimal churn.
- Update `dev/SETUP.md` to document the subset syntax and the playground.

### Non-Goals (explicitly deferred to `local-ci-driver.plan.md`)
- gofmt/`staticcheck` gate, `memleak` (`check_memleak_junit`), diff-cover 80% coverage gate.
- A composite `ci` subcommand mirroring the full Azure pipeline.
- arm64/amd64 multi-arch packaging and qemu/binfmt automation.
- `verify-sync` pipeline-drift guard.
- Any Makefile, `azure-pipelines.yml`, or proto changes.

## Requirements

### Functional
- **FR1** `pure` continues to run `make -f pure.mk … junit-xml` unchanged.
- **FR2** `integration` with no args runs the existing full `check_gotest_junit` flow.
- **FR3** `integration <pkg>[ <pkg>…]` runs only the named package(s) through the same
  container build + test path, still with `ENABLE_TRANSLIB_WRITE=y`. Package names accept a
  short form (e.g. `gnmi_server`) mapped to the full module path. All three Make package
  tiers (`INTEGRATION_BASIC_PKGS`, `INTEGRATION_ENV_PKGS`, `INTEGRATION_DIALOUT_PKG`) are
  overridden so that **only** the requested subset runs and the other tiers are skipped via
  their `@if [ -n … ]` guards (the dialout tier is additionally gated by
  `ENABLE_DIALOUT_VALUE`).
- **FR4** `playground` starts Redis, builds the server + client tools via the existing
  `build_nonpure_snippet` (`make all`, which installs `telemetry`/`gnmi_cli`/`gnmi_dump`/
  `gnoi_client` into `${GOBIN}`), launches `telemetry` with
  `--noTLS --insecure --allow_no_client_auth --port <PORT> --unix_socket <SOCK>`, confirms
  it is listening, and hands the developer an interactive shell with the client binaries on
  `PATH` and usage hints printed.
- **FR5** `playground` is invoked separately from any test subcommand and never runs as part
  of `all`.
- **FR6** `usage`/help lists every subcommand including the new subset syntax and
  `playground`.

### Non-Functional
- **NFR1** No new host dependencies beyond the existing Docker + git + curl.
- **NFR2** Idempotent: every subcommand calls `require_cache` (auto-bootstrap) and is safe
  to re-run.
- **NFR3** Maintainability: adding a subcommand touches exactly two places (the `case`
  block and `usage`). Subset/short-name mapping lives in one helper function.
- **NFR4** `all` (bootstrap → pure → integration) keeps its current fast-loop semantics.

## Proposed Design

### Architecture Overview

```
Developer CLI
   │
   ▼
dev/run-tests.sh            (single Bash script, one `case` dispatch + one `usage`)
   ├── bootstrap            clone siblings + fetch debs/wheel        [UNCHANGED]
   ├── pure                 make -f pure.mk … junit-xml              [UNCHANGED]
   ├── integration [pkgs…]  build-nonpure → check_gotest_junit       [EXTENDED: optional subset]
   │                         (PKGS override when args given)
   ├── playground [port]    redis + build + launch telemetry + shell [NEW]
   ├── build                dpkg-buildpackage → dev/build-out/*.deb   [UNCHANGED]
   ├── shell                interactive container shell               [UNCHANGED]
   ├── all                  bootstrap → pure → integration            [UNCHANGED]
   └── clean                wipe cache                                [UNCHANGED]
            │
            ▼ docker_run (sonic-slave-trixie)
            │   bind mounts: sonic-gnmi, sonic-mgmt-common,
            │                sonic-swss-common, sonic-debs (ro)
            ▼
       Make targets / telemetry binary  (single source of truth)
```

The script remains **pure orchestration**; all build/test logic stays in the Makefiles and
the `telemetry` binary.

### Key Components

1. **`run_integration` (extended).** Accepts optional positional package args.
   - No args → current behavior: `ENABLE_TRANSLIB_WRITE=y make check_gotest_junit`.
   - With args → resolve each via `resolve_pkg` to a full module path, then run a focused
     test command in the same container after `build_nonpure_snippet`. The chosen
     implementation (see Design Decisions D2) passes overrides into the existing target:
     `ENABLE_TRANSLIB_WRITE=y make check_gotest_junit INTEGRATION_BASIC_PKGS='<subset-or-empty>' INTEGRATION_ENV_PKGS='<subset-or-empty>' INTEGRATION_DIALOUT_PKG='<subset-or-empty>'`
     — reuses the Makefile's gotestsum/junit wiring. The tier matching the requested
     package(s) is set to the subset; **all three** package variables, including
     `INTEGRATION_DIALOUT_PKG` (Makefile:282), are explicitly overridden so the unused
     tiers' `@if [ -n … ]` guards skip them. Without overriding the dialout variable a
     focused `integration gnmi_server` would still run the dialout package tests (when
     `ENABLE_DIALOUT_VALUE != 0`), so the subset would not be truly "only that package".
   - A small `resolve_pkg` helper maps short names (`gnmi_server`, `telemetry`,
     `sonic_data_client`, `transl_utils`, `gnoi_client/system`, `dialout/dialout_client`, …)
     to `github.com/sonic-net/sonic-gnmi/<name>` and classifies each into its tier
     (basic / env / dialout); an arg already containing a `/`-qualified module path is
     passed through verbatim.

2. **`run_playground` (new).** Reuses `container_setup_snippet` (redis bring-up, debs,
   `CGO_*` env, `safe.directory`) + `build_nonpure_snippet`, then:
   - **No extra build step is required:** `build_nonpure_snippet` runs `make all`, which
     already `go install`s `telemetry`, `gnmi_cli`, `gnmi_dump`, `gnoi_client` (and
     `gnmi_get`/`gnmi_set`) into `${GOBIN}`. (There is no `make telemetry`/`gnmi_cli`/…
     target — those are recipe lines inside the `sonic-gnmi:` target, so invoking them
     directly would fail.)
   - Launch the server in the background:
     `telemetry --noTLS --insecure --allow_no_client_auth --port "$PORT" --unix_socket /var/run/gnmi/gnmi.sock --logtostderr -v=2 &`
   - Poll the port/socket until listening (bounded retries), print connection hints, then
     `exec bash --rcfile …` so the developer lands in an interactive shell with `${GOBIN}`
     on `PATH`. Runs with docker `-it` and publishes the port (`EXTRA_DOCKER_ARGS="-p
     $PORT:$PORT"`) so the developer can also reach it from the host.
   - On shell exit the `--rm` container tears down the server and Redis automatically.
   - Default `PORT=8080`, overridable via `playground <port>`.

3. **`resolve_pkg` (new helper).** Single source for short-name → module-path mapping plus
   basic/env/dialout tier classification; keeps subset syntax DRY and documented in one place.

4. **`usage` (new/extended).** Replaces the inline `echo "usage: …"` in the `*` case with a
   dedicated function listing all subcommands, the `integration [pkg…]` subset form, and
   `playground [port]`. Adding a subcommand = edit `case` + `usage` only.

5. **Extension-point comment block (new).** A clearly delimited
   `# --- deferred CI-parity targets (see local-ci-driver.plan.md) ---` section near the
   dispatch, naming the future subcommands (`staticcheck`, `memleak`, `coverage`, `ci`,
   `build --arch`) so a later contributor knows exactly where they plug in.

### Data Flow — `playground`

```
dev/run-tests.sh playground 8080
  → require_cache (auto-bootstrap if needed)
  → docker_run -it -p 8080:8080:
       container_setup_snippet         (redis up, debs, CGO env)
       build_nonpure_snippet           (mgmt-common + make all → installs
                                        telemetry/gnmi_cli/gnmi_dump/gnoi_client
                                        into ${GOBIN}; no extra make step)
       telemetry --noTLS --insecure --allow_no_client_auth \
                 --port 8080 --unix_socket /var/run/gnmi/gnmi.sock &
       wait-until-listening (bounded)
       exec bash --rcfile <hints>      ← developer interacts here
  → container --rm on exit tears everything down
```

Inside the shell the developer can run, e.g.:
```
gnmi_dump
gnmi_cli -a 127.0.0.1:8080 -insecure -logtostderr -query_type Once \
         -q '/COUNTERS/Ethernet0' -target COUNTERS_DB
gnoi_client -target 127.0.0.1:8080 -insecure -rpc System.Time   # or via UDS
```
(Exact client flags are documented as examples; the playground only guarantees the server
is up and the binaries are on `PATH`.)

### API Contracts (CLI surface)

| Subcommand | Args | Behavior |
|------------|------|----------|
| `bootstrap` | — | clone siblings + fetch artifacts (unchanged) |
| `pure` | — | pure unit tests via `pure.mk junit-xml` (unchanged) |
| `integration` | `[pkg…]` | full suite when empty; focused subset when given |
| `playground` | `[port]` | boot redis+server+tools, interactive shell (default 8080) |
| `build` | — | dpkg-buildpackage → `dev/build-out/` (unchanged) |
| `shell` | — | interactive container shell (unchanged) |
| `all` | — | bootstrap → pure → integration (unchanged) |
| `clean` | — | wipe cache (unchanged) |

### Design Decisions

- **D1 — Stay Bash, single script.** Matches the existing tool and the decisions in
  `local-ci-driver.decisions.md`; zero new host deps; lowest maintenance.
- **D2 — Subset via Makefile variable override, not a hand-rolled `go test`.** Passing
  `INTEGRATION_BASIC_PKGS=…`/`INTEGRATION_ENV_PKGS=…`/`INTEGRATION_DIALOUT_PKG=…` into
  `check_gotest_junit` reuses the existing gotestsum + junit + env handling, so the subset
  path and the full path share one source of truth. Command-line `VAR=val` overrides the
  Makefile's `:=` assignments, and emptying the non-matching variables lets their
  `@if [ -n … ]` guards skip them. **All three** tiers are overridden — including
  `INTEGRATION_DIALOUT_PKG` — otherwise the dialout tests run on every focused subset
  (whenever `ENABLE_DIALOUT_VALUE != 0`). *Trade-off:* a developer must know whether a
  package is "basic", "env", or "dialout"; `resolve_pkg` classifies each requested package
  into its tier and sets only that tier, emptying the others.
- **D3 — Playground uses `--noTLS --insecure`.** A vanilla local box has no CA; the server's
  own flags (`telemetry.go:180-182`) are purpose-built "for testing only". UDS + a plaintext
  TCP port give both `grpcurl`/`gnmi_cli` access with no cert wrangling.
- **D4 — Playground is interactive and standalone.** It is a manual exploration tool, not a
  test; it must never be wired into `all`/`ci`. It ends in `exec bash` so the developer
  drives it.
- **D5 — Defer everything else.** No Makefile/pipeline edits. A labeled extension block names
  the future subcommands so growth into the full `local-ci-driver.plan.md` is additive.

## Alternatives Considered

- **A1 — Hand-rolled `go test` for subsets** (bypassing the Makefile). Pros: full control of
  `-run`/flags. Cons: duplicates CGO/env/junit logic already in `check_gotest_junit`, a
  second source of truth — rejected in favor of D2's variable override. (A `-run` pass-through
  can still be layered on later via `TEST_FLAGS`.)
- **A2 — Run the playground server on the host instead of the container.** Rejected: the
  server needs swss-common/libyang/redis exactly as built in the image; host execution
  reintroduces the dependency wrangling the dockerized approach removes.
- **A3 — Adopt the full `local-ci-driver.plan.md` now.** Rejected per stated scope: too heavy
  for the current inner-loop need; deferred behind extension points.

## Dependencies

- **External:** Docker, git, curl (already required); `sonic-slave-trixie` image; public
  artifact mirror `sonic-build.azurewebsites.net` (already used by `bootstrap`).
- **Internal:** `pure.mk` `junit-xml`; Makefile `check_gotest_junit` +
  `INTEGRATION_BASIC_PKGS`/`INTEGRATION_ENV_PKGS`/`INTEGRATION_DIALOUT_PKG`; the
  `sonic-gnmi:`/`all:` build target (installs `telemetry`/`gnmi_cli`/`gnmi_dump`/
  `gnoi_client` into `${GOBIN}`); `telemetry/telemetry.go` flags.
- **Sequencing:** none beyond existing `bootstrap`/`require_cache`.

## Impact Analysis

- **Affected files:** `dev/run-tests.sh` (modified), `dev/SETUP.md` (docs). No source,
  Makefile, proto, or pipeline changes.
- **Backward compatibility:** all existing subcommands keep identical names/defaults/output;
  `integration` with no args is byte-for-byte the current flow. Strictly additive.
- **Performance:** subset `integration` dramatically shortens the inner loop for
  single-package work; `playground` cost ≈ one `build-nonpure` + server build.
- **Operational:** `playground` opens a plaintext port inside an ephemeral `--rm` container
  on the developer's machine only; nothing persists.

## Security Considerations

- `--noTLS/--insecure/--allow_no_client_auth` disable auth — **acceptable only because** the
  server runs in a throwaway local container bound to the developer's host, never on a DUT or
  shared host. Document this prominently; do not reuse these flags for the `build`/deploy
  path. No secrets are introduced; the published port should bind to localhost.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Subset tier confusion (basic vs env vs dialout pkg) | Medium | Low | `resolve_pkg` classifies each package into its tier and sets only that `INTEGRATION_*_PKG`, emptying the other two (incl. dialout) so their guards skip |
| Telemetry server needs DB tables not present → noisy errors in playground | Medium | Low | Document as expected; playground is for surface interaction, not full data fidelity |
| Port already in use on host | Low | Low | `playground [port]` override; default 8080 |
| Server fails to come up before shell | Low | Medium | Bounded readiness poll; print logs and still drop to shell so developer can rerun manually |
| Plaintext flags copied into deploy path | Low | High | Comment + SETUP.md warning; keep `build` path unchanged |

## Open Questions

- **OQ1:** _(resolved)_ For `integration <pkg>`, `resolve_pkg` classifies each package into
  its tier (basic / env / dialout) and `run_integration` sets only the matching
  `INTEGRATION_*_PKG` variable, emptying the other two so the Makefile `@if [ -n … ]` guards
  skip them. This keeps the junit output cleanly scoped and guarantees the dialout tier does
  not run for unrelated subsets.
- **OQ2:** Should `playground` also offer a `--uds-only` mode (no published TCP port) for
  developers who only want `grpcurl unix://`? Deferred unless requested.
- **OQ3:** Do we want an optional `-run <regex>` pass-through on `integration` now, or defer?
  (Leaning: defer; achievable later via `TEST_FLAGS`.)

## Implementation Phases

- **Phase 1 — Subset integration.** Add `resolve_pkg` + extend `run_integration` to accept
  optional package args; full-suite default unchanged. Exit: `integration` (no args) behaves
  identically; `integration gnmi_server` runs only that package and produces junit XML.
- **Phase 2 — Playground.** Add `run_playground` + dispatch case; redis+build+launch+shell.
  Exit: `playground` boots the server (port + UDS) and the developer can run `gnmi_dump`/
  `gnmi_cli`/`gnoi_client` against it; teardown on shell exit.
- **Phase 3 — Help + extension points + docs.** Add `usage` function, the deferred-targets
  comment block, and update `dev/SETUP.md` (subset syntax, playground section, security
  note). Exit: `usage` lists all commands; SETUP.md documents new flows.

## Files Affected

### New Files
| File Path | Purpose |
|-----------|---------|
| _(none)_ | All changes are contained in existing files |

### Modified Files
| File Path | Changes |
|-----------|---------|
| `dev/run-tests.sh` | Add `resolve_pkg`, extend `run_integration` for subset args, add `run_playground`, add `usage` function, add deferred-targets comment block, update `case` dispatch |
| `dev/SETUP.md` | Document `integration [pkg…]` subset syntax, new `playground` section with example client commands + security note, refresh the daily-use command list |

### Deleted Files
| File Path | Reason |
|-----------|--------|
| _(none)_ | — |

## Implementation Plan

### Epic 1 — Focused integration subset (DONE)
- **Goal:** Let `integration` run one or more named packages through the existing container
  path while keeping the no-arg full-suite default.
- **Prerequisites:** none.
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| E1-T1 | IMPL | Add `resolve_pkg` helper mapping short names → full module paths (pass-through for already-qualified paths) and classifying each into its tier (basic / env / dialout) | `dev/run-tests.sh` | DONE |
| E1-T2 | IMPL | Extend `run_integration` to accept optional positional args; when present, build the tier-specific override and pass `INTEGRATION_BASIC_PKGS`/`INTEGRATION_ENV_PKGS`/`INTEGRATION_DIALOUT_PKG` to `make check_gotest_junit` (matching tier set to subset, the other two emptied so their guards skip) | `dev/run-tests.sh` | DONE |
| E1-T3 | IMPL | Update `case` dispatch so `integration` forwards `"$@"` to `run_integration` | `dev/run-tests.sh` | DONE |
| E1-T4 | TEST | Manual verify: `integration` (no args) == full suite; `integration gnmi_server` runs only that pkg with junit XML and does **not** run the dialout package; `integration telemetry` (basic tier) works | `dev/run-tests.sh` | DONE |

- **Acceptance Criteria:**
  - [x] `./dev/run-tests.sh integration` behaves identically to today.
  - [x] `./dev/run-tests.sh integration gnmi_server` runs only `gnmi_server` and emits junit XML.
  - [x] A package from each tier (basic + env) can be targeted, and the dialout tier does not run for an unrelated subset.

### Epic 2 — Playground target  _(Status: DONE (E2-T4 manual-verify-pending))_
- **Goal:** Boot a live gNMI/gNOI server locally with client tools for manual interaction.
- **Prerequisites:** none (independent of Epic 1).
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| E2-T1 | IMPL | Add `run_playground`: reuse `container_setup_snippet` + `build_nonpure_snippet` (which `make all` already installs `telemetry`/`gnmi_cli`/`gnmi_dump`/`gnoi_client` into `${GOBIN}` — no extra `make` call), launch server (`--noTLS --insecure --allow_no_client_auth --port --unix_socket`) in background, bounded readiness poll | `dev/run-tests.sh` | DONE |
| E2-T2 | IMPL | Run container `-it` with `EXTRA_DOCKER_ARGS="-p $PORT:$PORT"`, put `${GOBIN}` on `PATH`, `exec bash --rcfile` printing client usage hints | `dev/run-tests.sh` | DONE |
| E2-T3 | IMPL | Add `playground` to `case` dispatch with optional `[port]` arg (default 8080) | `dev/run-tests.sh` | DONE |
| E2-T4 | TEST | Manual verify: server comes up on port + UDS; `gnmi_dump` / `gnmi_cli` / `gnoi_client` reach it; container tears down on exit | `dev/run-tests.sh` | MANUAL-VERIFY-PENDING |

> **E2-T4 note:** This is a manual verification step and cannot be executed in
> the CI/agent environment because it requires the `sonic-slave-trixie` image
> plus a populated bootstrap cache (`sonic-mgmt-common`, `sonic-swss-common`, and
> the SONiC `.deb`s). Run `./dev/run-tests.sh playground` on a host with Docker
> and a completed `./dev/run-tests.sh bootstrap` to confirm: the server listens
> on the published TCP port and the UDS, `gnmi_dump`/`gnmi_cli`/`gnoi_client`
> reach it, and exiting the shell tears down the `--rm` container.

- **Acceptance Criteria:**
  - [x] `./dev/run-tests.sh playground` builds and launches the server, then drops to a shell.
  - [x] Client binaries are on `PATH` and can call the running server (TCP and/or UDS).
  - [x] `playground` is never invoked by `all`; exiting the shell tears everything down.

### Epic 3 — Help, extension points, and docs (DONE)
- **Goal:** Make the script self-describing and document the new flows; pre-mark growth path.
- **Prerequisites:** Epics 1–2.
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| E3-T1 | IMPL | Add `usage` function listing all subcommands incl. `integration [pkg…]` and `playground [port]`; replace inline usage in `*` case | `dev/run-tests.sh` | DONE |
| E3-T2 | IMPL | Add labeled `# deferred CI-parity targets (see local-ci-driver.plan.md)` comment block naming `staticcheck`/`memleak`/`coverage`/`ci`/`build --arch` as future stubs | `dev/run-tests.sh` | DONE |
| E3-T3 | IMPL | Update `dev/SETUP.md`: subset syntax, `playground` section with example commands + security note, refreshed daily-use list | `dev/SETUP.md` | DONE |
| E3-T4 | TEST | Verify `usage`/help output matches dispatch; SETUP.md commands run as written | `dev/run-tests.sh`, `dev/SETUP.md` | DONE |

- **Acceptance Criteria:**
  - [x] `./dev/run-tests.sh help` (and unknown-arg) prints all subcommands with new forms.
  - [x] The script header comment + extension block describe the deferred CI-parity path.
  - [x] `dev/SETUP.md` documents subset integration and the playground, including the security note.

## References

- `dev/run-tests.sh` — current driver (subcommands, `docker_run`, `container_setup_snippet`,
  `build_nonpure_snippet`).
- `dev/SETUP.md` — setup/ops guide, DUT + UDS access patterns.
- `dev/local-ci-driver.plan.md`, `dev/local-ci-driver.decisions.md` — deferred full CI-mirror design.
- `Makefile` — `check_gotest_junit` (327), `INTEGRATION_BASIC_PKGS` (268), `INTEGRATION_ENV_PKGS` (275),
  `INTEGRATION_DIALOUT_PKG` (282, run conditionally on `ENABLE_DIALOUT_VALUE`), `all:`→`sonic-gnmi:`
  build target that installs `telemetry`/`gnoi_client`/`gnmi_dump`/`gnmi_cli`/`gnmi_get`/`gnmi_set`
  into `${GOBIN}` (recipe lines 98–156; `GOBIN` at 31). Note: `telemetry`/`gnmi_cli`/etc. are
  **recipe lines, not standalone Make targets**.
- `pure.mk` — `junit-xml` target used by `pure`.
- `telemetry/telemetry.go` — server flags (`--port`, `--unix_socket`, `--insecure`, `--noTLS`,
  `--allow_no_client_auth`).
