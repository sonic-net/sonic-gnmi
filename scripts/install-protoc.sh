#!/bin/sh
# Install the protobuf compiler for [arch] — single source of truth for the ADO
# install-dependencies.yml step. The leading apt-get update runs on arm64 only.
set -e
arch="${1:-amd64}"
if [ "$arch" = "arm64" ]; then
  sudo apt-get update
fi
sudo apt-get install -y protobuf-compiler
protoc --version
