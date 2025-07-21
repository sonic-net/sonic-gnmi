#!/bin/bash
set -e

# Default values if environment variables are not set
: ${GNMI_ADDR:=":50051"}
: ${GNMI_SHUTDOWN_TIMEOUT:="10s"}

# Execute the sonic-gnmi-standalone binary with the configured parameters
exec /usr/local/bin/sonic-gnmi-standalone -addr="${GNMI_ADDR}" -shutdown-timeout="${GNMI_SHUTDOWN_TIMEOUT}" "$@"
