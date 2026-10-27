import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest


REPO = pathlib.Path(__file__).resolve().parents[2]
STUB = r'''#!/usr/bin/python3
import json
import os
import pathlib
import subprocess
import sys

root = pathlib.Path(__file__).resolve().parent
name = pathlib.Path(sys.argv[0]).name.split("-v")[0]
args = sys.argv[1:]
with (root / "events").open("a") as log:
    log.write(json.dumps([name, args, os.environ.get("CGO_ENABLED")]) + "\n")
failure = (root / "fail").read_text().strip() if (root / "fail").exists() else ""
if failure == name:
    sys.exit(7)
if name == "sudo":
    if args[:3] != ["-H", "env", "-i"]:
        sys.exit(0)  # Never touch the host's /etc or services.
    args = args[3:]
    env = {}
    while args and "=" in args[0]:
        key, value = args.pop(0).split("=", 1)
        env[key] = value
    sys.exit(subprocess.run(args, env=env).returncode)
if name == "go" and args[:1] == ["env"]:
    print({"GOROOT": str(root), "GOHOSTOS": "linux", "GOHOSTARCH": "amd64"}.get(args[1], ""))
elif name == "gotestsum":
    junit = pathlib.Path(args[args.index("--junitfile") + 1])
    junit.write_text("<testsuites/>")
    for arg in args:
        if arg.startswith("-coverprofile="):
            pathlib.Path(arg.split("=", 1)[1]).write_text(
                "mode: atomic\nfixture/file.go:1.1,1.2 1 1\n")
elif name == "gocov":
    for filename in args[1:]:
        if not pathlib.Path(filename).read_text().startswith("mode:"):
            sys.exit(8)
    print("{}")
elif name == "gocov-xml":
    json.load(sys.stdin)
    print("<coverage/>")
'''


class MakeContractsTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = pathlib.Path(self.directory.name)
        for path in ("internal", "pkg/fixture", "cmd", "tools/test", "bin",
                     "build/tools/bin", "build/test", "testdata", "mgmt/patches",
                     "mgmt/build/yang", "mgmt/models/yang/sonic"):
            (self.root / path).mkdir(parents=True, exist_ok=True)
        for name in ("Makefile", "pure.mk", "tools/test/test_pure_tags.py"):
            shutil.copyfile(REPO / name, self.root / name)
        (self.root / "pkg/fixture/fixture.go").write_text("package fixture\n")
        (self.root / "testdata/database_config.json").write_text("{}")
        (self.root / "database_config.json").write_text("{}")
        (self.root / "build/test/env.txt").write_text("FIXTURE_ENV=sentinel\n")
        stub = self.root / "stub.py"
        stub.write_text(STUB)
        stub.chmod(0o755)
        for name in ("go", "sudo"):
            (self.root / "bin" / name).symlink_to(stub)
        for name in ("gotestsum-v1.12.3", "gotestsum-v1.13.0", "gocov-v1.1.0",
                     "gocov-xml-v1.1.0", "gocov-xml-v1.2.0"):
            (self.root / "build/tools/bin" / name).symlink_to(stub)

    def make(self, makefile, *targets, **variables):
        defaults = {
            "GO": str(self.root / "bin/go"),
            "GOROOT": str(self.root),
            "MGMT_COMMON_DIR": str(self.root / "mgmt"),
            "DBDIR": str(self.root),
        }
        defaults.update(variables)
        env = os.environ.copy()
        env["PATH"] = str(self.root / "bin") + ":" + env["PATH"]
        return subprocess.run(
            ["make", "--no-print-directory", "-s", "-f", makefile, *targets]
            + [f"{key}={value}" for key, value in defaults.items()],
            cwd=self.root, env=env, capture_output=True, text=True, timeout=30,
            check=False,
        )

    def events(self):
        path = self.root / "events"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def assert_ok(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_aliases_preserve_operations_and_outputs(self):
        for makefile, old, new, outputs in (
            ("Makefile", "check_gotest_junit", "test-integration",
             ("test-results/junit-integration-basic.xml",
              "test-results/junit-integration-env.xml",
              "test-results/junit-integration-dialout.xml", "coverage.xml")),
            ("Makefile", "check_memleak_junit", "test-memleak",
             ("test-results/junit-memleak-standard.xml",)),
        ):
            with self.subTest(target=new):
                (self.root / "events").write_text("")
                self.assert_ok(self.make(makefile, old))
                before = self.events()
                expected = [(self.root / path).read_bytes() for path in outputs]
                (self.root / "events").write_text("")
                self.assert_ok(self.make(makefile, new))
                self.assertCountEqual(self.events(), before)
                self.assertEqual(
                    [(self.root / path).read_bytes() for path in outputs], expected)

    def test_pure_entrypoint_produces_reports_without_legacy_name(self):
        self.assert_ok(self.make("pure.mk", "ci-report"))
        for name, contents in (
            ("junit-pure.xml", "<testsuites/>"),
            ("coverage-pure.txt", "mode: atomic\nfixture/file.go:1.1,1.2 1 1\n"),
            ("coverage-pure.xml", "<coverage/>\n"),
        ):
            self.assertEqual((self.root / "test-results" / name).read_text(), contents)
        result = self.make("pure.mk", "junit-xml")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("No rule to make target", result.stderr)

    def test_test_failure_propagates_through_old_and_new_names(self):
        (self.root / "fail").write_text("gotestsum")
        for makefile, targets in (
            ("pure.mk", ("ci-report",)),
            ("Makefile", ("check_gotest_junit", "test-integration",
                          "check_memleak_junit", "test-memleak")),
        ):
            for target in targets:
                with self.subTest(target=target):
                    result = self.make(makefile, target)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn("Error", result.stderr)


if __name__ == "__main__":
    unittest.main()
