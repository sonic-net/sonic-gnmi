import os
import pathlib
import subprocess
import tempfile
import unittest


MAKEFILE = pathlib.Path(__file__).resolve().parents[2] / "pure.mk"
CONSTRAINTS = (
    "//go:build !pure",
    "//go:build\tpure",
    "//go:build\t!pure",
    "// +build pure",
    "// +build !pure",
    "//\t+build\t!pure",
)


class PureTagPlacementTest(unittest.TestCase):
    def check_tags(self, path, constraint):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for name in ("internal", "pkg", "cmd"):
                (root / name).mkdir()
            source = root / path
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_text(constraint + "\n\npackage fixture\n")
            return subprocess.run(
                ["make", "-s", "-f", str(MAKEFILE), "check-tags"],
                cwd=root,
                capture_output=True,
                text=True,
                check=False,
            )

    def test_rejects_tagged_business_logic(self):
        for path in ("pkg/logic.go", "pathz_authorizer/logic.go"):
            for constraint in CONSTRAINTS:
                with self.subTest(path=path, constraint=constraint):
                    result = self.check_tags(path, constraint)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn(path, result.stdout)

    def test_accepts_direct_provider(self):
        for constraint in CONSTRAINTS:
            with self.subTest(constraint=constraint):
                result = self.check_tags(
                    "internal/adaptors/dbconfig/provider_file.go", constraint
                )
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_nested_provider(self):
        for constraint in CONSTRAINTS:
            with self.subTest(constraint=constraint):
                result = self.check_tags(
                    "internal/adaptors/dbconfig/nested/provider_file.go", constraint
                )
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("nested/provider_file.go", result.stdout)


class PurePackageDiscoveryTest(unittest.TestCase):
    def test_discovers_path_authorization_packages(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for path in (
                "pathz_authorizer/logic.go",
                "pathz_authorizer/nested/logic.go",
            ):
                source = root / path
                source.parent.mkdir(parents=True, exist_ok=True)
                source.write_text("package fixture\n")
            env = os.environ.copy()
            for name in ("MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES", "PACKAGES"):
                env.pop(name, None)
            result = subprocess.run(
                ["make", "-s", "-f", str(MAKEFILE), "list-packages"],
                cwd=root,
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            sections = result.stdout.split("Currently testing packages:\n")
            self.assertEqual(len(sections), 2, result.stdout)
            for section in sections:
                self.assertIn("  - pathz_authorizer\n", section)
                self.assertIn("  - pathz_authorizer/nested\n", section)


if __name__ == "__main__":
    unittest.main()
