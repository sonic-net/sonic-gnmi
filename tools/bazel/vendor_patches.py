"""Keep Bazel's module-relative copies of Make's vendor patches in sync.

Unified-diff path headers and GNU-patch-compatible context markers are
normalized. Source hunk bytes remain unchanged, including GNU patch's existing
CRLF-to-LF handling for gnmi_path.
"""
import argparse
from pathlib import Path
import re

PATCHES = {
    'github.com/google/gnxi': ['gnmi_path.patch', 'gnmi_xpath.patch', 'gnmi_set.patch', 'gnmi_get.patch'],
    'github.com/openconfig/gnmi': ['gnmi_cli.all.patch', '0001-Updated-to-filter-and-write-to-file.patch', '0003-Fix-client-json-parsing-issue.patch'],
    'github.com/fsnotify/fsnotify': ['0004-CloseWrite-event-support.patch'],
    'google.golang.org/grpc/security/advancedtls': ['0002-Fix-advance-tls-build-with-go-119.patch'],
}


def normalize(text, module):
    lines = []
    remaining = None
    for line in text.splitlines(keepends=True):
        hunk = re.match(r'@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@', line)
        if hunk:
            remaining = [int(hunk[2] or 1), int(hunk[4] or 1)]
        elif remaining and any(remaining):
            # GNU patch accepts the legacy gnmi_set hunks whose context
            # lines omit the leading space; Bazel's strict patcher does not.
            if line[0] not in ' +-\\':
                line = ' ' + line
            if line[0] in ' -':
                remaining[0] -= 1
            if line[0] in ' +':
                remaining[1] -= 1
            if min(remaining) < 0:
                raise ValueError('Inconsistent legacy patch hunk')
        if line.startswith('patch--- '):
            line = line[5:]
        if line.startswith(('--- ', '+++ ')):
            marker, value = line[:4], line[4:]
            name = value.split()[0].replace('github.com//', 'github.com/')
            if name != '/dev/null':
                prefix = module + '/'
                if prefix not in name:
                    raise ValueError(f'Unexpected {module} patch path: {name}')
                name = ('a/' if marker == '--- ' else 'b/') + name.split(prefix, 1)[1]
            if marker == '--- ' and name != '/dev/null':
                lines.append('diff --git ' + name + ' b/' + name[2:] + '\n')
            line = marker + name + '\n'
        elif line.startswith('diff --git '):
            continue
        lines.append(line)
    return ''.join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    root = Path(__file__).absolute().parents[2]
    outputs = root / 'patches/bazel'
    outputs.mkdir(exist_ok=True)
    for number, (module, patches) in enumerate(PATCHES.items(), 1):
        target = outputs / (f'{number:04d}-' + module.rsplit('/', 1)[1] + '.patch')
        result = ''.join(normalize((root / 'patches' / name).read_text(), module) + '\n' for name in patches)
        if args.check:
            if target.read_text() != result:
                raise SystemExit(f'{target}: regenerate with tools/bazel/vendor_patches.py')
        else:
            target.write_text(result)


if __name__ == '__main__':
    main()
