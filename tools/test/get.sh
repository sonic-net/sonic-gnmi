#!/usr/bin/env bash
set -e

function print_usage() {
cat <<EOM
usage: $(basename $0) [OPTIONS] PATH* [-- [gnmi_get args]]

OPTIONS:
  -host HOST        Server IP address (default 127.0.0.1)
  -port PORT        Server port (default 8080)
  -proto            Use PROTO encoding
  -user USER:PASS   Username and password for authentication
  -origin ORIGIN    Origin to be prefixed to subsequent paths

EOM
}

TOPDIR=$(git rev-parse --show-toplevel)
BINDIR=${TOPDIR}/build/bin
gnmi_get=$(realpath --relative-to ${PWD} ${BINDIR}/gnmi_get)

if [[ ! -f ${gnmi_get} ]]; then
    echo "error: gNMI tools are not compiled"
    echo "Please run 'make telemetry' and try again"
    exit 1
fi

HOST=localhost
PORT=8080
ARGS=()
PATHS=()
ORIGN=

while [[ $# -gt 0 ]]; do
    case "$1" in
    -h|-help|--help)
        print_usage
        exit 0;;
    -H|-host|--host)
        HOST=$2
        shift 2;;
    -p|-port|--port)
        PORT=$2
        shift 2;;
    -u|-user|--user)
        ARGS+=( -username "${2%%:*}" -password "${2#*:}" )
        shift 2;;
    -proto|--proto)
        ARGS+=( -encoding PROTO )
        shift;;
    -origin|--origin)
        ORIGN=$2
        shift 2;;
    [/_a-zA-Z]*)
        PATHS+=( -xpath "/${ORIGN}:${1#/}" )
        shift;;
    --)
        shift
        ARGS+=( "$@" )
        break;;
    *)
        echo "error: unknown option: $1"
        print_usage
        exit 1;;
    esac
done

ARGS+=( -insecure )
[[ "$@" =~ -(also)?log*  ]] || ARGS+=( -logtostderr )
[[ "$@" =~ -target_addr* ]] || ARGS+=( -target_addr ${HOST}:${PORT} )

set -x
${gnmi_get} "${ARGS[@]}" "${PATHS[@]}"
