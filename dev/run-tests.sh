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
#   playground   - boot a live no-TLS gNMI/gNOI server + interactive client shell
#   all          - bootstrap + pure + integration
#   clean        - wipe the cache dir
#   help         - print the usage summary
#
# Deferred CI-parity targets (gofmt/staticcheck, memleak, coverage, ci, build
# --arch) are intentionally NOT here; they belong to a future full CI-mirror
# driver. See the extension-point block next to the case dispatch at the bottom
# of this file for exactly where they plug in.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GNMI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"          # the sonic-gnmi checkout
BUILD_OUT="$SCRIPT_DIR/build-out"
CACHE_DIR="${ACR_IMAGE_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/acr-image-build}"
MGMT_COMMON_DIR="$CACHE_DIR/sonic-mgmt-common"
SWSS_COMMON_DIR="$CACHE_DIR/sonic-swss-common"
DEBS_DIR="$CACHE_DIR/sonic-debs"
IMAGE="sonicdev-microsoft.azurecr.io:443/sonic-slave-trixie:latest"

# Dependency artifact names/versions/globs and the mirror URL live in the shared
# manifest (scripts/deps-manifest.sh, G2) so a version bump touches exactly one
# line there and stays in sync with the ADO install scripts. Sourcing it sets
# ARTIFACTS_URL and provides deps_bootstrap_targets (used for DEB_TARGETS below).
. "$GNMI_DIR/scripts/deps-manifest.sh"

MODULE_PREFIX="github.com/sonic-net/sonic-gnmi"

# resolve_pkg <name> -> prints "<tier> <full-module-path>"
# Accepts a short package name (e.g. gnmi_server) or an already-qualified module
# path, maps it to its full module path, and classifies it into the integration
# tier (basic / env / dialout) used by `make check_gotest_junit`.
resolve_pkg() {
  local name="$1" short full tier
  if [[ "$name" == *"/sonic-net/"* || "$name" == "$MODULE_PREFIX"* ]]; then
    full="$name"
    short="${name#"$MODULE_PREFIX"/}"
  else
    short="$name"
    full="$MODULE_PREFIX/$name"
  fi
  case "$short" in
    sonic_db_config|sonic_service_client|telemetry|sonic_data_client)
      tier=basic ;;
    gnmi_server|pathz_authorizer|transl_utils|gnoi_client/system)
      tier=env ;;
    dialout/dialout_client|dialout/dialout_client_cli|dialout)
      tier=dialout ;;
    *)
      echo "resolve_pkg: unknown integration package '$name'" >&2
      return 1 ;;
  esac
  printf '%s %s\n' "$tier" "$full"
}

# NOTE: the public mirror only keeps the CURRENT build's artifacts, so these
# filenames go stale over time. If bootstrap 404s, find the new version in
# rules/<pkg>.mk in sonic-net/sonic-buildimage@master and bump it in
# scripts/deps-manifest.sh (the shared SSOT for dep names/versions/URL).
mapfile -t DEB_TARGETS < <(deps_bootstrap_targets)

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
SG=/work/sonic-gnmi/scripts
export PIP_FLAGS=--break-system-packages   # PEP 668 (trixie); ADO leaves this unset
export FIX_DEPS=1                           # preserves dpkg `|| apt-get install -f -y` fallback
bash "$SG/install-test-deps.sh"
bash "$SG/install-debs.sh" /sonic-debs
bash "$SG/install-yang-models.sh" '/sonic-debs/sonic_yang_models-*.whl'
bash "$SG/setup-redis.sh"
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

# Shared scaffold for the interactive subcommands (shell, playground). Runs the
# container with -it, executes the standard container setup snippet, then any
# caller-supplied pre-exec steps (e.g. build deps + boot the server), writes the
# caller's rc-file body, and exec's an interactive bash seeded from it. Callers
# set EXTRA_DOCKER_ARGS (e.g. -p) as a command prefix when extra docker flags are
# needed; docker_run picks it up exactly as before.
#   run_interactive_container <rcfile> <rc_body> [pre_exec]
run_interactive_container() {
  local rcfile="$1" rc_body="$2" pre_exec="${3:-}"
  docker_run "-it" bash -c "$(container_setup_snippet)${pre_exec:+
$pre_exec}
cat >$rcfile <<'RC'
$rc_body
RC
exec bash --rcfile $rcfile"
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

# Build the non-pure (CGO) deps inside the container: sonic-mgmt-common
# (generates the YANG bindings + cvl schema), then `make all`, which generates
# the swsscommon Go wrapper and vendors + patches the deps. Required before any
# `go test` / build of gnmi_server, sonic_data_client, dialout, telemetry, etc.
# The steps are `&&`-chained so a mgmt-common failure short-circuits before
# `make all`: this matters most in `run_shell`'s interactive `build-nonpure`
# helper, which runs without `set -e`, so without the chain a broken YANG build
# would fall through to a confusing `make all` error instead of exiting early.
build_nonpure_snippet() {
  cat <<'EOF'
bash /work/sonic-gnmi/scripts/build-deb.sh mgmt-common /work/sonic-mgmt-common \
  && echo '--- make all (swsscommon wrapper + vendor + patches) ---' \
  && ( cd /work/sonic-gnmi && make all )
EOF
}

# `make all` installs the gnmi/gnoi client + telemetry binaries into GOBIN
# (build/bin). This block puts that GOBIN on PATH so the binaries are runnable;
# it is duplicated in the playground's server-boot steps and its rc-file body.
gobin_on_path_snippet() {
  cat <<'EOF'
export GOBIN=/work/sonic-gnmi/build/bin
export PATH="$GOBIN:$PATH"
EOF
}

run_integration() {
  require_cache
  echo "=== Integration tests in container ==="
  # With no args, run the full suite exactly as before. With package args,
  # classify each into its tier and override only the matching tier variables,
  # emptying the others so their Makefile guards skip them.
  local make_overrides=""
  if [[ $# -gt 0 ]]; then
    local basic_pkgs="" env_pkgs="" dialout_pkg=""
    local arg resolved tier full
    for arg in "$@"; do
      if ! resolved="$(resolve_pkg "$arg")"; then
        exit 1
      fi
      read -r tier full <<<"$resolved"
      case "$tier" in
        basic)   basic_pkgs="${basic_pkgs:+$basic_pkgs }$full" ;;
        env)     env_pkgs="${env_pkgs:+$env_pkgs }$full" ;;
        dialout) dialout_pkg="${dialout_pkg:+$dialout_pkg }$full" ;;
      esac
    done
    echo "--- targeting subset: $* ---"
    make_overrides="INTEGRATION_BASIC_PKGS='$basic_pkgs' INTEGRATION_ENV_PKGS='$env_pkgs' INTEGRATION_DIALOUT_PKG='$dialout_pkg'"
  fi
  docker_run "-t" bash -c "$(container_setup_snippet)
$(build_nonpure_snippet)
echo '--- integration tests ---'
cd /work/sonic-gnmi && ENABLE_TRANSLIB_WRITE=y make check_gotest_junit $make_overrides"
}

run_shell() {
  require_cache
  # Drop into an interactive shell pre-wired for BOTH pure and non-pure work:
  #  - CGO_* env so a bare `go test` on CGO packages finds swss-common headers/libs
  #    (otherwise: swsscommon_wrap.cxx: fatal error: schema.h: No such file).
  #  - a `build-nonpure` helper that builds mgmt-common + the swsscommon wrapper
  #    + vendored/patched deps (run once per shell before testing gnmi_server etc).
  run_interactive_container /tmp/gnmi-shellrc "[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc
export GOFLAGS=-buildvcs=false TMPDIR=/tmp
export CGO_LDFLAGS='-lswsscommon -lhiredis'
export CGO_CXXFLAGS='-I/usr/include/swss -w -Wall -fpermissive'
export CVL_SCHEMA_PATH=/work/sonic-mgmt-common/build/cvl/schema
build-nonpure() {
$(build_nonpure_snippet)
}
cd /work/sonic-gnmi
echo
echo 'Pure packages (build instantly):'
echo '  go test ./pkg/... ./internal/...'
echo 'Non-pure packages (gnmi_server, sonic_data_client, dialout, ...):'
echo '  build-nonpure   # once per shell: mgmt-common + swsscommon wrapper + vendor/patches'
echo '  go test -mod=vendor -tags gnmi_translib_write -gcflags=all=-l ./gnmi_server/ -run TestServer -v'"
}

# Boot a live gNMI/gNOI telemetry server inside the container (no-TLS, insecure)
# and drop the developer into an interactive shell with the client binaries on
# PATH so they can hand-exercise RPCs. This is a MANUAL exploration tool, never a
# test: it must never be wired into `all`/`ci`. The --noTLS/--insecure/
# --allow_no_client_auth flags disable auth and are acceptable ONLY because the
# server runs in a throwaway --rm container bound to the developer's host; do not
# reuse these flags for the build/deploy path.
run_playground() {
  require_cache
  local port="${1:-8080}"
  echo "=== Playground: live gNMI/gNOI server on 127.0.0.1:$port (no-TLS) ==="
  # Pre-exec steps: build the non-pure deps, then boot the telemetry server and
  # wait for it to listen before dropping into the shell.
  local boot_body="$(build_nonpure_snippet)
PORT=$port
SOCK=/var/run/gnmi/gnmi.sock
# make all installed the binaries into \${GOBIN} (build/bin); put it on PATH.
$(gobin_on_path_snippet)
sudo mkdir -p /var/run/gnmi
sudo chmod 777 /var/run/gnmi
echo \"--- launching telemetry on port \$PORT + UDS \$SOCK (log: /tmp/telemetry.log) ---\"
telemetry --noTLS --insecure --allow_no_client_auth --port \"\$PORT\" --unix_socket \"\$SOCK\" --logtostderr -v=2 >/tmp/telemetry.log 2>&1 &
TELEMETRY_PID=\$!
# Bounded readiness poll: wait up to ~30s for the TCP port to accept connections.
ready=0
for i in \$(seq 1 30); do
  if ! kill -0 \"\$TELEMETRY_PID\" 2>/dev/null; then
    echo '[warn] telemetry exited early; see /tmp/telemetry.log'; break
  fi
  if (exec 3<>/dev/tcp/127.0.0.1/\$PORT) 2>/dev/null; then
    ready=1; break
  fi
  sleep 1
done
if [ \"\$ready\" = 1 ]; then
  echo \"[ok] telemetry listening on 127.0.0.1:\$PORT (pid \$TELEMETRY_PID)\"
else
  echo '[warn] telemetry not confirmed listening after 30s; recent log:'
  tail -n 20 /tmp/telemetry.log || true
  echo '[warn] dropping to shell anyway — rerun telemetry manually if needed.'
fi"
  local rc_body="[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc
$(gobin_on_path_snippet)
cd /work/sonic-gnmi
echo
echo 'Playground: telemetry server (no-TLS) on 127.0.0.1:$port + UDS /var/run/gnmi/gnmi.sock'
echo 'Client tools on PATH: telemetry gnmi_cli gnmi_dump gnoi_client gnmi_get gnmi_set'
echo
echo 'Examples:'
echo '  gnmi_dump   # no args: prints this server process GNMI/GNOI/DBUS counters'
echo \"  gnmi_cli -a 127.0.0.1:$port -insecure -logtostderr -query_type Once -q '/COUNTERS/Ethernet0' -target COUNTERS_DB\"
echo '  gnoi_client -target 127.0.0.1:$port -insecure -rpc System.Time'
echo
echo 'Server log: /tmp/telemetry.log   Exit this shell to tear everything down.'"
  # -it + publish the port so the server is also reachable from the host.
  EXTRA_DOCKER_ARGS="-p $port:$port" \
  run_interactive_container /tmp/gnmi-playground-rc "$rc_body" "$boot_body"
}

run_build() {
  require_cache
  echo "=== Building sonic-gnmi.deb in container ==="
  mkdir -p "$BUILD_OUT"
  local uid=$(id -u) gid=$(id -g)
  EXTRA_DOCKER_ARGS="-v $BUILD_OUT:/build-out" \
  docker_run "-t" bash -c "$(container_setup_snippet)
bash /work/sonic-gnmi/scripts/build-deb.sh mgmt-common /work/sonic-mgmt-common
echo '--- vendor sync sonic-gnmi ---'
cd /work/sonic-gnmi && go mod tidy && go mod vendor
bash /work/sonic-gnmi/scripts/build-deb.sh gnmi /work/sonic-gnmi /build-out sonic-gnmi_*.deb
chown -R $uid:$gid /build-out"
  echo "deb(s) in $BUILD_OUT/:"
  ls -la "$BUILD_OUT/"
}

clean() {
  echo "rm -rf $CACHE_DIR"
  rm -rf "$CACHE_DIR"
}

# usage prints the full subcommand summary. It is the single source of truth for
# help text: the `*` catch-all and the `help` subcommand both call it. Adding a
# new subcommand should touch exactly two places — the `case` dispatch below and
# this function.
usage() {
  cat <<EOF
usage: $0 [subcommand] [args]

Subcommands:
  bootstrap            clone sibling repos + fetch deb/wheel artifacts (idempotent)
  pure                 pure-package unit tests (no SONiC deps, runs in container)
  integration [pkg…]   integration tests; full suite when empty, focused subset when given
  playground [port]    boot a live no-TLS gNMI/gNOI server + interactive client shell (default 8080)
  build                dpkg-buildpackage sonic-gnmi.deb into dev/build-out/
  shell                drop into container with deps installed
  all                  bootstrap + pure + integration (default when no subcommand)
  clean                wipe the dependency cache
  help                 print this usage summary
EOF
}

# --- deferred CI-parity targets ---
# The following subcommands are intentionally NOT implemented in this dev driver;
# they belong to a future full CI-mirror driver.
# They are listed here as future stubs so a later contributor knows exactly where
# they plug in: each one is added in EXACTLY two places — the `case` dispatch
# below and the usage() function above.
#   staticcheck    - gofmt + staticcheck lint gate
#   memleak        - make check_memleak_junit
#   coverage       - diff-cover 80% coverage gate
#   ci             - run the full CI-parity sequence (lint + tests + memleak + coverage)
#   build --arch   - cross-arch (e.g. arm64) package build
# ----------------------------------------------------------------

# Only dispatch when executed directly. When sourced to reuse docker_run/
# require_cache, the functions above are defined but no subcommand runs. This is
# behavior-preserving for direct execution.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  case "${1:-all}" in
    bootstrap)   bootstrap ;;
    pure)        run_pure ;;
    integration) shift; run_integration "$@" ;;
    build)       run_build ;;
    shell)       run_shell ;;
    playground)  shift; run_playground "$@" ;;
    all)         bootstrap; run_pure; run_integration ;;
    clean)       clean ;;
    help|-h|--help) usage ;;
    *) usage >&2; exit 1 ;;
  esac
fi
