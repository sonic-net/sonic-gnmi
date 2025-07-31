#!/bin/bash
#
# Build and run sonic-gnmi-standalone server directly (no container)
# 
# This script is for TESTING ONLY - not for production use.
# It builds the server binary and runs it directly on the host machine.
#
# Examples:
#   ./build_run_baremetal_testonly.sh                    # Run on default port :50055
#   ./build_run_baremetal_testonly.sh -a :8080           # Run on custom port
#   ./build_run_baremetal_testonly.sh --enable-tls       # Enable TLS
#   ./build_run_baremetal_testonly.sh --rootfs /mnt/sonic # Custom rootfs path
#
# The server runs in foreground - press Ctrl+C to stop.

set -e

# Parse command line arguments
SERVER_ADDR=":50055"
NO_TLS="true"
ROOTFS=""

usage() {
  echo "Usage: $0 [-a <address>] [--enable-tls] [--rootfs <path>]"
  echo "  -a <address>     Server address to listen on (default: :50055)"
  echo "  --enable-tls     Enable TLS for secure connections (default: disabled)"
  echo "  --rootfs <path>  Root filesystem path for file operations (default: /)"
  echo "  -h, --help       Show this help message"
  exit 1
}

# Parse arguments (including long options)
while [[ $# -gt 0 ]]; do
  case $1 in
    -a)
      SERVER_ADDR="$2"
      shift 2
      ;;
    --enable-tls)
      NO_TLS="false"
      shift
      ;;
    --rootfs)
      ROOTFS="$2"
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "Unknown option: $1"
      usage
      ;;
  esac
done

# Directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
ROOT_DIR="$( cd "$SCRIPT_DIR/.." && pwd )"

# Build the binary
cd "$ROOT_DIR"
echo "Building sonic-gnmi-standalone binary..."
make build

# Check if binary was built successfully
BINARY_PATH="$ROOT_DIR/bin/sonic-gnmi-standalone"
if [ ! -f "$BINARY_PATH" ]; then
  echo "Failed to build sonic-gnmi-standalone binary"
  exit 1
fi

echo "Binary built successfully: $BINARY_PATH"

# Build the command arguments
CMD_ARGS="--addr=$SERVER_ADDR"
if [ "$NO_TLS" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --no-tls"
fi
if [ -n "$ROOTFS" ]; then
  CMD_ARGS="$CMD_ARGS --rootfs=$ROOTFS"
fi

echo ""
echo "Starting sonic-gnmi-standalone server..."
echo "  Server address: $SERVER_ADDR"
echo "  TLS enabled: $([ "$NO_TLS" = "true" ] && echo "No" || echo "Yes")"
if [ -n "$ROOTFS" ]; then
  echo "  Root filesystem: $ROOTFS"
fi
echo ""
echo "Command: $BINARY_PATH $CMD_ARGS"
echo ""
echo "Press Ctrl+C to stop the server"
echo "========================================="

# Run the binary directly
exec "$BINARY_PATH" $CMD_ARGS