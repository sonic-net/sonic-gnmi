#!/bin/sh
# Install the Go toolchain into /usr/local/go. Single source of truth for the
# ADO install-go.yml template and dev callers.
set -e
version="${1:-1.24.4}"
arch="${2:-amd64}"
wget -q https://go.dev/dl/go${version}.linux-${arch}.tar.gz
sudo tar -C /usr/local -xzf go${version}.linux-${arch}.tar.gz
# export only affects this script's shell; ADO callers re-export PATH.
export PATH=$PATH:/usr/local/go/bin
go version
