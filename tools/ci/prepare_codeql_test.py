"""Check CodeQL's Go owner handoff without running Bazel or a compiler."""
import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock

from prepare_codeql import digest, vendor_owner


class SharedGoOwnerTest(unittest.TestCase):
    def test_uses_patched_owner_and_removes_stale_vendor_sources(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            module = "github.com/openconfig/ygot"
            execution = root / "bazel-output/execroot/_main"
            execution.mkdir(parents=True)
            source = root / "bazel-output/external/openconfig_ygot+"
            source.mkdir(parents=True)
            (source / "go.mod").write_text("module " + module + "\n\ngo 1.25\n")
            (source / "patched.go").write_text("package ygot\n// selected SONiC patch\n")
            vendor = root / "vendor"
            destination = vendor / module
            destination.mkdir(parents=True)
            (destination / "obsolete.go").write_text("package stale\n")
            bazel = Mock(return_value="INFO: Analyzed target\nexternal/openconfig_ygot+\nINFO: Build completed\n")
            report = vendor_owner(bazel, execution, vendor, module, "@owner//ygot:ygot")
            self.assertFalse((execution / "external").exists())
            self.assertFalse((destination / "obsolete.go").exists())
            self.assertEqual((destination / "patched.go").read_bytes(), (source / "patched.go").read_bytes())
            self.assertEqual(report["source_sha256"]["patched.go"], digest(source / "patched.go"))
            bazel.assert_called_once_with("cquery", ["config(@owner//ygot:ygot, target)",
                "--output=starlark", "--starlark:expr=target.label.workspace_root"])

    def test_rejects_ambiguous_or_wrong_owner_before_replacing_vendor(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            execution = root / "bazel-output/execroot/_main"
            execution.mkdir(parents=True)
            source = root / "bazel-output/external/owner+"
            source.mkdir(parents=True)
            (source / "go.mod").write_text("module wrong.example/module\n")
            destination = root / "vendor/github.com/openconfig/ygot"
            destination.mkdir(parents=True)
            sentinel = destination / "original.go"
            sentinel.write_text("package original\n")
            for result in ["", "/tmp/owner\n", "external/../owner\n",
                           "external/owner+\nexternal/other+\n", "external/owner+\n"]:
                with self.subTest(result=result), self.assertRaises(AssertionError):
                    vendor_owner(Mock(return_value=result), execution, root / "vendor",
                                 "github.com/openconfig/ygot", "@owner//ygot:ygot")
                self.assertEqual(sentinel.read_text(), "package original\n")


if __name__ == "__main__":
    unittest.main()
