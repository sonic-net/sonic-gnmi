#!/bin/sh
# Install the sonic_yang_models wheel(s) matching <wheel_glob> — single source of
# truth for the ADO install-dependencies.yml step.
#
# Optional env PIP_FLAGS (default empty) is forwarded as an explicit argv token
# to `sudo pip3 install` because sudo resets the environment by default.
set -e
wheel_glob="$1"
sudo pip3 install ${PIP_FLAGS} $wheel_glob
