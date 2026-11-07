"""Check public gNMI TAR stability and one telemetry source edit/restoration.

Equal no-op hashes prove output stability, not an executed-action cache hit.
BAZEL_COMMAND may supply startup options; BAZEL_BUILD_FLAGS selects the same
platform, repository overrides and dialout setting used by the producer build.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import time


ROOT = Path(__file__).resolve().parents[2]
TARGETS = (
    "//dist:runtime",
    "//dist:runtime.debug_symbols",
    "//telemetry",
    "//gnoi_client",
    "@sonic_swss_common//dist:libswsscommon_pkg",
)
CHANGED = {"//dist:runtime", "//dist:runtime.debug_symbols", "//telemetry"}
SOURCE = "telemetry/telemetry.go"
ORIGINAL_TEXT = b"invalid shared-memory configuration: %w"
EDITED_TEXT = b"invalid shared-memory configuration (incremental probe): %w"


def sha(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def output_file(target, bazel, flags, log):
    # cquery retains configured instances from previous builds. The TAR's
    # debug transition and source-test transition may both leave another
    # instance of this Go target in that graph. Select this invocation's
    # ordinary target configuration without discarding the cached state.
    files = subprocess.check_output(
        [*bazel, "cquery", *flags, "--output=files", "config(" + target + ", target)"], cwd=ROOT,
        stderr=log, text=True).splitlines()
    require(len(files) == 1, "Expected one public output for " + target + ": " + repr(files))
    path = ROOT / files[0]
    require(path.is_file() and path.stat().st_size, "Missing output for " + target)
    return path


def snapshot(name, output, bazel, flags):
    started = time.monotonic()
    with (output / (name + ".log")).open("w") as log:
        subprocess.run([*bazel, "build", *flags, *TARGETS], cwd=ROOT,
                       stdout=log, stderr=subprocess.STDOUT, check=True)
    seconds = time.monotonic() - started
    hashes = {}
    with (output / (name + ".queries.log")).open("w") as log:
        for target in TARGETS:
            hashes[target] = sha(output_file(target, bazel, flags, log))
    return {"seconds": seconds, "sha256": hashes}


def retain_restored(output, bazel, flags, baseline):
    """Publish only this invocation's restored TARs, never a later bazel-bin link."""
    records = {}
    with tempfile.TemporaryDirectory(prefix=".restored-", dir=output) as temporary, \
            (output / "retain.queries.log").open("w") as log:
        for name, target in (("runtime.tar", "//dist:runtime"),
                             ("runtime.debug_symbols.tar", "//dist:runtime.debug_symbols")):
            staged = Path(temporary) / name
            shutil.copyfile(output_file(target, bazel, flags, log), staged)
            staged.chmod(0o644)
            digest = sha(staged)
            require(digest == baseline[target], "Retained TAR differs from restored baseline: " + name)
            records[name] = {"target": target, "sha256": digest, "bytes": staged.stat().st_size}
            os.replace(staged, output / name)
    return records


def run(output, bazel, flags):
    output.mkdir(parents=True, exist_ok=True)
    result = {"status": "running", "input": SOURCE, "expected_changed_targets": sorted(CHANGED)}
    source = ROOT / SOURCE
    original = source.read_bytes()
    require(original.count(ORIGINAL_TEXT) == 1 and EDITED_TEXT not in original,
            "Telemetry source probe must match exactly one unmodified error message")
    result["source_sha256"] = hashlib.sha256(original).hexdigest()
    try:
        result["baseline"] = snapshot("baseline", output, bazel, flags)
        result["noop"] = snapshot("noop", output, bazel, flags)
        baseline = result["baseline"]["sha256"]
        require(result["noop"]["sha256"] == baseline, "No-op build changed public outputs")
        try:
            source.write_bytes(original.replace(ORIGINAL_TEXT, EDITED_TEXT))
            result["edit"] = snapshot("telemetry-edit", output, bazel, flags)
            changed = {target for target in TARGETS if result["edit"]["sha256"][target] != baseline[target]}
            require(changed == CHANGED, "Unexpected telemetry invalidation: " + repr(sorted(changed)))
        finally:
            source.write_bytes(original)
        result["restore"] = snapshot("telemetry-restore", output, bazel, flags)
        require(result["restore"]["sha256"] == baseline, "Restored source did not reproduce public outputs")
        require(source.read_bytes() == original, "Telemetry source restoration failed")
        result["artifacts"] = retain_restored(output, bazel, flags, baseline)
        result["status"] = "passed"
    finally:
        # Keep the checkout intact even when a build, query or hash check fails.
        if source.read_bytes() != original:
            source.write_bytes(original)
        if result["status"] != "passed":
            result["status"] = "failed"
        (output / "incremental.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path("bazel-evidence"))
    args = parser.parse_args()
    bazel = shlex.split(os.environ.get("BAZEL_COMMAND", "bazel"))
    require(bool(bazel), "BAZEL_COMMAND is empty")
    flags = ["--jobs=2", *shlex.split(os.environ.get("BAZEL_BUILD_FLAGS", ""))]
    result = run(args.output.resolve(), bazel, flags)
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
