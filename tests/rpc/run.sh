#!/usr/bin/env bash
# Native RPC integration: selected production service + injected data reader.
set -euo pipefail
repo=$(realpath "$(dirname "$0")/../..")
results="$repo/test-results/auth-rpc"
[[ -f /.dockerenv && ! -e "$results" ]]
mkdir -p "$results"
trap 'rc=$?; chmod -R a+rX "$results"; printf "%s\n" "$rc" > "$results/exit"' EXIT
cd "$repo"
export PATH="/usr/local/go/bin:$PATH"
source_sha=$(git -c safe.directory="$repo" rev-parse HEAD)
[[ -z "${BUILD_SOURCEVERSION:-}" || "$source_sha" == "$BUILD_SOURCEVERSION" ]]
{
    printf 'source_sha=%s\nsource_ref=%s\nbuild_id=%s\n' "$source_sha" "${BUILD_SOURCEBRANCH:-local}" "${BUILD_BUILDID:-local}"
    sha256sum tests/rpc/*.go tests/rpc/testutil/*.go gnmi_server/get_service.go gnmi_server/server.go
    go version
} > "$results/source.txt"
reporter="$repo/build/tools/bin/gotestsum-v1.13.0"
CGO_ENABLED=0 make "$reporter" > "$results/reporter.log" 2>&1
env GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory \
    GIT_CONFIG_VALUE_0="$(realpath ../sonic-mgmt-common)" tools/test/env.sh > "$results/native-env.txt"
export YANG_MODELS_PATH="$repo/build/test/yangs"
export CVL_SCHEMA_PATH="$repo/../sonic-mgmt-common/build/cvl/schema"
export CVL_CFG_FILE="$repo/build/test/cvl_cfg.json"
export CGO_ENABLED=1
export CGO_LDFLAGS='-lswsscommon -lhiredis'
export CGO_CXXFLAGS='-I/usr/include/swss -w -Wall -fpermissive'
timeout --signal=TERM --kill-after=15s 300 "$reporter" \
    --junitfile "$results/junit.xml" --jsonfile "$results/go-test.jsonl" -- \
    -mod=vendor -tags=e2e -count=1 -timeout=2m ./tests/rpc \
    -run '^TestCertificateReadonlyGet$' > "$results/test.log" 2>&1
jq -s -e '
  ([.[] | select(.Action == "run" and .Test != null) | .Test] | sort) ==
    ["TestCertificateReadonlyGet", "TestCertificateReadonlyGet/readonly"] and
  ([.[] | select(.Action == "pass" and .Test != null) | .Test] | sort) ==
    ["TestCertificateReadonlyGet", "TestCertificateReadonlyGet/readonly"] and
  ([.[] | select(.Action == "fail" or .Action == "skip")] | length == 0)
' "$results/go-test.jsonl" > /dev/null
printf 'AUTH_RPC_VERIFIED examples=1 cases=1 failures=0 skips=0 source_sha=%s\n' "$source_sha"
