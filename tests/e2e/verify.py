"""Fail closed on missing cases, skips, teardown failures or artifact mismatch."""
import json
from pathlib import Path
import sys
import xml.etree.ElementTree as ET


def verify(directory):
    root = Path(directory)
    suite = ET.parse(root / "junit.xml").getroot().find("testsuite")
    assert suite is not None, "Missing JUnit suite"
    assert {key: int(suite.get(key)) for key in ("tests", "errors", "failures", "skipped")} == {
        "tests": 7, "errors": 0, "failures": 0, "skipped": 0,
    }, "Seven tests must pass without errors or skips"
    phases = [json.loads(line) for line in (root / "phases.jsonl").read_text().splitlines()]
    assert len(phases) == 21 and all(p["outcome"] == "passed" for p in phases)
    nodes = {p["node"] for p in phases}
    assert len(nodes) == 7
    assert all({p["phase"] for p in phases if p["node"] == node} == {"setup", "call", "teardown"}
               for node in nodes)
    cleanup = json.loads((root / "cleanup.json").read_text())
    assert all(cleanup[key] for key in ("private_files_removed", "db_config_restored", "socket_removed"))
    assert len(cleanup["processes"]) == 2 and all(p["returncode"] == 0 for p in cleanup["processes"])
    runtime = json.loads((root / "runtime.json").read_text())
    artifact = json.loads((root / "artifact.json").read_text())
    assert runtime["running_sha256"] == runtime["telemetry_sha256"] == artifact["telemetry_sha256"]
    print("AUTH_ARTIFACT_E2E_VERIFIED tests=7 phases=21 failures=0 errors=0 skips=0 cleanup=complete "
          f"product_sha={artifact['product_sha']} telemetry_sha256={artifact['telemetry_sha256']}")


if __name__ == "__main__":
    verify(sys.argv[1])
