#!/usr/bin/env bash
# Execute sequentially inside the disposable amd64 Package job, never on a DUT.
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
python3 -m venv --system-site-packages "$scratch/venv"
python="$scratch/venv/bin/python"
"$python" -m pip install -r tests/e2e/requirements.txt > "$results/dependencies.log" 2>&1
"$python" tests/e2e/bootstrap.py "$scratch/generated" \
    --gnoi-protos vendor/github.com/openconfig/gnoi > "$results/bootstrap.log" 2>&1
"$python" -m pip freeze --all > "$results/python-packages.txt"
digest=$("$python" tests/e2e/package.py "$staging" "$scratch/package" "$results")
# Reuse the production build's YANG preparation, not a server/backend substitute.
env GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory \
    GIT_CONFIG_VALUE_0="$(realpath ../sonic-mgmt-common)" tools/test/env.sh > "$results/native-env.txt"
export YANG_MODELS_PATH="$repo/build/test/yangs"
export CVL_SCHEMA_PATH="$repo/../sonic-mgmt-common/build/cvl/schema"
export CVL_CFG_FILE="$repo/build/test/cvl_cfg.json"
export PYTHONPATH="$scratch/generated" PYTHONDONTWRITEBYTECODE=1 PYTEST_DISABLE_PLUGIN_AUTOLOAD=1
set +e
timeout --signal=TERM --kill-after=15s 600 "$python" -m pytest \
    -c tests/e2e/pytest.ini --confcutdir=tests/e2e -p no:cacheprovider tests/e2e \
    --telemetry="$scratch/package/usr/sbin/telemetry" --telemetry-sha256="$digest" \
    --evidence-dir="$results" --junitxml="$results/junit.xml" > "$results/pytest.log" 2>&1
rc=$?
set -e
"$python" tests/e2e/verify.py "$results"
exit "$rc"
