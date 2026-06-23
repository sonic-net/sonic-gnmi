# sonic-gnmi CI/Dev Tooling — Design & Decisions

This document is the single, final-state design record for the CI/dev tooling on
the `donghaoli/new-setup2` branch. It supersedes and merges the five intermediate
design documents produced during the effort (the two dedup/dev-runner plan +
decision pairs plus a lone CI-script-extraction plan). It describes the
architecture **as it exists now**, not the iterations that produced it.

For day-to-day usage of the local developer runner, see the companion
[`dev/SETUP.md`](SETUP.md) (user-facing how-to). This document covers the
*architecture and rationale*; `SETUP.md` covers *how to run it*. The split is
intentional (see DD5). The `consolidation.plan.md` / `consolidation.decisions.md`
pair tracks the consolidation work itself and is retained as a project record.

---

## 1. Overview

The Azure DevOps (ADO) pipeline logic for sonic-gnmi was lifted out of the YAML
templates into reusable shell scripts under `scripts/`, and a dockerized local
developer runner (`dev/run-tests.sh`) was added so contributors can reproduce CI
steps on a workstation. After the extraction was functionally complete, a
behavior-preserving consolidation pass removed the remaining rough edges:

- **Cosmetic drift** — descriptive comments stripped from the ADO templates during
  extraction were restored so `master..HEAD` contains only functional changes.
- **Duplicated dependency data** — package names, versions, download globs, and the
  artifact mirror URL, previously declared in three or more places, were
  centralized into a single `scripts/deps-manifest.sh`.
- **Two overlapping build scripts** — `build-mgmt-common.sh` and `build-gnmi-deb.sh`
  were folded into one `scripts/build-deb.sh` with subcommands.
- **Internal duplication in the dev runner** — shared container/interactive
  scaffolding in `dev/run-tests.sh` was factored into helpers.
- **Doc sprawl** — five intermediate design documents were merged into this file.

**Invariant:** No CI behavior changes. Every rendered ADO command remains
byte-equivalent in effect to `origin/master`. ADO behavior is the authoritative
contract; the consolidation is lift-and-shift only.

---

## 2. Architecture

```
                  ┌──────────────────────────────┐
                  │   scripts/deps-manifest.sh    │  ← single source of truth
                  │   (names · versions · globs   │     for all dependency DATA
                  │    · ARTIFACTS_URL)           │
                  └───────────────┬──────────────┘
          sourced by              │             sourced by
   ┌──────────────────────────────┼──────────────────────────────┐
   ▼                              ▼                              ▼
 install-swsscommon.sh      (guard test)                   dev/run-tests.sh
 (ADO + dev install path)   test_deps_manifest.sh          (bootstrap: DEB_TARGETS,
                                                            ARTIFACTS_URL)
   ▲
   │  patterns guard test (asserts set-equality with the manifest)
   │
 .azure/templates/install-dependencies.yml
 (literal `patterns:` globs — byte-identical to master)

                  ┌──────────────────────────────┐
                  │   scripts/build-deb.sh        │  mgmt-common | gnmi
                  └───────────────┬──────────────┘
        called by: build-deb.yml · setup-test-env.yml · dev/run-tests.sh
```

- **Deployment model:** ADO pipeline + local Docker container (`sonic-slave-trixie`);
  no new infrastructure.
- **Data flow:** the manifest is sourced as POSIX `sh`; no network calls at render
  time.
- **ADO behavior:** rendered commands are byte-equivalent to `origin/master`.

---

## 3. Key Components

### C1 — `scripts/deps-manifest.sh` (dependency SSOT)

A POSIX-`sh` data file with **no side effects when sourced**. It declares:

- Versioned package basenames: `LIBYANG3_VER`, `LIBNL_VER`, `SWSSCOMMON_VER`,
  `YANG_MODELS_VER`.
- The artifact mirror base URL: `ARTIFACTS_URL`.
- Accessor functions:
  - `deps_swsscommon_debs <arch>` — emits the exact swss-common deb basenames
    `install-swsscommon.sh` installs for `<arch>` (`python3-swsscommon` is
    amd64-only).
  - `deps_bootstrap_targets` — emits the artifact target paths the dev runner
    downloads (`DEB_TARGETS`); the libnl `+` is URL-encoded as `%2B` for the
    mirror query.
  - `deps_download_globs` — emits the deb/wheel glob patterns the ADO
    `install-dependencies.yml` `patterns:` blocks must match, including the
    download-only `libpcre*` family.

**Consumers:** `scripts/install-swsscommon.sh` (version/filenames), `dev/run-tests.sh`
(`ARTIFACTS_URL` + `DEB_TARGETS`), and `scripts/test_deps_manifest.sh` (guard).
`install-debs.sh` / `install-yang-models.sh` glob a passed directory and hold no
version literals, so they do not source the manifest.

### C2 — ADO `patterns:` guard (`scripts/test_deps_manifest.sh`)

ADO compile-time YAML `patterns:` are render-time literals and **cannot** source a
shell file. The manifest is therefore declared canonical and a guard test parses
the `patterns:` block from `install-dependencies.yml` and asserts set-equality with
`deps_download_globs`. This keeps the rendered YAML byte-for-byte identical to
master while preventing future drift.

### C3 — `scripts/build-deb.sh` (unified build entry point)

A single script folding the two former build scripts:

```
scripts/build-deb.sh mgmt-common [DIR]                           # = build-mgmt-common.sh
scripts/build-deb.sh gnmi [DIR] [OUT_DIR] [COPY_GLOB]            # = build-gnmi-deb.sh
```

Internals are a single `case` over the subcommand, preserving
exact env (`NO_TEST_BINS=1`; `ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y`) and
argv (`-rfakeroot -b -us -uc`) plus the `OUT_DIR` / `COPY_GLOB` copy semantics.
Callers: `.azure/templates/build-deb.yml`, `.azure/templates/setup-test-env.yml`,
and `dev/run-tests.sh`. The two predecessor scripts were deleted after all callers
were rewired.

### C4 — `dev/run-tests.sh` shared helpers

The dockerized dev runner exposes `bootstrap / pure / integration / build / shell /
playground / clean / help`. Its CLI surface is unchanged by the consolidation; only
internal duplication was removed:

- **`run_interactive_container(...)`** — factors the common `docker run -it … exec
  bash --rcfile …` scaffolding shared by `run_shell` and `run_playground`.
- **`gobin_on_path_snippet()`** — the `GOBIN=…/build/bin; PATH=$GOBIN:$PATH` block
  previously duplicated in both interactive flows.
- **mgmt-common build factoring (behavior-preserving — see DD6).** The three build
  flows are **not** interchangeable:
  - `build_nonpure_snippet()` = mgmt-common build + `make all`.
  - `run_shell`'s `build-nonpure` helper = mgmt-common build + `make all` — the only
    flow that matches `build_nonpure_snippet()`, so it (and only it) is replaced by
    a call to the snippet.
  - `run_build` = mgmt-common build + `go mod tidy && go mod vendor` (vendor-sync) +
    gnmi build. It does **not** run `make all`; it is left functionally intact, with
    only its single mgmt-common build call routed through `build-deb.sh mgmt-common`.

### C5 — Documentation

This `dev/design.md` (architecture + rationale) plus the user-facing
[`dev/SETUP.md`](SETUP.md) (how-to). The five intermediate plan/decision docs were
deleted; their final-state content is captured here.

---

## 4. Data Flow — dependency version bump (post-consolidation)

1. An engineer edits one line in `scripts/deps-manifest.sh`.
2. `install-swsscommon.sh` and `dev/run-tests.sh bootstrap` pick up the new version
   on their next run automatically.
3. `scripts/test_deps_manifest.sh` fails if the ADO `patterns:` literals no longer
   match the manifest, prompting the (rare) YAML glob edit. CI and dev stay in sync
   by construction.

Before this work, the same bump required editing three or more files in lock-step,
and drift caused silent CI/dev divergence.

---

## 5. API Contracts

- **`deps-manifest.sh`** (sourced, POSIX `sh`): exports `LIBYANG3_VER`, `LIBNL_VER`,
  `SWSSCOMMON_VER`, `YANG_MODELS_VER`, `ARTIFACTS_URL`; provides functions
  `deps_swsscommon_debs <arch>`, `deps_bootstrap_targets`, `deps_download_globs`.
  No stdout or side effects on source.
- **`build-deb.sh <subcommand> [args]`**: exits non-zero with usage on an unknown
  subcommand; `mgmt-common` / `gnmi` are argv-compatible with the scripts they
  replaced.

---

## 6. Design Decisions

- **DD1 — Manifest is shell, not YAML/JSON.** All real consumers are shell scripts;
  a sourceable `sh` file is the lowest-friction single source of truth and needs no
  parser. ADO YAML cannot consume it directly (DD2).
- **DD2 — ADO `patterns:` stay literal + guarded.** ADO compile-time YAML has no
  mechanism to read a shell file, and the rendered patterns must remain unchanged.
  The literals are kept and a guard test asserts equality with the manifest, rather
  than templating them. This is the only behavior-preserving option.
- **DD3 — `build-deb.sh` preserves argv exactly, not "equivalently".** The
  verification harness asserts on recorded argv/env, so the subcommands reproduce
  the same tokens and effect as master's inline commands.
- **DD4 — Restore comments verbatim where the step is unchanged; keep new comments
  only where they describe a changed (split) step.** Restoring stripped comments
  shrinks `master..HEAD` to purely functional changes; genuinely new split-step
  comments stay.
- **DD5 — One merged `dev/design.md`, separate from `dev/SETUP.md`.** Keeping the
  user-facing how-to (`SETUP.md`) separate from the rationale (`design.md`) mirrors
  common repo conventions and preserves decision rationale without bloating the user
  guide. The five intermediate plan/decision docs are deleted.
- **DD6 — `run-tests.sh` build flows refactored individually, not unified.**
  `run_build`'s vendor-sync flow differs from the `make all` flow of
  `build_nonpure_snippet()`; forcing them through one helper would change
  `run_build`'s behavior. `build_nonpure_snippet()` is reused only where it already
  matches (`run_shell`'s `build-nonpure` helper); `run_build`'s vendor-sync + gnmi
  build lines are left intact, factoring out only the shared single mgmt-common
  build call.

---

## 7. Verification Approach (proving ADO behavior == master)

1. **Rendered-command diff.** For each ADO step that became a script call, confirm
   the script reproduces master's exact command (env vars, argv, working
   directory):
   - install-dependencies: `install-{test-deps,debs,yang-models,swsscommon,protoc}.sh`
     + `setup-redis.sh` vs master inline (structurally verified by
     `scripts/test_install_scripts.sh`).
   - build: `build-deb.sh {mgmt-common,gnmi}` vs master `build-deb.yml` /
     `setup-test-env.yml` inline — asserted by `scripts/test_build_scripts.sh`
     (recorded env + argv).
   - go: `install-go.sh` vs master `wget` / `tar`.
   - gofmt: `gofmt-check.sh` vs master inline (identical body).
2. **`patterns:` invariance.** The diff of `install-dependencies.yml` shows no change
   to any `patterns:` line; the guard test (`test_deps_manifest.sh`) keeps it equal
   to the manifest.
3. **Static checks.** `bash -n dev/run-tests.sh scripts/build-deb.sh`; `sh -n` the
   POSIX scripts.
4. **Test suites green.** `scripts/test_install_scripts.sh`,
   `scripts/test_build_scripts.sh`, `scripts/test_deps_manifest.sh`,
   `scripts/test_run_tests.sh`.
5. **Final diff review.** `git diff origin/master...HEAD` contains only: extracted
   scripts + manifest + `build-deb.sh` + ADO script-call substitutions + `repoRoot`
   param + dev runner + this design doc + `SETUP.md` — no cosmetic-only hunks.

---

## 8. References

- ADO templates: `.azure/templates/{install-dependencies,install-go,build-deb,setup-test-env}.yml`,
  `azure-pipelines.yml`.
- Extracted scripts: `scripts/install-*.sh`, `scripts/setup-redis.sh`,
  `scripts/gofmt-check.sh`, `scripts/build-deb.sh`, `scripts/deps-manifest.sh`.
- Dev runner: `dev/run-tests.sh`, `dev/setup.sh`, `dev/SETUP.md`.
- Test harnesses: `scripts/test_install_scripts.sh`, `scripts/test_build_scripts.sh`,
  `scripts/test_deps_manifest.sh`, `scripts/test_run_tests.sh`.
- SONiC artifact mirror: `https://sonic-build.azurewebsites.net/api/sonic/artifacts`.
- Upstream version sources: `rules/libyang3.mk`, `rules/libnl3.mk` in
  `sonic-net/sonic-buildimage@master`.
