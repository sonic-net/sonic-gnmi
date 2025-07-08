#!/bin/bash

# Script to install the correct protoc version and regenerate proto files
set -e

SCRIPT_DIR="$(dirname "$0")"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Install protoc if needed
if ! command -v protoc &> /dev/null; then
    echo "Installing protoc v31.1 (latest stable)..."
    PROTOC_VERSION="31.1"
    PROTOC_ZIP="protoc-${PROTOC_VERSION}-linux-x86_64.zip"
    TEMP_DIR=$(mktemp -d)
    cd $TEMP_DIR

    # Download and install protoc
    curl -OL "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/${PROTOC_ZIP}"
    unzip -o ${PROTOC_ZIP} -d ./protoc
    echo "Installing protoc to $HOME/.local/bin (make sure this is in your PATH)"
    mkdir -p $HOME/.local/bin
    sudo cp ./protoc/bin/protoc $HOME/.local/bin/
    mkdir -p $HOME/.local/include
    sudo cp -r ./protoc/include/* $HOME/.local/include/

    # Clean up temp directory
    cd -
    rm -rf $TEMP_DIR
fi

# Change to the root directory
cd "$ROOT_DIR"

# Use the Makefile target to generate proto files
echo "Regenerating proto files using Makefile..."
make proto

echo "Proto files regenerated successfully!"
echo "Please verify the changes and commit them."
