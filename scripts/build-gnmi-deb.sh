#!/bin/sh
# Build the sonic-gnmi .deb with translib + native write enabled — single source
# of truth for both ADO build-deb.yml and dev/run-tests.sh `build`.
#
# Uses `set -e` (fail fast) without `-x`: the original ADO step ran `set -ex`,
# but per-command echoing is intentionally omitted here to keep this shared
# script's logs uncluttered. Callers that want command tracing can invoke it via
# `sh -x` or `bash -x`.
set -e
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
