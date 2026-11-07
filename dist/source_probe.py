"""Check the component source TAR against declared owner/generated inputs."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import tarfile


def paths(groups):
    return [Path(name).absolute() for group in groups for name in group.split()]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--anchor', type=Path, required=True)
    parser.add_argument('--owner', nargs='+', required=True)
    parser.add_argument('--generated', nargs='+', required=True)
    parser.add_argument('--proto', action='append', required=True)
    args = parser.parse_args()
    root = args.anchor.absolute().parent
    prefix = 'usr/src/sonic-gnmi/'
    expected = {}
    def add(name, path):
        if name in expected:
            raise AssertionError('Duplicate source path: ' + name)
        expected[name] = path.read_bytes()
    for path in paths(args.owner):
        add(prefix + path.relative_to(root).as_posix(), path)
    for path in paths(args.generated):
        marker = '/gnoi_yang/generated/'
        if str(path).count(marker) != 1:
            raise AssertionError('Unexpected generated source path: ' + str(path))
        add(prefix + 'build/gnoi_yang/' + str(path).split(marker, 1)[1], path)
    for entry in args.proto:
        name, path = entry.split('=', 1)
        add(prefix + name, Path(path))
    actual = {}
    with tarfile.open(args.archive) as archive:
        for member in archive:
            name = member.name.removeprefix('./')
            if '..' in PurePosixPath(name).parts or name.startswith('/'):
                raise AssertionError('Unsafe source archive path: ' + name)
            if (member.uid, member.gid, member.mtime) != (0, 0, 0):
                raise AssertionError('Noncanonical source metadata: ' + name)
            if member.isdir():
                if member.mode != 0o755:
                    raise AssertionError('Source directory mode differs: ' + name)
                continue
            if not member.isfile() or member.mode != 0o644 or name in actual:
                raise AssertionError('Noncanonical/duplicate source file: ' + name)
            actual[name] = archive.extractfile(member).read()
    if actual != expected:
        raise AssertionError(json.dumps({
            'missing': sorted(expected.keys() - actual.keys()),
            'unexpected': sorted(actual.keys() - expected.keys()),
            'different': sorted(name for name in actual.keys() & expected.keys()
                                if actual[name] != expected[name]),
        }))
    for name in ['telemetry/telemetry.go', 'go.mod', 'go.bazel.work', 'MODULE.bazel',
                 'proto/sonic_internal.pb.go', 'build/gnoi_yang/server/gnoiyang.go',
                 'pytest.ini', 'testdata/mtls/gold/ca.pem',
                 'testdata/mtls/client_1_key.pem', 'testdata/crl/unrevoked.pem']:
        if prefix + name not in actual:
            raise AssertionError('Missing required source: ' + name)
    print(json.dumps({'source_files': len(actual),
                      'sha256': hashlib.sha256(args.archive.read_bytes()).hexdigest(),
                      'owner_relative_paths_verified': True}, sort_keys=True))


if __name__ == '__main__':
    main()
