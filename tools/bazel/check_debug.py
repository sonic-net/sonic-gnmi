"""Prove each installed ELF finds source lines only with its matching debug TAR."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence', type=Path, default=Path('bazel-evidence'))
    parser.add_argument('--gdb', default='gdb')
    args = parser.parse_args()
    evidence = args.evidence.resolve()
    runtime = evidence / 'runtime.tar'
    debug = evidence / 'runtime.debug_symbols.tar'
    gdb = shutil.which(args.gdb)
    if not gdb:
        raise FileNotFoundError('Declared validation environment must provide GDB')
    records = {}
    with tempfile.TemporaryDirectory(prefix='gnmi-debug-') as directory:
        root = Path(directory)
        runtime_root, debug_root = root / 'runtime', root / 'symbols'
        for archive, destination in [(runtime, runtime_root), (debug, debug_root)]:
            with tarfile.open(archive) as tar:
                tar.extractall(destination, filter='data')
        empty = root / 'without-symbols'
        empty.mkdir()
        environment = dict(os.environ, DEBUGINFOD_URLS='', HOME=str(root))
        binaries = sorted((runtime_root / 'usr/sbin').iterdir())
        expected = {'telemetry', 'dialout_client_cli', 'gnmi_get', 'gnmi_set', 'gnmi_cli',
                    'gnmi_dump', 'gnoi_client', 'gnoi_openconfig_client', 'gnoi_sonic_client'}
        if {p.name for p in binaries} != expected:
            raise AssertionError('Debug proof requires all nine production executables')
        for binary in binaries:
            outputs = {}
            for case, symbols in [('matching', debug_root / 'usr/lib/debug'), ('missing', empty)]:
                result = subprocess.run([
                    gdb, '--batch', '--nx', '--quiet',
                    '-ex', 'set debuginfod enabled off',
                    '-ex', 'set auto-load off',
                    '-ex', 'set debug-file-directory ' + str(symbols),
                    '-ex', 'file ' + str(binary),
                    '-ex', 'info line main.main',
                ], cwd=root, env=environment, stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=30)
                outputs[case] = result.stdout
                (evidence / (binary.name + '-gdb-' + case + '.log')).write_text(result.stdout)
            lines = re.findall(r'Line ([0-9]+) of "([^"]+\.go)"', outputs['matching'])
            if not lines or re.search(r'Line [0-9]+ of ', outputs['missing']):
                raise AssertionError('Detached debug positive/negative lookup failed for ' + binary.name)
            records[binary.name] = {'main_source_line': lines[0], 'missing_debug_rejected': True}
    receipt = {
        'binaries': records,
        'runtime_sha256': hashlib.sha256(runtime.read_bytes()).hexdigest(),
        'debug_sha256': hashlib.sha256(debug.read_bytes()).hexdigest(),
        'gdb': subprocess.check_output([gdb, '--version'], text=True).splitlines()[0],
    }
    (evidence / 'debug-lookup.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps(receipt, indent=2))


if __name__ == '__main__':
    main()
