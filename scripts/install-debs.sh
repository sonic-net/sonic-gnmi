#!/bin/sh
# Install libyang + libnl debs from <deb_dir>. Single source of truth for the
# ADO install-dependencies.yml step.
#
# FIX_DEPS (default off; dev only) appends a dependency-fixup fallback. ADO never
# sets it, so the trace stays verbatim.
set -ex
deb_dir="$1"
sudo apt-get -y purge libnl-3-dev libnl-route-3-dev || true
if [ -n "${FIX_DEPS:-}" ]; then
  sudo dpkg -i $(find "$deb_dir" -name '*.deb') || sudo apt-get install -f -y
else
  sudo dpkg -i $(find "$deb_dir" -name '*.deb')
fi
