#!/usr/bin/env python3
"""Trusted orchestration and fail-closed validation; no DUT credentials or commands."""

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import re
import statistics
import subprocess
import tempfile
import urllib.request
import xml.etree.ElementTree as ET


REPOSITORY = "sonic-net/sonic-gnmi"
BENCHMARK_SHA = "49f34002e111acaf79c64b4b0e212b2c47695d05"
ADAPTER = "/opt/gnmi-benchmark/bin/execute"
CORRECTNESS_CASES = {
    "native_get_values", "native_set_readback", "native_missing_key",
    "native_null_only_key", "bypass_partial_write", "fixture_cleanup",
}
FINGERPRINT_FIELDS = {
    "build_image", "dut_image", "dependencies", "client_environment",
    "runner_configuration", "adapter", "correctness_suite",
}
PROFILE = {
    "route_distribution": {"16000": 1, "20000": 12}, "vnet_count": 13,
    "total_routes": 256000, "routes_per_request": 1000,
    "measured_vnet_count": 12, "measured_routes": 240000,
    "background_routes": 16000, "platform_route_limit": 256000,
    "bypass_requested": True,
    "selection": "largest_vnets_round_robin_then_disjoint_batches", "preloaded": True,
}
WORKLOAD = {
    "profile": PROFILE, "concurrency": 2, "logical_requests": 100,
    "duration_seconds": 0, "warmup_seconds": 5, "timeout_seconds": 120,
    "traffic_pattern": "closed-loop", "marker": "ci-benchmark",
}


class InvalidEvidence(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise InvalidEvidence(message)


def integer(value, minimum=0):
    require(type(value) is int and value >= minimum, "invalid integer evidence")
    return value


def number(value, positive=False):
    require(type(value) in (int, float) and math.isfinite(value)
            and (value > 0 if positive else value >= 0), "invalid numeric evidence")
    return value


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def load_json(path):
    # Duplicate keys and non-finite constants cannot silently override evidence.
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, "duplicate JSON key")
            result[key] = value
        return result

    def invalid_constant(_):
        raise InvalidEvidence("non-finite JSON constant")

    require(path.is_file() and not path.is_symlink(), "missing or linked evidence file")
    require(path.stat().st_size <= 10 * 1024 * 1024, "oversized evidence file")
    return json.loads(path.read_text(), object_pairs_hook=pairs, parse_constant=invalid_constant)


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True, allow_nan=False) + "\n")


def make_plan(pr, control_sha, run_id):
    require(pr["state"] == "open", "PR must be open")
    require(pr["base"]["repo"]["full_name"] == REPOSITORY, "unexpected target repository")
    require(re.fullmatch(r"[0-9a-f]{40}", control_sha), "invalid control revision")
    require(re.fullmatch(r"[0-9]+:[0-9]+", run_id), "invalid dispatch identity")
    sources = {}
    for side in ("base", "head"):
        sha = pr[side]["sha"]
        repo = pr[side]["repo"]["full_name"]
        require(pr[side]["repo"]["private"] is False, "only public source repositories are supported")
        require(re.fullmatch(r"[0-9a-f]{40}", sha), "source must be a full commit SHA")
        require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo), "invalid source repository")
        sources[side] = {"repository": repo, "sha": sha}
    return {
        "schema_version": 1, "pr": integer(pr["number"], 1), "sources": sources,
        "control_sha": control_sha, "run_id": run_id,
        "benchmark_sha": BENCHMARK_SHA, "go_version": "1.25.9",
        "workload": WORKLOAD,
        "order": ["base", "head", "head", "base", "base", "head", "head", "base"],
    }


def plan_command(output):
    pr_number = os.environ["PR_NUMBER"]
    require(re.fullmatch(r"[1-9][0-9]*", pr_number), "invalid PR number")
    request = urllib.request.Request(
        "https://api.github.com/repos/{}/pulls/{}".format(REPOSITORY, pr_number),
        headers={"Authorization": "Bearer " + os.environ["GH_TOKEN"],
                 "Accept": "application/vnd.github+json"})
    with urllib.request.urlopen(request, timeout=30) as response:
        pr = json.load(response)
    run_id = os.environ["GITHUB_RUN_ID"] + ":" + os.environ["GITHUB_RUN_ATTEMPT"]
    plan = make_plan(pr, os.environ["GITHUB_SHA"], run_id)
    write_json(output, plan)
    with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
        for side, source in plan["sources"].items():
            stream.write("{}={}\n".format(side, source["sha"]))
    with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as stream:
        stream.write("## Manual benchmark preview: PR #{}\n\n".format(plan["pr"]))
        for side, source in plan["sources"].items():
            stream.write("- {}: `{}` at `{}`\n".format(side, source["repository"], source["sha"]))
        stream.write("\nApprove only after reviewing these exact commits. Performance is report-only.\n")


def junit_cases(path):
    require(path.is_file() and not path.is_symlink(), "missing JUnit evidence")
    require(path.stat().st_size <= 10 * 1024 * 1024, "oversized JUnit evidence")
    root = ET.fromstring(path.read_bytes())
    require(root.tag in ("testsuites", "testsuite"), "invalid JUnit root")
    require(not list(root.iter("error")) and not list(root.iter("skipped")), "JUnit errors or skips")
    cases = list(root.iter("testcase"))
    require(cases, "empty JUnit evidence")
    # Reject failures outside testcases rather than silently ignoring them.
    require(len(list(root.iter("failure"))) == sum(len(c.findall("failure")) for c in cases),
            "unexpected JUnit failure location")
    return cases


def validate_run(directory, request):
    evidence = load_json(directory / "evidence.json")
    require(evidence["request_sha256"] == digest(request), "stale or mismatched run evidence")
    require(evidence["source"] == request["source"], "tested source differs from requested source")
    require(evidence["benchmark_sha"] == BENCHMARK_SHA, "benchmark revision mismatch")
    require(evidence["go_version"] == "1.25.9", "toolchain mismatch")
    require(evidence["workload_sha256"] == digest(WORKLOAD), "workload configuration mismatch")
    require(evidence["restored"] is True, "restoration not verified")
    fingerprints = evidence["fingerprints"]
    require(set(fingerprints) == FINGERPRINT_FIELDS, "incomplete environment fingerprint")
    for value in fingerprints.values():
        require(isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value), "invalid fingerprint")
    for field in ("package_sha256", "loaded_binary_sha256"):
        require(re.fullmatch(r"[0-9a-f]{64}", evidence[field]), "missing build identity")

    correctness = junit_cases(directory / "correctness.xml")
    names = [c.get("name") for c in correctness]
    require(len(names) == len(CORRECTNESS_CASES) and set(names) == CORRECTNESS_CASES,
            "required correctness cases missing or duplicated")
    require(not any(c.findall("failure") for c in correctness), "correctness checks failed")
    require(integer(evidence["correctness_exit_code"]) == 0, "correctness process failed")

    report = load_json(directory / "report.json")
    require(report["schema_version"] == 10 and report["record_type"] == "gnmi_benchmark",
            "unsupported benchmark schema")
    require(report["marker"] == WORKLOAD["marker"], "benchmark marker mismatch")
    benchmark = report["benchmark"]
    require(benchmark["profile"] == PROFILE, "benchmark inventory or batching mismatch")
    for key, value in {
        "client": "grpcio", "blaster": "route-table", "protocol": "gnmi",
        "connection_type": "TLS", "auth_mode": "normal", "connections": 1,
        "connection_policy": "single_persistent_channel", "connection_setup": "ready_before_load",
        "workload_model": "closed", "rpc_timeout_scope": "per_rpc",
        "response_validation": "per_rpc_grpc_status_and_set_response_errors",
        "histogram_profile": "grpc_a66_latency_ms_v1",
    }.items():
        require(benchmark[key] == value, "benchmark protocol or measurement mismatch")
    require(report["load"] == {"iterations": 100, "concurrency": 2,
                               "duration_seconds": 0, "warmup_seconds": 5}, "load mismatch or empty measurement")
    execution = report["execution"]
    require(execution["traffic_pattern"] == "closed-loop" and execution["mode"] == "count"
            and "scheduling" not in execution, "unexpected scheduling mode")
    require(execution["concurrency"] == 2 and execution["workers_with_requests"] == 2,
            "worker execution incomplete")
    require(1 <= integer(execution["peak_client_inflight"]) <= 2, "invalid concurrency evidence")
    warmup = execution["warmup"]
    completed = integer(warmup["completed"], 1)
    require(warmup["admission_seconds"] == 5 and warmup["iteration_status_counts"] == {"OK": completed}
            and warmup["response_errors"] == 0 and warmup["dropped"] == 0, "warmup failed or missing")
    number(execution["connection_ready_seconds"])
    require(set(report["requests"]) == {"get:1000", "set:1000"}, "missing Get or Set samples")
    metrics, exceeded, elapsed_values = {}, 0, []
    for method in ("get", "set"):
        rpc = report["requests"][method + ":1000"]
        require(rpc["request_type"] == method and rpc["entry_count"] == 1000
                and rpc["count_unit"] == "rpc", "wrong request population")
        expected_counts = {"planned": 100, "started": 100, "completed": 100, "successful": 100,
                           "failed": 0, "unfinished": 0, "attempts": 100, "response_errors": 0}
        require(rpc["counts"] == expected_counts and rpc["grpc_status_counts"] == {"OK": 100},
                "RPC failures, incomplete work, or invalid counts")
        latency = rpc["latency_ms"]
        require(integer(latency["samples"]) == 100, "missing latency samples")
        require(latency["percentile_method"] == "nearest_rank", "percentile method mismatch")
        require(latency["sample_population"] == "successful_{}:1000_calls".format(method)
                and latency["bucket_semantics"] == "lower_exclusive_upper_inclusive", "latency population mismatch")
        buckets = latency["bucket_counts"]
        require(len(buckets) == 42 and sum(integer(v) for v in buckets) == 100, "invalid histogram")
        values = [number(latency[k], positive=True) for k in ("p50", "p95", "p99", "max")]
        require(values == sorted(values), "inconsistent percentiles")
        average = number(latency["average"], positive=True)
        require(average <= values[-1] and math.isclose(number(latency["sum"]), average * 100),
                "inconsistent latency aggregate")
        requirement = rpc["latency_requirement"]
        slow = integer(requirement["exceeded"])
        require(requirement["limit_ms"] == 1000 and requirement["evaluated_successful_requests"] == 100
                and integer(requirement["within_limit"]) + slow == 100
                and sum(buckets[35:]) == slow and requirement["passed"] is (slow == 0),
                "inconsistent latency requirement")
        exceeded += slow
        elapsed = number(rpc["measurement_elapsed_seconds"], positive=True)
        elapsed_values.append(elapsed)
        for key in ("successful", "completed"):
            require(math.isclose(number(rpc["rates_per_second"][key], positive=True), 100 / elapsed),
                    "inconsistent completion rate")
        metrics[method] = {"p95_ms": latency["p95"], "mean_ms": average,
                           "completed_rpcs_per_second": 100 / elapsed, "over_1s": slow}
    require(elapsed_values[0] == elapsed_values[1], "Get/Set measurement windows differ")

    cases = junit_cases(directory / "benchmark.xml")
    require(len(cases) == 1 and cases[0].get("name") == "test_gnmi_benchmark", "unexpected benchmark tests")
    failures = cases[0].findall("failure")
    exit_code = integer(evidence["benchmark_exit_code"])
    if exceeded:
        expected = ("Failed: gNMI benchmark marker=ci-benchmark has RPC failures, "
                    "requests over 1000ms, or dropped arrivals")
        require(exit_code == 1 and len(failures) == 1 and failures[0].get("message") == expected,
                "benchmark failure is not solely the known latency assertion")
    else:
        require(exit_code == 0 and not failures, "benchmark process failed")
    return {"metrics": metrics, "fingerprints": fingerprints,
            "package_sha256": evidence["package_sha256"], "loaded_binary_sha256": evidence["loaded_binary_sha256"]}


def compare(runs):
    require(len(runs) == 8, "four complete base/head pairs required")
    fingerprints = runs[0]["fingerprints"]
    require(all(r["fingerprints"] == fingerprints for r in runs), "environment or dependencies changed")
    for indices in ((0, 3, 4, 7), (1, 2, 5, 6)):
        for field in ("package_sha256", "loaded_binary_sha256"):
            require(len({runs[i][field] for i in indices}) == 1,
                    "same source did not use the same artifact across repetitions")
    result = {}
    for method in ("get", "set"):
        result[method] = {}
        for metric in ("p95_ms", "mean_ms", "completed_rpcs_per_second"):
            changes = []
            for pair, offset in enumerate(range(0, 8, 2)):
                base, head = (runs[offset], runs[offset + 1]) if pair % 2 == 0 else (runs[offset + 1], runs[offset])
                changes.append(100 * (head["metrics"][method][metric] / base["metrics"][method][metric] - 1))
            result[method][metric] = {"paired_change_percent": changes,
                                      "median_change_percent": statistics.median(changes),
                                      "min_change_percent": min(changes), "max_change_percent": max(changes)}
    return result


def run_command(plan_path, output):
    output.mkdir(parents=True, exist_ok=False)
    # All error output is fixed text: adapter logs/reports may contain private data.
    write_json(output / "comparison.json", {"status": "failed", "performance_gate": "not_evaluated"})
    (output / "summary.md").write_text("## Benchmark gate failed\n\nMissing, invalid, or failed execution evidence.\n")
    plan = load_json(plan_path)
    # Reconstruct the allowlisted plan rather than trusting arbitrary artifact fields.
    resolved_pr = {"state": "open", "number": plan["pr"]}
    for side in ("base", "head"):
        resolved_pr[side] = {"sha": plan["sources"][side]["sha"],
                             "repo": {"full_name": plan["sources"][side]["repository"], "private": False}}
    require(plan == make_plan(resolved_pr, plan["control_sha"], plan["run_id"]), "invalid comparison plan")
    require(plan["control_sha"] == os.environ["GITHUB_SHA"]
            and plan["run_id"] == os.environ["GITHUB_RUN_ID"] + ":" + os.environ["GITHUB_RUN_ATTEMPT"],
            "plan belongs to another workflow execution")
    require(Path(ADAPTER).is_file() and os.access(ADAPTER, os.X_OK), "runner adapter not commissioned")
    runs = []
    with tempfile.TemporaryDirectory(prefix="gnmi-benchmark-private-") as temporary:
        root = Path(temporary)
        for index, side in enumerate(plan["order"]):
            directory = root / str(index)
            directory.mkdir(mode=0o700)
            request = dict(plan, sequence=index, side=side, source=plan["sources"][side])
            request_path = directory / "request.json"
            write_json(request_path, request)
            with (directory / "adapter.log").open("wb") as log:
                result = subprocess.run(
                    [ADAPTER, "--request", str(request_path), "--output", str(directory)],
                    stdout=log, stderr=subprocess.STDOUT, timeout=1200, check=False,
                    env={"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                         "HOME": str(root), "LANG": "C.UTF-8"})
            require(result.returncode == 0, "runner build, execution, or restoration failed")
            runs.append(validate_run(directory, request))
            # Stop before the next run if isolation/comparability has already failed.
            require(runs[-1]["fingerprints"] == runs[0]["fingerprints"], "incomparable execution environments")
    comparisons = compare(runs)
    write_json(output / "comparison.json", {
        "status": "valid_evidence", "correctness_gate": "passed", "performance_gate": "report_only",
        "plan": plan, "runs": runs, "comparison": comparisons,
    })
    lines = ["## Benchmark evidence gate passed", "", "**Performance: report-only; no regression verdict.**", "",
             "Four paired repetitions (AB, BA, AB, BA). Each run: 100 Gets and 100 Sets.", "",
             "| RPC | Metric | Median paired change | Range across pairs |", "|---|---|---:|---:|"]
    for method, metrics in comparisons.items():
        for metric, value in metrics.items():
            lines.append("| {} | {} | {:+.2f}% | {:+.2f}% to {:+.2f}% |".format(
                method, metric, value["median_change_percent"],
                value["min_change_percent"], value["max_change_percent"]))
    lines += ["", "Positive latency change is slower; positive completion-rate change is faster.",
              "Completion rate includes drain and is not a steady-state throughput claim.",
              "The original 1-second exceedance counts remain in comparison.json."]
    (output / "summary.md").write_text("\n".join(lines) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    plan_parser = subparsers.add_parser("plan")
    plan_parser.add_argument("--output", type=Path, required=True)
    run_parser = subparsers.add_parser("run")
    run_parser.add_argument("--plan", type=Path, required=True)
    run_parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "plan":
            plan_command(args.output)
        else:
            run_command(args.plan, args.output)
    except Exception:
        # Never reflect source-controlled strings, raw XML, paths, or adapter output.
        print("Benchmark gate failed: execution or evidence validation did not complete.")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
