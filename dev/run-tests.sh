#!/usr/bin/env bash
# sonic-gnmi dev driver — runs everything in docker so the checkout stays clean.
#
# This script lives INSIDE the sonic-gnmi checkout at sonic-gnmi/dev/.
# The checkout itself is the parent of this script's directory.
#
# Layout on disk:
#   sonic-gnmi/                    (the checkout — parent of this dev/ dir)
#   sonic-gnmi/dev/                (this driver + setup.sh + SETUP.md)
#   sonic-gnmi/dev/build-out/      (built .deb lands here)
#   $CACHE_DIR/sonic-mgmt-common/  (go.mod replace target, cloned by bootstrap)
#   $CACHE_DIR/sonic-swss-common/  (CGO header source, cloned by bootstrap)
#   $CACHE_DIR/sonic-debs/         (libyang/libnl/swsscommon debs + yang wheel)
#
# The heavy deps live in $CACHE_DIR (default ~/.cache/acr-image-build) and are
# shared across projects, so the checkout only holds code you edit + this dev/.
#
# Inside the container the bind mounts re-create the sibling layout the build
# expects (sonic-gnmi/ next to sonic-mgmt-common/ + sonic-swss-common/),
# satisfying the `replace github.com/Azure/sonic-mgmt-common => ../sonic-mgmt-common`
# directive in go.mod and the `-I../../sonic-swss-common/common` CGO flag.
#
# Targets:
#   bootstrap    - clone sibling repos and download deb/wheel artifacts (idempotent)
#   pure         - pure-package unit tests (no SONiC deps, runs in container)
#   integration  - full integration tests inside sonic-slave-trixie container
#   build        - dpkg-buildpackage sonic-gnmi.deb into dev/build-out/
#   shell        - drop into container with deps installed
#   all          - bootstrap + pure + integration
#   clean        - wipe the cache dir

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GNMI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"          # the sonic-gnmi checkout
BUILD_OUT="$SCRIPT_DIR/build-out"
CACHE_DIR="${ACR_IMAGE_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/acr-image-build}"
MGMT_COMMON_DIR="$CACHE_DIR/sonic-mgmt-common"
SWSS_COMMON_DIR="$CACHE_DIR/sonic-swss-common"
DEBS_DIR="$CACHE_DIR/sonic-debs"
IMAGE="sonicdev-microsoft.azurecr.io:443/sonic-slave-trixie:latest"

ARTIFACTS_URL='https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target='

# NOTE: the public mirror only keeps the CURRENT build's artifacts, so these
# filenames go stale over time. If bootstrap 404s, find the new version in
# rules/<pkg>.mk in sonic-net/sonic-buildimage@master and update here.
DEB_TARGETS=(
  target/debs/trixie/libyang3_3.12.2-1_amd64.deb
  target/debs/trixie/libyang-dev_3.12.2-1_amd64.deb
  'target/debs/trixie/libnl-3-200_3.7.0-0.2%2Bb1sonic1_amd64.deb'
  'target/debs/trixie/libnl-genl-3-200_3.7.0-0.2%2Bb1sonic1_amd64.deb'
  'target/debs/trixie/libnl-route-3-200_3.7.0-0.2%2Bb1sonic1_amd64.deb'
  'target/debs/trixie/libnl-nf-3-200_3.7.0-0.2%2Bb1sonic1_amd64.deb'
  target/debs/trixie/libswsscommon_1.0.0_amd64.deb
  target/debs/trixie/libswsscommon-dev_1.0.0_amd64.deb
  target/debs/trixie/python3-swsscommon_1.0.0_amd64.deb
  target/python-wheels/trixie/sonic_yang_models-1.0-py3-none-any.whl
)

ensure_sibling_repo() {
  local dir="$1" url="$2"
  if [[ -d "$dir/.git" ]]; then
    echo "[ok] $dir already cloned"
  else
    echo "[get] cloning $url"
    git clone --depth 1 "$url" "$dir"
  fi
}

bootstrap() {
  mkdir -p "$CACHE_DIR" "$DEBS_DIR"
  ensure_sibling_repo "$MGMT_COMMON_DIR" https://github.com/sonic-net/sonic-mgmt-common.git
  ensure_sibling_repo "$SWSS_COMMON_DIR" https://github.com/sonic-net/sonic-swss-common.git
  for tgt in "${DEB_TARGETS[@]}"; do
    local out="$DEBS_DIR/$(basename "${tgt//%2B/+}")"
    if [[ -s "$out" ]]; then
      continue
    fi
    echo "[get] $out"
    curl -fsSL -o "$out" "${ARTIFACTS_URL}${tgt}"
  done
  # Drop any zero-byte file a transient failure may have left, so a re-run refetches.
  find "$DEBS_DIR" -type f -empty -delete 2>/dev/null || true
  echo "[ok] cache ready at $CACHE_DIR"
}

require_cache() {
  if [[ ! -d "$MGMT_COMMON_DIR" || ! -d "$SWSS_COMMON_DIR" || -z "$(ls -A "$DEBS_DIR" 2>/dev/null || true)" ]]; then
    echo "cache missing — running bootstrap" >&2
    bootstrap
  fi
}

# Shell snippet executed inside the container before any build/test.
container_setup_snippet() {
  cat <<'EOF'
set -euo pipefail
cd /work
sudo apt-get update >/dev/null
sudo apt-get -y purge libnl-3-dev libnl-route-3-dev 2>/dev/null || true
sudo dpkg -i /sonic-debs/*.deb || sudo apt-get install -f -y
sudo pip3 install --break-system-packages /sonic-debs/sonic_yang_models-*.whl jsonpatch
sudo apt-get install -y --no-install-recommends redis-server
sudo sed -ri 's/^# unixsocket/unixsocket/' /etc/redis/redis.conf
sudo sed -ri 's/^unixsocketperm .../unixsocketperm 777/' /etc/redis/redis.conf
sudo sed -ri 's/redis-server.sock/redis.sock/' /etc/redis/redis.conf
sudo service redis-server start
git config --global --add safe.directory '*'
export GOFLAGS=-buildvcs=false TMPDIR=/tmp
EOF
}

docker_run() {
  # First arg is docker flags string (e.g. "-t" or "-it"); rest is the command.
  local docker_flags=$1; shift
  docker run --rm $docker_flags \
    -v "$GNMI_DIR":/work/sonic-gnmi \
    -v "$MGMT_COMMON_DIR":/work/sonic-mgmt-common \
    -v "$SWSS_COMMON_DIR":/work/sonic-swss-common \
    -v "$DEBS_DIR":/sonic-debs:ro \
    ${EXTRA_DOCKER_ARGS:-} \
    -w /work/sonic-gnmi \
    "$IMAGE" "$@"
}

run_pure() {
  require_cache
  echo "=== Pure tests in container (no SONiC deps) ==="
  # pkg/exec is excluded: its tests call nsenter, which needs CAP_SYS_ADMIN +
  # a relaxed seccomp profile. The test has a skip-path for "Permission denied"
  # but the container returns "Operation not permitted" (same errno, different
  # wording), so those tests fail spuriously. Easiest fix is to skip the package.
  local pure_packages="internal/exec pkg/gnoi/debug pkg/bypass internal/diskspace internal/hash internal/download internal/firmware pkg/interceptors pkg/server/operational-handler pkg/gnoi/file pkg/gnoi/os pkg/gnoi/system"
  docker_run "-t -e HOME=/tmp -e GOCACHE=/tmp/go-cache -e GOMODCACHE=/tmp/go-modcache" bash -c "
set -euo pipefail
export TMPDIR=/tmp GOFLAGS=-buildvcs=false
git config --global --add safe.directory '*'
cd /work/sonic-gnmi
make -f pure.mk PACKAGES='$pure_packages' junit-xml
"
}

run_integration() {
  require_cache
  echo "=== Integration tests in container ==="
  docker_run "-t" bash -c "$(container_setup_snippet)
echo '--- build sonic-mgmt-common ---'
cd /work/sonic-mgmt-common && NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc
cd /work/sonic-gnmi
echo '--- make all ---'
make all
echo '--- integration tests ---'
ENABLE_TRANSLIB_WRITE=y make check_gotest_junit"
}

run_shell() {
  require_cache
  docker_run "-it" bash -c "$(container_setup_snippet); exec bash"
}

run_build() {
  require_cache
  echo "=== Building sonic-gnmi.deb in container ==="
  mkdir -p "$BUILD_OUT"
  local uid=$(id -u) gid=$(id -g)
  EXTRA_DOCKER_ARGS="-v $BUILD_OUT:/build-out" \
  docker_run "-t" bash -c "$(container_setup_snippet)
echo '--- build sonic-mgmt-common ---'
cd /work/sonic-mgmt-common && NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc
echo '--- vendor sync sonic-gnmi ---'
cd /work/sonic-gnmi && go mod tidy && go mod vendor
echo '--- dpkg-buildpackage sonic-gnmi (translib + native write enabled) ---'
ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y dpkg-buildpackage -rfakeroot -b -us -uc
cp -v /work/sonic-gnmi_*.deb /build-out/
chown -R $uid:$gid /build-out"
  echo "deb(s) in $BUILD_OUT/:"
  ls -la "$BUILD_OUT/"
}

clean() {
  echo "rm -rf $CACHE_DIR"
  rm -rf "$CACHE_DIR"
}

case "${1:-all}" in
  bootstrap)   bootstrap ;;
  pure)        run_pure ;;
  integration) run_integration ;;
  build)       run_build ;;
  shell)       run_shell ;;
  all)         bootstrap; run_pure; run_integration ;;
  clean)       clean ;;
  *) echo "usage: $0 [bootstrap|pure|integration|build|shell|all|clean]"; exit 1 ;;
esac
