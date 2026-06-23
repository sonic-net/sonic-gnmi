#!/usr/bin/env bash
# Regression guard for the shared scaffolding helpers in dev/run-tests.sh (Epic D).
#
# run_interactive_container() and gobin_on_path_snippet() collapse the rc-file +
# `exec bash --rcfile` boilerplate that `shell` and `playground` used to inline.
# Their quoting model is subtle: the rc-file body is written via a `<<'RC'`
# heredoc (no host expansion) while the surrounding `bash -c` string DOES expand
# on the host, so `$port` lands literally and `\$VAR` defers to the container.
# A future edit that flips `<<'RC'` to `<<RC`, drops a `\$`, or breaks the
# `&&`-chained build_nonpure_snippet would silently corrupt both subcommands.
#
# This test sources run-tests.sh (the bottom dispatch is guarded by a
# BASH_SOURCE check, so sourcing defines the functions without running anything),
# stubs docker_run + require_cache, invokes each subcommand builder, and asserts
# the captured `bash -c` body contains the expected commands.
#
# Run: bash scripts/test_run_tests.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_TESTS="$SCRIPT_DIR/../dev/run-tests.sh"
PASS=0
FAIL=0

pass() { PASS=$((PASS + 1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL + 1)); echo "FAIL - $1"; }

assert_contains() {
  # assert_contains <file> <needle> <description>
  if grep -qF -- "$2" "$1"; then
    pass "$3"
  else
    fail "$3 (expected to find: $2)"
    echo "      --- $1 ---"
    sed 's/^/      /' "$1"
  fi
}

assert_absent() {
  # assert_absent <file> <needle> <description>
  if grep -qF -- "$2" "$1"; then
    fail "$3 (must NOT contain: $2)"
    echo "      --- $1 ---"
    sed 's/^/      /' "$1"
  else
    pass "$3"
  fi
}

# ---------------------------------------------------------------------------
# bash -n syntax gate
# ---------------------------------------------------------------------------
if bash -n "$RUN_TESTS"; then
  pass "run-tests.sh: bash -n passes"
else
  fail "run-tests.sh: bash -n failed"
fi

# ---------------------------------------------------------------------------
# Source the driver and replace the side-effecting primitives with capturing
# stubs. CAPTURE holds the full docker_run invocation (flags + EXTRA_DOCKER_ARGS
# + every argument), so the rendered `bash -c` body is grep-able as text.
# ---------------------------------------------------------------------------
CAPTURE=$(mktemp)
cleanup() { rm -f "$CAPTURE"; }
trap cleanup EXIT

# shellcheck source=/dev/null
. "$RUN_TESTS"
set +e  # run-tests.sh enables `set -e`; relax it so assertions keep running.

require_cache() { :; }
docker_run() {
  {
    echo "FLAGS=$1"
    echo "EXTRA=${EXTRA_DOCKER_ARGS:-}"
    shift
    printf '%s\n' "$@"
  } >"$CAPTURE"
}

# ---------------------------------------------------------------------------
# shell: interactive scaffold + build-nonpure helper.
# ---------------------------------------------------------------------------
test_shell_snippet() {
  run_shell >/dev/null 2>&1
  assert_contains "$CAPTURE" "FLAGS=-it" "shell: runs container with -it"
  assert_contains "$CAPTURE" "cat >/tmp/gnmi-shellrc <<'RC'" "shell: writes rc-file via quoted heredoc"
  assert_contains "$CAPTURE" "exec bash --rcfile /tmp/gnmi-shellrc" "shell: exec's interactive bash from rc-file"
  assert_contains "$CAPTURE" "build-nonpure() {" "shell: defines build-nonpure helper"
  assert_contains "$CAPTURE" "CGO_LDFLAGS='-lswsscommon -lhiredis'" "shell: seeds CGO flags"
  # build_nonpure_snippet must short-circuit: mgmt-common && make all (no bare newline).
  assert_contains "$CAPTURE" "build-deb.sh mgmt-common /work/sonic-mgmt-common \\" "shell: build-nonpure &&-chains to make all"
  assert_contains "$CAPTURE" "&& ( cd /work/sonic-gnmi && make all )" "shell: build-nonpure runs make all only on success"
}

# ---------------------------------------------------------------------------
# playground: same scaffold + server boot + -p publish + GOBIN-on-PATH.
# ---------------------------------------------------------------------------
test_playground_snippet() {
  run_playground 9090 >/dev/null 2>&1
  assert_contains "$CAPTURE" "FLAGS=-it" "playground: runs container with -it"
  assert_contains "$CAPTURE" "EXTRA=-p 9090:9090" "playground: publishes the requested port"
  assert_contains "$CAPTURE" "cat >/tmp/gnmi-playground-rc <<'RC'" "playground: writes its own rc-file"
  assert_contains "$CAPTURE" "exec bash --rcfile /tmp/gnmi-playground-rc" "playground: exec's interactive bash from rc-file"
  assert_contains "$CAPTURE" "export GOBIN=/work/sonic-gnmi/build/bin" "playground: GOBIN-on-PATH snippet present"
  assert_contains "$CAPTURE" "telemetry --noTLS --insecure --allow_no_client_auth" "playground: boots the telemetry server"
  assert_contains "$CAPTURE" "PORT=9090" "playground: port is host-expanded into the boot body"
}

# ---------------------------------------------------------------------------
# build: DD6 — vendor-sync flow must stay intact (NOT routed through make all).
# ---------------------------------------------------------------------------
test_build_snippet() {
  run_build >/dev/null 2>&1
  assert_contains "$CAPTURE" "build-deb.sh mgmt-common /work/sonic-mgmt-common" "build: mgmt-common via build-deb.sh"
  assert_contains "$CAPTURE" "go mod tidy && go mod vendor" "build: keeps vendor-sync step"
  assert_contains "$CAPTURE" "build-deb.sh gnmi /work/sonic-gnmi /build-out" "build: gnmi deb via build-deb.sh"
  assert_absent "$CAPTURE" "make all" "build: does NOT inject make all (DD6)"
}

test_shell_snippet
test_playground_snippet
test_build_snippet

echo "-------------------------------------"
echo "PASS: $PASS  FAIL: $FAIL"
[ "$FAIL" -eq 0 ]
