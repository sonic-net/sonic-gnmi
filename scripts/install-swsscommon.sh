#!/bin/sh
# Install sonic-swss-common debs for <arch> from [deb_dir]. Single source of
# truth for the ADO install-dependencies.yml step.
#
# deb_dir defaults to `.` because the ADO step sets workingDirectory and dpkg -i
# uses bare filenames. The python3-swsscommon package is amd64-only.
set -e
arch="$1"
deb_dir="${2:-.}"
sudo dpkg -i ${deb_dir}/libswsscommon_1.0.0_${arch}.deb
sudo dpkg -i ${deb_dir}/libswsscommon-dev_1.0.0_${arch}.deb
if [ "$arch" = "amd64" ]; then
  sudo dpkg -i ${deb_dir}/python3-swsscommon_1.0.0_${arch}.deb
fi
