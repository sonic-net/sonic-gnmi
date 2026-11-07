"""Retain native source, test and installed-payload evidence without environment dumps."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess


def retain_tests(destination):
    destination.mkdir(parents=True, exist_ok=True)
    for source in Path('bazel-testlogs').rglob('test.*'):
        if source.name not in ('test.log', 'test.xml') or not source.is_file():
            continue
        target = destination / source.relative_to('bazel-testlogs')
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--save-testlogs', type=Path)
    args = parser.parse_args()
    if args.save_testlogs:
        retain_tests(args.save_testlogs)
        return
    evidence = Path('bazel-evidence')
    evidence.mkdir(exist_ok=True)
    for name in ['MODULE.bazel.lock', 'go.mod', 'go.sum', 'go.bazel.work', 'requirements_lock.txt']:
        source = Path(name)
        if source.is_file():
            shutil.copyfile(source, evidence / source.name)
    # Save the source test reports before later profiles reuse bazel-testlogs.
    # On failure, retain whatever the interrupted source invocation produced.
    if not (evidence / 'tests').exists():
        retain_tests(evidence / 'tests')
    results = {name: os.environ.get('CI_' + name.upper() + '_RESULT', 'unknown')
               for name in ['source_test', 'installed', 'debug', 'generator', 'incremental', 'consumer', 'dialout_disabled']}
    complete = all(value == 'success' for value in results.values())
    if results['source_test'] == 'success':
        required = ['sonic_db_config', 'sonic_service_client', 'sonic_data_client',
                    'gnmi_server', 'telemetry', 'dialout/dialout_client']
        for package in required:
            logs = list((evidence / 'tests' / package).glob('*/test.log'))
            if not logs or not any('--- PASS:' in p.read_text(errors='replace') for p in logs):
                raise AssertionError('Missing executed original Go cases for ' + package)
    payloads = {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                for p in evidence.glob('*.tar*') if p.is_file()}
    if complete:
        incremental = json.loads((evidence / 'incremental.json').read_text())
        generator = json.loads((evidence / 'generator-incremental.json').read_text())
        debug = json.loads((evidence / 'debug-lookup.json').read_text())
        assert incremental['status'] == generator['status'] == 'passed'
        package_log = (evidence / 'tests/dist/package_test/test.log').read_text()
        packages = [json.loads(line) for line in package_log.splitlines()
                    if line.startswith('{"debug_sha256":')]
        assert len(packages) == 1 and packages[0]['matching_elf_pairs'] == 9
        for filename, target, key in [
            ('runtime.tar', '//dist:runtime', 'runtime_sha256'),
            ('runtime.debug_symbols.tar', '//dist:runtime.debug_symbols', 'debug_sha256'),
        ]:
            digest = payloads[filename]
            assert incremental['artifacts'][filename]['sha256'] == digest
            assert all(incremental[stage]['sha256'][target] == digest
                       for stage in ['baseline', 'noop', 'restore'])
            assert all(generator[stage]['sha256'][target] == digest
                       for stage in ['baseline', 'noop', 'restore'])
            assert debug[key] == packages[0][key] == digest
        assert len(debug['binaries']) == 9
    def git(*arguments):
        return subprocess.check_output(['git', '-c', 'safe.directory=' + str(Path.cwd()), *arguments], text=True).strip()
    revision = git('rev-parse', 'HEAD')
    headers = git('cat-file', '-p', 'HEAD').split('\n\n', 1)[0]
    parents = [line.removeprefix('parent ') for line in headers.splitlines() if line.startswith('parent ')]
    head, base = os.environ.get('CI_HEAD_SHA', ''), os.environ.get('CI_BASE_SHA', '')
    if head:
        assert head == revision or head in parents, 'Tested checkout does not include the PR head'
    if base and head != revision:
        assert base in parents, 'Tested merge does not include the intended base'
    identity = {
        'checkout': revision, 'commit_headers': headers, 'parents': parents,
        'pull_request_head': head, 'pull_request_base': base,
        'machine': platform.machine(), 'os_release': Path('/etc/os-release').read_text(),
        'target': '@sonic_build_infra//platforms:x86_64_trixie',
        'bazel': Path('.bazelversion').read_text().strip(), 'go_sdk': '1.25.9',
        'consumer_compilation_mode': 'fastbuild', 'consumer_strip': 'always',
        'complete': complete, 'results': results, 'payload_sha256': payloads,
    }
    (evidence / 'provenance.json').write_text(json.dumps(identity, indent=2) + '\n')


if __name__ == '__main__':
    main()
