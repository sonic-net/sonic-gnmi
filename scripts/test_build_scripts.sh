#!/bin/sh
# Functional tests for scripts/build-deb.sh.
#
# These tests stub `dpkg-buildpackage` and `nproc` on PATH so no real package
# build runs. The stubs record the environment, working directory, and argv they
# were invoked with so the tests can assert that each subcommand reproduces the
# original inlined commands exactly:
#   - build-deb.sh mgmt-common : NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc
#   - build-deb.sh gnmi        : ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y
#                                dpkg-buildpackage -rfakeroot -b -us -uc -j<nproc>
# plus the OUT_DIR / COPY_GLOB copy semantics, and the `all` subcommand that
# chains mgmt-common then gnmi.
#
# Run: sh scripts/test_build_scripts.sh
set -e

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
BUILD_DEB="$SCRIPT_DIR/build-deb.sh"
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

assert_file_exists() {
  if [ -e "$1" ]; then
    pass "$2"
  else
    fail "$2 (missing: $1)"
  fi
}

make_workdir() {
  WORK=$(mktemp -d)
  BIN="$WORK/bin"
  mkdir -p "$BIN"

  # Stub dpkg-buildpackage: record cwd, argv, and the relevant env vars.
  cat > "$BIN/dpkg-buildpackage" <<EOF
#!/bin/sh
{
  echo "CWD=\$(pwd)"
  echo "ARGV=\$*"
  echo "NO_TEST_BINS=\${NO_TEST_BINS:-}"
  echo "ENABLE_TRANSLIB_WRITE=\${ENABLE_TRANSLIB_WRITE:-}"
  echo "ENABLE_NATIVE_WRITE=\${ENABLE_NATIVE_WRITE:-}"
} >> "$WORK/dpkg.log"
EOF
  chmod +x "$BIN/dpkg-buildpackage"

  # Stub nproc to a deterministic value so -j flag is predictable.
  cat > "$BIN/nproc" <<'EOF'
#!/bin/sh
echo 4
EOF
  chmod +x "$BIN/nproc"
}

cleanup() { [ -n "${WORK:-}" ] && rm -rf "$WORK"; }
trap cleanup EXIT

# ---------------------------------------------------------------------------
# build-deb.sh mgmt-common
# ---------------------------------------------------------------------------
test_mgmt_common() {
  make_workdir
  mkdir -p "$WORK/sonic-mgmt-common"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" mgmt-common sonic-mgmt-common )

  assert_file_exists "$WORK/dpkg.log" "mgmt-common: dpkg-buildpackage invoked"
  assert_contains "$WORK/dpkg.log" "NO_TEST_BINS=1" "mgmt-common: NO_TEST_BINS=1 set"
  assert_contains "$WORK/dpkg.log" "ARGV=-rfakeroot -b -us -uc" "mgmt-common: correct dpkg args"
  assert_contains "$WORK/dpkg.log" "CWD=$WORK/sonic-mgmt-common" "mgmt-common: ran inside the target dir"
  cleanup
}

# Default arg should be 'sonic-mgmt-common' when none is passed.
test_mgmt_common_default_dir() {
  make_workdir
  mkdir -p "$WORK/sonic-mgmt-common"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" mgmt-common )
  assert_contains "$WORK/dpkg.log" "CWD=$WORK/sonic-mgmt-common" "mgmt-common: default dir is sonic-mgmt-common"
  cleanup
}

# ---------------------------------------------------------------------------
# build-deb.sh gnmi
# ---------------------------------------------------------------------------
test_gnmi_deb_env_and_jflag() {
  make_workdir
  mkdir -p "$WORK/sonic-gnmi"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" gnmi sonic-gnmi )

  assert_contains "$WORK/dpkg.log" "ENABLE_TRANSLIB_WRITE=y" "gnmi: ENABLE_TRANSLIB_WRITE=y set"
  assert_contains "$WORK/dpkg.log" "ENABLE_NATIVE_WRITE=y" "gnmi: ENABLE_NATIVE_WRITE=y set"
  assert_contains "$WORK/dpkg.log" "ARGV=-rfakeroot -b -us -uc -j4" "gnmi: dpkg args include -j\$(nproc)=4"
  assert_contains "$WORK/dpkg.log" "CWD=$WORK/sonic-gnmi" "gnmi: ran inside the gnmi dir"
  cleanup
}

# Default '*.deb' glob copies ALL debs in the parent dir (ADO staging behavior).
test_gnmi_deb_default_glob() {
  make_workdir
  mkdir -p "$WORK/sonic-gnmi" "$WORK/out"
  : > "$WORK/sonic-gnmi_1.0_amd64.deb"
  : > "$WORK/sonic-mgmt-common_1.0_amd64.deb"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" gnmi sonic-gnmi out )

  assert_file_exists "$WORK/out/sonic-gnmi_1.0_amd64.deb" "gnmi: default glob copies gnmi deb"
  assert_file_exists "$WORK/out/sonic-mgmt-common_1.0_amd64.deb" "gnmi: default glob also copies mgmt-common deb"
  cleanup
}

# Narrower 'sonic-gnmi_*.deb' glob (dev behavior) copies only the gnmi deb.
test_gnmi_deb_narrow_glob() {
  make_workdir
  mkdir -p "$WORK/sonic-gnmi" "$WORK/out"
  : > "$WORK/sonic-gnmi_1.0_amd64.deb"
  : > "$WORK/sonic-mgmt-common_1.0_amd64.deb"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" gnmi sonic-gnmi out 'sonic-gnmi_*.deb' )

  assert_file_exists "$WORK/out/sonic-gnmi_1.0_amd64.deb" "gnmi: narrow glob copies gnmi deb"
  if [ -e "$WORK/out/sonic-mgmt-common_1.0_amd64.deb" ]; then
    fail "gnmi: narrow glob must NOT copy mgmt-common deb"
  else
    pass "gnmi: narrow glob excludes mgmt-common deb"
  fi
  cleanup
}

# OUT_DIR that does not yet exist must be created (mkdir -p guard).
test_gnmi_deb_creates_out_dir() {
  make_workdir
  mkdir -p "$WORK/sonic-gnmi"
  : > "$WORK/sonic-gnmi_1.0_amd64.deb"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" gnmi sonic-gnmi newout 'sonic-gnmi_*.deb' )
  assert_file_exists "$WORK/newout/sonic-gnmi_1.0_amd64.deb" "gnmi: missing OUT_DIR is created"
  cleanup
}

# No OUT_DIR => build only, no copy.
test_gnmi_deb_no_outdir() {
  make_workdir
  mkdir -p "$WORK/sonic-gnmi"
  : > "$WORK/sonic-gnmi_1.0_amd64.deb"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" gnmi sonic-gnmi )
  assert_file_exists "$WORK/dpkg.log" "gnmi: builds even when OUT_DIR omitted"
  cleanup
}

# ---------------------------------------------------------------------------
# build-deb.sh all
# ---------------------------------------------------------------------------
# `all` chains mgmt-common then gnmi, preserving each build's env/argv and the
# gnmi copy semantics.
test_all_chains_both_builds() {
  make_workdir
  mkdir -p "$WORK/sonic-mgmt-common" "$WORK/sonic-gnmi" "$WORK/out"
  : > "$WORK/sonic-gnmi_1.0_amd64.deb"
  : > "$WORK/sonic-mgmt-common_1.0_amd64.deb"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" all sonic-mgmt-common sonic-gnmi out 'sonic-gnmi_*.deb' )

  assert_contains "$WORK/dpkg.log" "NO_TEST_BINS=1" "all: mgmt-common NO_TEST_BINS=1 set"
  assert_contains "$WORK/dpkg.log" "CWD=$WORK/sonic-mgmt-common" "all: mgmt-common build ran"
  assert_contains "$WORK/dpkg.log" "ENABLE_TRANSLIB_WRITE=y" "all: gnmi ENABLE_TRANSLIB_WRITE=y set"
  assert_contains "$WORK/dpkg.log" "ARGV=-rfakeroot -b -us -uc -j4" "all: gnmi dpkg args include -j4"
  assert_contains "$WORK/dpkg.log" "CWD=$WORK/sonic-gnmi" "all: gnmi build ran"
  assert_file_exists "$WORK/out/sonic-gnmi_1.0_amd64.deb" "all: gnmi copy glob honored"
  cleanup
}

# `all` with no args uses the default dirs for both builds.
test_all_default_dirs() {
  make_workdir
  mkdir -p "$WORK/sonic-mgmt-common" "$WORK/sonic-gnmi"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" all )
  assert_contains "$WORK/dpkg.log" "CWD=$WORK/sonic-mgmt-common" "all: default mgmt-common dir"
  assert_contains "$WORK/dpkg.log" "CWD=$WORK/sonic-gnmi" "all: default gnmi dir"
  cleanup
}

# ---------------------------------------------------------------------------
# error handling
# ---------------------------------------------------------------------------
# Unknown subcommand must exit non-zero and print usage.
test_unknown_subcommand() {
  make_workdir
  if ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" bogus >"$WORK/out.log" 2>&1 ); then
    fail "unknown subcommand: must exit non-zero"
  else
    pass "unknown subcommand: exits non-zero"
  fi
  assert_contains "$WORK/out.log" "Usage:" "unknown subcommand: prints usage"
  cleanup
}

# No subcommand at all must also fail with usage.
test_no_subcommand() {
  make_workdir
  if ( cd "$WORK" && PATH="$BIN:$PATH" sh "$BUILD_DEB" >"$WORK/out.log" 2>&1 ); then
    fail "no subcommand: must exit non-zero"
  else
    pass "no subcommand: exits non-zero"
  fi
  assert_contains "$WORK/out.log" "Usage:" "no subcommand: prints usage"
  cleanup
}

test_mgmt_common
test_mgmt_common_default_dir
test_gnmi_deb_env_and_jflag
test_gnmi_deb_default_glob
test_gnmi_deb_narrow_glob
test_gnmi_deb_creates_out_dir
test_gnmi_deb_no_outdir
test_all_chains_both_builds
test_all_default_dirs
test_unknown_subcommand
test_no_subcommand

echo "-------------------------------------"
echo "PASS: $PASS  FAIL: $FAIL"
[ "$FAIL" -eq 0 ]
