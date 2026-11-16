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
    def test_native_command_keeps_other_packages_in_pure_suite(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for name in ("internal/logic", "pkg/logic", "cmd/telemetry", "cmd/other", "cmd/telemetry/subpackage"):
                package = root / name
                package.mkdir(parents=True, exist_ok=True)
                (package / "main.go").write_text("package fixture\n")
            result = subprocess.run(
                ["make", "-s", "-f", str(MAKEFILE), "list-packages"],
                cwd=root, capture_output=True, text=True, check=True,
            )
            packages = {line.strip()[2:] for line in result.stdout.splitlines()
                        if line.strip().startswith("- ")}
            self.assertEqual(packages, {"internal/logic", "pkg/logic", "cmd/other", "cmd/telemetry/subpackage"})

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
        for constraint in CONSTRAINTS:
            with self.subTest(constraint=constraint):
                result = self.check_tags("pkg/logic.go", constraint)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("pkg/logic.go", result.stdout)

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


if __name__ == "__main__":
    unittest.main()
