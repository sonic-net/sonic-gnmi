#!/usr/bin/env python3
"""Prepare declared generated inputs, then compile ordinary Go for CodeQL.

Bazel prepares owner artifacts before tracing starts. CodeQL traces only the
subsequent native Go build in the actual PR checkout, including CGo and tests.
The independent subscription regression's inputs and behavior stay unchanged.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import subprocess
import tarfile
import zipfile

_spec = importlib.util.spec_from_file_location(
    "native_subscription", Path(__file__).with_name("native_subscription.py"))
_native = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_native)
digest, run, unpack, owner = _native.digest, _native.run, _native.unpack, _native.owner

ROOT = Path(__file__).resolve().parents[2]
TAGS = "gnmi_translib_write,gnmi_native_write"
MGMT = {"revision": "05f83dcab8fe2e1a0e0fc37ac172b289d6968e2b",
        "sha256": "a30cdd092457ec7579c0a6c07632a55f6fca8e749877fabb368d9fb75e41d98e"}
LIBNL_RUNTIME_TARGETS = ["@libnl3//:libnl-3_pkg", "@libnl3//:libnl-route-3_pkg",
                         "@libnl3//:libnl-nf-3_pkg"]
TARGETS = ["@sonic_swss_common//dist:libswsscommon_sdk",
           "@sonic_swss_common//dist:libswsscommon_pkg",
           "@sonic_swss_common//goext:swsscommon_go_gen",
           "@sonic_mgmt_common//dist:codegen", "@sonic_mgmt_common//dist:runtime",
           "@libyang//:libyang_pkg", "@libyang//:public_headers", "//dist:sources",
           *LIBNL_RUNTIME_TARGETS]
GENERATED = {
    "proto/sonic_internal.pb.go", "proto/gnoi/sonic_debug.pb.go", "proto/gnoi/oras/oras.pb.go",
    "build/gnoi_yang/client/gnoi_openconfig_client/main.go",
    "build/gnoi_yang/client/gnoi_sonic_client/main.go",
    "build/gnoi_yang/proto/sonic_show_techsupport/sonic_show_techsupport.proto",
    "build/gnoi_yang/proto/sonic_show_techsupport/sonic_show_techsupport.pb.go",
    "build/gnoi_yang/server/gnoiyang.go", "build/gnoi_yang/server/openconfig_register.go",
    "build/gnoi_yang/server/sonic_register.go",
    "build/gnoi_yang/server/sonic_show_techsupport/sonic_show_techsupport.go",
}


def save(path, data):
    path.write_text(json.dumps(data, indent=2) + "\n")


def write_source(relative, content):
    path = ROOT / relative
    # Generated protobufs are tracked legacy files. Replace only this explicit
    # inventory with the exact corresponding public Bazel generator output.
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(content)
    return digest(path)


def prepare(work, evidence):
    assert platform.machine() == "x86_64"
    assert "VERSION_CODENAME=trixie" in Path("/etc/os-release").read_text()
    assert Path("/.dockerenv").exists() or Path("/run/.containerenv").exists()
    assert not (work / "prepared.json").exists(), "Use a fresh preparation directory"
    head = run(["git", "rev-parse", "HEAD"], cwd=ROOT).stdout.strip()
    parents = run(["git", "show", "-s", "--format=%P", "HEAD"], cwd=ROOT).stdout.split()
    assert not os.environ.get("CI_HEAD_SHA") or os.environ["CI_HEAD_SHA"] in [head, *parents]
    assert not os.environ.get("CI_BASE_SHA") or os.environ["CI_BASE_SHA"] in parents
    registry = "main"
    # Source CI and CodeQL use the maintained registry; MODULE controls owners.
    rc = (ROOT / ".bazelrc").read_text()
    main = "https://raw.githubusercontent.com/securely1g/sonic-bazel-registry/main"
    assert rc.count(main) == 1
    home = Path.home() / ".bazelrc"
    if home.exists():
        rc += "\n" + "\n".join(line for line in home.read_text().splitlines()
            if line.startswith(("build --disk_cache=", "common --repository_cache="))) + "\n"
    config = work / "inputs.bazelrc"; config.write_text(rc)
    command = shlex.split(os.environ.get("CODEQL_BAZEL_COMMAND", "bazel")) + [
        "--nosystem_rc", "--nohome_rc", "--noworkspace_rc", "--bazelrc=" + str(config),
        "--output_base=" + str(work / "bazel-output")]
    flags = ["--jobs=2", "--host_platform=@sonic_build_infra//platforms:x86_64_trixie",
             *shlex.split(os.environ.get("CODEQL_BAZEL_FLAGS", ""))]
    sequence = 0

    def bazel(operation, arguments):
        nonlocal sequence
        sequence += 1
        return run([*command, operation, *flags, *arguments], cwd=ROOT,
                   log=evidence / f"inputs-{sequence:02d}-{operation}.log").stdout

    bazel("build", TARGETS)
    hdr_expr = "labels(hdrs, @libyang//:public_headers)"
    headers = bazel("cquery", [hdr_expr, "--output=label"])
    labels = [line.split()[0] for line in headers.splitlines() if line.startswith("@")]
    if not labels:
        hdr_expr = 'labels(hdrs, filter("^@@?libyang", deps(@libyang//:public_headers, 1)))'
        headers = bazel("cquery", [hdr_expr, "--output=label"])
        labels = [line.split()[0] for line in headers.splitlines() if line.startswith("@")]
    assert labels
    bazel("build", labels)
    execution = work / "bazel-output/execroot/_main"
    outputs = {}
    for target in TARGETS:
        expression = (hdr_expr if target.endswith(":public_headers")
                      else "config(" + target + ", target)")
        result = bazel("cquery", [expression, "--output=files"])
        paths = [execution / line for line in result.splitlines()
                 if line.startswith(("bazel-out/", "external/")) and (execution / line).is_file()]
        assert paths, (target, result)
        outputs[target] = paths
    report = {"tested_revision": head, "tested_parents": parents, "registry": registry,
              "module_sha256": digest(ROOT / "MODULE.bazel"), "tags": TAGS,
              "driver_sha256": digest(__file__), "generated_sha256": {},
              "owner_output_sha256": {target: {p.name: digest(p) for p in paths}
                                      for target, paths in outputs.items()}}
    sdk, runtime = work / "sdk", work / "runtime"
    for target, destination in [(TARGETS[0], sdk), (TARGETS[1], runtime),
                                (TARGETS[4], runtime), (TARGETS[5], runtime),
                                *((target, runtime) for target in LIBNL_RUNTIME_TARGETS)]:
        assert len(outputs[target]) == 1
        unpack(outputs[target][0], destination)
    mgmt = owner(work, "sonic-mgmt-common", MGMT)
    assert MGMT["revision"] in (ROOT / "MODULE.bazel").read_text()
    unpack(outputs[TARGETS[3]][0], mgmt)
    include = sdk / "include/libyang"; include.mkdir(parents=True, exist_ok=True)
    for path in outputs[TARGETS[6]]:
        shutil.copyfile(path, include / path.name)
    libdir = runtime / "usr/lib/x86_64-linux-gnu"
    if not (libdir / "libyang.so").exists():
        (libdir / "libyang.so").symlink_to("libyang.so.3")
    with tarfile.open(outputs["//dist:sources"][0]) as archive:
        for relative in sorted(GENERATED):
            member = archive.getmember("usr/src/sonic-gnmi/" + relative)
            assert member.isfile()
            report["generated_sha256"][relative] = write_source(relative, archive.extractfile(member).read())
    swig = outputs[TARGETS[2]]
    assert {p.name for p in swig} == {"swsscommon.go", "swsscommon_wrap.cxx", "swsscommon_wrap.h"}
    for path in swig:
        relative = "swsscommon/" + path.name
        report["generated_sha256"][relative] = write_source(relative, path.read_bytes())
    environment = dict(GOFLAGS="", CGO_ENABLED="1", GOMAXPROCS="2", GOTOOLCHAIN="local",
        CGO_CFLAGS="-I" + str(sdk / "include") + " -I" + str(sdk / "include/swss"),
        CGO_CXXFLAGS="-I" + str(sdk / "include/swss") + " -w -Wall -fpermissive",
        CGO_LDFLAGS="-L" + str(sdk / "lib") + " -L" + str(libdir) + " -lswsscommon -lhiredis",
        LD_LIBRARY_PATH=str(sdk / "lib") + ":" + str(libdir),
        GOCACHE=str(work / "go-cache"))
    env = dict(os.environ, **environment)
    assert run(["go", "env", "GOVERSION"], cwd=ROOT, env=env).stdout.strip() == "go1.25.9"
    run(["go", "mod", "edit", "-replace=github.com/Azure/sonic-mgmt-common=" + str(mgmt)], cwd=ROOT, env=env)
    run(["go", "mod", "tidy"], cwd=ROOT, env=env, log=evidence / "go-tidy.log")
    run(["go", "mod", "vendor"], cwd=ROOT, env=env, log=evidence / "go-vendor.log")
    vendor = ROOT / "vendor"
    for module, version in [("github.com/openconfig/ygot", "v0.7.1"),
                            ("github.com/openconfig/goyang", "v0.0.0-20200309174518-a00bece872fc")]:
        downloaded = json.loads(run(["go", "mod", "download", "-json", module + "@" + version],
                                    cwd=ROOT, env=env).stdout)
        shutil.copytree(downloaded["Dir"], vendor / module, dirs_exist_ok=True)
    for path in vendor.rglob("*"):
        if path.is_file() or path.is_dir():
            path.chmod(path.stat().st_mode | 0o200)
    for module, patches in [
        # The owner's 0001 patch changes only BUILD.bazel, which ordinary Go
        # does not consume. Preserve all selected Go-source patches here.
        ("github.com/openconfig/ygot", ["ygot/ygot.patch"]),
        ("github.com/openconfig/goyang", ["goyang/goyang.patch"]),
        ("github.com/antchfx/jsonquery", ["jsonquery.patch"]),
        ("github.com/antchfx/xmlquery", ["xmlquery.patch"]),
        ("github.com/antchfx/xpath", ["xpath.patch"]),
    ]:
        for patch in patches:
            run(["patch", "-p1", "-i", str(mgmt / "patches" / patch)], cwd=vendor / module)
    for patch in ["gnmi_path.patch", "gnmi_xpath.patch", "0002-Fix-advance-tls-build-with-go-119.patch", "0004-CloseWrite-event-support.patch"]:
        run(["patch", "-p0", "-i", str(ROOT / "patches" / patch)], cwd=vendor)
    report["native_dependency_sha256"] = {name: digest(ROOT / name) for name in ("go.mod", "go.sum")}
    report["environment"] = environment
    shutil.copyfile(ROOT / "MODULE.bazel.lock", evidence / "MODULE.bazel.lock")
    save(work / "prepared.json", report)
    save(evidence / "prepared.json", {key: value for key, value in report.items() if key != "environment"})


def json_objects(text):
    decoder = json.JSONDecoder()
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        yield value
        text = text.lstrip()[end:]


def cgo_inputs(packages):
    """Bind original CGo sources to the physical Go inputs CodeQL extracts.

    packages.Load(NeedCompiledGoFiles) parses cmd/cgo's generated Go cache
    files. Their //line directives identify the original source, but CodeQL
    archives the physical generated file rather than that original pathname.
    """
    originals, compiled = {}, {}
    for package in packages:
        directory = Path(package["Dir"]).resolve()
        owner_files = {
            (directory / name).resolve(): str((directory / name).relative_to(ROOT))
            for name in package.get("CgoFiles", [])
        }
        if not owner_files:
            continue
        mapped = {path: [] for path in owner_files}
        ordinary = {(directory / name).resolve() for name in package.get("GoFiles", [])}
        for name in package.get("CompiledGoFiles", []):
            path = Path(name)
            path = (path if path.is_absolute() else directory / path).resolve()
            if path in ordinary:
                continue
            data = path.read_bytes()
            attributed = set()
            for line in data.decode("utf-8").splitlines():
                if not line.startswith("//line "):
                    continue
                match = re.fullmatch(r"(.+?):[0-9]+(?::[0-9]+)?", line[7:])
                assert match, (path, line)
                source = Path(match.group(1))
                assert source.is_absolute(), (path, line)
                source = source.resolve()
                if source in owner_files:
                    attributed.add(source)
            if attributed:
                assert data.startswith(b"// Code generated by cmd/cgo; DO NOT EDIT.\n"), path
            record = {"sha256": hashlib.sha256(data).hexdigest(),
                      "originals": sorted(owner_files[source] for source in attributed)}
            if str(path) in compiled:
                assert compiled[str(path)] == record, path
            compiled[str(path)] = record
            for source in attributed:
                mapped[source].append(str(path))
        for source, paths in mapped.items():
            assert paths, ("CGo source has no attributed compiler input", source)
            originals[owner_files[source]] = {
                "source_sha256": digest(source), "compiled_files": sorted(paths)}
    return {"originals": originals, "compiled_files": compiled}


def build(work, evidence):
    report = json.loads((work / "prepared.json").read_text())
    env = dict(os.environ, **report["environment"])
    flags = ["-mod=vendor", "-tags=" + TAGS, "-p=2"]
    # -compiled can emit C compiler warnings on stderr. Preserve them separately
    # so the JSON stream remains strict instead of filtering arbitrary output.
    with (evidence / "go-packages.jsonl").open("w") as output, \
         (evidence / "go-packages.stderr").open("w") as diagnostics:
        subprocess.run(["go", "list", *flags, "-compiled", "-json", "./..."],
                       cwd=ROOT, env=env, stdout=output, stderr=diagnostics, check=True)
    packages = list(json_objects((evidence / "go-packages.jsonl").read_text()))
    expected = {}
    for package in packages:
        directory = Path(package["Dir"])
        assert directory.is_relative_to(ROOT)
        for key in ("GoFiles", "CgoFiles", "TestGoFiles", "XTestGoFiles"):
            for name in package.get(key, []):
                path = directory / name
                expected[str(path.relative_to(ROOT))] = digest(path)
    assert {"telemetry/telemetry.go", "gnmi_server/server.go", "swsscommon/swsscommon.go"} <= expected.keys()
    assert {p for p in GENERATED if p.endswith(".go")} <= expected.keys()
    cgo = cgo_inputs(packages)
    # Force compiler execution in CodeQL's traced process; owner Bazel builds
    # and the pre-init Go dependency preparation cannot satisfy this step.
    run(["go", "build", "-a", *flags, "./..."], cwd=ROOT, env=env, log=evidence / "go-build.log")
    test_bins = work / "test-binaries"; test_bins.mkdir(exist_ok=True)
    for index, package in enumerate(packages):
        if not package.get("TestGoFiles") and not package.get("XTestGoFiles"):
            continue
        # gnoi_client/system and pkg/gnoi/system (likewise both exec packages)
        # have tests: one shared output directory would collide on system.test.
        run(["go", "test", "-c", *flags, "-o", str(test_bins / f"{index}.test"), package["ImportPath"]],
            cwd=ROOT, env=env, log=evidence / f"go-test-compile-{index:02d}.log")
    assert all(digest(ROOT / name) == value for name, value in expected.items()), "Source changed during compilation"
    assert all(digest(Path(name)) == value["sha256"] for name, value in cgo["compiled_files"].items()), \
        "CGo compiler input changed during compilation"
    save(evidence / "compiled.json", {"status": "passed", "tested_revision": report["tested_revision"],
         "tags": TAGS, "packages": sorted(p["ImportPath"] for p in packages),
         "source_sha256": expected, "cgo": cgo})


def verify(evidence, database):
    compiled = json.loads((evidence / "compiled.json").read_text())
    # Ordinary Go inputs must match their original paths and bytes. CGo inputs
    # must instead match every physical compiled file, with complete //line
    # attribution to the separately hashed original source. Never waive an
    # original merely because its package uses CGo.
    cgo = compiled.get("cgo", {"originals": {}, "compiled_files": {}})
    assert set(cgo["originals"]) <= set(compiled["source_sha256"])
    for relative, record in cgo["originals"].items():
        assert record["source_sha256"] == compiled["source_sha256"][relative]
        assert record["compiled_files"], relative
        for path in record["compiled_files"]:
            assert relative in cgo["compiled_files"][path]["originals"], (relative, path)
    with zipfile.ZipFile(database / "src.zip") as archive:
        members = set(archive.namelist())
        missing, changed = [], []
        for relative, expected in compiled["source_sha256"].items():
            if relative in cgo["originals"]:
                continue
            name = str(ROOT / relative).lstrip("/")
            if name not in members:
                missing.append(relative)
            elif hashlib.sha256(archive.read(name)).hexdigest() != expected:
                changed.append(relative)
        missing_cgo, changed_cgo = [], []
        for path, record in cgo["compiled_files"].items():
            name = path.lstrip("/")
            if name not in members:
                missing_cgo.append(path)
            elif hashlib.sha256(archive.read(name)).hexdigest() != record["sha256"]:
                changed_cgo.append(path)
    failed = missing or changed or missing_cgo or changed_cgo
    save(evidence / "extraction.json", {"status": "failed" if failed else "passed",
         "verified_ordinary_owner_files": len(compiled["source_sha256"]) - len(cgo["originals"]) - len(missing) - len(changed),
         "verified_cgo_compiler_files": len(cgo["compiled_files"]) - len(missing_cgo) - len(changed_cgo),
         "cgo_originals": cgo["originals"], "missing": missing, "changed": changed,
         "missing_cgo": missing_cgo, "changed_cgo": changed_cgo})
    assert not failed, (missing, changed, missing_cgo, changed_cgo)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["prepare", "build", "verify"])
    parser.add_argument("--work-dir", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--database", type=Path)
    args = parser.parse_args()
    work, evidence = args.work_dir.resolve(), args.evidence.resolve()
    assert not work.is_relative_to(ROOT), "Keep dependency work outside the analyzed checkout"
    work.mkdir(parents=True, exist_ok=True); evidence.mkdir(parents=True, exist_ok=True)
    if args.phase == "verify":
        assert args.database
        verify(evidence, args.database.resolve())
    else:
        {"prepare": prepare, "build": build}[args.phase](work, evidence)


if __name__ == "__main__":
    main()
