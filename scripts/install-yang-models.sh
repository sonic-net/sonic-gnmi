#!/bin/sh
# Install the sonic_yang_models wheel(s) matching <wheel_glob>. Single source of
# truth for the ADO install-dependencies.yml step.
#
# PIP_FLAGS (default empty) is forwarded as an explicit argv token because sudo
# resets the environment.
set -ex
wheel_glob="$1"
sudo pip3 install ${PIP_FLAGS} $wheel_glob
