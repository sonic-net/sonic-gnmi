#!/bin/bash
set -e

# Default values if environment variables are not set
: ${MOPD_ADDR:=":50051"}
: ${MOPD_SHUTDOWN_TIMEOUT:="10s"}

# Execute the mopd binary with the configured parameters
exec /usr/local/bin/mopd --addr="${MOPD_ADDR}" --shutdown-timeout="${MOPD_SHUTDOWN_TIMEOUT}" "$@"
