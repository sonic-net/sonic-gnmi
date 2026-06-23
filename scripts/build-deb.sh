#!/bin/sh
# Single build entry point for the SONiC .deb packages, shared by the ADO
# templates (build-deb.yml, setup-test-env.yml) and dev/run-tests.sh.
#   build-deb.sh mgmt-common [DIR]
#   build-deb.sh gnmi [DIR] [OUT_DIR] [COPY_GLOB]
set -ex

case "${1:-}" in
  mgmt-common)
    dir="${2:-sonic-mgmt-common}"
    ( cd "$dir" && NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc )
    ;;
  gnmi)
    dir="${2:-sonic-gnmi}"
    out="${3:-}"
    glob="${4:-*.deb}"
    ( cd "$dir" && ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y \
        dpkg-buildpackage -rfakeroot -b -us -uc -j"$(nproc)" )
    if [ -n "$out" ]; then
      mkdir -p "$out"
      # shellcheck disable=SC2086  # COPY_GLOB is an intentional glob
      cp "$(dirname "$dir")"/$glob "$out"/
    fi
    ;;
  *)
    echo "Usage: $0 {mgmt-common [DIR] | gnmi [DIR] [OUT_DIR] [COPY_GLOB]}" >&2
    exit 2
    ;;
esac
