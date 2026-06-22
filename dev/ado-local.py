#!/usr/bin/env python3
# dev/ado-local.py — read-only ADO-YAML step extractor/driver for sonic-gnmi.
#
# Parses the UNMODIFIED azure-pipelines.yml + .azure/templates/*.yml, expands
# template includes, resolves a fixed enumerated set of variables/parameters,
# classifies each step (keep / no-op / error), and emits the per-job bash
# program. The environment-setup bodies are sourced from install-dependencies.yml
# (deb-install path rewritten to the cached /sonic-debs mount, plus the
# redis/pytest/protoc install bodies) so the bootstrap is single-sourced from the
# pipeline rather than re-hardcoded in run-tests.sh.
#
# This tool NEVER opens any pipeline file for writing (NFR1): load_yaml() is the
# only file reader and it opens in 'r' mode exclusively. It does not reference,
# wrap, or invoke `playground` or any other run-tests.sh-only feature.
#
# See dev/local-ado-runner.plan.md (Epic 1) for the full design.

import argparse
import os
import re
import shlex
import subprocess
import sys

import yaml

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Supported jobs (job key -> stage). Indexed by the `job:` key, never displayName.
SUPPORTED_JOBS = {
    "go_static_checks": "StaticChecks",
    "pure_tests": "Test",
    "memleak_tests": "Test",
    "integration_tests": "Test",
    "amd64": "Package",
}

# Tasks mapped to local no-ops (the cache/bind-mounts already substitute them).
NOOP_TASKS = {
    "DownloadPipelineArtifact@2",
    "PublishTestResults@2",
    "PublishCodeCoverageResults@2",
}

# ADO staging paths rewritten to their local equivalents (OQ2=ii). Order matters:
# the more specific /download form must be rewritten before the bare staging root.
PATH_REWRITES = [
    ("$(Build.ArtifactStagingDirectory)/download", "/sonic-debs"),
    ("$(Build.ArtifactStagingDirectory)", "/build-out"),
    ("$(Pipeline.Workspace)", "/sonic-debs"),
    ("../target/python-wheels/trixie", "/sonic-debs"),
]

KEEP, NOOP, ERROR = "keep", "no-op", "error"


class UnsupportedConstruct(Exception):
    """Raised when the pipeline contains a construct the extractor cannot honor (FR7)."""


class Record:
    """One classified step."""

    def __init__(self, kind, kind_label, display, source, body=None, workdir=None, note=None):
        self.kind = kind
        self.kind_label = kind_label
        self.display = display
        self.source = source
        self.body = body
        self.workdir = workdir
        self.note = note


def load_yaml(path):
    # Read-only: the sole file open in this tool, always mode 'r' (NFR1).
    with open(path, "r") as f:
        return yaml.safe_load(f)


def _split_commas(s):
    """Split on top-level commas, respecting nested parentheses."""
    parts, depth, cur = [], 0, ""
    for ch in s:
        if ch == "(":
            depth += 1
            cur += ch
        elif ch == ")":
            depth -= 1
            cur += ch
        elif ch == "," and depth == 0:
            parts.append(cur)
            cur = ""
        else:
            cur += ch
    if cur.strip():
        parts.append(cur)
    return [p.strip() for p in parts]


class Pipeline:
    def __init__(self, root_path, branch="master"):
        self.root_path = os.path.abspath(root_path)
        self.root = load_yaml(self.root_path)
        self.branch = branch
        self.vars = self._root_variables()
        # Enumerated $(VAR) macro table (C3). Bash command substitutions like
        # $(go env GOPATH) or $(nproc) are deliberately NOT in this table and are
        # left untouched for the shell to evaluate.
        self.macros = {
            "GO_VERSION": self.vars.get("GO_VERSION"),
            "UNIT_TEST_FLAG": self.vars.get("UNIT_TEST_FLAG"),
            "BUILD_BRANCH": branch,
            "System.DefaultWorkingDirectory": "/work",
        }

    def _root_variables(self):
        out = {}
        for item in self.root.get("variables", []) or []:
            if isinstance(item, dict) and "name" in item and "value" in item:
                out[item["name"]] = item["value"]
        return out

    # --- C2: job selection + recursive step flattening -------------------

    def find_job(self, job_key):
        for stage in self.root.get("stages", []) or []:
            for job in stage.get("jobs", []) or []:
                if job.get("job") == job_key:
                    return job
        raise UnsupportedConstruct("unknown job: %s" % job_key)

    def flatten(self, job_key):
        job = self.find_job(job_key)
        return self._expand(job.get("steps", []) or [], {}, self.root_path)

    def _expand(self, steps, params, source_path):
        out = []
        for step in steps:
            keys = list(step.keys())
            # Conditional guard: `- ${{ if <expr> }}: [ ...steps ]`
            if len(keys) == 1 and str(keys[0]).strip().startswith("${{"):
                if self._eval_guard(keys[0], params):
                    out += self._expand(step[keys[0]], params, source_path)
                continue
            if "template" in step:
                inc_path = os.path.normpath(
                    os.path.join(os.path.dirname(source_path), step["template"])
                )
                tdoc = load_yaml(inc_path)
                bound = self._bind_params(
                    tdoc.get("parameters", []) or [], step.get("parameters", {}) or {}, params
                )
                out += self._expand(tdoc.get("steps", []) or [], bound, inc_path)
                continue
            leaf = dict(step)
            leaf["_source"] = os.path.basename(source_path)
            leaf["_params"] = params
            out.append(leaf)
        return out

    def _bind_params(self, decls, callsite, parent_params):
        bound = {}
        for d in decls:
            bound[d["name"]] = d.get("default")
        for k, v in callsite.items():
            bound[k] = self._resolve_pval(v, parent_params)
        return bound

    def _resolve_pval(self, v, parent):
        if isinstance(v, str):
            def sub(m):
                name = m.group(1)
                if name not in parent:
                    raise UnsupportedConstruct("unbound parameter ${{ parameters.%s }}" % name)
                return str(parent[name])

            return re.sub(r"\$\{\{\s*parameters\.([A-Za-z_]\w*)\s*\}\}", sub, v)
        return v

    # --- C3: enumerated ${{ if ... }} predicate evaluation ---------------

    def _eval_guard(self, key, params):
        m = re.match(r"^\$\{\{\s*if\s+(.*?)\s*\}\}$", str(key).strip())
        if not m:
            raise UnsupportedConstruct("unsupported template directive: %s" % key)
        return self._eval_expr(m.group(1), params)

    def _eval_expr(self, expr, params):
        expr = expr.strip()
        if expr.startswith("and(") and expr.endswith(")"):
            return all(self._eval_expr(a, params) for a in _split_commas(expr[4:-1]))
        if expr.startswith("eq(") and expr.endswith(")"):
            ops = _split_commas(expr[3:-1])
            if len(ops) != 2:
                raise UnsupportedConstruct("eq() expects 2 operands: %s" % expr)
            return self._operand(ops[0], params) == self._operand(ops[1], params)
        raise UnsupportedConstruct("unsupported ${{ }} expression: %s" % expr)

    def _operand(self, tok, params):
        tok = tok.strip()
        if len(tok) >= 2 and tok[0] == "'" and tok[-1] == "'":
            return tok[1:-1]
        if tok in ("true", "false"):
            return tok
        if tok.startswith("parameters."):
            name = tok.split(".", 1)[1]
            if name not in params:
                raise UnsupportedConstruct("unbound parameter in guard: %s" % tok)
            val = params[name]
            if isinstance(val, bool):
                return "true" if val else "false"
            return str(val)
        raise UnsupportedConstruct("unsupported operand in guard: %s" % tok)

    # --- C3: variable/parameter substitution into a body -----------------

    def render(self, text, params):
        if not isinstance(text, str):
            text = str(text)

        def psub(m):
            name = m.group(1)
            if name not in params:
                raise UnsupportedConstruct("unbound parameter ${{ parameters.%s }}" % name)
            return str(params[name])

        text = re.sub(r"\$\{\{\s*parameters\.([A-Za-z_]\w*)\s*\}\}", psub, text)
        for old, new in PATH_REWRITES:
            text = text.replace(old, new)
        for name, val in self.macros.items():
            text = text.replace("$(%s)" % name, str(val))
        # FR7: refuse to guess at anything left unresolved.
        if "${{" in text:
            raise UnsupportedConstruct("unresolved ${{ }} expression in body: %r" % text)
        m = re.search(r"\$\(([A-Za-z_]\w*(?:\.\w+)+)\)", text)
        if m:
            raise UnsupportedConstruct("unknown ADO macro $(%s)" % m.group(1))
        return text

    # --- C4: step classifier ---------------------------------------------

    def classify(self, step, opts):
        src = step["_source"]
        params = step["_params"]
        display = self._safe_display(step.get("displayName", ""), params)
        if "bash" in step or "script" in step:
            kind_label = "bash" if "bash" in step else "script"
            # FG2: skip install-go.yml's host wget; use the container's Go unless
            # the developer explicitly opts to honor it.
            if src == "install-go.yml" and not opts.honor_go_install:
                return Record(NOOP, kind_label, display, src,
                              note="FG2: using container Go (skip install-go.yml wget)")
            body = self.render(step[kind_label], params)
            workdir = None
            if step.get("workingDirectory"):
                workdir = self.render(step["workingDirectory"], params)
            return Record(KEEP, kind_label, display, src, body=body, workdir=workdir)
        if "checkout" in step:
            return Record(NOOP, "checkout", display or str(step["checkout"]), src,
                          note="bind-mounts already provide the repo")
        if "task" in step:
            task = step["task"]
            if task in NOOP_TASKS:
                return Record(NOOP, "task:%s" % task, display, src,
                              note="mapped to cache/local artifact")
            return Record(ERROR, "task:%s" % task, display, src,
                          note="FR7: unsupported task")
        if "publish" in step or "download" in step:
            return Record(NOOP, "publish" if "publish" in step else "download", display, src,
                          note="artifact publish/download has no local meaning")
        return Record(ERROR, "unknown", display, src, note="FR7: unsupported step kind")

    def _safe_display(self, text, params):
        try:
            return self.render(text, params) if text else ""
        except UnsupportedConstruct:
            return str(text)

    def extract(self, job_key, opts):
        records = [self.classify(s, opts) for s in self.flatten(job_key)]
        if opts.exclude_pkg_exec and job_key == "pure_tests":
            _apply_exclude_pkg_exec(records)
        return records


def _read_pure_packages():
    """Read PURE_PACKAGES from pure.mk so --exclude-pkg-exec drops pkg/exec
    without re-hardcoding the package list."""
    pkgs = []
    capture = False
    with open(os.path.join(REPO_ROOT, "pure.mk"), "r") as f:
        for line in f:
            if line.startswith("PURE_PACKAGES"):
                capture = True
                continue
            if capture:
                tok = line.strip().rstrip("\\").strip()
                if not tok:
                    break
                pkgs.append(tok)
                if not line.rstrip().endswith("\\"):
                    break
    out = [p for p in pkgs if p and not p.startswith("#")]
    if not out:
        # Guard against silent behavior changes: an empty PACKAGES list would
        # alter `make` semantics. Fail loudly if pure.mk's structure changed.
        raise UnsupportedConstruct("no packages parsed from pure.mk (structure changed?)")
    return out


def _apply_exclude_pkg_exec(records):
    pkgs = [p for p in _read_pure_packages() if p != "pkg/exec"]
    repl = "make -f pure.mk PACKAGES='%s' junit-xml" % " ".join(pkgs)
    for r in records:
        if r.kind == KEEP and r.body and "make -f pure.mk junit-xml" in r.body:
            r.body = r.body.replace("make -f pure.mk junit-xml", repl)
            r.note = "FG1: pkg/exec excluded (deviation from CI body)"


# --- C5: emitter ----------------------------------------------------------

def emit_program(records):
    parts = ["set -euo pipefail", ""]
    for r in records:
        if r.kind != KEEP:
            continue
        parts.append("# === %s [%s] ===" % (r.display or "(no name)", r.source))
        parts.append("(")
        parts.append("cd %s" % (r.workdir or "/work"))
        parts.append(r.body.rstrip("\n"))
        parts.append(")")
        parts.append("")
    return "\n".join(parts)


def assert_no_errors(records):
    bad = [r for r in records if r.kind == ERROR]
    if bad:
        msg = "; ".join("%s in %s (%s)" % (r.kind_label, r.source, r.note) for r in bad)
        raise UnsupportedConstruct("unsupported pipeline construct(s): " + msg)


# --- CLI ------------------------------------------------------------------

def print_explain(job_key, records):
    print("# explain %s (stage %s)" % (job_key, SUPPORTED_JOBS.get(job_key, "?")))
    print("%-6s %-26s %-26s %s" % ("STATUS", "TYPE", "SOURCE", "DISPLAY / NOTE"))
    for r in records:
        detail = r.display or ""
        if r.note:
            detail = (detail + "  " if detail else "") + "(%s)" % r.note
        print("%-6s %-26s %-26s %s" % (r.kind, r.kind_label, r.source, detail))


def cmd_list(_opts):
    print("%-20s %s" % ("JOB", "STAGE"))
    for job, stage in SUPPORTED_JOBS.items():
        print("%-20s %s" % (job, stage))
    return 0


def cmd_explain(opts):
    pl = Pipeline(opts.pipeline, opts.branch)
    records = pl.extract(opts.job, opts)
    print_explain(opts.job, records)
    assert_no_errors(records)
    return 0


def cmd_run(opts):
    pl = Pipeline(opts.pipeline, opts.branch)
    records = pl.extract(opts.job, opts)
    program = emit_program(records)
    if opts.dry_run:
        print_explain(opts.job, records)
        print("\n# --- emitted program ---")
        print(program)
        assert_no_errors(records)
        return 0
    # Mirror cmd_explain: print the full classification (including ERROR rows
    # with source refs) BEFORE asserting, so --explain still reports every step
    # before exiting non-zero rather than crashing on the first error.
    if opts.explain:
        print_explain(opts.job, records)
    assert_no_errors(records)
    runtests = os.path.join(os.path.dirname(os.path.abspath(__file__)), "run-tests.sh")
    driver = (
        "set -euo pipefail\n"
        "source %s\n"
        "require_cache\n"
        "docker_run -t bash -c %s\n"
    ) % (shlex.quote(runtests), shlex.quote(program))
    return subprocess.call(["bash", "-c", driver])


def build_parser():
    p = argparse.ArgumentParser(
        prog="ado-local.py",
        description="Read-only ADO-YAML step extractor/driver for sonic-gnmi.",
    )
    p.add_argument("--pipeline", default=os.path.join(REPO_ROOT, "azure-pipelines.yml"),
                   help="override azure-pipelines.yml path (default: repo-root)")
    p.add_argument("--branch", default="master", help="value for $(BUILD_BRANCH) (default: master)")
    p.add_argument("--honor-go-install", action="store_true",
                   help="run install-go.yml's wget body instead of using container Go")
    p.add_argument("--exclude-pkg-exec", action="store_true",
                   help="pure_tests only: drop pkg/exec to match run-tests.sh (logged FG1 deviation)")
    sub = p.add_subparsers(dest="command", required=True)

    sub.add_parser("list", help="list supported jobs and their stage")

    pe = sub.add_parser("explain", help="print per-step classification + source refs")
    pe.add_argument("job", choices=sorted(SUPPORTED_JOBS))

    pr = sub.add_parser("run", help="extract + run the job's step bodies in the container")
    pr.add_argument("job", choices=sorted(SUPPORTED_JOBS))
    pr.add_argument("--dry-run", action="store_true", help="print the emitted program, do not execute")
    pr.add_argument("--explain", action="store_true", help="verbose per-step mapping report")
    return p


def main(argv=None):
    opts = build_parser().parse_args(argv)
    try:
        if opts.command == "list":
            return cmd_list(opts)
        if opts.command == "explain":
            return cmd_explain(opts)
        if opts.command == "run":
            return cmd_run(opts)
    except UnsupportedConstruct as e:
        print("error: %s" % e, file=sys.stderr)
        return 2
    return 1


if __name__ == "__main__":
    sys.exit(main())
