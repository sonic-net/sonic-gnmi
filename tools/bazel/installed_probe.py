"""Exercise installed telemetry and its client against private Redis."""
import hashlib
import json
from pathlib import Path
import re
import socket
import subprocess
import time


def run(work, environment, native_environment):
    binaries = work / 'build/bin'
    # Inspect the installed executable's real flag defaults. An external root
    # must preserve SONiC's selected write profile without inheriting our rc.
    help_result = subprocess.run([str(binaries / 'telemetry'), '-help'], cwd=work,
                                 env=native_environment, capture_output=True,
                                 text=True, timeout=10, check=True)
    help_text = help_result.stdout + help_result.stderr
    for flag in ('gnmi_translib_write', 'gnmi_native_write'):
        sections = re.findall(r'(?m)^  -' + flag + r'\b[^\n]*\n(?:(?!  -)[^\n]*\n?)*', help_text)
        if len(sections) != 1 or '(default true)' not in sections[0]:
            raise AssertionError('Installed telemetry does not enable ' + flag + ':\n' + help_text)
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
    address = f'127.0.0.1:{port}'
    command = [str(binaries / 'telemetry'), '-noTLS', '-bind_address', '127.0.0.1',
               '-port', str(port), '-client_auth', 'none', '-logtostderr']
    with (work / 'telemetry.log').open('w+') as log:
        service = subprocess.Popen(command, cwd=work, env=native_environment,
                                   stdout=log, stderr=subprocess.STDOUT)
        try:
            get = [str(binaries / 'gnmi_get'), '-notls', '-target_addr', address,
                   '-time_out', '2s', '-xpath', '/sonic-db:CONFIG_DB/localhost/DEVICE_METADATA/localhost/hostname']
            deadline = time.monotonic() + 30
            while service.poll() is None and time.monotonic() < deadline:
                result = subprocess.run(get, cwd=work, env=native_environment, capture_output=True, text=True, timeout=5)
                if result.returncode == 0:
                    break
                time.sleep(0.1)
            else:
                log.seek(0)
                raise AssertionError('Installed telemetry did not answer a real Get:\n' + log.read())
            # Require two actual database changes, not merely an open socket.
            values = ['bazel-native-gnmi-one', 'bazel-native-gnmi-two']
            for value in values:
                subprocess.run(['redis-cli', '-n', '4', 'HSET', 'DEVICE_METADATA|localhost', 'hostname', value],
                               cwd=work, env=environment, check=True, capture_output=True)
                result = subprocess.run(get, cwd=work, env=native_environment, capture_output=True, text=True, timeout=5)
                if result.returncode or value not in result.stdout + result.stderr:
                    raise AssertionError('Get did not observe private Redis value: ' + result.stdout + result.stderr)
            bad = get.copy()
            bad[bad.index('-xpath') + 1] = '/sonic-db:INVALID_DB/localhost/invalid'
            result = subprocess.run(bad, cwd=work, env=native_environment, capture_output=True, text=True, timeout=5)
            if result.returncode == 0:
                raise AssertionError('Invalid database unexpectedly succeeded')
            print(json.dumps({'installed_binaries': sorted(p.name for p in binaries.iterdir()),
                              'write_profile': ['gnmi_translib_write', 'gnmi_native_write'],
                              'real_get_values': values, 'invalid_database_rejected': True,
                              'telemetry_sha256': hashlib.sha256((Path(environment['SONIC_TEST_NATIVE_BIN']) / 'telemetry').read_bytes()).hexdigest()}, sort_keys=True))
        finally:
            service.terminate()
            try:
                service.wait(timeout=5)
            except subprocess.TimeoutExpired:
                service.kill(); service.wait()
