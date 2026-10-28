import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
TOOL = """#!/bin/sh
name=${0##*/}
echo "$name $*" >> "$EVENT_LOG"
case "$name" in
    go) ;;
    runner)
        echo '<testsuite/>' > test-results/junit-pure.xml
        echo 'mode: atomic' > test-results/coverage-pure.txt
        ;;
    converter) echo '{"Packages":[]}' ;;
    reporter) cat >/dev/null; echo '<coverage/>' ;;
esac
[ "$FAIL" != "$name" ]
"""


class PureReportsTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = pathlib.Path(self.directory.name)
        shutil.copy(ROOT / "pure.mk", self.root)
        for path in ("internal", "cmd", "pkg/fixture", "tools/test", "bin"):
            (self.root / path).mkdir(parents=True, exist_ok=True)
        shutil.copy(ROOT / "tools/test/test_pure_tags.py", self.root / "tools/test")
        (self.root / "pkg/fixture/fixture.go").write_text("package fixture\n")
        for name in ("go", "runner", "converter", "reporter"):
            tool = self.root / name
            tool.write_text(TOOL)
            tool.chmod(0o755)
        (self.root / "bin/gofmt").symlink_to("/bin/true")
        self.results = self.root / "test-results"
        self.results.mkdir()
        self.profile = self.results / "coverage-pure.txt"
        self.output = self.results / "coverage-pure.xml"

    def make(self, *targets, fail=""):
        return subprocess.run(
            ["make", "-s", "-f", "pure.mk", "PACKAGES=pkg/fixture",
             f"GO={self.root}/go", f"GOROOT={self.root}",
             f"GOTESTSUM={self.root}/runner", f"GOCOV={self.root}/converter",
             f"GOCOV_XML={self.root}/reporter", *targets],
            cwd=self.root, env={**os.environ, "FAIL": fail,
                               "EVENT_LOG": str(self.root / "events")},
            capture_output=True, text=True, check=False,
        )

    def events(self):
        path = self.root / "events"
        return path.read_text().splitlines() if path.exists() else []

    def assert_ok(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_conversion_only_preserves_input_and_cleans_temporary_files(self):
        self.profile.write_text("mode: atomic\n")
        self.assert_ok(self.make("report-coverage"))
        self.assertEqual(self.profile.read_text(), "mode: atomic\n")
        self.assertEqual(self.output.read_text(), "<coverage/>\n")
        self.assertEqual({p.name for p in self.results.iterdir()},
                         {"coverage-pure.txt", "coverage-pure.xml"})
        self.assertFalse(any("runner" in event or event.startswith("go ")
                             for event in self.events()))

    def test_conversion_failure_removes_stale_output(self):
        for profile, fail in ((None, ""), ("", ""), ("mode: atomic\n", "converter"),
                              ("mode: atomic\n", "reporter")):
            with self.subTest(profile=profile, fail=fail):
                self.profile.unlink(missing_ok=True)
                if profile is not None:
                    self.profile.write_text(profile)
                self.output.write_text("stale")
                result = self.make("report-coverage", fail=fail)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertFalse(self.output.exists())
                self.assertEqual(set(self.results.iterdir()),
                                 {self.profile} if profile is not None else set())

    def test_failed_tests_retain_junit_but_not_coverage(self):
        for name in ("junit-pure.xml", "coverage-pure.txt", "coverage-pure.xml"):
            (self.results / name).write_text("stale")
        result = self.make("ci-report", fail="runner")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertEqual((self.results / "junit-pure.xml").read_text(),
                         "<testsuite/>\n")
        self.assertFalse(self.profile.exists())
        self.assertFalse(self.output.exists())
        self.assertFalse(any(event.startswith(("converter", "reporter"))
                             for event in self.events()))

    def test_parallel_aggregate_orders_operations_without_clearing_cache(self):
        self.assert_ok(self.make("-j4", "ci-report"))
        events = self.events()
        build = next(i for i, event in enumerate(events) if "build " in event)
        compile_test = next(i for i, event in enumerate(events) if "-c " in event)
        run = next(i for i, event in enumerate(events) if event.startswith("runner "))
        convert = next(i for i, event in enumerate(events) if event.startswith("converter "))
        self.assertLess(build, compile_test)
        self.assertLess(compile_test, run)
        self.assertLess(run, convert)
        self.assertFalse(any(event.startswith("go clean") for event in events))
        self.assertTrue(self.profile.exists())
        self.assertTrue(self.output.exists())
        (self.root / "events").unlink()
        self.assert_ok(self.make("ci"))
        self.assertFalse(any(event.startswith("go clean") for event in self.events()))

    def test_aggregate_checks_global_boundary_before_narrowed_tests(self):
        (self.root / "cmd/bad.go").write_text("//go:build pure\n\npackage bad\n")
        self.output.write_text("stale")
        result = self.make("-j4", "ci-report")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("cmd/bad.go", result.stdout)
        self.assertEqual(self.events(), [])
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
