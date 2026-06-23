#!/bin/sh
# Build SONiC .deb packages (ADO templates + dev/run-tests.sh): mgmt-common [DIR] | gnmi [DIR] [OUT_DIR] [GLOB]
set -ex
case "$1" in
  mgmt-common) cd "${2:-sonic-mgmt-common}" && NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc ;;
  gnmi) cd "${2:-sonic-gnmi}" && ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y dpkg-buildpackage -rfakeroot -b -us -uc -j"$(nproc)" && { [ -z "$3" ] || cp ../${4:-*.deb} "$3"/; } ;;
esac
