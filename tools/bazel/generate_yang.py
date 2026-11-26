"""Run the existing gNMI pyang plugin against the Common-owned model tree."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--models', type=Path, required=True)
    parser.add_argument('--pyang', type=Path, required=True)
    parser.add_argument('--plugin', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--expected', action='append', required=True)
    args = parser.parse_args()
    models = args.models.absolute()
    env = {key: value for key, value in os.environ.items()
           if not key.startswith('RUNFILES_') and key not in ('JAVA_RUNFILES', 'PYTHONPATH')}
    with tempfile.TemporaryDirectory(prefix='gnmi-yang-') as temp:
        root = Path(temp)
        common = [str(args.pyang.absolute()), '-f', 'proto',
                  '--proto-outdir', str(root / 'proto'),
                  '--plugindir', str(args.plugin.absolute().parent),
                  '--server-rpc-outdir', str(root / 'server'),
                  '--client-rpc-outdir', str(root / 'client')]
        for paths, sources in (
            ([models / 'common', models / 'extensions'],
             sorted(models.glob('*.yang')) + sorted((models / 'extensions').glob('*.yang'))),
            ([models / 'common', models / 'sonic/common'], sorted((models / 'sonic').glob('*.yang'))),
        ):
            if not sources:
                raise ValueError('Missing owner model sources')
            subprocess.run([*common, '-p', ':'.join(map(str, paths)), *map(str, sources)], check=True, env=env)
        actual = sorted(p.relative_to(root).as_posix() for p in root.rglob('*') if p.is_file())
        if actual != sorted(args.expected):
            raise ValueError({'generated': actual, 'declared': sorted(args.expected)})
        for name in actual:
            output = args.output / name
            output.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(root / name, output)
            output.chmod(0o644)


if __name__ == '__main__':
    main()
