#!/bin/sh
# Shared dependency manifest — the single source of truth (SSOT) for SONiC
# dependency artifact names, versions, download globs, and the artifact mirror
# URL (G2). This file is SOURCED, never executed: it only declares variables and
# accessor functions and has no side effects, so a version bump touches exactly
# one line here and stays in sync across every consumer.
#
# Consumers:
#   - scripts/install-swsscommon.sh : deps_swsscommon_debs <arch>
#   - dev/run-tests.sh              : ARTIFACTS_URL + deps_bootstrap_targets
#   - scripts/test_deps_manifest.sh : guard test asserting the ADO
#                                     install-dependencies.yml `patterns:` globs
#                                     equal deps_download_globs.
#
# NOTE: the ADO install-dependencies.yml `patterns:` literals CANNOT source this
# file (compile-time YAML), so they stay literal in the YAML and are kept in sync
# by the guard test rather than templated from here.
#
# POSIX sh: no bashisms, no side effects on source.

# --- Versioned package basenames ---
LIBYANG3_VER=3.12.2-1
LIBNL_VER=3.7.0-0.2+b1sonic1
SWSSCOMMON_VER=1.0.0
YANG_MODELS_VER=1.0

# --- Artifact mirror base URL (append a target= path) ---
ARTIFACTS_URL='https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target='

# deps_swsscommon_debs <arch> — emit the swss-common deb basenames that
# install-swsscommon.sh installs for <arch>, one per line. python3-swsscommon is
# amd64-only.
deps_swsscommon_debs() {
  echo "libswsscommon_${SWSSCOMMON_VER}_${1}.deb"
  echo "libswsscommon-dev_${SWSSCOMMON_VER}_${1}.deb"
  if [ "$1" = "amd64" ]; then
    echo "python3-swsscommon_${SWSSCOMMON_VER}_${1}.deb"
  fi
}

# deps_bootstrap_targets — emit the artifact target paths the dev runner
# downloads from the mirror (DEB_TARGETS), one per line. The libnl `+` is
# URL-encoded as %2B for the mirror query; the dev runner decodes it for the
# on-disk filename. These are amd64-only (the dev runner is amd64).
deps_bootstrap_targets() {
  _libnl_enc=$(echo "$LIBNL_VER" | sed 's/+/%2B/g')
  echo "target/debs/trixie/libyang3_${LIBYANG3_VER}_amd64.deb"
  echo "target/debs/trixie/libyang-dev_${LIBYANG3_VER}_amd64.deb"
  echo "target/debs/trixie/libnl-3-200_${_libnl_enc}_amd64.deb"
  echo "target/debs/trixie/libnl-genl-3-200_${_libnl_enc}_amd64.deb"
  echo "target/debs/trixie/libnl-route-3-200_${_libnl_enc}_amd64.deb"
  echo "target/debs/trixie/libnl-nf-3-200_${_libnl_enc}_amd64.deb"
  echo "target/debs/trixie/libswsscommon_${SWSSCOMMON_VER}_amd64.deb"
  echo "target/debs/trixie/libswsscommon-dev_${SWSSCOMMON_VER}_amd64.deb"
  echo "target/debs/trixie/python3-swsscommon_${SWSSCOMMON_VER}_amd64.deb"
  echo "target/python-wheels/trixie/sonic_yang_models-${YANG_MODELS_VER}-py3-none-any.whl"
}

# deps_download_globs — emit the deb/wheel glob patterns the ADO
# install-dependencies.yml `patterns:` blocks must match, one per line. Includes
# the libpcre* family, which is download-only (pulled by ADO but never installed
# by install-debs.sh / install-swsscommon.sh) (OQ3), and the yang-models wheel
# glob.
deps_download_globs() {
  echo "target/debs/trixie/libyang3_*.deb"
  echo "target/debs/trixie/libyang-dev_3*.deb"
  echo "target/debs/trixie/libpcre3_*.deb"
  echo "target/debs/trixie/libpcre3-dev_*.deb"
  echo "target/debs/trixie/libpcre16-3_*.deb"
  echo "target/debs/trixie/libpcre32-3_*.deb"
  echo "target/debs/trixie/libpcrecpp0v5_*.deb"
  echo "target/debs/trixie/libnl-3-200_*.deb"
  echo "target/debs/trixie/libnl-genl-3-200_*.deb"
  echo "target/debs/trixie/libnl-route-3-200_*.deb"
  echo "target/debs/trixie/libnl-nf-3-200_*.deb"
  echo "target/python-wheels/trixie/sonic_yang_models*.whl"
}
