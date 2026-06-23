#!/bin/sh
# Install python test deps (pytest, jsonpatch) and refresh the apt index.
# Single source of truth for the ADO install-dependencies.yml test branch.
#
# PIP_FLAGS (default empty) is forwarded as an explicit argv token because sudo
# resets the environment.
set -e
sudo pip3 install ${PIP_FLAGS} -U pytest
sudo pip3 install ${PIP_FLAGS} -U jsonpatch
sudo apt-get update
