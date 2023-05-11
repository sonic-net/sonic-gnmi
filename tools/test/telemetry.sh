#!/usr/bin/env bash
set -e

TOPDIR=$(git rev-parse --show-toplevel || echo ${PWD})
ARGV=()

for V in "$@"; do
    case "$V" in
    -port|--port|-port=*|--port=*) HAS_PORT=1 ;;
    -v|--v|-v=*|--v=*) HAS_V=1 ;;
    -*logto*|--*logto*|-log_dir*|--log_dir*) HAS_LOG=1;;
    -server_crt|--server_crt|-server_crt=*|--server_crt=*) HAS_CERT=1 ;;
    -server_key|--server_key|-server_key=*|--server_key=*) HAS_CERT=1 ;;
    -client_auth|--client_auth|-client_auth=*|--client_auth=*) HAS_AUTH=1 ;;
    esac
    ARGV+=( $V )
done

source ${TOPDIR}/tools/test/env.sh

TELEMETRY_BIN=${TOPDIR}/build/bin/telemetry
if [[ ! -f ${TELEMETRY_BIN} ]]; then
    >&2 echo "error: Telemetry server not compiled"
    >&2 echo "Please run 'make telemetry' and try again"
    exit 1
fi

EXTRA_ARGS=()
[[ -z ${HAS_PORT} ]] && EXTRA_ARGS+=( -port 8080 )
[[ -z ${HAS_LOG}  ]] && EXTRA_ARGS+=( -logtostderr )
[[ -z ${HAS_V} ]]    && EXTRA_ARGS+=( -v 2 )
[[ -z ${HAS_CERT} ]] && EXTRA_ARGS+=( -insecure )
[[ -z ${HAS_AUTH} ]] && EXTRA_ARGS+=( -client_auth none )

EXTRA_ARGS+=( -allow_no_client_auth )

set -x
${TELEMETRY_BIN} "${EXTRA_ARGS[@]}" "${ARGV[@]}"
