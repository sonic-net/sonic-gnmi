#!/bin/sh
# Guard test for the shared dependency manifest (scripts/deps-manifest.sh, G2).
#
# The ADO install-dependencies.yml `patterns:` literals are render-time YAML and
# CANNOT source a shell file, so they stay literal in the YAML. To prevent the
# manifest and those literals from drifting, this test:
#   1. parses every `patterns:` block out of install-dependencies.yml,
#   2. sources the manifest and reads `deps_download_globs`,
#   3. asserts the two sets are equal (incl. the download-only libpcre* family
#      and the yang-models wheel glob — OQ3),
#   4. asserts the swss-common filenames the manifest emits carry SWSSCOMMON_VER
#      and that the dev-runner bootstrap target list matches the manifest.
#
# Run: sh scripts/test_deps_manifest.sh
set -e

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH='' cd -- "$SCRIPT_DIR/.." && pwd)
MANIFEST="$SCRIPT_DIR/deps-manifest.sh"
YAML="$REPO_ROOT/.azure/templates/install-dependencies.yml"

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL + 1)); echo "FAIL - $1"; }

cleanup() { [ -n "${WORK:-}" ] && rm -rf "$WORK"; }
trap cleanup EXIT
WORK=$(mktemp -d)

# Extract the de-indented contents of every `patterns: |` block in the YAML.
# A block ends at the first line indented no deeper than the `patterns:` key.
extract_yaml_patterns() {
  awk '
    {
      if ($0 ~ /patterns:[ \t]*\|[ \t]*$/) {
        match($0, /^ */); keyindent = RLENGTH; inblock = 1; next
      }
      if (inblock) {
        if ($0 ~ /^[ \t]*$/) { next }
        match($0, /^ */); indent = RLENGTH
        if (indent <= keyindent) { inblock = 0; next }
        line = $0
        sub(/^[ \t]+/, "", line)
        sub(/[ \t]+$/, "", line)
        print line
      }
    }
  ' "$YAML"
}

# --- 1. patterns: globs == deps_download_globs ------------------------------
. "$MANIFEST"

extract_yaml_patterns | LC_ALL=C sort > "$WORK/yaml_globs.txt"
deps_download_globs | LC_ALL=C sort > "$WORK/manifest_globs.txt"

if diff -u "$WORK/yaml_globs.txt" "$WORK/manifest_globs.txt" > "$WORK/globs.diff"; then
  pass "ADO patterns: globs are set-equal to deps_download_globs"
else
  fail "ADO patterns: globs differ from deps_download_globs"
  sed 's/^/      /' "$WORK/globs.diff"
fi

# libpcre* is download-only: present in the globs but never installed, so it must
# appear in deps_download_globs yet be absent from deps_bootstrap_targets (OQ3).
if grep -q '^target/debs/trixie/libpcre' "$WORK/manifest_globs.txt"; then
  pass "manifest globs include the download-only libpcre* family"
else
  fail "manifest globs include the download-only libpcre* family"
fi
if deps_bootstrap_targets | grep -q 'libpcre'; then
  fail "deps_bootstrap_targets must NOT download libpcre* (download-only)"
else
  pass "deps_bootstrap_targets omits the download-only libpcre* family"
fi

# --- 2. swss-common filenames carry SWSSCOMMON_VER --------------------------
swss_amd64=$(deps_swsscommon_debs amd64)
for f in libswsscommon libswsscommon-dev python3-swsscommon; do
  if echo "$swss_amd64" | grep -qx "${f}_${SWSSCOMMON_VER}_amd64.deb"; then
    pass "deps_swsscommon_debs amd64 emits ${f}_${SWSSCOMMON_VER}_amd64.deb"
  else
    fail "deps_swsscommon_debs amd64 emits ${f}_${SWSSCOMMON_VER}_amd64.deb"
  fi
done

# arm64 has no python3-swsscommon package.
swss_arm64=$(deps_swsscommon_debs arm64)
if echo "$swss_arm64" | grep -q 'python3-swsscommon'; then
  fail "deps_swsscommon_debs arm64 omits python3-swsscommon"
else
  pass "deps_swsscommon_debs arm64 omits python3-swsscommon"
fi

# --- 3. install-swsscommon.sh installs exactly the manifest filenames -------
# Stub sudo + dpkg so install-swsscommon.sh records the basenames it installs,
# then assert they equal deps_swsscommon_debs (proves the script is wired to the
# manifest, not to its own literals).
BIN="$WORK/bin"
mkdir -p "$BIN"
cat > "$BIN/sudo" <<'EOF'
#!/bin/sh
exec "$@"
EOF
cat > "$BIN/dpkg" <<EOF
#!/bin/sh
for a in "\$@"; do
  case "\$a" in *.deb) basename "\$a" >> "$WORK/installed.txt" ;; esac
done
EOF
chmod +x "$BIN/sudo" "$BIN/dpkg"

PATH="$BIN:$PATH" sh "$SCRIPT_DIR/install-swsscommon.sh" amd64 /debs
LC_ALL=C sort "$WORK/installed.txt" > "$WORK/installed_sorted.txt"
deps_swsscommon_debs amd64 | LC_ALL=C sort > "$WORK/expected_swss.txt"
if diff -u "$WORK/expected_swss.txt" "$WORK/installed_sorted.txt" > "$WORK/swss.diff"; then
  pass "install-swsscommon.sh amd64 installs exactly deps_swsscommon_debs"
else
  fail "install-swsscommon.sh amd64 installs exactly deps_swsscommon_debs"
  sed 's/^/      /' "$WORK/swss.diff"
fi

# --- 4. bootstrap targets carry the versioned swss + yang basenames ---------
targets=$(deps_bootstrap_targets)
for needle in \
  "libyang3_${LIBYANG3_VER}_amd64.deb" \
  "libswsscommon_${SWSSCOMMON_VER}_amd64.deb" \
  "python3-swsscommon_${SWSSCOMMON_VER}_amd64.deb" \
  "sonic_yang_models-${YANG_MODELS_VER}-py3-none-any.whl"; do
  if echo "$targets" | grep -qF "$needle"; then
    pass "deps_bootstrap_targets includes $needle"
  else
    fail "deps_bootstrap_targets includes $needle"
  fi
done

# libnl `+` must be URL-encoded as %2B in the mirror target path.
if echo "$targets" | grep -qF "libnl-3-200_3.7.0-0.2%2Bb1sonic1_amd64.deb"; then
  pass "deps_bootstrap_targets URL-encodes the libnl + as %2B"
else
  fail "deps_bootstrap_targets URL-encodes the libnl + as %2B"
fi

# --- 5. ARTIFACTS_URL exported by manifest ----------------------------------
if [ -n "${ARTIFACTS_URL:-}" ]; then
  pass "manifest exports ARTIFACTS_URL"
else
  fail "manifest exports ARTIFACTS_URL"
fi

echo "-------------------------------------"
echo "PASS: $PASS  FAIL: $FAIL"
[ "$FAIL" -eq 0 ]
