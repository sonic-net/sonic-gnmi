#!/bin/bash
set -e

# Default values if environment variables are not set
: ${OPSD_ADDR:=":50051"}
: ${OPSD_SHUTDOWN_TIMEOUT:="10s"}

# Execute the sonic-ops-server binary with the configured parameters
exec /usr/local/bin/sonic-ops-server --addr="${OPSD_ADDR}" --shutdown-timeout="${OPSD_SHUTDOWN_TIMEOUT}" "$@"
