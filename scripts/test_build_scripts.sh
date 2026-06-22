#!/bin/sh
# Functional tests for scripts/build-mgmt-common.sh and scripts/build-gnmi-deb.sh.
#
# These tests stub `dpkg-buildpackage` and `nproc` on PATH so no real package
# build runs. The stubs record the environment, working directory, and argv they
# were invoked with so the tests can assert that each build script reproduces the
# original inlined commands exactly:
#   - build-mgmt-common.sh : NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc
#   - build-gnmi-deb.sh    : ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y
#                            dpkg-buildpackage -rfakeroot -b -us -uc -j<nproc>
# plus the OUT_DIR / COPY_GLOB copy semantics.
#
# Run: sh scripts/test_build_scripts.sh
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
# build-mgmt-common.sh
# ---------------------------------------------------------------------------
test_mgmt_common() {
  make_workdir
  mkdir -p "$WORK/sonic-mgmt-common"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/build-mgmt-common.sh" sonic-mgmt-common )

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
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/build-mgmt-common.sh" )
  assert_contains "$WORK/dpkg.log" "CWD=$WORK/sonic-mgmt-common" "mgmt-common: default dir is sonic-mgmt-common"
  cleanup
}

# ---------------------------------------------------------------------------
# build-gnmi-deb.sh
# ---------------------------------------------------------------------------
test_gnmi_deb_env_and_jflag() {
  make_workdir
  mkdir -p "$WORK/sonic-gnmi"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/build-gnmi-deb.sh" sonic-gnmi )

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
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/build-gnmi-deb.sh" sonic-gnmi out )

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
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/build-gnmi-deb.sh" sonic-gnmi out 'sonic-gnmi_*.deb' )

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
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/build-gnmi-deb.sh" sonic-gnmi newout 'sonic-gnmi_*.deb' )
  assert_file_exists "$WORK/newout/sonic-gnmi_1.0_amd64.deb" "gnmi: missing OUT_DIR is created"
  cleanup
}

# No OUT_DIR => build only, no copy.
test_gnmi_deb_no_outdir() {
  make_workdir
  mkdir -p "$WORK/sonic-gnmi"
  : > "$WORK/sonic-gnmi_1.0_amd64.deb"
  ( cd "$WORK" && PATH="$BIN:$PATH" sh "$SCRIPT_DIR/build-gnmi-deb.sh" sonic-gnmi )
  assert_file_exists "$WORK/dpkg.log" "gnmi: builds even when OUT_DIR omitted"
  cleanup
}

test_mgmt_common
test_mgmt_common_default_dir
test_gnmi_deb_env_and_jflag
test_gnmi_deb_default_glob
test_gnmi_deb_narrow_glob
test_gnmi_deb_creates_out_dir
test_gnmi_deb_no_outdir

echo "-------------------------------------"
echo "PASS: $PASS  FAIL: $FAIL"
[ "$FAIL" -eq 0 ]
