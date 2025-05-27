#!/bin/bash
set -e

# Execute the sonic-gnmi-standalone binary with all provided arguments
exec /usr/local/bin/sonic-gnmi-standalone "$@"
