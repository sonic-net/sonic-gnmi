#!/usr/bin/env python3
"""Run the real subscription regression with independently built owner inputs."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import tarfile
import tempfile
import time
import urllib.request


CASE = "TestClientRunConcurrentClose"


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def run(command, *, cwd, env=None, log=None, check=True):
    if log:
        with Path(log).open("w") as output:
            result = subprocess.run(command, cwd=cwd, env=env, text=True,
                                    stdout=output, stderr=subprocess.STDOUT)
        result.stdout = Path(log).read_text()
    else:
        result = subprocess.run(command, cwd=cwd, env=env, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if check and result.returncode:
        raise RuntimeError(f"Command failed ({result.returncode}): {shlex.join(command)}; {log}\n{result.stdout[-4000:]}")
    return result


def owner(work, name, pin):
    archive = work / (name + "-" + pin["revision"] + ".tar.gz")
    if not archive.exists():
        temporary = archive.with_suffix(".download")
        with urllib.request.urlopen(f"https://github.com/securely1g/{name}/archive/{pin['revision']}.tar.gz") as source:
            with temporary.open("wb") as output:
                shutil.copyfileobj(source, output)
        if digest(temporary) != pin["sha256"]:
            raise AssertionError(f"Unexpected {name} source archive")
        temporary.replace(archive)
    assert digest(archive) == pin["sha256"], name
    destination = work / name
    marker = work / (name + ".source.json")
    if destination.exists():
        assert json.loads(marker.read_text()) == pin, f"Different retained {name} revision"
        return destination
    with tarfile.open(archive) as source:
        prefix = name + "-" + pin["revision"]
        assert all(m.name == prefix or m.name.startswith(prefix + "/") for m in source)
        source.extractall(work, filter="data")
    (work / prefix).rename(destination)
    marker.write_text(json.dumps(pin, sort_keys=True) + "\n")
    return destination


def unpack(archive, destination):
    destination.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive) as package:
        package.extractall(destination, filter="data")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--work-dir", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--prepare-only", action="store_true")
    args = parser.parse_args()
    source = Path(__file__).resolve().parents[2]
    work = args.work_dir.resolve(); work.mkdir(parents=True, exist_ok=True)
    evidence = args.evidence.resolve(); evidence.mkdir(parents=True, exist_ok=True)
    assert platform.machine() == "x86_64"
    assert "VERSION_CODENAME=trixie" in Path("/etc/os-release").read_text()
    assert Path("/.dockerenv").exists() or Path("/run/.containerenv").exists()
    pins = json.loads(Path(__file__).with_name("native_subscription_inputs.json").read_text())
    head = run(["git", "rev-parse", "HEAD"], cwd=source).stdout.strip()
    parents = run(["git", "show", "-s", "--format=%P", "HEAD"], cwd=source).stdout.split()
    expected = os.environ.get("CI_HEAD_SHA")
    assert not expected or expected in [head, *parents], "Wrong PR checkout"
    base = os.environ.get("CI_BASE_SHA")
    assert not base or base in parents, "Wrong merge base"
    report = {"tested_revision": head, "tested_parents": parents, "inputs": pins,
              "test": CASE, "configuration": "native AMD64/Trixie, Go race detector",
              "driver_sha256": digest(__file__)}
    run(["dpkg-query", "-W", "-f=${Package}\t${Version}\t${Architecture}\n"],
        cwd=work, log=evidence / "native-tools.txt")
    common = owner(work, "sonic-swss-common", pins["owners"]["sonic-swss-common"])
    mgmt = owner(work, "sonic-mgmt-common", pins["owners"]["sonic-mgmt-common"])
    outputs = {}

    def bazel(root, operation, targets, extra=(), log_suffix=""):
        rc = work / (root.name + ".bazelrc")
        text = (root / ".bazelrc").read_text().replace(
            "https://raw.githubusercontent.com/securely1g/sonic-bazel-registry/main",
            "https://raw.githubusercontent.com/securely1g/sonic-bazel-registry/" + pins["registry"])
        home_rc = Path.home() / ".bazelrc"
        if home_rc.exists():
            text += "\n" + "\n".join(line for line in home_rc.read_text().splitlines()
                if line.startswith(("build --disk_cache=", "common --repository_cache="))) + "\n"
        rc.write_text(text)
        command = shlex.split(os.environ.get("NATIVE_BAZEL_COMMAND", "bazel")) + [
            "--nosystem_rc", "--nohome_rc", "--noworkspace_rc", "--bazelrc=" + str(rc),
            "--output_base=" + str(work / (root.name + "-output")), operation,
            "--jobs=2", "--host_platform=@sonic_build_infra//platforms:x86_64_trixie",
            *shlex.split(os.environ.get("NATIVE_BAZEL_FLAGS", "")), *targets, *extra]
        return run(command, cwd=root, log=evidence / (root.name + "-" + operation + log_suffix + ".log")).stdout

    common_targets = ["//dist:libswsscommon_sdk", "//dist:libswsscommon_pkg", "//:swig_template"]
    mgmt_targets = ["//dist:codegen", "//dist:runtime", "@libyang//:libyang_pkg", "@libyang//:public_headers"]
    for root, targets in [(common, common_targets), (mgmt, mgmt_targets)]:
        bazel(root, "build", targets)
        for target in targets:
            expression = "labels(hdrs," + target + ")" if target.endswith(":public_headers") else target
            if target.endswith(":public_headers"):
                # A header-only CcInfo need not materialize all its generated
                # headers when requested alone. Request its declared hdrs too.
                labels = bazel(root, "cquery", [expression], ["--output=label"], "-headers")
                header_targets = [line.split()[0] for line in labels.splitlines() if line.startswith("@")]
                assert header_targets, labels
                bazel(root, "build", header_targets, log_suffix="-headers")
            files = bazel(root, "cquery", [expression], ["--output=files"])
            # Bazel diagnostics share the captured stream; select actual paths.
            paths = []
            for line in files.splitlines():
                if not line.startswith(("bazel-out/", "external/", "pyext/", "common/")):
                    continue
                path = (work / (root.name + "-output") / line
                        if line.startswith("external/") else root / line)
                if path.is_file():
                    paths.append(path)
            assert paths, (target, files)
            outputs[target] = paths
        shutil.copyfile(root / "MODULE.bazel.lock", evidence / (root.name + "-MODULE.bazel.lock"))
    report["owner_output_sha256"] = {target: {p.name: digest(p) for p in paths}
                                     for target, paths in outputs.items()}
    (evidence / "owner-inputs.json").write_text(json.dumps(report, indent=2) + "\n")
    if args.prepare_only:
        return

    # Test an exact tracked source snapshot; generated/vendor inputs stay private.
    test_root = work / "tested" / head
    test_root.mkdir(parents=True, exist_ok=True)
    tested = test_root / "sonic-gnmi"
    if not tested.exists():
        tested.mkdir()
        archive = subprocess.check_output(["git", "archive", "HEAD"], cwd=source)
        with tarfile.open(fileobj=io.BytesIO(archive)) as tree:
            tree.extractall(tested, filter="data")
    sdk, runtime = test_root / "sdk", test_root / "runtime"
    staged = test_root / "owner-artifacts.json"
    if staged.exists():
        assert json.loads(staged.read_text()) == report["owner_output_sha256"]
    else:
        unpack(outputs["//dist:libswsscommon_sdk"][0], sdk)
        unpack(outputs["//dist:libswsscommon_pkg"][0], runtime)
        unpack(outputs["//dist:runtime"][0], runtime)
        unpack(outputs["@libyang//:libyang_pkg"][0], runtime)
        staged.write_text(json.dumps(report["owner_output_sha256"], sort_keys=True) + "\n")
    # The owner's codegen TAR carries exactly these generated source locations.
    native_mgmt = test_root / "sonic-mgmt-common"
    if not native_mgmt.exists():
        archive = work / ("sonic-mgmt-common-" + pins["owners"]["sonic-mgmt-common"]["revision"] + ".tar.gz")
        unpack(archive, test_root)
        (test_root / ("sonic-mgmt-common-" + pins["owners"]["sonic-mgmt-common"]["revision"])).rename(native_mgmt)
    unpack(outputs["//dist:codegen"][0], native_mgmt)
    include = sdk / "include/libyang"; include.mkdir(parents=True, exist_ok=True)
    for path in outputs["@libyang//:public_headers"]:
        shutil.copyfile(path, include / path.name)
    libdir = runtime / "usr/lib/x86_64-linux-gnu"
    if not (libdir / "libyang.so").exists():
        (libdir / "libyang.so").symlink_to("libyang.so.3")
    template = tested / "swsscommon/swsscommon.i"
    shutil.copyfile(outputs["//:swig_template"][0], template)
    run(["swig", "-go", "-cgo", "-c++", "-intgosize", "64", "-I" + str(sdk / "include/swss"),
         "swsscommon.i"], cwd=template.parent, log=evidence / "swig.log")
    # Preserve the native neighboring-checkout contract without changing go.mod.
    neighbor = tested.parent / "sonic-mgmt-common"
    assert neighbor.resolve() == native_mgmt.resolve()
    environment = dict(os.environ, GOFLAGS="", CGO_ENABLED="1", GOMAXPROCS="2",
        CGO_CFLAGS="-I" + str(sdk / "include") + " -I" + str(sdk / "include/swss"),
        CGO_CXXFLAGS="-I" + str(sdk / "include/swss") + " -w -Wall -fpermissive",
        CGO_LDFLAGS="-L" + str(sdk / "lib") + " -L" + str(libdir) + " -lswsscommon -lhiredis",
        LD_LIBRARY_PATH=str(sdk / "lib") + ":" + str(libdir))
    assert run(["go", "env", "GOVERSION"], cwd=tested, env=environment).stdout.strip() == "go1.25.9"
    vendor = tested / "vendor"
    if not (work / ("vendor-" + head + ".done")).exists():
        # Go's module cache is read-only; copied generator directories need
        # owner write access for the native patch command's temporary files.
        if vendor.exists():
            for path in vendor.rglob("*"):
                if path.is_file() or path.is_dir():
                    path.chmod(path.stat().st_mode | 0o200)
        # The native go-deps recipe tidies after installing its generated inputs.
        # Keep those dependency-only edits in this private exact-source snapshot.
        run(["go", "mod", "tidy"], cwd=tested, env=environment, log=evidence / "tidy.log")
        run(["go", "mod", "vendor"], cwd=tested, env=environment, log=evidence / "vendor.log")
        # Match native go-deps: retain module files omitted from ordinary vendor.
        for module, version in [("github.com/openconfig/ygot", "v0.7.1"),
                                ("github.com/openconfig/goyang", "v0.0.0-20200309174518-a00bece872fc")]:
            downloaded = json.loads(run(["go", "mod", "download", "-json", module + "@" + version],
                                        cwd=tested, env=environment).stdout)
            shutil.copytree(downloaded["Dir"], vendor / module, dirs_exist_ok=True)
        for path in vendor.rglob("*"):
            if path.is_file() or path.is_dir():
                path.chmod(path.stat().st_mode | 0o200)
        for module, patch in [("github.com/openconfig/ygot", "ygot/ygot.patch"),
                              ("github.com/openconfig/goyang", "goyang/goyang.patch"),
                              ("github.com/antchfx/jsonquery", "jsonquery.patch"),
                              ("github.com/antchfx/xmlquery", "xmlquery.patch"),
                              ("github.com/antchfx/xpath", "xpath.patch")]:
            run(["patch", "-p1", "-i", str(mgmt / "patches" / patch)], cwd=vendor / module)
        # Native gNMI intentionally omits Common's glog patch for glog 1.2.4.
        for patch in ["gnmi_path.patch", "gnmi_xpath.patch", "0002-Fix-advance-tls-build-with-go-119.patch", "0004-CloseWrite-event-support.patch"]:
            run(["patch", "-p0", "-i", str(tested / "patches" / patch)], cwd=vendor)
        (work / ("vendor-" + head + ".done")).write_text("complete\n")
    for name in ("go.mod", "go.sum"):
        shutil.copyfile(tested / name, evidence / name)
    report["native_dependency_sha256"] = {name: digest(tested / name) for name in ("go.mod", "go.sum")}

    private = test_root / "test-runtime"; private.mkdir(exist_ok=True)
    config = json.loads((tested / "testdata/database_config.json").read_text())
    dbconfig = private / "database_config.json"
    environment.update(DB_CONFIG_PATH=str(dbconfig),
        CVL_SCHEMA_PATH=str(runtime / "usr/sbin/schema"), CVL_CFG_FILE=str(runtime / "usr/sbin/cvl_cfg.json"),
        YANG_MODELS_PATH=str(runtime / "usr/models/yang"),
        DBUS_SYSTEM_BUS_ADDRESS="unix:path=" + str(private / "absent-dbus.sock"))
    production = tested / "gnmi_server/client_subscribe.go"
    exact = production.read_bytes()
    baseline_file = work / ("client_subscribe-" + pins["baseline"]["revision"] + ".go")
    if not baseline_file.exists():
        with urllib.request.urlopen("https://raw.githubusercontent.com/securely1g/sonic-gnmi/" +
                pins["baseline"]["revision"] + "/gnmi_server/client_subscribe.go") as response:
            baseline = response.read()
        assert hashlib.sha256(baseline).hexdigest() == pins["baseline"]["sha256"]
        baseline_file.write_bytes(baseline)
    baseline = baseline_file.read_bytes()
    assert hashlib.sha256(baseline).hexdigest() == pins["baseline"]["sha256"]
    assert baseline != exact, "The regression must compare against the original unfixed source"
    command = ["go", "test", "-race", "-gcflags=all=-l", "-mod=vendor", "-count=3", "-json",
               "-timeout=5m", "-run=^" + CASE + "$", "./gnmi_server", "-args", "-logtostderr=true"]
    # Keep the Unix socket below Linux's 108-byte limit even in long CI paths.
    with tempfile.TemporaryDirectory(prefix="gnmi-race-") as socket_directory, (evidence / "redis.log").open("w") as redis_log:
        socket = Path(socket_directory) / "redis.sock"
        for instance in config["INSTANCES"].values():
            instance.update(port=0, unix_socket_path=str(socket))
        dbconfig.write_text(json.dumps(config))
        redis = subprocess.Popen(["redis-server", "--port", "0", "--unixsocket", str(socket),
            "--save", "", "--appendonly", "no", "--databases", "32", "--dir", str(private)],
            stdout=redis_log, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 15
            while redis.poll() is None and time.monotonic() < deadline:
                if socket.exists():
                    info = run(["redis-cli", "--raw", "-s", str(socket), "INFO", "server"], cwd=private, check=False)
                    if f"process_id:{redis.pid}" in info.stdout.splitlines():
                        break
                time.sleep(0.05)
            else:
                raise RuntimeError("Private Redis did not start")
            positive = run(command, cwd=tested, env=environment, log=evidence / "fixed.jsonl", check=False)
            events = [json.loads(line) for line in positive.stdout.splitlines() if line.startswith("{")]
            assert positive.returncode == 0 and sum(e.get("Test") == CASE and e.get("Action") == "pass" for e in events) == 3, positive.stdout[-4000:]
            production.write_bytes(baseline)
            negative = run(command, cwd=tested, env=environment, log=evidence / "baseline.jsonl", check=False)
            negative_events = [json.loads(line) for line in negative.stdout.splitlines() if line.startswith("{")]
            assert negative.returncode != 0 and any(e.get("Test") == CASE and e.get("Action") == "fail"
                for e in negative_events), negative.stdout[-4000:]
            for marker in ("DATA RACE", "client_subscribe.go:", "(*Client).Run", "(*Client).Close"):
                assert marker in negative.stdout, (marker, negative.stdout[-4000:])
        finally:
            production.write_bytes(exact)
            redis.terminate()
            try: redis.wait(timeout=5)
            except subprocess.TimeoutExpired: redis.kill(); redis.wait()
    assert production.read_bytes() == exact
    report.update(fixed_passes=3, baseline_race_reproduced=True,
                  production_sha256=hashlib.sha256(exact).hexdigest(),
                  baseline_sha256=hashlib.sha256(baseline).hexdigest(),
                  evidence_sha256={name: digest(evidence / name) for name in ["fixed.jsonl", "baseline.jsonl"]})
    (evidence / "result.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
