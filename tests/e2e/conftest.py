"""Real telemetry/Redis fixture for the disposable amd64 package job only."""
import hashlib
import json
import logging
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import tempfile
import time
from contextlib import contextmanager, ExitStack
from dataclasses import dataclass

import grpc
import pytest
import redis
from cert_utils import create_gnmi_cert_generator
from pygnmi.spec.v080 import gnmi_pb2, gnmi_pb2_grpc

CASE_CN = "artifact.case.gnmi.sonic"
CONTROL_CN = "artifact.control.gnmi.sonic"
LOG = logging.getLogger(__name__)
EXPECTED_NODES = {
    "test_auth.py::test_configdb_get[readonly]",
    "test_auth.py::test_configdb_get[readwrite]",
    "test_auth.py::test_unmapped_cn",
    *{f"test_auth.py::test_file_write[{role}-{method}]"
      for role in ("readonly", "readwrite") for method in ("Put", "Remove")},
}


def pytest_addoption(parser):
    parser.addoption("--telemetry", required=True)
    parser.addoption("--telemetry-sha256", required=True)
    parser.addoption("--evidence-dir", required=True)


def pytest_collection_modifyitems(session, config, items):
    if {item.nodeid for item in items} != EXPECTED_NODES or len(items) != 7:
        raise pytest.UsageError("Artifact auth requires the exact seven reviewed nodes")


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    report = (yield).get_result()
    with (Path(item.config.getoption("--evidence-dir")) / "phases.jsonl").open("a") as stream:
        stream.write(json.dumps({"node": report.nodeid, "phase": report.when,
                                 "outcome": report.outcome}) + "\n")


def get_request():
    request = gnmi_pb2.GetRequest(encoding=gnmi_pb2.JSON_IETF)
    path = request.path.add(origin="sonic-db")
    for part in ("CONFIG_DB", "localhost", "DEVICE_METADATA", "localhost", "cloudtype"):
        path.elem.add(name=part)
    return request


def response_values(response):
    return [json.loads(update.val.json_ietf_val)
            for notification in response.notification for update in notification.update]


def stop(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
            raise RuntimeError("Owned process required forced termination")


def wait_ready(check, process, timeout=60):
    deadline = time.monotonic() + timeout
    while True:
        if process.poll() is not None:
            raise RuntimeError(f"Owned process exited during setup: {process.returncode}")
        try:
            if check():
                return
        except (grpc.RpcError, redis.ConnectionError):
            pass
        if time.monotonic() >= deadline:
            raise RuntimeError("Private service readiness timed out")
        time.sleep(0.2)


@dataclass
class Environment:
    db: redis.Redis
    certs: Path
    files: Path
    process: subprocess.Popen
    port: int

    @contextmanager
    def channel(self, identity="case"):
        credentials = grpc.ssl_channel_credentials(
            root_certificates=(self.certs / "control-issuer.cer").read_bytes(),
            private_key=(self.certs / f"{identity}.key").read_bytes(),
            certificate_chain=(self.certs / f"{identity}.cer").read_bytes(),
        )
        with grpc.secure_channel(f"127.0.0.1:{self.port}", credentials,
                                 options=(("grpc.enable_retries", 0),)) as channel:
            yield channel

    def control_ready(self):
        with self.channel("control") as channel:
            stub = gnmi_pb2_grpc.gNMIStub(channel)
            capabilities = stub.Capabilities(gnmi_pb2.CapabilityRequest(), timeout=3)
            assert any(m.name == "sonic-db" for m in capabilities.supported_models)
            assert response_values(stub.Get(get_request(), timeout=3)) == ["Private"]
        return True


@pytest.fixture(scope="session")
def environment(request):
    assert Path("/.dockerenv").exists(), "Disposable package container required"
    binary = Path(request.config.getoption("--telemetry")).resolve(strict=True)
    digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    assert digest == request.config.getoption("--telemetry-sha256"), "Artifact digest mismatch"
    evidence = Path(request.config.getoption("--evidence-dir"))
    config = Path("/var/run/redis/sonic-db/database_config.json")
    host_root = Path("/mnt/host")
    # Container presence alone is not ownership: refuse existing host roots and
    # live default Redis. CI runs this sequentially after packaging, never on a DUT.
    assert not host_root.exists() and not host_root.is_symlink()
    assert not Path("/var/run/redis/redis.sock").exists(), "Existing Redis socket"
    with socket.socket() as probe:
        assert probe.connect_ex(("127.0.0.1", 6379)) != 0, "Existing Redis listener"
    original_config = config.read_bytes() if config.exists() else None
    original_mode = config.stat().st_mode & 0o777 if config.exists() else None
    assert not config.is_symlink(), "Do not overwrite a database config symlink"
    root = Path(tempfile.mkdtemp(prefix="gnmi-artifact-"))
    redis_socket = root / "redis.sock"
    processes = []
    try:
        with ExitStack() as cleanup:
            cleanup.callback(shutil.rmtree, root)
            def terminate(signum, frame):
                raise SystemExit(128 + signum)
            for signum in (signal.SIGTERM, signal.SIGINT):
                previous = signal.signal(signum, terminate)
                cleanup.callback(signal.signal, signum, previous)
            old_umask = os.umask(0o077)
            cleanup.callback(os.umask, old_umask)
            config.parent.mkdir(parents=True, exist_ok=True)
            if original_config is None:
                cleanup.callback(config.unlink)
            else:
                cleanup.callback(config.chmod, original_mode)
                cleanup.callback(config.write_bytes, original_config)
            config.write_text(json.dumps({
                "INSTANCES": {"redis": {"hostname": "127.0.0.1", "port": 6379,
                                        "unix_socket_path": str(redis_socket)}},
                "DATABASES": {name: {"id": number, "separator": sep, "instance": "redis"}
                              for name, number, sep in (("APPL_DB", 0, ":"), ("ASIC_DB", 1, ":"),
                                  ("COUNTERS_DB", 2, ":"), ("CONFIG_DB", 4, "|"),
                                  ("FLEX_COUNTER_DB", 5, ":"), ("STATE_DB", 6, "|"))},
                "VERSION": "1.0",
            }))
            (host_root / "tmp").mkdir(parents=True)
            cleanup.callback(shutil.rmtree, host_root)
            redis_log = cleanup.enter_context((evidence / "redis.log").open("w"))
            redis_process = subprocess.Popen([
                "redis-server", "--port", "0", "--save", "", "--appendonly", "no",
                "--unixsocket", str(redis_socket), "--unixsocketperm", "700", "--dir", str(root),
            ], stdout=redis_log, stderr=subprocess.STDOUT)
            processes.append(redis_process)
            cleanup.callback(stop, redis_process)
            db = redis.Redis(unix_socket_path=str(redis_socket), db=4, decode_responses=True,
                             socket_timeout=3, socket_connect_timeout=3)
            cleanup.callback(db.close)
            wait_ready(db.ping, redis_process, 15)
            db.hset("DEVICE_METADATA|localhost", mapping={"cloudtype": "Private", "hostname": "artifact"})
            db.set("CONFIG_DB_INITIALIZED", "1")
            db.hset(f"GNMI_CLIENT_CERT|{CONTROL_CN}", "role@", "gnmi_readwrite,gnmi_config_db_readwrite,gnoi_readwrite")
            certs = root / "certs"
            certs.mkdir(mode=0o700)
            for identity, cn in (("control", CONTROL_CN), ("case", CASE_CN)):
                generator = create_gnmi_cert_generator(
                    server_ip="127.0.0.1", client_cn=cn,
                    ca_cert_name=f"{identity}-issuer.cer", ca_key_name=f"{identity}-issuer.key",
                    server_cert_name=f"{identity}-server.cer", server_key_name=f"{identity}-server.key",
                    client_cert_name=f"{identity}.cer", client_key_name=f"{identity}.key",
                )
                generator.write_all(str(certs))
            (certs / "trust.cer").write_bytes(b"".join((certs / f"{i}-issuer.cer").read_bytes()
                                                     for i in ("control", "case")))
            with socket.socket() as reservation:
                reservation.bind(("127.0.0.1", 0))
                port = reservation.getsockname()[1]
            argv = [str(binary), "--port", str(port), "--bind_address", "127.0.0.1", "--unix_socket=",
                    "--client_auth", "cert", "--config_table_name", "GNMI_CLIENT_CERT",
                    "--ca_crt", str(certs / "trust.cer"), "--server_crt", str(certs / "control-server.cer"),
                    "--server_key", str(certs / "control-server.key"),
                    "--gnmi_native_write=true", "--gnmi_translib_write=true", "-logtostderr"]
            log = cleanup.enter_context((evidence / "telemetry.log").open("w"))
            process = subprocess.Popen(argv, env=dict(os.environ, DB_CONFIG_PATH=str(config)),
                                       stdout=log, stderr=subprocess.STDOUT)
            processes.append(process)
            cleanup.callback(stop, process)
            fixture = Environment(db, certs, host_root / "tmp", process, port)
            wait_ready(fixture.control_ready, process)
            running_digest = hashlib.sha256(Path(f"/proc/{process.pid}/exe").read_bytes()).hexdigest()
            assert running_digest == digest
            (evidence / "runtime.json").write_text(json.dumps({
                "telemetry_sha256": digest, "running_sha256": running_digest, "argv": argv,
                "pid": process.pid, "redis_pid": redis_process.pid,
                "profile": "certificate-db-get-file-write", "control_cn": CONTROL_CN, "case_cn": CASE_CN,
            }, indent=2))
            yield fixture
            assert process.poll() is None, "Telemetry exited during tests"
    finally:
        restored = config.read_bytes() == original_config if original_config is not None else not config.exists()
        (evidence / "cleanup.json").write_text(json.dumps({
            "processes": [{"pid": p.pid, "returncode": p.poll()} for p in processes],
            "private_files_removed": not root.exists() and not host_root.exists(),
            "db_config_restored": restored, "socket_removed": not redis_socket.exists(),
        }, indent=2))
    assert all(p.poll() == 0 for p in processes)
    assert not root.exists() and not host_root.exists() and not redis_socket.exists() and restored
    LOG.info("Owned telemetry/Redis exited; certificates/files/socket removed; DB config restored")


@pytest.fixture
def case(environment):
    key = f"GNMI_CLIENT_CERT|{CASE_CN}"
    assert not environment.db.exists(key)
    try:
        yield environment
    finally:
        environment.db.delete(key)
        assert not environment.db.exists(key)
        assert environment.db.hget("DEVICE_METADATA|localhost", "cloudtype") == "Private"
        assert environment.process.poll() is None
