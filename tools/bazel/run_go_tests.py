"""Run Go assertions using private Redis/paths and the declared Trixie runtime."""
import argparse
import ctypes
from contextlib import ExitStack
import importlib.util
import json
import os
from pathlib import Path
import shutil
import secrets
import shlex
import socket
import subprocess
import tarfile
import tempfile
import time


def redis_process(address, family=socket.AF_INET):
    """Bind readiness to the private Redis PID, including over TCP."""
    with socket.socket(family) as client:
        client.settimeout(0.5)
        client.connect(address)
        client.sendall(b'*2\r\n$4\r\nINFO\r\n$6\r\nserver\r\n')
        stream = client.makefile('rb')
        header = stream.readline()
        if not header.startswith(b'$'):
            raise RuntimeError('Redis INFO did not return a bulk response')
        body = stream.read(int(header[1:].strip())).decode()
        return int(dict(line.split(':', 1) for line in body.splitlines()
                        if ':' in line)['process_id'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'runtime', 'common_helper', 'models', 'schema', 'initializer', 'platform', 'anchor'):
        parser.add_argument('--' + name, type=lambda s: Path(s).absolute(), required=True)
    parser.add_argument('--suite', required=True)
    parser.add_argument('--installed', action='store_true')
    parser.add_argument('--pytest', type=lambda s: Path(s).absolute())
    parser.add_argument('--clients', type=lambda s: Path(s).absolute())
    parser.add_argument('--fixtures', nargs='+', required=True)
    args = parser.parse_args()
    # rules_python expands rootpaths as one argument; no fixture paths have spaces.
    fixtures = [Path(p).absolute() for group in args.fixtures for p in group.split()]
    spec = importlib.util.spec_from_file_location('common_runtime', args.common_helper)
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    with tempfile.TemporaryDirectory(prefix='gnmi-test-', dir='/tmp') as temporary, ExitStack() as cleanup:
        root = Path(temporary)
        runtime, work = root / 'runtime', root / 'work'
        runtime.mkdir(); work.mkdir()
        for archive in (args.runtime, args.models):
            with tarfile.open(archive) as tar:
                tar.extractall(runtime, filter=helper.runtime_member_filter)
        source_root = args.anchor.parent.parent
        for fixture in fixtures:
            output = work / fixture.relative_to(source_root)
            output.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(fixture, output)
        schema = work / 'testdata/schema'
        shutil.copytree(args.schema, schema)
        config_dir = root / 'redis/sonic-db'
        config_dir.mkdir(parents=True)
        checkpoint = root / 'checkpoint'; checkpoint.mkdir()
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        unix_socket = str(root / 'redis.sock')
        config_only = args.suite == 'sonic_db_config'
        config = json.loads(args.anchor.read_text())
        if not config_only:
            for instance in config['INSTANCES'].values():
                instance.update(hostname='127.0.0.1', port=port, unix_socket_path=unix_socket)
            # Multi-namespace assertions use the same private Redis process.
            for path in (work / 'testdata').glob('database_config*.json'):
                data = json.loads(path.read_text())
                for instance in data['INSTANCES'].values():
                    instance.update(hostname='127.0.0.1', port=port, unix_socket_path=unix_socket)
                path.write_text(json.dumps(data))
        dbconfig = config_dir / 'database_config.json'
        dbconfig.write_text(json.dumps(config))
        library_paths = sorted({str(p.parent.resolve()) for p in runtime.rglob('lib*.so*') if p.is_file()})
        environment = dict(os.environ)
        environment.pop('GO_TEST_RUN_FROM_BAZEL', None)
        environment.pop('LD_LIBRARY_PATH', None)
        environment.pop('LD_PRELOAD', None)
        environment.pop('PYTHONHOME', None)
        environment.update({
            'GO_TEST_WRAP': '0',
            'TMPDIR': str(root),
            'DB_CONFIG_PATH': str(dbconfig),
            'SONIC_TEST_REDIS_MANAGED': '1',
            'SONIC_TEST_REDIS_ROOT': str(root),
            'SONIC_TEST_REDIS_ADDRESS': f'127.0.0.1:{port}',
            'SONIC_GNMI_CHECKPOINT_DIR': str(checkpoint),
            'CVL_SCHEMA_PATH': str(schema),
            'CVL_CFG_FILE': str(runtime / 'usr/sbin/cvl_cfg.json'),
            'YANG_MODELS_PATH': str(runtime / 'usr/models/yang'),
            'SWSS_COMMON_LUA_DIR': str(runtime / 'usr/share/swss'),
            # Negative/mocked host-service cases must never contact a host bus.
            'DBUS_SYSTEM_BUS_ADDRESS': 'unix:path=' + str(root / 'absent-dbus.sock'),
        })
        # Reserve an exclusively owned SysV segment for telemetry/gnmi_dump.
        libc = ctypes.CDLL(None, use_errno=True)
        for _ in range(20):
            key = secrets.randbelow(2**30 - 1) + 1
            shmid = libc.shmget(key, 1024, 0o1000 | 0o2000 | 0o600)
            if shmid >= 0:
                cleanup.callback(libc.shmctl, shmid, 0, None)
                environment['SONIC_GNMI_SHM_KEY'] = str(key)
                break
        else:
            raise RuntimeError('Cannot reserve private telemetry counter segment')
        loaders = {p.resolve() for p in runtime.rglob('ld-linux-x86-64.so.2') if p.is_file()}
        if len(loaders) != 1 or not next(iter(loaders)).is_relative_to(runtime):
            raise AssertionError('Expected one confined declared AMD64 loader')
        loader = loaders.pop()
        libpath = ':'.join(library_paths)
        def launcher(output, binary):
            # Do not rewrite Go ELF segments with patchelf. Use the declared
            # loader on the original bytes, including the owner helper tests
            # that re-exec os.Args[0]. This does not alter /proc/self/exe.
            output.parent.mkdir(parents=True, exist_ok=True)
            command = shlex.join([str(loader), '--library-path', libpath])
            output.write_text('#!/bin/sh\nexec ' + command + ' --argv0 "$0" ' +
                              shlex.quote(str(binary)) + ' "$@"\n')
            output.chmod(0o755)
            return str(output)
        if args.clients:
            clients = work / 'build/bin'
            clients.mkdir(parents=True)
            with tarfile.open(args.clients) as tar:
                tar.extractall(root / 'clients', filter=helper.runtime_member_filter)
            for binary in (root / 'clients/usr/sbin').iterdir():
                output = clients / binary.name
                launcher(output, binary)
            environment['SONIC_TEST_NATIVE_BIN'] = str(root / 'clients/usr/sbin')
            environment['SONIC_TEST_PYTEST'] = str(args.pytest)
            private_bin = root / 'bin'; private_bin.mkdir()
            redis_cli = private_bin / 'redis-cli'
            redis_cli.write_text('#!/bin/sh\nexec ' + shlex.join([
                str(loader), '--library-path', libpath, str(runtime / 'usr/bin/redis-cli'),
                '-s', unix_socket]) + ' "$@"\n')
            redis_cli.chmod(0o755)
            environment['PATH'] = str(private_bin) + ':' + environment.get('PATH', '')
        with (root / 'redis.log').open('w+') as log:
            service = subprocess.Popen([str(loader), '--library-path', libpath,
                str(runtime / 'usr/bin/redis-server'), '--bind', '127.0.0.1', '--port', str(port),
                '--unixsocket', unix_socket, '--save', '', '--appendonly', 'no', '--databases', '32',
                '--dir', str(root)], stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + 15
                while service.poll() is None and time.monotonic() < deadline:
                    try:
                        unix_pid = redis_process(unix_socket, socket.AF_UNIX)
                        tcp_pid = redis_process(('127.0.0.1', port))
                        if unix_pid == tcp_pid == service.pid:
                            break
                    except OSError:
                        pass
                    time.sleep(0.05)
                else:
                    log.seek(0); raise RuntimeError(log.read())
                if not config_only:
                    subprocess.run([str(args.initializer), '--port', str(port), '--platconf',
                        str(args.platform), '--overwrite'], cwd=work, env=environment, check=True)
                if args.installed:
                    probe_spec = importlib.util.spec_from_file_location(
                        "gnmi_installed_probe", Path(__file__).with_name("installed_probe.py"))
                    probe = importlib.util.module_from_spec(probe_spec)
                    probe_spec.loader.exec_module(probe)
                    probe.run(work, environment, dict(environment, PYTHONHOME=str(runtime / "usr")))
                    return
                command = [launcher(root / 'test-bin' / args.binary.name, args.binary),
                           '-test.v', '-test.timeout=40m']
                if environment.get('TESTBRIDGE_TEST_ONLY'):
                    command.append('-test.run=' + environment['TESTBRIDGE_TEST_ONLY'])
                # Embedded Python uses the declared stdlib; no target-libc
                # environment is inherited by host shells or Python launchers.
                environment['PYTHONHOME'] = str(runtime / 'usr')
                cwd = work / args.suite; cwd.mkdir(parents=True, exist_ok=True)
                subprocess.run(command, cwd=cwd, env=environment, check=True)
            finally:
                service.terminate()
                try:
                    service.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    service.kill(); service.wait()


if __name__ == '__main__':
    main()
