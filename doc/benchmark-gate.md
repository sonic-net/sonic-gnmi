# Manual gNMI benchmark gate preview

This draft adds a manual **evidence/correctness gate**, with report-only performance
comparisons. It does not modify the server or establish a performance threshold.
It is not yet a required PR check: a dispatched workflow is associated with its
trusted workflow commit, not automatically with the candidate PR head.

## Status and execution environment

The workflow/evaluator are implemented; the site-owned execution adapter is a
commissioning prerequisite, **not an included implementation**. No on-device
base/head run has validated this workflow. The repository variable
`GNMI_BENCHMARK_ENABLED` must equal `true` before it can allocate a benchmark runner.
Leave it unset until the following controls and execution adapter are verified.
An absent adapter, absent evidence, or invalid result fails rather than simulating
success. Synthetic unit-test data is not performance evidence.

1. Create a `gnmi-benchmark` GitHub environment with required reviewers, prevent
   self-review, and allow only `master` deployments. Review the resolved full
   base/head SHAs in the preparation summary before approving execution.
2. Provision the `gnmi-benchmark` runner group, restricted to this workflow on the
   trusted default branch, with label `gnmi-benchmark-ephemeral`. Use a disposable
   controller and an exclusively leased, resettable single-ASIC SONiC test target.
   The owner must validate the 256k-route inventory on that target. No ordinary
   shared/public PR runner or shared production device is suitable.
3. Install the reviewed adapter at `/opt/gnmi-benchmark/bin/execute` outside candidate
   checkouts. Candidate code runs only in isolated build/target environments, with
   no access to controller files, GitHub tokens, host Docker socket, credentials,
   or other devices. The controller validates evidence produced by the trusted
   adapter. Environment approval alone is not isolation from candidate code.
4. Commission reset, build, correctness, benchmark, timeout and recovery behavior.
   Run the retest checklist below; only then enable manual dispatch.

Only `workflow_dispatch` on upstream `master` is accepted. There are no comment,
fork-PR, `pull_request_target`, or automatic PR triggers. Actions use immutable
SHAs; checkout credentials are not persisted. Candidate PR YAML and scripts never
replace the trusted gate. All runs share one concurrency group without cancellation.
An external lease/recovery watchdog must survive job timeout, manual cancellation,
and controller loss; release the target only after verified restoration, otherwise
quarantine it. A GitHub concurrency group is not a device lease.

## Paired comparison policy

The preparation job resolves the open PR's **current base tip and head** to full
commit SHAs, including the head repository for fork PRs. It does not use merge-base
or silently advance either revision later. It pins the benchmark from
[sonic-mgmt PR #27935](https://github.com/sonic-net/sonic-mgmt/pull/27935) at
`49f34002e111acaf79c64b4b0e212b2c47695d05` (schema 10), and Go 1.25.9.
This preview pin has known review concerns; commissioning must resolve or explicitly
test them. Do not silently replace it with a moving PR branch.

Run four adjacent pairs in order **AB, BA, AB, BA** (A=base, B=head). Build each side
once and reuse that exact binary across its repetitions. Reset the same target to
the same clean image/configuration before each run. Preload fresh inventory and
wait for downstream state to settle before measurement. Do not run the two sides
concurrently. This small fixed count is a commissioning starting point, not a
statistical proof of no regression.

| Setting | Fixed value |
|---|---|
| Inventory | 12 × 20,000 measured routes + 1 × 16,000 background routes |
| Request | 1,000 explicit-path Get entries, followed by one bypass-requested batch Set |
| Load | Closed-loop, 2 workers, 100 measured Get→Set iterations |
| Warmup / timeout | 5 seconds / 120 seconds per RPC |
| Transport | One persistent TLS channel, same client/transport path on both sides |

Use the same build-container **digest**, dependency source SHAs and binary artifact
hashes, toolchain, build flags, client-container digest, DUT image digest, CPU/memory
allocation, Redis pool configuration, target, topology and transport on both sides.
Resolve those inputs once per dispatch and reject changes. The adapter must validate
the effective dependency graph after building (the existing Makefile can tidy/vendor
and apply patches). A changed `go.mod`, `go.sum`, module replacement, dependency patch
or build flag must not become an unreported dependency change. If identical effective
dependencies cannot be demonstrated, fail as incomparable; do not force new lockfiles
onto one side or attribute the mixed change to a server optimization.

Report median and min/max of the four **paired percentage changes** for independent
Get/Set mean, P95 and completed RPC/s. Do not pool per-run percentiles. Count-mode
completion rates include drain and tail effects and are not steady-state capacity.
Raw 1-second exceedance counts remain visible. Four repetitions cannot establish
stable P99 or a production capacity claim. First measure A/A variability across
sessions, then choose a regression threshold in a separate reviewed change.

## Adapter protocol (version 1)

The gate calls a fixed executable, with no shell interpolation:

```text
/opt/gnmi-benchmark/bin/execute --request REQUEST_JSON --output PRIVATE_OUTPUT_DIRECTORY
```

`request.json` contains the complete immutable plan plus `sequence` (0..7), `side`
and `source={repository,sha}`. `workload` includes every load input, the inventory
profile and marker. The adapter must check out exact source/benchmark SHAs, build,
verify the loaded binary, restore the target, and return within 20 minutes for each
invocation, including restoration. No arbitrary command input is accepted.

The adapter returns zero only when it successfully produced all evidence and
verified restoration. It may return zero after a test assertion failure so the
evaluator can inspect the actual test exit code; it must never reinterpret a
timeout, missing report, setup/teardown error, skip, or collection failure as success.
Four files are required for each invocation:

| File | Contract |
|---|---|
| `report.json` | Unmodified schema-10 `BenchmarkReport` output for this measured run |
| `benchmark.xml` | Original pytest JUnit with exactly `test_gnmi_benchmark`, including teardown outcomes |
| `correctness.xml` | Trusted correctness-suite JUnit with the six cases below, no failures/errors/skips |
| `evidence.json` | Trusted adapter provenance and completion evidence, described below |

`evidence.json` fields:

```text
request_sha256         SHA256 of canonical request JSON (sorted keys, compact separators)
source                 Exact request.source repository and full SHA
benchmark_sha          Exact benchmark source SHA
go_version             1.25.9
workload_sha256        SHA256 of canonical request.workload
package_sha256         SHA256 of installed candidate package
loaded_binary_sha256   SHA256 read back from the running server binary
correctness_exit_code  Actual correctness test process exit code
benchmark_exit_code    Actual benchmark pytest process exit code
restored               Boolean true only after verified runtime + persistent restoration
fingerprints           Object of SHA256 strings for the seven manifests below
```

`fingerprints` has exactly `build_image`, `dut_image`, `dependencies`,
`client_environment`, `runner_configuration`, `adapter`, `correctness_suite`.
Hashes must be computed from the **observed** resolved manifests, not copied from
the request or hardcoded constants. Keep full manifests in private owner-controlled
storage. `dependencies` covers the effective module graph, patches and native
libraries. `runner_configuration` covers target identity, clean initial state,
transport, build flags, CPU/memory limits and server settings. All seven fingerprints
must match across all eight invocations. Binary hashes must remain identical for
each source across repetitions. These attestations trust the commissioned adapter;
hash equality alone cannot prove that a compromised adapter ran the requested code.

### Correctness is independent of performance

The existing benchmark only checks RPC status and explicit Set response errors. It
does not verify Get contents, read freshness, Set persistence, forwarding convergence
or the executed backend path. The adapter's separate versioned suite must emit:

| Case name | Required assertion outside timed measurement |
|---|---|
| `native_get_values` | Exact requested key/value set on the live-Redis path; explicitly exclude stale checkpoints |
| `native_set_readback` | Change values, then independently read Redis and gNMI to verify persistence/freshness |
| `native_missing_key` | Missing-key status matches the defined native Get contract |
| `native_null_only_key` | NULL-only hash yields the expected no-data status after filtering |
| `bypass_partial_write` | Prove bypass execution and verify first-error stop, prior writes retained and later writes untouched using deterministic fault injection |
| `fixture_cleanup` | Persistent backup restored, generated runtime keys/VNET/tunnel absent, downstream state settled and no late writes after timeout |

The last check runs after benchmark fixture teardown. The first five must pass for
both source versions before that invocation's performance measurement. Fault injection
must be confined to the isolated target, removed before measuring, and identical
between sides. Do not retry failed correctness cases into a pass. A benchmark report
with `backend_path=unverified` is not evidence that the bypass path executed.

### Fail-closed result handling

The evaluator rejects missing/zero-sample reports, unknown schemas, mismatched load,
inventory/dependencies, failed warmup, missing Get/Set, RPC/response errors, malformed
counts/histograms, failed/skipped correctness, fixture teardown errors, and incomplete
pairs. The profile is deliberately closed-loop; open-loop overload/drop experiments
are not silently treated as equivalent gate evidence.

The benchmark's existing pytest assertion combines RPC errors, dropped arrivals and
the absolute 1-second requirement. The evaluator permits **only** its exact known
latency-assertion message with pytest exit code 1, complete zero-error Get/Set evidence,
and no other JUnit error/skip/failure. It does not use `continue-on-error`, blanket
`|| true`, or patch the benchmark's pass/fail logic. All other exits fail. If pytest's
JUnit format or benchmark assertion changes, update the pinned adapter/validator and
contract tests together. The original absolute SLO remains a reported failure;
passing the evidence gate is not a claim that the SLO or a relative threshold passed.

## Public output and private diagnostics

Only allowlisted numeric metrics, immutable public source identities and manifest
hashes enter `comparison.json` and `summary.md`. Raw report `device`, free-text labels,
JUnit, inventory, adapter stdout/stderr and device logs are not uploaded. The adapter
must retain diagnostics privately under the dispatch identity before returning;
temporary controller files are deleted. Public failures use fixed text, with no raw
exception strings. Never put endpoints, device names, credentials, customer data or
private service URLs in the request, committed files or public logs.

## Validation and retest before activation

1. Run `python3 -m unittest discover -s tools/benchmark_gate -p 'test_*.py' -v`
   and validate the workflow with actionlint. These are offline policy checks only.
2. Address/test upstream benchmark review concerns: zero-request false pass, readiness
   labels after preload, small-batch allocation, cleanup and repository-owned unit
   coverage. The gate rejects zero samples and uses explicit warmup; it does not
   repair the producer. The reported all-dropped `max(empty)` concern does not reproduce
   in the pinned `_phase_metrics`: a set finish timestamp short-circuits its fallback.
3. Validate the pinned public sonic-mgmt fixture stack end to end on the commissioned
   target. Previously documented release-overlay measurements do not validate it.
   Exercise normal completion, RPC failure, setup/teardown failure, timeout and missing
   report. Verify no late writes and no contamination of the next run.
4. Run same-source A/A calibration, then a controlled base/head comparison with identical
   dependencies and a deliberately failing correctness case. Verify binary identities,
   result sanitization, manual approval, lease/watchdog and cancellation recovery.
   No extreme-load retest is needed merely to validate report-only gate plumbing.
5. Resolve outstanding producer CI failures from their logs before claiming its CI is
   green. Do not assume failed jobs are infrastructure noise or rerun blindly. Record
   real evidence and remaining limitations in this draft before enabling it; only later
   design head-SHA check publication and required-check policy with maintainers.
