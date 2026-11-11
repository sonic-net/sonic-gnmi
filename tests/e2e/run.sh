#!/usr/bin/env bash
# Run sequentially in the disposable amd64 package container, never on a DUT.
set -euo pipefail
repo=$(realpath "$(dirname "$0")/../..")
staging=$(realpath "${1:?package staging directory}")
results="$repo/test-results/auth-e2e"
[[ -f /.dockerenv && ! -e "$results" ]]
mkdir -p "$results"
scratch=$(mktemp -d)
cleanup() {
    rc=$?
    trap - EXIT
    rm -rf -- "$scratch"
    chmod -R a+rX "$results"
    printf '%s\n' "$rc" > "$results/exit"
    exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
cd "$repo"
export PATH="/usr/local/go/bin:$PATH"
source_sha=$(git -c safe.directory="$repo" rev-parse HEAD)
[[ -z "${BUILD_SOURCEVERSION:-}" || "$source_sha" == "$BUILD_SOURCEVERSION" ]]
packages=()
for package in "$staging"/sonic-gnmi_*.deb; do
    [[ -f "$package" ]] || continue
    if [[ $(dpkg-deb -f "$package" Package) == sonic-gnmi && $(dpkg-deb -f "$package" Architecture) == amd64 ]]; then
        packages+=("$package")
    fi
done
[[ ${#packages[@]} == 1 ]]
dpkg-deb -x "${packages[0]}" "$scratch/package"
export TELEMETRY_BIN="$scratch/package/usr/sbin/telemetry"
[[ -f "$TELEMETRY_BIN" && ! -L "$TELEMETRY_BIN" ]]
TELEMETRY_SHA256=$(sha256sum "$TELEMETRY_BIN" | cut -d' ' -f1)
export TELEMETRY_SHA256
{
    printf 'source_sha=%s\nsource_ref=%s\nbuild_id=%s\n' "$source_sha" "${BUILD_SOURCEBRANCH:-local}" "${BUILD_BUILDID:-local}"
    dpkg-deb -f "${packages[0]}" Package Version Architecture
    sha256sum "${packages[0]}" "$TELEMETRY_BIN" tests/e2e/auth_test.go
    go version
    ldd "$TELEMETRY_BIN"
} > "$results/artifact.txt"
# Use the existing repository test reporter; it converts real Go test events to JUnit.
reporter="$repo/build/tools/bin/gotestsum-v1.13.0"
CGO_ENABLED=0 make "$reporter" > "$results/reporter.log" 2>&1
env GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory \
    GIT_CONFIG_VALUE_0="$(realpath ../sonic-mgmt-common)" tools/test/env.sh > "$results/native-env.txt"
export YANG_MODELS_PATH="$repo/build/test/yangs"
export CVL_SCHEMA_PATH="$repo/../sonic-mgmt-common/build/cvl/schema"
export CVL_CFG_FILE="$repo/build/test/cvl_cfg.json"
export CGO_ENABLED=0
timeout --signal=TERM --kill-after=15s 180 "$reporter" \
    --junitfile "$results/junit.xml" --jsonfile "$results/go-test.jsonl" -- \
    -mod=vendor -tags=e2e -count=1 -timeout=2m ./tests/e2e \
    -run '^TestCertificateReadonlyGet$' > "$results/test.log" 2>&1
jq -s -e '
  ([.[] | select(.Action == "run" and .Test != null) | .Test] == ["TestCertificateReadonlyGet"]) and
  ([.[] | select(.Action == "pass" and .Test != null) | .Test] == ["TestCertificateReadonlyGet"]) and
  ([.[] | select(.Action == "fail" or .Action == "skip")] | length == 0)
' "$results/go-test.jsonl" > /dev/null
printf 'AUTH_ARTIFACT_E2E_VERIFIED tests=1 failures=0 skips=0 source_sha=%s telemetry_sha256=%s\n' "$source_sha" "$TELEMETRY_SHA256"
