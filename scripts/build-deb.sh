#!/bin/sh
# Single build entry point for the SONiC .deb packages — the one source of truth
# for both the ADO templates (build-deb.yml, setup-test-env.yml) and
# dev/run-tests.sh.
#
# Subcommands:
#   build-deb.sh mgmt-common [DIR]                 # build sonic-mgmt-common
#   build-deb.sh gnmi [DIR] [OUT_DIR] [COPY_GLOB]  # build sonic-gnmi .deb
#   build-deb.sh all [MGMT_DIR] [GNMI_DIR] [OUT_DIR] [COPY_GLOB]
#
# Uses `set -e` (fail fast) without `-x`: per-command echoing is intentionally
# omitted to keep this shared script's logs uncluttered. Callers that want
# command tracing can invoke it via `sh -x` or `bash -x`.
set -e

usage() {
  cat >&2 <<EOF
Usage: $0 <subcommand> [args]

Subcommands:
  mgmt-common [DIR]                 Build sonic-mgmt-common (default DIR: sonic-mgmt-common)
  gnmi [DIR] [OUT_DIR] [COPY_GLOB]  Build sonic-gnmi .deb (default DIR: sonic-gnmi, COPY_GLOB: *.deb)
  all [MGMT_DIR] [GNMI_DIR] [OUT_DIR] [COPY_GLOB]
                                    Build mgmt-common then gnmi
EOF
}

# Build sonic-mgmt-common (generates YANG bindings + cvl schema).
_build_mgmt_common() {
  MGMT_COMMON_DIR="${1:-sonic-mgmt-common}"
  echo "--- build sonic-mgmt-common ($MGMT_COMMON_DIR) ---"
  ( cd "$MGMT_COMMON_DIR" && NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc )
}

# Build the sonic-gnmi .deb with translib + native write enabled.
_build_gnmi() {
  GNMI_DIR="${1:-sonic-gnmi}"
  OUT_DIR="${2:-}"               # if set, copy resulting .deb(s) here
  # Copy pattern is RELATIVE to the parent of GNMI_DIR. Defaults to '*.deb' to
  # faithfully reproduce the ADO 'cp ../*.deb' behavior (which stages the
  # sonic-mgmt-common debs alongside the sonic-gnmi deb). The dev caller passes
  # 'sonic-gnmi_*.deb' to keep its narrower out-dir contents unchanged.
  COPY_GLOB="${3:-*.deb}"
  echo "--- dpkg-buildpackage sonic-gnmi (translib + native write enabled) ---"
  ( cd "$GNMI_DIR" && ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y \
      dpkg-buildpackage -rfakeroot -b -us -uc -j"$(nproc)" )
  if [ -n "$OUT_DIR" ]; then
    mkdir -p "$OUT_DIR"
    # shellcheck disable=SC2086  # COPY_GLOB is an intentional glob
    cp -v "$(dirname "$GNMI_DIR")"/$COPY_GLOB "$OUT_DIR"/
  fi
}

SUBCOMMAND="${1:-}"
[ "$#" -gt 0 ] && shift

case "$SUBCOMMAND" in
  mgmt-common)
    _build_mgmt_common "$@"
    ;;
  gnmi)
    _build_gnmi "$@"
    ;;
  all)
    _build_mgmt_common "${1:-}"
    _build_gnmi "${2:-}" "${3:-}" "${4:-}"
    ;;
  *)
    echo "Error: unknown subcommand '${SUBCOMMAND}'" >&2
    usage
    exit 2
    ;;
esac
