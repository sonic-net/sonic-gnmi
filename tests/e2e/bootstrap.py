"""Reuse a pinned certificate generator and the build's public gNOI schemas."""
import argparse
import hashlib
from pathlib import Path
import subprocess
import sys
import tempfile
import urllib.request

MGMT_SHA = "4c06cfad89168fc52e8186fa84e609146e3a53ec"
CERT_SHA256 = "3b2521e5f13f091e0c77c55e1175a055689cd04b2c72def6cf8193bc6ed377be"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path)
    parser.add_argument("--gnoi-protos", required=True, type=Path)
    args = parser.parse_args()
    args.destination.mkdir(parents=True, exist_ok=True)
    url = f"https://raw.githubusercontent.com/sonic-net/sonic-mgmt/{MGMT_SHA}/tests/common/cert_utils.py"
    with urllib.request.urlopen(url, timeout=30) as response:
        data = response.read()
    if hashlib.sha256(data).hexdigest() != CERT_SHA256:
        raise RuntimeError("Pinned certificate utility digest mismatch")
    (args.destination / "cert_utils.py").write_bytes(data)
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        names = []
        for name in ("file/file.proto", "types/types.proto", "common/common.proto"):
            # Adapt import filenames only, avoiding the Python stdlib types name.
            target = root / name.replace("types/", "gnoi_types/")
            target.parent.mkdir(parents=True, exist_ok=True)
            source = (args.gnoi_protos / name).read_text()
            source = source.replace("github.com/openconfig/gnoi/", "").replace("types/types.proto", "gnoi_types/types.proto")
            target.write_text(source)
            names.append(str(target))
        subprocess.run([
            sys.executable, "-m", "grpc_tools.protoc", f"-I{root}",
            f"--python_out={args.destination}", f"--grpc_python_out={args.destination}", *names,
        ], check=True)


if __name__ == "__main__":
    main()
