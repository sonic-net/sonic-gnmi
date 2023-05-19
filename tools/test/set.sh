#!/usr/bin/env bash
set -e

function print_usage() {
cat <<EOM
usage: $(basename $0) [OPTIONS] OPERATION* [-- [gnmi_set args]]

OPTIONS:
  -host HOST          Server IP address (default 127.0.0.1)
  -port PORT          Server port (default 8080)
  -user USER:PASS     Username and password for authentication
  -origin ORIGIN      Origin to be prefixed to subsequent paths

OPERATION: (can be repeated)
  -delete  PATH       Delete path
  -update  PATH JSON  Update path and json value
  -replace PATH JSON  Replace path and json value

EOM
}

TOPDIR=$(git rev-parse --show-toplevel)
BINDIR=${TOPDIR}/build/bin
gnmi_set=$(realpath --relative-to ${PWD} ${BINDIR}/gnmi_set)

if [[ ! -f ${gnmi_set} ]]; then
    echo "error: gNMI tools are not compiled"
    echo "Please run 'make telemetry' and try again"
    exit 1
fi

HOST=localhost
PORT=8080
ARGS=()
ORIGIN=

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
    -origin|--origin)
        ORIGIN=$2
        shift 2;;
    -D|-delete|--delete)
        ARGS+=( -delete "/${ORIGIN}:${2#/}" )
        shift 2;;
    -U|-update|--update)
        F=$(mktemp -t 'u_XXXXX.json')
        echo "$3" > $F
        ARGS+=( -update "/${ORIGIN}:${2#/}:@$F" )
        shift 3;;
    -R|-replace|--replace)
        F=$(mktemp -t 'r_XXXXX.json')
        echo "$3" > $F
        ARGS+=( -replace "/${ORIGIN}:${2#/}:@$F" )
        shift 3;;
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
${gnmi_set} "${ARGS[@]}"
