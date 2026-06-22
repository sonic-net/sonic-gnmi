#!/usr/bin/env python3
# Unit + negative tests for dev/ado-local.py (Epic 1, E1-T7 / E1-T8).
#
# Run with:  python3 -m unittest discover -s dev/tests
# or:        python3 dev/tests/test_ado_local.py

import contextlib
import importlib.util
import io
import os
import re
import tempfile
import types
import unittest

DEV_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REPO_ROOT = os.path.dirname(DEV_DIR)
ADO_PATH = os.path.join(DEV_DIR, "ado-local.py")
PIPELINE = os.path.join(REPO_ROOT, "azure-pipelines.yml")

_spec = importlib.util.spec_from_file_location("ado_local", ADO_PATH)
ado = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(ado)


def opts(**kw):
    base = dict(
        pipeline=PIPELINE,
        branch="master",
        honor_go_install=False,
        exclude_pkg_exec=False,
        dry_run=False,
        explain=False,
    )
    base.update(kw)
    return types.SimpleNamespace(**base)


def classes(records):
    return [(r.kind, r.source) for r in records]


def keep_bodies(records):
    return "\n".join(r.body for r in records if r.kind == ado.KEEP and r.body)


class TestListAndJobs(unittest.TestCase):
    def test_five_supported_jobs(self):
        self.assertEqual(
            set(ado.SUPPORTED_JOBS),
            {"go_static_checks", "pure_tests", "memleak_tests", "integration_tests", "amd64"},
        )

    def test_all_jobs_extract_without_error(self):
        pl = ado.Pipeline(PIPELINE)
        for job in ado.SUPPORTED_JOBS:
            records = pl.extract(job, opts())
            ado.assert_no_errors(records)  # must not raise
            prog = ado.emit_program(records)
            self.assertIn("set -euo pipefail", prog)


class TestIntegrationGolden(unittest.TestCase):
    def setUp(self):
        self.pl = ado.Pipeline(PIPELINE)
        self.records = self.pl.extract("integration_tests", opts())

    def test_classification_table(self):
        # Golden ordered (status, source) classification for integration_tests.
        expected = [
            (ado.NOOP, "setup-test-env.yml"),        # checkout self
            (ado.NOOP, "setup-test-env.yml"),        # checkout mgmt-common
            (ado.NOOP, "setup-test-env.yml"),        # checkout swss-common
            (ado.NOOP, "install-dependencies.yml"),  # download libyang/libnl
            (ado.KEEP, "install-dependencies.yml"),  # install test deps (redis/pytest)
            (ado.KEEP, "install-dependencies.yml"),  # install libyang/libnl debs
            (ado.NOOP, "install-dependencies.yml"),  # download yang models
            (ado.KEEP, "install-dependencies.yml"),  # install sonic yangs
            (ado.NOOP, "install-dependencies.yml"),  # download swss-common
            (ado.KEEP, "install-dependencies.yml"),  # install libswsscommon
            (ado.KEEP, "install-dependencies.yml"),  # install protoc
            (ado.KEEP, "setup-test-env.yml"),        # build mgmt-common
            (ado.KEEP, "azure-pipelines.yml"),       # run integration tests
            (ado.NOOP, "azure-pipelines.yml"),       # publish test results
            (ado.KEEP, "azure-pipelines.yml"),       # pre-install diff-cover
            (ado.NOOP, "azure-pipelines.yml"),       # publish coverage
            (ado.NOOP, "azure-pipelines.yml"),       # publish artifact
        ]
        self.assertEqual(classes(self.records), expected)

    def test_env_setup_path_rewritten_to_sonic_debs(self):
        bodies = keep_bodies(self.records)
        self.assertIn("sudo dpkg -i $(find /sonic-debs -name '*.deb')", bodies)
        # The ADO staging path must be fully rewritten away.
        self.assertNotIn("$(Build.ArtifactStagingDirectory)", bodies)
        self.assertNotIn("$(Pipeline.Workspace)", bodies)

    def test_env_setup_sourced_not_hardcoded(self):
        # The redis/pytest + protoc bodies come from install-dependencies.yml.
        for r in self.records:
            if r.kind == ado.KEEP and "redis-server" in (r.body or ""):
                self.assertEqual(r.source, "install-dependencies.yml")
                break
        else:
            self.fail("redis install body not sourced from install-dependencies.yml")

    def test_unit_test_flag_resolved(self):
        prog = ado.emit_program(self.records)
        self.assertIn("ENABLE_TRANSLIB_WRITE=y make check_gotest_junit", prog)
        self.assertNotIn("$(UNIT_TEST_FLAG)", prog)


class TestOtherJobs(unittest.TestCase):
    def test_memleak_program(self):
        records = ado.Pipeline(PIPELINE).extract("memleak_tests", opts())
        prog = ado.emit_program(records)
        self.assertIn("ENABLE_TRANSLIB_WRITE=y make check_memleak_junit", prog)
        self.assertIn("/sonic-debs", prog)

    def test_amd64_drops_test_deps_keeps_build(self):
        # installTestDeps=false for the package job: redis/pytest block must drop.
        records = ado.Pipeline(PIPELINE).extract("amd64", opts())
        bodies = keep_bodies(records)
        self.assertNotIn("redis-server", bodies)
        self.assertIn("dpkg-buildpackage", bodies)
        self.assertIn("/sonic-debs", bodies)

    def test_go_install_noop_by_default(self):
        records = ado.Pipeline(PIPELINE).extract("go_static_checks", opts())
        go = [r for r in records if r.source == "install-go.yml"]
        self.assertTrue(go and all(r.kind == ado.NOOP for r in go))

    def test_go_install_honored_with_flag(self):
        records = ado.Pipeline(PIPELINE).extract(
            "go_static_checks", opts(honor_go_install=True)
        )
        go = [r for r in records if r.source == "install-go.yml"]
        self.assertTrue(go and all(r.kind == ado.KEEP for r in go))
        # ${{ parameters.version }} must be bound to the GO_VERSION value.
        self.assertIn("go1.24.4.linux-amd64", go[0].body)

    def test_pure_exclude_pkg_exec(self):
        records = ado.Pipeline(PIPELINE).extract(
            "pure_tests", opts(exclude_pkg_exec=True)
        )
        bodies = keep_bodies(records)
        self.assertIn("PACKAGES=", bodies)
        self.assertNotIn("pkg/exec ", bodies + " ")


class TestResolver(unittest.TestCase):
    def test_compound_and_guard_included(self):
        pl = ado.Pipeline(PIPELINE)
        params = {"arch": "amd64", "installTestDeps": True}
        guard = "${{ if and(eq(parameters.arch, 'amd64'), eq(parameters.installTestDeps, true)) }}"
        self.assertTrue(pl._eval_guard(guard, params))

    def test_arch_arm64_guard_dropped(self):
        pl = ado.Pipeline(PIPELINE)
        params = {"arch": "amd64"}
        self.assertFalse(pl._eval_guard("${{ if eq(parameters.arch, 'arm64') }}", params))
        self.assertTrue(pl._eval_guard("${{ if eq(parameters.arch, 'amd64') }}", params))

    def test_unknown_predicate_raises(self):
        pl = ado.Pipeline(PIPELINE)
        with self.assertRaises(ado.UnsupportedConstruct):
            pl._eval_guard("${{ if or(eq(parameters.arch, 'amd64'), true) }}", {"arch": "amd64"})


class TestNegative(unittest.TestCase):
    """E1-T8: unenumerated constructs must error, never silently skip (FR7)."""

    def _write(self, text):
        fd, path = tempfile.mkstemp(suffix=".yml")
        os.write(fd, text.encode())
        os.close(fd)
        self.addCleanup(os.unlink, path)
        return path

    def test_unknown_task_errors(self):
        path = self._write(
            """
variables:
- name: GO_VERSION
  value: '1.24.4'
stages:
- stage: Test
  jobs:
  - job: integration_tests
    steps:
    - task: SomeRandomTask@9
      displayName: 'mystery'
"""
        )
        pl = ado.Pipeline(path)
        records = pl.extract("integration_tests", opts(pipeline=path))
        self.assertTrue(any(r.kind == ado.ERROR for r in records))
        with self.assertRaises(ado.UnsupportedConstruct):
            ado.assert_no_errors(records)

    def test_unknown_expression_errors(self):
        path = self._write(
            """
variables:
- name: GO_VERSION
  value: '1.24.4'
stages:
- stage: Test
  jobs:
  - job: integration_tests
    steps:
    - ${{ if ne(parameters.arch, 'amd64') }}:
      - bash: echo hi
"""
        )
        pl = ado.Pipeline(path)
        with self.assertRaises(ado.UnsupportedConstruct):
            pl.extract("integration_tests", opts(pipeline=path))

    def test_unknown_job_errors(self):
        pl = ado.Pipeline(PIPELINE)
        with self.assertRaises(ado.UnsupportedConstruct):
            pl.flatten("nonexistent_job")

    def test_run_explain_prints_table_before_error(self):
        # cmd_run --explain (no --dry-run) must print the full classification
        # table (including ERROR rows + source refs) BEFORE exiting non-zero,
        # mirroring cmd_explain rather than crashing on the first error.
        path = self._write(
            """
variables:
- name: GO_VERSION
  value: '1.24.4'
stages:
- stage: Test
  jobs:
  - job: integration_tests
    steps:
    - bash: echo before
      displayName: 'kept step'
    - task: SomeRandomTask@9
      displayName: 'mystery'
"""
        )
        run_opts = opts(pipeline=path, command="run", job="integration_tests", explain=True)
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            with self.assertRaises(ado.UnsupportedConstruct):
                ado.cmd_run(run_opts)
        printed = out.getvalue()
        # The classification table (with the ERROR row + source) was emitted
        # to stdout before the exception propagated.
        self.assertIn("STATUS", printed)
        self.assertIn(ado.ERROR, printed)
        self.assertIn("task:SomeRandomTask@9", printed)


class TestReadOnly(unittest.TestCase):
    """NFR1: the tool never opens any pipeline file for writing."""

    def test_source_has_no_write_opens(self):
        with open(ADO_PATH) as f:
            src = f.read()
        # The only open() call must be read mode.
        for m in re.finditer(r"open\(([^)]*)\)", src):
            self.assertNotRegex(m.group(1), r"['\"][wax]")

    def test_load_yaml_uses_read_mode(self):
        opened = {}
        real_open = open

        def spy_open(path, mode="r", *a, **k):
            opened[path] = mode
            return real_open(path, mode, *a, **k)

        import builtins
        orig = builtins.open
        builtins.open = spy_open
        try:
            ado.Pipeline(PIPELINE).extract("integration_tests", opts())
        finally:
            builtins.open = orig
        self.assertTrue(opened)
        for mode in opened.values():
            self.assertEqual(mode.replace("b", "").replace("t", ""), "r")


if __name__ == "__main__":
    unittest.main(verbosity=2)
