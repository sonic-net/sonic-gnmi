"""Prove client-template invalidation without changing unrelated gNMI outputs.

BAZEL_COMMAND and BAZEL_BUILD_FLAGS have the same meaning as in
check_incremental.py. Equal no-op hashes establish byte stability, not a
claim that an action ran or that the build was served from a cache.
"""

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shlex
import subprocess
import time

# Load this declared sibling even when PYTHONSAFEPATH omits the script directory.
_spec = importlib.util.spec_from_file_location(
    "check_incremental", Path(__file__).with_name("check_incremental.py"))
_incremental = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_incremental)
output_file, require, sha = _incremental.output_file, _incremental.require, _incremental.sha


ROOT = Path(__file__).resolve().parents[2]
SOURCE = "tools/pyang_plugins/templates/client/main.j2"
ORIGINAL_TEXT = b"Address:port of gNOI Server"
EDITED_TEXT = b"Address:port of gNOI Server (generator incremental probe)"
GENERATED_TARGET = "//gnoi_yang:generated"
GENERATED = {
    "client/gnoi_openconfig_client/main.go",
    "client/gnoi_sonic_client/main.go",
    "proto/sonic_show_techsupport/sonic_show_techsupport.proto",
    "server/gnoiyang.go",
    "server/openconfig_register.go",
    "server/sonic_register.go",
    "server/sonic_show_techsupport/sonic_show_techsupport.go",
}
TARGETS = (
    "//dist:runtime",
    "//dist:runtime.debug_symbols",
    "//gnoi_yang:gnoi_openconfig_client",
    "//gnoi_yang:gnoi_sonic_client",
    "//telemetry",
    "//gnoi_client",
    "@sonic_swss_common//dist:libswsscommon_pkg",
    "//gnoi_yang:generated_proto_source",
    "//proto:generated_proto_source",
    "//proto/gnoi:generated_proto_source",
    "//proto/gnoi/oras:generated_proto_source",
)
CHANGED = set(TARGETS[:4]) | {
    "generated/client/gnoi_openconfig_client/main.go",
    "generated/client/gnoi_sonic_client/main.go",
}


def snapshot(stage, output, bazel, flags):
    started = time.monotonic()
    with (output / (stage + ".log")).open("w") as log:
        subprocess.run([*bazel, "build", *flags, *TARGETS, GENERATED_TARGET],
                       cwd=ROOT, stdout=log, stderr=subprocess.STDOUT, check=True)
    seconds = time.monotonic() - started
    with (output / (stage + ".queries.log")).open("w") as log:
        hashes = {target: sha(output_file(target, bazel, flags, log)) for target in TARGETS}
        paths = subprocess.check_output(
            [*bazel, "cquery", *flags, "--output=files", "config(" + GENERATED_TARGET + ", target)"],
            cwd=ROOT, stderr=log, text=True).splitlines()
        generated = {}
        for value in paths:
            marker = "/gnoi_yang/generated/"
            require(value.count(marker) == 1, "Unexpected generated source location")
            name = value.split(marker, 1)[1]
            require(name not in generated, "Duplicate generated source: " + name)
            generated[name] = sha(ROOT / value)
        require(set(generated) == GENERATED, "Generated source inventory changed")
        hashes.update({"generated/" + name: digest for name, digest in generated.items()})
    return {"seconds": seconds, "sha256": hashes}


def run(output, bazel, flags):
    output.mkdir(parents=True, exist_ok=True)
    result = {"status": "running", "input": SOURCE, "expected_changed_outputs": sorted(CHANGED)}
    source = ROOT / SOURCE
    original = source.read_bytes()
    try:
        require(original.count(ORIGINAL_TEXT) == 1 and EDITED_TEXT not in original,
                "Client template must contain exactly one unmodified target help string")
        result["source_sha256"] = hashlib.sha256(original).hexdigest()
        result["baseline"] = snapshot("generator-baseline", output, bazel, flags)
        result["noop"] = snapshot("generator-noop", output, bazel, flags)
        baseline = result["baseline"]["sha256"]
        require(result["noop"]["sha256"] == baseline, "No-op build changed generator or public outputs")
        try:
            source.write_bytes(original.replace(ORIGINAL_TEXT, EDITED_TEXT))
            result["edit"] = snapshot("generator-edit", output, bazel, flags)
            changed = {key for key, digest in result["edit"]["sha256"].items() if baseline[key] != digest}
            require(changed == CHANGED, "Unexpected template invalidation: " + repr(sorted(changed)))
        finally:
            source.write_bytes(original)
        result["restore"] = snapshot("generator-restore", output, bazel, flags)
        require(result["restore"]["sha256"] == baseline, "Restored template did not reproduce outputs")
        require(source.read_bytes() == original, "Template restoration failed")
        result["status"] = "passed"
    finally:
        if source.read_bytes() != original:
            source.write_bytes(original)
        if result["status"] != "passed":
            result["status"] = "failed"
        (output / "generator-incremental.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path("bazel-evidence"))
    args = parser.parse_args()
    bazel = shlex.split(os.environ.get("BAZEL_COMMAND", "bazel"))
    require(bool(bazel), "BAZEL_COMMAND is empty")
    flags = ["--jobs=2", *shlex.split(os.environ.get("BAZEL_BUILD_FLAGS", ""))]
    print(json.dumps(run(args.output.resolve(), bazel, flags), indent=2))


if __name__ == "__main__":
    main()
