"""Extract exactly one produced amd64 sonic-gnmi package and record provenance."""
import hashlib
import json
from pathlib import Path
import os
import subprocess
import sys


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main(staging, extracted, results):
    candidates = []
    for package in Path(staging).glob("sonic-gnmi_*.deb"):
        info = subprocess.check_output([
            "dpkg-deb", "-f", str(package), "Package", "Architecture", "Version"
        ], text=True)
        fields = dict(line.split(": ", 1) for line in info.splitlines())
        if fields["Package"] == "sonic-gnmi" and fields["Architecture"] == "amd64":
            candidates.append((package, fields))
    if len(candidates) != 1:
        raise RuntimeError(f"Expected exactly one sonic-gnmi amd64 package, found {len(candidates)}")
    package, fields = candidates[0]
    subprocess.run(["dpkg-deb", "-x", str(package), extracted], check=True)
    binary = Path(extracted) / "usr/sbin/telemetry"
    if not binary.is_file() or binary.is_symlink():
        raise RuntimeError("Package lacks regular usr/sbin/telemetry")
    source = subprocess.check_output(["git", "-c", f"safe.directory={Path.cwd()}", "rev-parse", "HEAD"], text=True).strip()
    expected = os.environ.get("BUILD_SOURCEVERSION")
    if expected and source != expected:
        raise RuntimeError("Pipeline source revision does not match checkout")
    receipt = {
        "product_sha": source, "test_sha": source, "package_name": package.name,
        "package_metadata": fields, "package_sha256": sha(package), "telemetry_sha256": sha(binary),
        "build_id": os.environ.get("BUILD_BUILDID"), "source_ref": os.environ.get("BUILD_SOURCEBRANCH"),
        "pr_head_sha": os.environ.get("SYSTEM_PULLREQUEST_SOURCECOMMITID"),
        "profile": "certificate-db-get-file-write",
        "installed_packages": subprocess.check_output(["dpkg-query", "-W", "-f=${Package}=${Version}\n"], text=True).splitlines(),
    }
    libraries = subprocess.check_output(["ldd", str(binary)], text=True)
    if "not found" in libraries:
        raise RuntimeError("Artifact has missing shared libraries")
    receipt["libraries"] = libraries
    receipt["library_sha256"] = {word: sha(Path(word)) for word in libraries.split()
                                 if word.startswith("/") and Path(word).is_file()}
    receipt["test_files"] = {str(p): sha(p) for p in sorted(Path("tests/e2e").rglob("*")) if p.is_file()}
    for name in ("sonic-mgmt-common", "sonic-swss-common"):
        receipt[name + "_sha"] = subprocess.check_output(
            ["git", "-c", f"safe.directory={(Path('..') / name).resolve()}",
             "-C", "../" + name, "rev-parse", "HEAD"], text=True).strip()
    Path(results, "artifact.json").write_text(json.dumps(receipt, indent=2, sort_keys=True))
    print(sha(binary))


if __name__ == "__main__":
    main(*sys.argv[1:])
