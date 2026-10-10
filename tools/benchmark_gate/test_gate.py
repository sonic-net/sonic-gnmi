"""Offline policy tests. Synthetic evidence is not a benchmark result."""

import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import xml.etree.ElementTree as ET

import gate


def evidence_fixture(directory, request, *, slow=False):
    """A minimal schema-10 producer result plus independently supplied runner evidence."""
    fingerprints = {key: "a" * 64 for key in gate.FINGERPRINT_FIELDS}
    evidence = {
        "request_sha256": gate.digest(request), "source": request["source"],
        "benchmark_sha": gate.BENCHMARK_SHA, "go_version": "1.25.9",
        "workload_sha256": gate.digest(gate.WORKLOAD), "restored": True,
        "fingerprints": fingerprints, "package_sha256": "b" * 64,
        "loaded_binary_sha256": "c" * 64, "correctness_exit_code": 0,
        "benchmark_exit_code": int(slow),
    }
    gate.write_json(directory / "evidence.json", evidence)
    suite = ET.Element("testsuite")
    for name in sorted(gate.CORRECTNESS_CASES):
        ET.SubElement(suite, "testcase", name=name)
    ET.ElementTree(suite).write(directory / "correctness.xml")
    suite = ET.Element("testsuite")
    case = ET.SubElement(suite, "testcase", name="test_gnmi_benchmark")
    if slow:
        ET.SubElement(case, "failure", message=(
            "Failed: gNMI benchmark marker=ci-benchmark has RPC failures, "
            "requests over 1000ms, or dropped arrivals"))
    ET.ElementTree(suite).write(directory / "benchmark.xml")
    benchmark = {
        "profile": copy.deepcopy(gate.PROFILE), "client": "grpcio", "blaster": "route-table",
        "protocol": "gnmi", "connection_type": "TLS", "auth_mode": "normal", "connections": 1,
        "connection_policy": "single_persistent_channel", "connection_setup": "ready_before_load",
        "workload_model": "closed", "rpc_timeout_scope": "per_rpc",
        "response_validation": "per_rpc_grpc_status_and_set_response_errors",
        "histogram_profile": "grpc_a66_latency_ms_v1",
    }
    report = {
        "record_type": "gnmi_benchmark", "schema_version": 10, "marker": "ci-benchmark",
        "benchmark": benchmark,
        "load": {"iterations": 100, "concurrency": 2, "duration_seconds": 0, "warmup_seconds": 5},
        "execution": {"traffic_pattern": "closed-loop", "mode": "count", "concurrency": 2,
                      "workers_with_requests": 2, "peak_client_inflight": 2, "connection_ready_seconds": 0.1,
                      "warmup": {"completed": 10, "admission_seconds": 5,
                                 "iteration_status_counts": {"OK": 10}, "response_errors": 0, "dropped": 0}},
        "requests": {},
    }
    latency = 2000 if slow else 100
    buckets = [0] * 42
    buckets[35 if slow else 24] = 100
    for method in ("get", "set"):
        report["requests"][method + ":1000"] = {
            "request_type": method, "entry_count": 1000, "count_unit": "rpc",
            "counts": {"planned": 100, "started": 100, "completed": 100, "successful": 100,
                       "failed": 0, "unfinished": 0, "attempts": 100, "response_errors": 0},
            "grpc_status_counts": {"OK": 100},
            "latency_ms": {"samples": 100, "average": latency, "sum": latency * 100, "p50": latency,
                           "p95": latency, "p99": latency, "max": latency, "percentile_method": "nearest_rank",
                           "sample_population": "successful_{}:1000_calls".format(method),
                           "bucket_semantics": "lower_exclusive_upper_inclusive",
                           "bucket_counts": buckets},
            "latency_requirement": {"limit_ms": 1000, "evaluated_successful_requests": 100,
                                    "within_limit": 0 if slow else 100, "exceeded": 100 if slow else 0,
                                    "passed": not slow},
            "measurement_elapsed_seconds": 100, "rates_per_second": {"completed": 1, "successful": 1},
        }
    gate.write_json(directory / "report.json", report)
    return report, evidence


class GateTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.request = {"source": {"repository": gate.REPOSITORY, "sha": "1" * 40}, "sequence": 0}

    def test_valid_slow_measurement_is_report_only(self):
        evidence_fixture(self.directory, self.request, slow=True)
        result = gate.validate_run(self.directory, self.request)
        self.assertEqual(result["metrics"]["get"]["over_1s"], 100)
        self.assertEqual(result["metrics"]["set"]["p95_ms"], 2000)

    def test_rejects_measurement_failure_matrix(self):
        def empty(report):
            report["requests"] = {}

        def missing_set(report):
            del report["requests"]["set:1000"]

        def rpc_error(report):
            report["requests"]["get:1000"]["grpc_status_counts"] = {"OK": 99, "NOT_FOUND": 1}

        def count_error(report):
            report["requests"]["get:1000"]["counts"]["successful"] = 0

        def wrong_inventory(report):
            report["benchmark"]["profile"]["route_distribution"] = {"1000": 256}

        def wrong_schema(report):
            report["schema_version"] = 11

        def nonfinite(report):
            report["requests"]["get:1000"]["latency_ms"]["p95"] = float("nan")

        def zero_sample(report):
            report["requests"]["get:1000"]["latency_ms"]["samples"] = 0

        def wrong_rate(report):
            report["requests"]["get:1000"]["rates_per_second"]["completed"] = 100

        def warmup_error(report):
            report["execution"]["warmup"]["response_errors"] = 1

        for change in (empty, missing_set, rpc_error, count_error, wrong_inventory, wrong_schema,
                       nonfinite, zero_sample, wrong_rate, warmup_error):
            with self.subTest(change=change.__name__):
                report, _ = evidence_fixture(self.directory, self.request)
                change(report)
                (self.directory / "report.json").write_text(json.dumps(report))
                with self.assertRaises(gate.InvalidEvidence):
                    gate.validate_run(self.directory, self.request)

    def test_missing_report_is_failure(self):
        evidence_fixture(self.directory, self.request)
        (self.directory / "report.json").unlink()
        with self.assertRaises(gate.InvalidEvidence):
            gate.validate_run(self.directory, self.request)

    def test_correctness_failure_skip_or_empty_suite_blocks(self):
        for kind in ("failure", "skipped", "error", "empty"):
            with self.subTest(kind=kind):
                evidence_fixture(self.directory, self.request)
                path = self.directory / "correctness.xml"
                root = ET.parse(path).getroot()
                if kind == "empty":
                    root.clear()
                else:
                    ET.SubElement(root.find("testcase"), kind)
                ET.ElementTree(root).write(path)
                with self.assertRaises(gate.InvalidEvidence):
                    gate.validate_run(self.directory, self.request)

    def test_benchmark_teardown_error_not_hidden_by_latency_failure(self):
        evidence_fixture(self.directory, self.request, slow=True)
        path = self.directory / "benchmark.xml"
        root = ET.parse(path).getroot()
        ET.SubElement(root.find("testcase"), "error", message="teardown failed")
        ET.ElementTree(root).write(path)
        with self.assertRaises(gate.InvalidEvidence):
            gate.validate_run(self.directory, self.request)

    def test_arbitrary_pytest_failure_cannot_be_waived(self):
        evidence_fixture(self.directory, self.request, slow=True)
        path = self.directory / "benchmark.xml"
        root = ET.parse(path).getroot()
        root.find("testcase/failure").set("message", "AssertionError: unrelated failure")
        ET.ElementTree(root).write(path)
        with self.assertRaises(gate.InvalidEvidence):
            gate.validate_run(self.directory, self.request)

    def test_stale_source_restoration_and_environment_evidence(self):
        mutations = {"request_sha256": "0" * 64, "restored": False,
                     "go_version": "1.26.0", "correctness_exit_code": 5,
                     "benchmark_exit_code": 2, "fingerprints": {}, "loaded_binary_sha256": ""}
        for key, value in mutations.items():
            with self.subTest(field=key):
                _, evidence = evidence_fixture(self.directory, self.request)
                evidence[key] = value
                gate.write_json(self.directory / "evidence.json", evidence)
                with self.assertRaises(gate.InvalidEvidence):
                    gate.validate_run(self.directory, self.request)

    def test_pair_order_and_median_not_pooled_percentiles(self):
        evidence_fixture(self.directory, self.request)
        base = gate.validate_run(self.directory, self.request)
        head = copy.deepcopy(base)
        head["metrics"]["get"]["p95_ms"] = 120
        head["metrics"]["set"]["completed_rpcs_per_second"] = 0.5
        runs = [base, head, head, base, base, head, head, base]
        comparison = gate.compare(runs)
        self.assertAlmostEqual(comparison["get"]["p95_ms"]["median_change_percent"], 20)
        self.assertEqual(comparison["set"]["completed_rpcs_per_second"]["paired_change_percent"], [-50] * 4)
        with self.assertRaises(gate.InvalidEvidence):
            gate.compare(runs[:-1])
        changed = copy.deepcopy(runs)
        changed[-1]["fingerprints"]["dependencies"] = "d" * 64
        with self.assertRaises(gate.InvalidEvidence):
            gate.compare(changed)

    def test_json_duplicates_rejected(self):
        path = self.directory / "duplicate.json"
        path.write_text('{"successful":0,"successful":100}')
        with self.assertRaises(gate.InvalidEvidence):
            gate.load_json(path)

    def test_plan_freezes_both_sources_and_rejects_foreign_base(self):
        pr = {"number": 123, "state": "open",
              "base": {"sha": "1" * 40, "repo": {"full_name": gate.REPOSITORY, "private": False}},
              "head": {"sha": "2" * 40, "repo": {"full_name": "example/sonic-gnmi", "private": False}}}
        plan = gate.make_plan(pr, "3" * 40, "123:1")
        self.assertEqual(plan["sources"]["head"]["sha"], "2" * 40)
        self.assertEqual(plan["order"], ["base", "head", "head", "base", "base", "head", "head", "base"])
        pr["base"]["repo"]["full_name"] = "example/other"
        with self.assertRaises(gate.InvalidEvidence):
            gate.make_plan(pr, "3" * 40, "123:1")

    def test_uncommissioned_runner_fails_with_sanitized_summary(self):
        pr = {"number": 123, "state": "open",
              "base": {"sha": "1" * 40, "repo": {"full_name": gate.REPOSITORY, "private": False}},
              "head": {"sha": "2" * 40, "repo": {"full_name": gate.REPOSITORY, "private": False}}}
        plan = gate.make_plan(pr, "3" * 40, "123:1")
        path = self.directory / "plan.json"
        gate.write_json(path, plan)
        output = self.directory / "output"
        with patch.dict(os.environ, {"GITHUB_SHA": "3" * 40, "GITHUB_RUN_ID": "123", "GITHUB_RUN_ATTEMPT": "1"}), \
                patch.object(gate, "ADAPTER", str(self.directory / "missing-adapter")):
            with self.assertRaises(gate.InvalidEvidence):
                gate.run_command(path, output)
        result = gate.load_json(output / "comparison.json")
        self.assertEqual(result["status"], "failed")
        self.assertNotIn(str(self.directory), (output / "summary.md").read_text())

    def test_orchestration_orders_all_runs_and_only_publishes_allowlisted_fields(self):
        pr = {"number": 123, "state": "open",
              "base": {"sha": "1" * 40, "repo": {"full_name": gate.REPOSITORY, "private": False}},
              "head": {"sha": "2" * 40, "repo": {"full_name": gate.REPOSITORY, "private": False}}}
        plan = gate.make_plan(pr, "3" * 40, "123:1")
        path = self.directory / "plan.json"
        gate.write_json(path, plan)
        output = self.directory / "output"
        observed = []

        def adapter(args, **kwargs):
            request = gate.load_json(Path(args[2]))
            directory = Path(args[4])
            observed.append(request["side"])
            report, _ = evidence_fixture(directory, request, slow=True)
            report["device"] = {"hostname": "PRIVATE_SENTINEL"}
            gate.write_json(directory / "report.json", report)
            self.assertNotIn("GH_TOKEN", kwargs["env"])
            return gate.subprocess.CompletedProcess(args, 0)

        with patch.dict(os.environ, {"GITHUB_SHA": "3" * 40, "GITHUB_RUN_ID": "123", "GITHUB_RUN_ATTEMPT": "1"}), \
                patch.object(gate, "ADAPTER", "/bin/true"), patch.object(gate.subprocess, "run", side_effect=adapter):
            gate.run_command(path, output)
        self.assertEqual(observed, plan["order"])
        result = gate.load_json(output / "comparison.json")
        self.assertEqual(result["performance_gate"], "report_only")
        self.assertNotIn("PRIVATE_SENTINEL", json.dumps(result))
        self.assertEqual(len(result["runs"]), 8)


if __name__ == "__main__":
    unittest.main()
