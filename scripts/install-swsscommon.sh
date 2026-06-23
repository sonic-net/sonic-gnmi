#!/bin/sh
# Install sonic-swss-common debs for <arch> from [deb_dir]. Single source of
# truth for the ADO install-dependencies.yml step.
#
# deb_dir defaults to `.` because the ADO step sets workingDirectory and dpkg -i
# uses bare filenames. The python3-swsscommon package is amd64-only.
#
# The filenames (incl. the swss-common version) come from the shared manifest
# (scripts/deps-manifest.sh) so a version bump touches one line there (G2).
set -e
arch="$1"
deb_dir="${2:-.}"
. "$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)/deps-manifest.sh"
for deb in $(deps_swsscommon_debs "$arch"); do
  sudo dpkg -i "${deb_dir}/${deb}"
done
