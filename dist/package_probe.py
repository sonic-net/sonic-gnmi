"""Verify Make's installed executable inventory and matching ELF/debug pairs."""
import hashlib
import json
from pathlib import Path
import re
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zlib

RUNTIME, DEBUG, READELF, OBJCOPY = [Path(p).absolute() for p in sys.argv[1:5]]
DIALOUT = sys.argv[5] == "enabled"
del sys.argv[1:6]


def entries(archive):
    with tarfile.open(archive) as tar:
        result = {}
        for member in tar:
            if member.isdir():
                continue
            name = member.name.removeprefix("./")
            if name in result or not member.isfile():
                raise AssertionError(f"Unexpected duplicate/non-file: {name}")
            result[name] = (member, tar.extractfile(member).read())
        return result


def readelf(*args):
    return subprocess.check_output([str(READELF), *map(str, args)], text=True)


class InstalledContract(unittest.TestCase):
    def test_all_native_binaries_and_symbols(self):
        runtime, debug = entries(RUNTIME), entries(DEBUG)
        names = {"telemetry", "gnmi_get", "gnmi_set", "gnmi_cli", "gnoi_client",
                 "gnoi_openconfig_client", "gnoi_sonic_client", "gnmi_dump"}
        if DIALOUT:
            names.add("dialout_client_cli")
        self.assertEqual(set(runtime), {"usr/sbin/" + name for name in names})
        expected_debug = set()
        with tempfile.TemporaryDirectory(prefix="gnmi-elf-") as temporary:
            root = Path(temporary)
            for name, (member, data) in runtime.items():
                with self.subTest(binary=name):
                    self.assertEqual((member.uid, member.gid, member.mode), (0, 0, 0o755))
                    self.assertIn(member.mtime, (0, 1672560000))
                    binary = root / Path(name).name
                    binary.write_bytes(data)
                    self.assertTrue(data.startswith(b"\x7fELF"))
                    self.assertIn("Advanced Micro Devices X86-64", readelf("-h", binary))
                    identity = re.search(r"Build ID: ([0-9a-f]+)", readelf("-n", binary))
                    self.assertIsNotNone(identity)
                    identity = identity[1]
                    debug_path = f"usr/lib/debug/.build-id/{identity[:2]}/{identity[2:]}.debug"
                    expected_debug.add(debug_path)
                    self.assertIn(debug_path, debug)
                    debug_member = debug[debug_path][0]
                    self.assertEqual((debug_member.uid, debug_member.gid, debug_member.mode), (0, 0, 0o644))
                    self.assertEqual(debug_member.mtime, member.mtime)
                    symbols = root / Path(debug_path).name
                    symbols.write_bytes(debug[debug_path][1])
                    self.assertEqual(re.search(r"Build ID: ([0-9a-f]+)", readelf("-n", symbols))[1], identity)
                    self.assertIn(".debug_info", readelf("-SW", symbols))
                    self.assertIn(".debug_line", readelf("-SW", symbols))
                    self.assertNotRegex(readelf("-SW", binary), r"\.(?:z?debug_)\w+")
                    dynamic = readelf("-d", binary)
                    self.assertNotIn("_solib", dynamic)
                    self.assertNotIn(".runfiles", dynamic)
                    self.assertNotRegex(dynamic, r"\((?:RPATH|RUNPATH)\)")
                    link = root / "debuglink"
                    subprocess.run([str(OBJCOPY), "--dump-section", f".gnu_debuglink={link}", str(binary)], check=True)
                    payload = link.read_bytes()
                    end = payload.index(b"\0")
                    self.assertEqual(payload[:end].decode(), symbols.name)
                    self.assertEqual(struct.unpack_from("<I", payload, (end + 4) & ~3)[0], zlib.crc32(symbols.read_bytes()))
        self.assertEqual(set(debug), expected_debug)
        print(json.dumps({"matching_elf_pairs": len(names),
                          "runtime_sha256": hashlib.sha256(RUNTIME.read_bytes()).hexdigest(),
                          "debug_sha256": hashlib.sha256(DEBUG.read_bytes()).hexdigest()}, sort_keys=True))


if __name__ == "__main__":
    unittest.main()
