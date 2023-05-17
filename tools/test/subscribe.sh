#!/usr/bin/env bash
set -e

function print_usage() {
echo "usage: $(basename $0) [OPTIONS] [MODE] PATH*"
echo ""
echo "MODE (one of):"
echo "  -onchange         ON_CHANGE subscription"
echo "  -sample SECS      SAMPLE subscription with sample interval seconds"
echo "  -target-defined   TARGET_DEFINED subscription"
echo "  -once             ONCE subscription (default mode)"
echo "  -poll SECS        POLL subscription with poll interval seconds"
echo ""
echo "OPTIONS:"
echo "  -host HOST        Server IP address (default 127.0.0.1)"
echo "  -port PORT        Server port (default 8080)"
echo "  -user NAME:PASS   Username and password for authentication"
echo "  -proto            Request PROTO encoded notifications"
echo "  -brief            Display compact output -- {path, value} lines"
echo "  -heartbeat SECS   Set heartbeat_interval value in seconds"
echo "  -noredundant      Set suppress_redundant flag"
echo ""
}

TOPDIR=$(git rev-parse --show-toplevel || echo ${PWD})
BINDIR=${TOPDIR}/build/bin
GNMCLI=$(realpath --relative-to ${PWD} ${BINDIR}/gnmi_cli)

if [[ ! -f ${GNMCLI} ]]; then
    >&2 echo "error: gNMI tools were not compiled"
    >&2 echo "Please run 'make telemetry' and try again"
    exit 1
fi

HOST=localhost
PORT=8080
ARGS=()
PATHS=()
DISP=proto

while [[ $# -gt 0 ]]; do
    case "$1" in
    -h|-help|--help)
        print_usage
        exit 0;;
    -once)
        ARGS+=( -query_type once )
        shift;;
    -onchange|-on-change|-on_change)
        ARGS+=( -query_type streaming )
        ARGS+=( -streaming_type ON_CHANGE )
        shift;;
    -sample)
        ARGS+=( -query_type streaming )
        ARGS+=( -streaming_type SAMPLE )
        ARGS+=( -streaming_sample_interval $2 )
        shift 2;;
    -td|-target-defined|-target_defined)
        ARGS+=( -query_type streaming )
        ARGS+=( -streaming_type TARGET_DEFINED )
        shift;;
    -poll)
        ARGS+=( -query_type polling )
        ARGS+=( -polling_interval $2s )
        shift 2;;
    -pass)
        ARGS+=( -with_user_pass )
        shift;;
    -proto)
        ARGS+=( -encoding PROTO )
        shift;;
    -heartbeat|-heartbeat_interval)
        ARGS+=( -heartbeat_interval $2 )
        shift 2;;
    -noredundant|-suppress_redundant)
        ARGS+=( -suppress_redundant )
        shift;;
    -H|-host)
        HOST=$2
        shift 2;;
    -p|-port)
        PORT=$2
        shift 2;;
    -u|-user)
        ARGS+=( -username "${2%%:*}" -password "${2#*:}" )
        shift 2;;
    -brief)
        DISP=single
        shift;;
    [/_a-zA-Z]*)
        PATHS+=( "$1" )
        shift;;
    *)
        echo "error: unknown option: $1"
        print_usage
        exit 1;;
    esac
done

if [[ -z ${PATHS} ]]; then
    echo "error: At least one path required"
    exit 1
fi

ARGS+=( -insecure )
ARGS+=( -logtostderr )
ARGS+=( -address ${HOST}:${PORT} )
ARGS+=( -display_type ${DISP} )
ARGS+=( -target OC_YANG )
ARGS+=( -timestamp on )
ARGS+=( -query $(IFS=,; echo "${PATHS[*]}") )

set -x
${GNMCLI} "${ARGS[@]}"
