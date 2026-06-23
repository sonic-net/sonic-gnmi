# sonic-gnmi CI/dev Tooling Consolidation — Solution Design & Implementation Plan

**Branch:** `donghaoli/new-setup2`
**Source of truth:** `origin/master` (authoritative for all ADO pipeline behavior)
**Revision notes:** Rev 2 — corrected the `run_build` dedup (lines 318-320 are mgmt-common
build + vendor-sync, **not** `make all`; only `run_shell` matches `build_nonpure_snippet()`);
corrected the design-doc count from six to **five** (`ci-script-extraction.decisions.md` does
not exist); fixed the install-dependencies header line count (21, not 24); added a disposition
for the new `dev/setup.sh`.

---

## Executive Summary

This branch lifted CI logic out of the Azure DevOps (ADO) YAML templates into reusable
shell scripts under `scripts/`, and added a dockerized local dev runner
(`dev/run-tests.sh`) plus supporting docs. The extraction work is functionally complete
but accumulated **cosmetic drift** (stripped comments), **duplicated data** (dependency
names/versions/URLs declared in 3+ places), **internal duplication** in the dev runner,
**two overlapping build scripts** that were meant to become one, and **five overlapping
design docs**. This proposal is a single behavior-preserving consolidation pass with five
goals: (1) revert no-impact deviations so `master..HEAD` contains only functional changes;
(2) centralize duplicated dependency data into one manifest consumed by both the ADO
install path and the dev bootstrap; (3) modularize `dev/run-tests.sh` by factoring shared
container/interactive scaffolding into helpers; (4) collapse the two build scripts into a
single `scripts/build-deb.sh` with subcommands; and (5) merge the design docs into one
coherent design/decisions document. **No CI behavior changes** — every rendered ADO command
must remain byte-equivalent to master in effect.

---

## Background

### Current architecture (as of `HEAD`)

The branch changed 25 files vs `origin/master` (`git diff --stat origin/master...HEAD`):

- **ADO templates rewired to call scripts** (functional, intended):
  - `azure-pipelines.yml` — gofmt inline → `scripts/gofmt-check.sh`; adds `repoRoot` to install-go template.
  - `.azure/templates/install-dependencies.yml` — five inline steps → `install-test-deps.sh`, `setup-redis.sh`, `install-debs.sh`, `install-yang-models.sh`, `install-swsscommon.sh`, `install-protoc.sh`.
  - `.azure/templates/install-go.yml` — inline `wget/tar` → `scripts/install-go.sh`; adds `repoRoot` param.
  - `.azure/templates/build-deb.yml` — inline mgmt-common+gnmi build → `scripts/build-mgmt-common.sh` + `scripts/build-gnmi-deb.sh`.
  - `.azure/templates/setup-test-env.yml` — inline mgmt-common build → `scripts/build-mgmt-common.sh`.
- **New extracted scripts:** `scripts/install-{debs,go,protoc,swsscommon,test-deps,yang-models}.sh`, `scripts/setup-redis.sh`, `scripts/gofmt-check.sh`, `scripts/build-mgmt-common.sh`, `scripts/build-gnmi-deb.sh`, plus test harnesses `scripts/test_install_scripts.sh`, `scripts/test_build_scripts.sh`.
- **New dev runner:** `dev/run-tests.sh` (dockerized: bootstrap/pure/integration/build/shell/playground/clean/help), `dev/setup.sh`, `dev/SETUP.md`.
- **Five design docs:** `dev/dedupe-ci-dev.{plan,decisions}.md`, `dev/local-dev-runner.{plan,decisions}.md`, `dev/ci-script-extraction.plan.md` (SETUP.md is a sixth user-facing doc). **Note:** the original purpose listed `dev/ci-script-extraction.decisions.md`, but that file **does not exist on disk** — only `ci-script-extraction.plan.md` was created. So there are five intermediate docs, not six.

### Why now

The branch is slated for a final squash + force-push into one commit. Before that, the
`master..HEAD` diff should be minimal and purely functional, the data should be DRY, and
the docs should reflect the *final* state rather than the intermediate iterations that
produced them.

### Prior art in the codebase

- `scripts/setup-redis.sh` and `scripts/build-mgmt-common.sh` already document themselves
  as "single source of truth … used by BOTH the ADO templates and dev/run-tests.sh", and
  `dev/run-tests.sh:container_setup_snippet()` (lines 132–146) already calls the extracted
  install scripts. The single-source-of-truth pattern is established; this pass extends it
  to dependency *data* and the *build* scripts, and removes the remaining duplication.

---

## Problem Statement

1. **Cosmetic drift inflates the diff.** The script extraction stripped descriptive header
   and section comments from the ADO templates. These deletions have zero behavioral impact
   but make `master..HEAD` larger and harder to review (and harder to prove behavior-preserving).
2. **Dependency identities are declared 3×.** The libyang/libnl/swss-common/yang-models
   names, versions, and download URLs appear in (a) `install-dependencies.yml` `patterns:`
   globs, (b) the `install-*.sh` scripts' hardcoded filenames, and (c) `dev/run-tests.sh`'s
   `DEB_TARGETS`/`ARTIFACTS_URL` (and again in `dev/SETUP.md`). A version bump requires
   editing several files in lock-step; drift causes silent CI/dev divergence.
3. **`dev/run-tests.sh` repeats itself.** `run_shell` and `run_playground` share container
   setup + interactive rc-file + binary-on-PATH scaffolding. Separately, the *mgmt-common build*
   call is repeated across `build_nonpure_snippet()`, `run_shell`'s `build-nonpure` helper, and
   `run_build`. Note these flows are **not** all identical: `build_nonpure_snippet()` and
   `run_shell`'s helper end with `make all`, whereas `run_build` does mgmt-common build +
   `go mod tidy && go mod vendor` (vendor-sync) followed by `build-gnmi-deb.sh` — so only the
   single mgmt-common build call is genuinely shared by all three. Adding a new subcommand means
   copy-pasting scaffolding.
4. **Two build scripts that were meant to be one.** `scripts/build-mgmt-common.sh` and
   `scripts/build-gnmi-deb.sh` are separate; the intended end state is one
   `scripts/build-deb.sh mgmt-common|gnmi|all`.
5. **Doc sprawl.** Five design docs (2,047 lines) describe three iterations of the same
   effort; only the final state matters going forward. Dead references to never-created
   files (`dev/local-ci-driver.plan.md`, `dev/ado-local.py`) linger in `run-tests.sh`.

---

## Goals and Non-Goals

### Goals
- **G1.** Reduce `master..HEAD` to only functionally-necessary changes by reverting
  no-impact deviations (restore stripped comments to match master).
- **G2.** Introduce one shared dependency manifest (`scripts/deps-manifest.sh`) consumed by
  the install scripts and the dev bootstrap; ADO rendered behavior unchanged.
- **G3.** Modularize `dev/run-tests.sh` by extracting shared helpers; no behavior change to
  any subcommand.
- **G4.** Replace `build-mgmt-common.sh` + `build-gnmi-deb.sh` with one
  `scripts/build-deb.sh mgmt-common|gnmi|all`; update all callers and tests; ADO build
  commands unchanged in effect.
- **G5.** Merge the five design docs into one coherent design + decisions doc; delete the
  superseded files and dead components.

### Non-Goals
- No new CI features, jobs, or stages. No change to *what* CI installs, builds, or tests.
- No change to ADO download `patterns:` *content* (the rendered glob list stays identical).
- No new dev-runner subcommands (the deferred CI-parity targets stay deferred).
- The final squash + force-push is **out of scope** for the code epics (mentioned as the
  closing manual step).

---

## Requirements

### Functional
- FR1. After G1, the only non-script-extraction changes in `master..HEAD` for ADO files are
  the script-call substitutions and the `repoRoot` parameter; all comments either match
  master or are functionally-justified by a changed step.
- FR2. `scripts/deps-manifest.sh` is the sole declaration of dependency artifact names,
  versions, deb/wheel globs, and the artifacts base URL. Install scripts and
  `dev/run-tests.sh` read from it.
- FR3. A guard test asserts the ADO `patterns:` literals in `install-dependencies.yml`
  match the manifest-derived globs (since ADO YAML cannot `source` a shell file at render
  time — see Design Decisions).
- FR4. `dev/run-tests.sh` subcommands (`bootstrap pure integration build shell playground
  clean help all`) produce identical container invocations and outputs after modularization.
- FR5. `scripts/build-deb.sh` exposes `mgmt-common`, `gnmi`, and `all` subcommands that
  reproduce the exact env vars and `dpkg-buildpackage` argv of the current two scripts.
- FR6. All callers (`build-deb.yml`, `setup-test-env.yml`, `dev/run-tests.sh`) invoke
  `build-deb.sh`; the orphan scripts are deleted.
- FR7. One merged design/decisions doc remains under `dev/`; the five intermediate docs are
  deleted; dead file references are removed.

### Non-functional
- NFR1. **Behavior-preserving:** rendered ADO commands are equivalent to master (verified,
  see Verification Approach).
- NFR2. Scripts remain POSIX `sh`-compatible where they currently are (`install-*.sh`,
  `build-*.sh`, `setup-redis.sh`); `gofmt-check.sh` and `run-tests.sh` stay `bash`.
- NFR3. All existing test harnesses (`scripts/test_install_scripts.sh`,
  `scripts/test_build_scripts.sh`) stay green; updated where contracts change.

---

## Proposed Design

### Architecture Overview

```
                    ┌─────────────────────────────┐
                    │  scripts/deps-manifest.sh    │  ← single source of truth
                    │  (names, versions, globs,    │     for dependency DATA
                    │   ARTIFACTS_URL)             │
                    └──────────────┬──────────────┘
            sourced by             │              sourced by
   ┌───────────────────────────────┼───────────────────────────────┐
   ▼                               ▼                               ▼
 scripts/install-debs.sh      scripts/install-          dev/run-tests.sh
 scripts/install-swsscommon.sh  yang-models.sh          (bootstrap DEB_TARGETS,
 (ADO + dev install path)       (ADO + dev)              ARTIFACTS_URL)
   ▲                                                          │
   │ patterns guard test (FR3)                                │ shared helpers (G3)
   │                                                          ▼
 .azure/templates/                              run_interactive_container()
 install-dependencies.yml  ── DownloadPipelineArtifact patterns:
 (literal globs, asserted == manifest)

                    ┌─────────────────────────────┐
                    │  scripts/build-deb.sh        │  mgmt-common | gnmi | all
                    └──────────────┬──────────────┘
        called by build-deb.yml, setup-test-env.yml, dev/run-tests.sh
```

### Key Components

#### C1 — `scripts/deps-manifest.sh` (new, G2)
A POSIX-`sh` data file (no side effects when sourced) declaring:
- Versioned package basenames: `LIBYANG3_VER=3.12.2-1`, `LIBNL_VER=3.7.0-0.2+b1sonic1`,
  `SWSSCOMMON_VER=1.0.0`, `YANG_MODELS_VER=1.0` (evidence: `run-tests.sh:84-94`,
  `install-swsscommon.sh:11-13`, `SETUP.md:92-95`).
- The deb/wheel **glob lists** used by ADO `patterns:` (evidence: `install-dependencies.yml:36-46,67`).
- `ARTIFACTS_URL` base (evidence: `run-tests.sh:50`, `SETUP.md:283`).
- Helper accessors, e.g. `deps_swsscommon_debs <arch>` → emits the exact filenames
  `install-swsscommon.sh` installs; `deps_bootstrap_targets` → emits the `DEB_TARGETS`
  list `run-tests.sh` downloads.

Consumers:
- `install-swsscommon.sh` sources it for `libswsscommon[-dev]_${VER}_${arch}.deb` /
  `python3-swsscommon_${VER}_${arch}.deb`.
- `install-yang-models.sh` / `install-debs.sh` need no version literals today (they glob a
  passed dir), so they consume only the URL/glob helpers if/where useful.
- `dev/run-tests.sh` sources it (or runs `sh -c '. manifest; deps_bootstrap_targets'`) to
  populate `ARTIFACTS_URL` and `DEB_TARGETS`, replacing the hardcoded block at lines 50,
  84–95.

#### C2 — ADO `patterns:` guard (new test, G2/FR3)
Because ADO YAML `patterns:` are render-time literals and **cannot** source a shell file,
the manifest is declared canonical and a guard test (extend `scripts/test_install_scripts.sh`
or add `scripts/test_deps_manifest.sh`) parses the `patterns:` block from
`install-dependencies.yml` and asserts set-equality with the manifest's glob list. This
keeps the rendered YAML byte-for-byte identical to master while preventing future drift.

#### C3 — `scripts/build-deb.sh` (new, G4)
Single entry point folding the two scripts:
```
scripts/build-deb.sh mgmt-common [DIR]                 # = build-mgmt-common.sh
scripts/build-deb.sh gnmi [DIR] [OUT_DIR] [COPY_GLOB]  # = build-gnmi-deb.sh
scripts/build-deb.sh all [...]                         # mgmt-common then gnmi
```
Internals factored into `_build_mgmt_common()` and `_build_gnmi()` preserving exact env
(`NO_TEST_BINS=1`; `ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y`) and argv
(`-rfakeroot -b -us -uc [-j<nproc>]`) plus the `OUT_DIR`/`COPY_GLOB` copy semantics
(evidence: `build-mgmt-common.sh:9-11`, `build-gnmi-deb.sh:15-22`).

#### C4 — `dev/run-tests.sh` shared helpers (refactor, G3)
- **`run_interactive_container(rc_builder, [extra_docker_args])`** — factors the common
  `docker_run "-it" bash -c "$(container_setup_snippet) … exec bash --rcfile …"` scaffolding
  shared by `run_shell` (220–247) and `run_playground` (256–309). Callers pass an rc-file
  body and (for playground) extra steps that boot the server + publish the port.
- **`gobin_on_path_snippet()`** — the `GOBIN=/work/sonic-gnmi/build/bin; PATH=$GOBIN:$PATH`
  block duplicated at 267–268 and 294–295.
- **mgmt-common build factoring (behavior-preserving — see DD6).** The three build flows are
  **not** interchangeable:
  - `build_nonpure_snippet()` (182–188) = mgmt-common build + `make all`.
  - `run_shell`'s `build-nonpure` helper (234–236) = mgmt-common build + `make all` — this is
    the **only** flow that matches `build_nonpure_snippet()`, so it (and only it) is replaced
    by a call to `build_nonpure_snippet()`.
  - `run_build` (317–322) = mgmt-common build + `go mod tidy && go mod vendor` (vendor-sync) +
    `build-gnmi-deb.sh`. It does **not** run `make all`; substituting `build_nonpure_snippet()`
    here would change behavior. So `run_build` is left functionally intact — only its single
    mgmt-common build call is routed through `build-deb.sh mgmt-common` (Epic C), and the
    vendor-sync + gnmi-build lines are unchanged.

#### C5 — Merged design doc (G5)
One `dev/design.md` (final-state architecture + rationale, superseding the two `*.plan.md` +
two `*.decisions.md` pairs plus the lone `ci-script-extraction.plan.md` — five docs total) and
the user-facing `dev/SETUP.md` retained/updated. This `consolidation.plan.md` tracks the
consolidation work itself.

### Data Flow — dependency version bump (post-G2)
1. Engineer edits `scripts/deps-manifest.sh` (one line).
2. `install-swsscommon.sh` and `run-tests.sh bootstrap` pick up the new version on next run.
3. `scripts/test_deps_manifest.sh` fails if the ADO `patterns:` literals no longer match,
   prompting the (rare) YAML glob edit. CI behavior stays in sync by construction.

### API Contracts
- **`deps-manifest.sh`** (sourced, POSIX sh): exports `LIBYANG3_VER`, `LIBNL_VER`,
  `SWSSCOMMON_VER`, `YANG_MODELS_VER`, `ARTIFACTS_URL`; functions `deps_swsscommon_debs <arch>`,
  `deps_bootstrap_targets`, `deps_download_globs`. No stdout/side effects on source.
- **`build-deb.sh <subcommand> [args]`**: exit non-zero with usage on unknown subcommand;
  `mgmt-common`/`gnmi` argv-compatible with the scripts they replace.

### Design Decisions

- **DD1 — Manifest is shell, not YAML/JSON.** All four real consumers are shell
  (`install-*.sh`, `run-tests.sh`). A sourceable `sh` file is the lowest-friction SSOT and
  needs no parser. ADO YAML cannot consume it directly (DD2).
- **DD2 — ADO `patterns:` stay literal + guarded.** ADO compile-time YAML has no mechanism
  to read a shell file, and the constraint is that rendered patterns must be *unchanged*.
  So we keep the literals and add a guard test asserting equality with the manifest, rather
  than templating them. This is the only behavior-preserving option.
- **DD3 — `build-deb.sh` preserves argv exactly, not "equivalently".** The verification
  harness asserts on recorded argv/env, so the subcommands must reproduce the same tokens
  (flag order is irrelevant to `dpkg-buildpackage`, but we keep master's effect and the
  tests' expectations).
- **DD4 — Restore comments verbatim where the step is unchanged; keep new comments only
  where they describe a changed (split) step.** E.g. `install-go.yml`'s usage header is
  restorable verbatim (the step is the same logical install); `build-deb.yml`'s split-step
  comments describe genuinely new structure and stay.
- **DD5 — One merged `dev/design.md`.** Keeping `SETUP.md` (user-facing how-to) separate
  from `design.md` (rationale) mirrors common repo conventions; the five intermediate
  plan/decision docs are deleted.
- **DD6 — `run-tests.sh` build flows refactored individually, not unified.** `run_build`'s
  vendor-sync flow differs from the `make all` flow of `build_nonpure_snippet()`; forcing them
  through one helper would change `run_build`'s behavior. We therefore reuse
  `build_nonpure_snippet()` only where it already matches (`run_shell`'s `build-nonpure` helper),
  and leave `run_build`'s vendor-sync + gnmi-build lines intact, factoring out only the shared
  single mgmt-common build call (via `build-deb.sh mgmt-common`).

---

## Alternatives Considered

- **Template ADO `patterns:` from the manifest (rejected).** Would require a pre-render
  generation step or pipeline variable injection, changing the rendered YAML and risking
  behavior drift — violates the "rendered patterns unchanged" constraint. Guard-test
  approach (DD2) gives the DRY benefit without touching rendered output.
- **Keep two build scripts, just dedupe internals (rejected).** Goal 4 explicitly wants one
  entry point with subcommands for extensibility and fewer callers to update.
- **Delete all design docs, keep only SETUP.md (rejected).** Loses the decision rationale
  that explains *why* the extraction was done; a single merged `design.md` preserves it
  cheaply.

---

## Dependencies

- **External:** none new. Existing: docker, `sonic-slave-trixie` image, `sonic-build.azurewebsites.net`
  public artifact mirror, `go.dev` Go tarballs.
- **Internal sequencing** (canonical scheme: A=G1 reverts, B=G2 manifest, C=G4 build-deb.sh,
  D=G3 run-tests modularization, E=G5 docs):
  - Epic A (G1 reverts) is independent and should land first (smallest, lowest risk).
  - Epic C (build-deb.sh, G4) rewires `run-tests.sh:235` to `build-deb.sh` (task C4) and Epic D
    later replaces lines 234-236 wholesale with a `build_nonpure_snippet()` call (task D3); these
    touch the same lines, so land **C before D** and treat D3 as superseding C4's edit on those
    lines (C4 still rewires `run_build`'s mgmt-common call at 318-321, which D3 leaves intact).
  - Epic B (manifest, G2) and Epic D (run-tests modularization, G3) both edit
    `run-tests.sh`; do B then D to avoid churn.
  - Epic E (docs, G5) lands last (reflects final state) and removes dead refs to the
    superseded docs and deleted build scripts.

---

## Impact Analysis

- **Components affected:** `.azure/templates/*`, `azure-pipelines.yml` (G1 only, comments),
  `scripts/*`, `dev/run-tests.sh`, `dev/*.md`.
- **Backward compatibility:** ADO behavior unchanged (NFR1). `dev/run-tests.sh` CLI surface
  unchanged. `build-deb.sh` is new; its two predecessors are deleted (no external callers
  outside this repo).
- **Performance:** negligible; one extra `source` of a tiny manifest per script invocation.
- **Operational:** CI logs' `displayName`s may change wording where comments are restored;
  step *commands* do not.

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Manifest refactor subtly changes a swss-common filename and breaks the amd64/arm64 install | Medium | High | `test_install_scripts.sh` already exercises `install-swsscommon.sh`; extend assertions to cover manifest-sourced names; diff rendered filenames vs master. |
| ADO `patterns:` drift from manifest goes unnoticed | Medium | Medium | Guard test (C2) asserts set-equality; runs in `test_install_scripts.sh`. |
| `build-deb.sh` argv differs from the two scripts → CI build changes | Low | High | `test_build_scripts.sh` updated to assert recorded env+argv for each subcommand; compare against the master inline command tokens. |
| `run-tests.sh` modularization changes container invocation | Low | Medium | Refactor is mechanical; manually diff the generated `bash -c` snippet for `shell`/`playground`/`build` before/after; no flag/env changes. |
| Deleting a doc that some external link references | Low | Low | Docs are branch-local (added on this branch, not on master); safe to delete. |

---

## Open Questions

- **OQ1.** Should the merged doc be `dev/design.md` + keep `dev/SETUP.md`, or a single
  `dev/SETUP.md` absorbing the rationale? (Proposed: two files — DD5.)
- **OQ2.** Should `install-debs.sh` / `install-yang-models.sh` (which currently glob a
  passed directory and hold no version literals) consume the manifest at all, or only
  `install-swsscommon.sh` + `run-tests.sh`? (Proposed: only where literals exist, to avoid
  ceremony.)
- **OQ3.** Is the `libpcre*` family (in `patterns:` lines 38–42) actually downloaded/installed
  anywhere, or download-only? It is absent from `DEB_TARGETS` and `install-swsscommon.sh`;
  the manifest should record it as download-only to keep the guard test accurate.
- **OQ4.** Confirm no out-of-repo automation invokes `build-mgmt-common.sh` /
  `build-gnmi-deb.sh` by path before deleting them.

---

## Implementation Phases

1. **Phase 1 (Epic A):** Revert no-impact ADO comment deviations. Exit: `master..HEAD` ADO
   diff is script-substitutions + `repoRoot` only.
2. **Phase 2 (Epic B):** Introduce `deps-manifest.sh`; rewire install scripts + run-tests
   bootstrap; add patterns guard. Exit: single declaration of dep data; guard green.
3. **Phase 3 (Epic C):** Create `build-deb.sh`; rewire callers; delete orphans; update
   build tests. Exit: orphans gone, all callers use `build-deb.sh`, tests green.
4. **Phase 4 (Epic D):** Modularize `run-tests.sh`. Exit: shared helpers in place, no
   subcommand behavior change.
5. **Phase 5 (Epic E):** Merge docs; delete superseded docs + dead refs. Exit: one merged
   design doc; no dangling references.
6. **Closing (manual, out of scope):** Squash entire branch into one commit + force-push.

---

## Files Affected

### New Files
| File Path | Purpose |
|-----------|---------|
| `scripts/deps-manifest.sh` | Single source of truth for dependency names/versions/globs/URL (G2). |
| `scripts/build-deb.sh` | Unified build entry point: `mgmt-common\|gnmi\|all` (G4). |
| `scripts/test_deps_manifest.sh` *(or extend `test_install_scripts.sh`)* | Guard: ADO `patterns:` == manifest globs; swss-common filenames (G2). |
| `dev/design.md` | Merged final-state design + decisions doc (G5). |

### Modified Files
| File Path | Changes |
|-----------|---------|
| `.azure/templates/install-dependencies.yml` | Restore stripped header/section comments to match master (G1). |
| `.azure/templates/install-go.yml` | Restore master usage-header comment; keep `repoRoot` doc note (G1). |
| `scripts/install-swsscommon.sh` | Source `deps-manifest.sh` for version/filenames (G2). |
| `scripts/install-yang-models.sh`, `scripts/install-debs.sh` | Consume manifest URL/glob helpers where applicable (G2, per OQ2). |
| `.azure/templates/build-deb.yml` | Call `build-deb.sh gnmi …` (+ `mgmt-common`) instead of the two scripts (G4). |
| `.azure/templates/setup-test-env.yml` | Call `build-deb.sh mgmt-common …` (G4). |
| `scripts/test_build_scripts.sh` | Test `build-deb.sh` subcommands instead of the two scripts (G4). |
| `dev/run-tests.sh` | Source manifest for `ARTIFACTS_URL`/`DEB_TARGETS` (G2); extract `run_interactive_container`/reuse `build_nonpure_snippet`/`gobin_on_path_snippet` (G3); call `build-deb.sh` (G4); remove dead refs to `local-ci-driver.plan.md`/`ado-local.py` (G5). |
| `dev/SETUP.md` | Point dependency table/URL at the manifest as canonical; remove duplicated version table or annotate it as generated (G2/G5). |
| `dev/setup.sh` | Audit for cosmetic drift / dead refs only (G1/G5); no functional change expected. |

### Deleted Files
| File Path | Reason |
|-----------|--------|
| `scripts/build-mgmt-common.sh` | Folded into `build-deb.sh mgmt-common` (G4). |
| `scripts/build-gnmi-deb.sh` | Folded into `build-deb.sh gnmi` (G4). |
| `dev/dedupe-ci-dev.plan.md` | Superseded intermediate doc (G5). |
| `dev/dedupe-ci-dev.decisions.md` | Superseded intermediate doc (G5). |
| `dev/local-dev-runner.plan.md` | Superseded intermediate doc (G5). |
| `dev/local-dev-runner.decisions.md` | Superseded intermediate doc (G5). |
| `dev/ci-script-extraction.plan.md` | Superseded intermediate doc (G5). |

---

## Evidence Catalog (file:line)

### (a) No-impact deviations to revert (G1)
| Location | Deviation | Action |
|----------|-----------|--------|
| `.azure/templates/install-dependencies.yml:1-3` (master `1-21`) | 21-line usage/dependency header (master lines 1-21, blank line 22) stripped to 3-line summary | Restore master header block. |
| `install-dependencies.yml` (master had `# === Download libyang… ===`, `# === Install test dependencies… ===`, `# === Install libyang + libnl debs ===`, `# === Download and install sonic yang models ===`, `# === Download and install sonic-swss-common ===`, `# === Install protoc ===`) | Section-marker comments removed during extraction (diff context shows deletions) | Restore the section-marker comments above the corresponding (now script-call) steps. |
| `.azure/templates/install-go.yml:1-2` (master `1-9`) | Usage/Downloads header reworded | Restore master header; append one line documenting new `repoRoot` param. |
| `dev/run-tests.sh:36,353,355,366` | References to never-created `dev/local-ci-driver.plan.md` and `dev/ado-local.py` | Remove/repoint dead references (also G5). |

> Note: `azure-pipelines.yml`, `build-deb.yml`, `setup-test-env.yml` comment changes are
> **functionally-justified** (they describe genuinely changed/split steps) and are *kept*
> per DD4 — listed here for completeness, not reverted.

### (b) Duplicated data to centralize (G2)
| Item | Locations |
|------|-----------|
| libyang/libpcre/libnl deb **globs** | `install-dependencies.yml:36-46` (`patterns:`) |
| yang-models wheel glob | `install-dependencies.yml:67`; `install-yang-models.sh` arg; `run-tests.sh:94,141` |
| swss-common filenames + `1.0.0` | `install-swsscommon.sh:11-13`; `run-tests.sh:91-93`; `SETUP.md:94` |
| libyang `3.12.2-1`, libnl `3.7.0-0.2+b1sonic1`, yang `1.0` versions | `run-tests.sh:84-94`; `SETUP.md:92-95` |
| `ARTIFACTS_URL` base | `run-tests.sh:50`; `SETUP.md:283` |
| `DEB_TARGETS` bootstrap list | `run-tests.sh:84-95` |

### (c) `run-tests.sh` overlaps to modularize (G3)
| Overlap | Locations |
|---------|-----------|
| Interactive container scaffolding (`docker_run "-it" … exec bash --rcfile`) | `run_shell:227-246` and `run_playground:262-308` |
| rc-file generation (`[ -f /etc/bash.bashrc ]…; cd /work/sonic-gnmi; echo examples`) | `run_shell:228-245` and `run_playground:292-307` |
| `GOBIN`/`PATH` binary-on-PATH block | `run_playground:267-268` and `:294-295` |
| **Single** mgmt-common build call (the only element shared by all three build flows) | `build_nonpure_snippet:184`; `run_shell` `build-nonpure`:235; `run_build:318` |
| mgmt-common + `make all` (matches `build_nonpure_snippet()` 182-188) — reuse the snippet here | `run_shell` `build-nonpure`:234-236 **only** |

> **Correction vs intermediate drafts:** `run_build:318-320` is mgmt-common build +
> `go mod tidy && go mod vendor` (vendor-sync), **not** `make all`; line 321 then runs
> `build-gnmi-deb.sh`. It is *not* a `build_nonpure_snippet()` candidate. Only `run_shell`'s
> `build-nonpure` helper (234-236) matches the snippet. See DD6.

### (d) Docs/files to merge or delete (G5)
| File | Disposition |
|------|-------------|
| `dev/dedupe-ci-dev.plan.md` (753), `dev/dedupe-ci-dev.decisions.md` (97) | Merge → `dev/design.md`, then delete. |
| `dev/local-dev-runner.plan.md` (435), `dev/local-dev-runner.decisions.md` (100) | Merge → `dev/design.md`, then delete. |
| `dev/ci-script-extraction.plan.md` (662) | Merge → `dev/design.md`, then delete. (`ci-script-extraction.decisions.md` was specified in the original purpose but **does not exist** — nothing to delete.) |
| `dev/SETUP.md` (346) | Keep (user-facing); update dep table/URL to reference manifest. |
| `dev/setup.sh` (85, new branch file) | Keep; audit for cosmetic drift and dead refs (G1/G5) — no functional change expected. |
| Dead refs `local-ci-driver.plan.md`, `ado-local.py` in `run-tests.sh` | Remove. |

> Five intermediate docs total = 753 + 97 + 435 + 100 + 662 = **2,047 lines**.

---

## Implementation Plan

### Epic A — Revert no-impact ADO deviations (G1) — DONE
- **Goal:** Shrink `master..HEAD` to only functional changes by restoring stripped comments.
- **Prerequisites:** none.
- **Completed:** 2026-06-23
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| A1 | IMPL | Restore master 21-line header (lines 1-21) + `# === … ===` section markers (above the new script-call steps) | `.azure/templates/install-dependencies.yml` | DONE |
| A2 | IMPL | Restore master usage-header; add one `repoRoot` doc line | `.azure/templates/install-go.yml` | DONE |
| A3 | TEST | `git diff origin/master...HEAD -- .azure azure-pipelines.yml` review: confirm only script-call + `repoRoot` substitutions remain (no cosmetic-only hunks) | — | DONE |

- **Acceptance Criteria:**
  - [x] ADO templates' comments match master except where a step genuinely changed (DD4).
  - [x] No whitespace-only or reword-only hunks remain in `master..HEAD` for ADO files.

### Epic B — Centralize dependency data into a manifest (G2) — DONE
- **Goal:** One declaration of dep names/versions/globs/URL consumed by scripts + dev runner.
- **Prerequisites:** Epic A (avoids re-touching the same YAML).
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| B1 | IMPL | Create manifest with versions, glob lists, `ARTIFACTS_URL`, accessors (`deps_swsscommon_debs`, `deps_bootstrap_targets`, `deps_download_globs`) | `scripts/deps-manifest.sh` | DONE |
| B2 | IMPL | Source manifest in `install-swsscommon.sh`; build filenames from `SWSSCOMMON_VER` | `scripts/install-swsscommon.sh` | DONE |
| B3 | IMPL | Source manifest in `run-tests.sh`; replace hardcoded `ARTIFACTS_URL` (50) + `DEB_TARGETS` (84-95) | `dev/run-tests.sh` | DONE |
| B4 | IMPL | Update `SETUP.md` dep table/URL to mark the manifest canonical (or generated) | `dev/SETUP.md` | DONE |
| B5 | TEST | Guard test: parse `install-dependencies.yml` `patterns:` and assert set-equality with manifest globs (incl. libpcre download-only, OQ3); assert swss filenames match manifest | `scripts/test_deps_manifest.sh` (or extend `test_install_scripts.sh`) | DONE |

- **Acceptance Criteria:**
  - [x] Editing one manifest line changes both the ADO install filenames and the dev bootstrap.
  - [x] Rendered ADO `patterns:` are byte-identical to master (literals untouched).
  - [x] `test_install_scripts.sh` + guard test green.

### Epic C — Consolidate build scripts into `build-deb.sh` (G4)
- **Goal:** One build entry point with subcommands; orphans deleted; callers updated.
- **Prerequisites:** Epic A; coordinate `run-tests.sh` edits with Epic B/D.
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| C1 | IMPL | Create `build-deb.sh` with `mgmt-common\|gnmi\|all`; preserve exact env+argv+copy semantics | `scripts/build-deb.sh` | TO DO |
| C2 | IMPL | Rewire `build-deb.yml` (mgmt-common + gnmi steps) to `build-deb.sh` | `.azure/templates/build-deb.yml` | TO DO |
| C3 | IMPL | Rewire `setup-test-env.yml` mgmt-common step to `build-deb.sh mgmt-common` | `.azure/templates/setup-test-env.yml` | TO DO |
| C4 | IMPL | Rewire `run-tests.sh` mgmt-common callers (184, 235, 318) + gnmi caller (321) to `build-deb.sh`; preserve `run_build`'s vendor-sync line (319-320) | `dev/run-tests.sh` | TO DO |
| C5 | IMPL | Delete orphan scripts | `scripts/build-mgmt-common.sh`, `scripts/build-gnmi-deb.sh` | TO DO |
| C6 | TEST | Rewrite `test_build_scripts.sh` to drive `build-deb.sh` subcommands; assert recorded env/argv/copy for `mgmt-common`, `gnmi`, `all` | `scripts/test_build_scripts.sh` | TO DO |

- **Acceptance Criteria:**
  - [ ] `grep -r build-mgmt-common\|build-gnmi-deb` returns no live callers.
  - [ ] Rendered ADO build commands reproduce master's env vars and `dpkg-buildpackage` argv.
  - [ ] `test_build_scripts.sh` green (covers `mgmt-common`, `gnmi`, `all`, copy globs).

### Epic D — Modularize `dev/run-tests.sh` (G3)
- **Goal:** Factor shared scaffolding into helpers; zero behavior change.
- **Prerequisites:** Epics B and C (they also edit `run-tests.sh`).
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| D1 | IMPL | Add `run_interactive_container()` factoring shared `docker_run "-it"` + rc-file `exec bash --rcfile` scaffold | `dev/run-tests.sh` | TO DO |
| D2 | IMPL | Rewrite `run_shell` + `run_playground` to call D1 (playground passes server-boot + `-p` extras) | `dev/run-tests.sh` | TO DO |
| D3 | IMPL | Add `gobin_on_path_snippet()`; reuse `build_nonpure_snippet()` in `run_shell`'s `build-nonpure` helper (234-236) **only** (the one flow that matches). Leave `run_build`'s vendor-sync flow (318-321) functionally intact — route only its single mgmt-common build call through `build-deb.sh` (per Epic C); do **not** substitute `build_nonpure_snippet()` there (would inject `make all` and drop vendor-sync — see DD6) | `dev/run-tests.sh` | TO DO |
| D4 | TEST | Diff generated `bash -c` snippets for `shell`/`playground`/`build` before vs after (manual/`bash -n` + snippet dump); confirm identical commands — in particular that `run_build` still emits vendor-sync (`go mod tidy && go mod vendor`) + `build-gnmi-deb`, not `make all` | `dev/run-tests.sh` | TO DO |

- **Acceptance Criteria:**
  - [ ] `bash -n dev/run-tests.sh` passes; `help` output unchanged.
  - [ ] Generated container commands for each subcommand are identical pre/post refactor.

### Epic E — Consolidate design docs (G5)
- **Goal:** One merged design/decisions doc; delete intermediates + dead refs.
- **Prerequisites:** Epics A–D (doc must reflect final state).
- **Tasks:**

| Task ID | Type | Description | Files | Status |
|---------|------|-------------|-------|--------|
| E1 | IMPL | Author `dev/design.md` merging the 2 plan + 2 decision docs + the lone `ci-script-extraction.plan.md` (five docs) into final-state narrative | `dev/design.md` | TO DO |
| E2 | IMPL | Delete the five superseded docs (`ci-script-extraction.decisions.md` does not exist) | `dev/{dedupe-ci-dev,local-dev-runner}.{plan,decisions}.md`, `dev/ci-script-extraction.plan.md` | TO DO |
| E3 | IMPL | Remove dead refs to `local-ci-driver.plan.md`/`ado-local.py` (run-tests.sh:36,353-366) | `dev/run-tests.sh` | TO DO |
| E4 | TEST | `grep -rn 'local-ci-driver\|ado-local\|dedupe-ci-dev\|ci-script-extraction\|local-dev-runner' dev/ scripts/ .azure/` returns nothing | — | TO DO |

- **Acceptance Criteria:**
  - [ ] Exactly one design doc + `SETUP.md` remain under `dev/`.
  - [ ] No dangling references to deleted files anywhere in the tree.

### Closing Step (manual, out of scope for code epics)
After all epics land and verification passes, **squash the entire branch into a single
commit and force-push** `donghaoli/new-setup2`. Not covered by the epics above.

---

## Verification Approach (proving ADO behavior == master)

1. **Rendered-command diff.** For each ADO step that became a script call, manually confirm
   the script reproduces master's exact command (env vars, argv, working directory):
   - install-dependencies: `install-{test-deps,debs,yang-models,swsscommon,protoc}.sh` +
     `setup-redis.sh` vs master inline (already structurally verified by
     `scripts/test_install_scripts.sh`, 444 lines).
   - build: `build-deb.sh {mgmt-common,gnmi}` vs master `build-deb.yml`/`setup-test-env.yml`
     inline — asserted by `test_build_scripts.sh` (recorded env+argv).
   - go: `install-go.sh` vs master `wget/tar` (`test_install_scripts.sh`).
   - gofmt: `gofmt-check.sh` vs master inline (identical body, confirmed by diff).
2. **`patterns:` invariance.** `git diff origin/master...HEAD -- .azure/templates/install-dependencies.yml`
   shows **no change** to any `patterns:` line; guard test (B5) keeps it equal to the manifest.
3. **Static checks.** `bash -n dev/run-tests.sh scripts/build-deb.sh`; `sh -n` the POSIX scripts.
4. **Test suites green.** `sh scripts/test_install_scripts.sh`, `sh scripts/test_build_scripts.sh`,
   `sh scripts/test_deps_manifest.sh` all pass (baseline today: build tests **PASS 15 FAIL 0**).
5. **Final diff review.** `git diff origin/master...HEAD` contains only: extracted scripts +
   manifest + build-deb.sh + ADO script-call substitutions + `repoRoot` param + dev runner +
   one design doc + SETUP.md — no cosmetic-only hunks.

---

## References
- Branch diff: `git diff --stat origin/master...HEAD` (25 files).
- ADO templates: `.azure/templates/{install-dependencies,install-go,build-deb,setup-test-env}.yml`, `azure-pipelines.yml`.
- Extracted scripts: `scripts/install-*.sh`, `scripts/setup-redis.sh`, `scripts/gofmt-check.sh`, `scripts/build-{mgmt-common,gnmi-deb}.sh`.
- Dev runner: `dev/run-tests.sh`, `dev/setup.sh`, `dev/SETUP.md`.
- Existing test harnesses: `scripts/test_install_scripts.sh`, `scripts/test_build_scripts.sh`.
- SONiC artifact mirror: `https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target=…`.
- Upstream version sources: `rules/libyang3.mk`, `rules/libnl3.mk` in `sonic-net/sonic-buildimage@master`.
