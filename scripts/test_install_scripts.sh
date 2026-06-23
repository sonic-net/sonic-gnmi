#!/bin/sh
# Functional tests for scripts/install-go.sh and scripts/gofmt-check.sh.
#
# These tests stub `wget`, `tar`, and `gofmt` on PATH so no real download,
# extraction, or Go toolchain is needed. The stubs record the argv they were
# invoked with so the tests can assert that each script reproduces the original
# inlined commands exactly:
#   - install-go.sh : wget -q https://go.dev/dl/go<ver>.linux-<arch>.tar.gz
#                     sudo tar -C /usr/local -xzf go<ver>.linux-<arch>.tar.gz
#                     go version
#   - gofmt-check.sh: gofmt -l of all non-excluded *.go files; exit 1 + diff on
#                     a mis-formatted file, exit 0 + message on a clean tree.
#
# It also asserts the install-go.yml call-path prefix logic resolves to
# `scripts/install-go.sh` for the single-checkout StaticChecks job and
# `sonic-gnmi/scripts/install-go.sh` for the multi-checkout pure_tests job.
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

make_workdir() {
  WORK=$(mktemp -d)
  BIN="$WORK/bin"
  mkdir -p "$BIN"

  # Stub wget: record argv.
  cat > "$BIN/wget" <<EOF
#!/bin/sh
echo "ARGV=\$*" >> "$WORK/wget.log"
EOF
  chmod +x "$BIN/wget"

  # Stub sudo: just run the wrapped command (so tar stub still records argv).
  cat > "$BIN/sudo" <<'EOF'
#!/bin/sh
exec "$@"
EOF
  chmod +x "$BIN/sudo"

  # Stub tar: record argv.
  cat > "$BIN/tar" <<EOF
#!/bin/sh
echo "ARGV=\$*" >> "$WORK/tar.log"
EOF
  chmod +x "$BIN/tar"

  # Stub go: record argv (covers `go version` inside install-go.sh).
  cat > "$BIN/go" <<EOF
#!/bin/sh
echo "ARGV=\$*" >> "$WORK/go.log"
EOF
  chmod +x "$BIN/go"
}

# ---------------------------------------------------------------------------
# install-go.sh
# ---------------------------------------------------------------------------
test_install_go_explicit_args() {
  make_workdir
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-go.sh" 1.24.4 amd64 )

  assert_contains "$WORK/wget.log" \
    "https://go.dev/dl/go1.24.4.linux-amd64.tar.gz" \
    "install-go: wget downloads linux-amd64 tarball"
  assert_contains "$WORK/wget.log" "-q" "install-go: wget runs quietly"
  assert_contains "$WORK/tar.log" "-C /usr/local -xzf go1.24.4.linux-amd64.tar.gz" \
    "install-go: tar extracts to /usr/local"
  assert_contains "$WORK/go.log" "ARGV=version" "install-go: go version invoked"
  cleanup
}

# Defaults: version=1.24.4, arch=amd64 when no args given.
test_install_go_defaults() {
  make_workdir
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-go.sh" )
  assert_contains "$WORK/wget.log" \
    "https://go.dev/dl/go1.24.4.linux-amd64.tar.gz" \
    "install-go: default version/arch resolve to go1.24.4.linux-amd64"
  cleanup
}

# arch parameter overrides the linux-<arch> segment.
test_install_go_arch_override() {
  make_workdir
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-go.sh" 1.24.4 arm64 )
  assert_contains "$WORK/wget.log" \
    "https://go.dev/dl/go1.24.4.linux-arm64.tar.gz" \
    "install-go: arch override produces linux-arm64 tarball"
  assert_contains "$WORK/tar.log" "go1.24.4.linux-arm64.tar.gz" \
    "install-go: tar uses arch-specific tarball"
  cleanup
}

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
# install-go.yml call-path prefix resolution (repoRoot logic)
# ---------------------------------------------------------------------------
# Mirrors `${{ parameters.repoRoot }}scripts/install-go.sh`: StaticChecks passes
# nothing (repoRoot=''), pure_tests passes repoRoot='sonic-gnmi/'.
resolve_path() { printf '%sscripts/install-go.sh' "$1"; }

test_static_checks_path() {
  assert_eq "$(resolve_path '')" "scripts/install-go.sh" \
    "install-go.yml: StaticChecks (repoRoot='') resolves scripts/install-go.sh"
}

test_pure_tests_path() {
  assert_eq "$(resolve_path 'sonic-gnmi/')" "sonic-gnmi/scripts/install-go.sh" \
    "install-go.yml: pure_tests (repoRoot='sonic-gnmi/') resolves sonic-gnmi/scripts/install-go.sh"
}

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
  assert_contains "$WORK/apt-get.log" "ARGV=update" \
    "install-test-deps: refreshes apt index"
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

# install-yang-models.sh ----------------------------------------------------
test_install_yang_no_flags() {
  make_deps_workdir
  ( PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-yang-models.sh" '/path/*.whl' )
  assert_contains "$WORK/pip3.log" "ARGV=install /path/*.whl" \
    "install-yang-models: PIP_FLAGS empty -> verbatim wheel argv"
  assert_not_contains "$WORK/pip3.log" "--break-system-packages" \
    "install-yang-models: PIP_FLAGS empty -> no --break-system-packages"
  cleanup
}

test_install_yang_with_flags() {
  make_deps_workdir
  ( PATH="$BIN:$PATH" PIP_FLAGS=--break-system-packages \
      sh "$SCRIPT_DIR/install-yang-models.sh" '/path/*.whl' )
  assert_contains "$WORK/pip3.log" "ARGV=install --break-system-packages /path/*.whl" \
    "install-yang-models: PIP_FLAGS forwarded as argv to pip3"
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

test_install_go_explicit_args
test_install_go_defaults
test_install_go_arch_override
test_gofmt_check_clean_tree
test_gofmt_check_bad_tree
test_gofmt_check_excludes
test_static_checks_path
test_pure_tests_path
test_static_checks_gofmt_path
test_install_test_deps_no_flags
test_install_test_deps_with_flags
test_install_debs_no_fix
test_install_debs_with_fix
test_install_yang_no_flags
test_install_yang_with_flags
test_install_swsscommon_amd64
test_install_swsscommon_arm64
test_install_protoc_amd64
test_install_protoc_arm64

echo "-------------------------------------"
echo "PASS: $PASS  FAIL: $FAIL"
[ "$FAIL" -eq 0 ]
