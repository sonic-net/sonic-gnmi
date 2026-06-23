#!/bin/sh
# Install python test deps (pytest, jsonpatch) and refresh the apt index —
# single source of truth for the ADO install-dependencies.yml test branch.
#
# Optional env PIP_FLAGS (default empty) is forwarded as an explicit argv token
# to `sudo pip3 install` because sudo resets the environment by default.
set -e
sudo pip3 install ${PIP_FLAGS} -U pytest
sudo pip3 install ${PIP_FLAGS} -U jsonpatch
sudo apt-get update
