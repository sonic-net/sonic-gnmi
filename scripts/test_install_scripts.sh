#!/bin/sh
# Functional tests for scripts/gofmt-check.sh and the SONiC dependency install
# scripts (install-test-deps.sh, install-debs.sh, install-swsscommon.sh,
# install-protoc.sh).
#
# These tests stub external commands (gofmt, pip3, apt-get, dpkg, find, protoc)
# on PATH so no real download, extraction, or package operations run. The stubs
# record the argv they were invoked with so the tests can assert that each
# script reproduces the original inlined commands exactly.
#
# Run: sh scripts/test_install_scripts.sh
set -e

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
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

assert_eq() {
  # assert_eq <actual> <expected> <description>
  if [ "$1" = "$2" ]; then
    pass "$3"
  else
    fail "$3 (expected: $2, got: $1)"
  fi
}

cleanup() { [ -n "${WORK:-}" ] && rm -rf "$WORK"; }
trap cleanup EXIT

# ---------------------------------------------------------------------------
# gofmt-check.sh
# ---------------------------------------------------------------------------
make_gofmt_workdir() {
  WORK=$(mktemp -d)
  BIN="$WORK/bin"
  TREE="$WORK/tree"
  mkdir -p "$BIN" "$TREE"
}

# Stub gofmt: a file is "unformatted" if it contains the literal token BADFMT.
# `gofmt -l` prints such files; `gofmt -d` prints a fake diff for them.
install_fake_gofmt() {
  cat > "$BIN/gofmt" <<'EOF'
#!/bin/sh
mode="$1"; shift
case "$mode" in
  -l)
    for f in "$@"; do
      if grep -q BADFMT "$f" 2>/dev/null; then echo "$f"; fi
    done
    ;;
  -d)
    for f in "$@"; do
      echo "--- $f (fake diff) ---"
    done
    ;;
esac
EOF
  chmod +x "$BIN/gofmt"
}

test_gofmt_check_clean_tree() {
  make_gofmt_workdir
  install_fake_gofmt
  printf 'package main\n' > "$TREE/clean.go"

  set +e
  out=$( cd "$TREE" && PATH="$BIN:$PATH" bash "$SCRIPT_DIR/gofmt-check.sh" 2>&1 )
  rc=$?
  set -e
  assert_eq "$rc" "0" "gofmt-check: clean tree exits 0"
  printf '%s' "$out" | grep -qF "All files properly formatted." \
    && pass "gofmt-check: clean tree prints success message" \
    || fail "gofmt-check: clean tree prints success message"
  cleanup
}

test_gofmt_check_bad_tree() {
  make_gofmt_workdir
  install_fake_gofmt
  printf 'package main\nBADFMT\n' > "$TREE/bad.go"

  set +e
  out=$( cd "$TREE" && PATH="$BIN:$PATH" bash "$SCRIPT_DIR/gofmt-check.sh" 2>&1 )
  rc=$?
  set -e
  assert_eq "$rc" "1" "gofmt-check: mis-formatted file exits 1"
  printf '%s' "$out" | grep -qF "::error::" \
    && pass "gofmt-check: bad tree prints ::error:: annotation" \
    || fail "gofmt-check: bad tree prints ::error:: annotation"
  printf '%s' "$out" | grep -qF "fake diff" \
    && pass "gofmt-check: bad tree prints gofmt diff" \
    || fail "gofmt-check: bad tree prints gofmt diff"
  cleanup
}

# Excluded directories (vendor/build/patches/proto/swsscommon) must be ignored
# even if they contain unformatted files.
test_gofmt_check_excludes() {
  make_gofmt_workdir
  install_fake_gofmt
  printf 'package main\n' > "$TREE/clean.go"
  for d in vendor build patches proto swsscommon; do
    mkdir -p "$TREE/$d"
    printf 'package x\nBADFMT\n' > "$TREE/$d/x.go"
  done

  set +e
  out=$( cd "$TREE" && PATH="$BIN:$PATH" bash "$SCRIPT_DIR/gofmt-check.sh" 2>&1 )
  rc=$?
  set -e
  assert_eq "$rc" "0" "gofmt-check: excluded dirs are ignored (exit 0)"
  cleanup
}

# ---------------------------------------------------------------------------
# StaticChecks gofmt step calls the script at root with no prefix. Extract the
# real call site from azure-pipelines.yml so this fails if it ever changes.
test_static_checks_gofmt_path() {
  actual=$(grep -oE '[^ ]*scripts/gofmt-check.sh' \
    "$SCRIPT_DIR/../azure-pipelines.yml" | head -1)
  assert_eq "$actual" "scripts/gofmt-check.sh" \
    "azure-pipelines.yml: StaticChecks gofmt resolves scripts/gofmt-check.sh"
}

# ---------------------------------------------------------------------------
# SONiC dependency install scripts
# ---------------------------------------------------------------------------
# Stub pip3, apt-get, dpkg, find, protoc on PATH (plus the pass-through sudo) so
# no real package operations run. Each stub records its argv so the tests can
# assert the extracted scripts reproduce the original install-dependencies.yml
# command bodies exactly.
make_deps_workdir() {
  WORK=$(mktemp -d)
  BIN="$WORK/bin"
  mkdir -p "$BIN"

  cat > "$BIN/sudo" <<'EOF'
#!/bin/sh
exec "$@"
EOF
  chmod +x "$BIN/sudo"

  for tool in pip3 apt-get protoc; do
    cat > "$BIN/$tool" <<EOF
#!/bin/sh
echo "ARGV=\$*" >> "$WORK/$tool.log"
EOF
    chmod +x "$BIN/$tool"
  done

  # find stub: echo a fake .deb path under the searched dir so dpkg gets argv.
  cat > "$BIN/find" <<EOF
#!/bin/sh
echo "\$1/pkg.deb" >> "$WORK/find.log"
echo "\$1/pkg.deb"
EOF
  chmod +x "$BIN/find"
}

# dpkg stub variant that records argv and succeeds.
install_dpkg_ok() {
  cat > "$BIN/dpkg" <<EOF
#!/bin/sh
echo "ARGV=\$*" >> "$WORK/dpkg.log"
EOF
  chmod +x "$BIN/dpkg"
}

# dpkg stub variant that records argv and fails (drives the FIX_DEPS fallback).
install_dpkg_fail() {
  cat > "$BIN/dpkg" <<EOF
#!/bin/sh
echo "ARGV=\$*" >> "$WORK/dpkg.log"
exit 1
EOF
  chmod +x "$BIN/dpkg"
}

assert_not_contains() {
  # assert_not_contains <file> <needle> <description>
  if [ ! -f "$1" ] || ! grep -qF -- "$2" "$1"; then
    pass "$3"
  else
    fail "$3 (did not expect: $2)"
  fi
}

# install-test-deps.sh ------------------------------------------------------
test_install_test_deps_no_flags() {
  make_deps_workdir
  ( PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-test-deps.sh" )
  assert_contains "$WORK/pip3.log" "ARGV=install -U pytest" \
    "install-test-deps: PIP_FLAGS empty -> verbatim pytest argv"
  assert_contains "$WORK/pip3.log" "ARGV=install -U jsonpatch" \
    "install-test-deps: PIP_FLAGS empty -> verbatim jsonpatch argv"
  assert_not_contains "$WORK/pip3.log" "--break-system-packages" \
    "install-test-deps: PIP_FLAGS empty -> no --break-system-packages"
  cleanup
}

test_install_test_deps_with_flags() {
  make_deps_workdir
  ( PATH="$BIN:$PATH" PIP_FLAGS=--break-system-packages \
      sh "$SCRIPT_DIR/install-test-deps.sh" )
  assert_contains "$WORK/pip3.log" "ARGV=install --break-system-packages -U pytest" \
    "install-test-deps: PIP_FLAGS forwarded as argv to pip3 (pytest)"
  assert_contains "$WORK/pip3.log" "ARGV=install --break-system-packages -U jsonpatch" \
    "install-test-deps: PIP_FLAGS forwarded as argv to pip3 (jsonpatch)"
  cleanup
}

# install-debs.sh -----------------------------------------------------------
test_install_debs_no_fix() {
  make_deps_workdir
  install_dpkg_fail
  set +e
  ( PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-debs.sh" /some/dir ) >/dev/null 2>&1
  set -e
  assert_contains "$WORK/dpkg.log" "ARGV=-i /some/dir/pkg.deb" \
    "install-debs: dpkg -i \$(find <dir> -name '*.deb')"
  assert_not_contains "$WORK/apt-get.log" "install -f -y" \
    "install-debs: FIX_DEPS unset -> no apt-get install -f fallback"
  cleanup
}

test_install_debs_with_fix() {
  make_deps_workdir
  install_dpkg_fail
  set +e
  ( PATH="$BIN:$PATH" FIX_DEPS=1 sh "$SCRIPT_DIR/install-debs.sh" /some/dir ) >/dev/null 2>&1
  rc=$?
  set -e
  assert_eq "$rc" "0" "install-debs: FIX_DEPS=1 -> fallback recovers dpkg failure"
  assert_contains "$WORK/apt-get.log" "install -f -y" \
    "install-debs: FIX_DEPS=1 -> appends || sudo apt-get install -f -y"
  cleanup
}

# install-swsscommon.sh -----------------------------------------------------
test_install_swsscommon_amd64() {
  make_deps_workdir
  install_dpkg_ok
  ( PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-swsscommon.sh" amd64 )
  assert_contains "$WORK/dpkg.log" "libswsscommon_1.0.0_amd64.deb" \
    "install-swsscommon amd64: installs libswsscommon"
  assert_contains "$WORK/dpkg.log" "libswsscommon-dev_1.0.0_amd64.deb" \
    "install-swsscommon amd64: installs libswsscommon-dev"
  assert_contains "$WORK/dpkg.log" "python3-swsscommon_1.0.0_amd64.deb" \
    "install-swsscommon amd64: installs python3-swsscommon"
  cleanup
}

test_install_swsscommon_arm64() {
  make_deps_workdir
  install_dpkg_ok
  ( PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-swsscommon.sh" arm64 )
  assert_contains "$WORK/dpkg.log" "libswsscommon_1.0.0_arm64.deb" \
    "install-swsscommon arm64: installs libswsscommon"
  assert_contains "$WORK/dpkg.log" "libswsscommon-dev_1.0.0_arm64.deb" \
    "install-swsscommon arm64: installs libswsscommon-dev"
  assert_not_contains "$WORK/dpkg.log" "python3-swsscommon" \
    "install-swsscommon arm64: skips python3-swsscommon"
  cleanup
}

# install-protoc.sh ---------------------------------------------------------
test_install_protoc_amd64() {
  make_deps_workdir
  ( PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-protoc.sh" amd64 )
  assert_not_contains "$WORK/apt-get.log" "ARGV=update" \
    "install-protoc amd64: no apt-get update"
  assert_contains "$WORK/apt-get.log" "ARGV=install -y protobuf-compiler" \
    "install-protoc amd64: installs protobuf-compiler"
  assert_contains "$WORK/protoc.log" "ARGV=--version" \
    "install-protoc amd64: prints protoc version"
  cleanup
}

test_install_protoc_arm64() {
  make_deps_workdir
  ( PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-protoc.sh" arm64 )
  assert_contains "$WORK/apt-get.log" "ARGV=update" \
    "install-protoc arm64: runs apt-get update first"
  assert_contains "$WORK/apt-get.log" "ARGV=install -y protobuf-compiler" \
    "install-protoc arm64: installs protobuf-compiler"
  cleanup
}

test_gofmt_check_clean_tree
test_gofmt_check_bad_tree
test_gofmt_check_excludes
test_static_checks_gofmt_path
test_install_test_deps_no_flags
test_install_test_deps_with_flags
test_install_debs_no_fix
test_install_debs_with_fix
test_install_swsscommon_amd64
test_install_swsscommon_arm64
test_install_protoc_amd64
test_install_protoc_arm64

echo "-------------------------------------"
echo "PASS: $PASS  FAIL: $FAIL"
[ "$FAIL" -eq 0 ]
