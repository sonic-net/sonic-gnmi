#!/usr/bin/env bash
# setup.sh — one-shot, idempotent bootstrap for this sonic-gnmi dev environment.
#
# Lives at sonic-gnmi/dev/setup.sh. The sonic-gnmi checkout is the PARENT of
# this dev/ directory. Run it from anywhere:
#
#   ./dev/setup.sh            # verify + bootstrap cache + run pure tests
#   ./dev/setup.sh --no-test  # verify + bootstrap only (skip pure tests)
#
# It is safe to re-run; every step is a no-op once satisfied. Heavy deps run
# inside the sonic-slave-trixie container via run-tests.sh, so the checkout
# stays clean. This script also registers dev/ in the repo's local
# .git/info/exclude so it never shows up in `git status` and is never removed
# by `git clean -fd`.

set -euo pipefail

RUN_TESTS=1
[[ "${1:-}" == "--no-test" ]] && RUN_TESTS=0

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GNMI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"      # the sonic-gnmi checkout
DRIVER="$SCRIPT_DIR/run-tests.sh"

say()  { printf '\033[1;34m[setup]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[setup] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# --- Step 0: prerequisites -------------------------------------------------
say "checking prerequisites"
command -v docker >/dev/null 2>&1 || fail "docker not found on PATH"
docker info >/dev/null 2>&1 || fail "docker does not work without sudo (daemon down, or you are not in the docker group)"
[[ -d "$GNMI_DIR/.git" ]] || fail "$GNMI_DIR is not a git checkout (expected the sonic-gnmi repo)"
[[ -f "$DRIVER" ]] || fail "missing driver $DRIVER"

# --- Step 1: normalise scripts + protect our files from git -----------------
# Strip any CR line endings (copying through a Windows/host editor introduces
# them; `/usr/bin/env: 'bash\r'` then fails).
for f in "$DRIVER" "$SCRIPT_DIR/setup.sh"; do
  if grep -q $'\r' "$f" 2>/dev/null; then
    say "normalising CRLF -> LF in $(basename "$f")"
    sed -i 's/\r$//' "$f"
  fi
done
chmod +x "$DRIVER" "$SCRIPT_DIR/setup.sh"

# Keep dev/ out of the checkout's git view without touching the tracked
# .gitignore. .git/info/exclude is local-only and also shields it (and
# build-out/, SETUP.md, all of which live under dev/) from `git clean -fd`.
EXCLUDE="$GNMI_DIR/.git/info/exclude"
mkdir -p "$(dirname "$EXCLUDE")"
for pat in '/dev/'; do
  grep -qxF "$pat" "$EXCLUDE" 2>/dev/null || echo "$pat" >> "$EXCLUDE"
done

# The sonic-gnmi tests like to leave go.mod/go.sum and testdata/ dirty. Warn,
# don't auto-discard — you decide whether it's leftover output or real work.
if [[ -n "$(git -C "$GNMI_DIR" status --porcelain)" ]]; then
  say "WARNING: checkout has other local changes. If it is leftover test output, reset with:"
  say "    git -C \"$GNMI_DIR\" checkout -- . && git -C \"$GNMI_DIR\" clean -fd"
fi

# --- Step 2: bootstrap the shared cache ------------------------------------
say "bootstrapping dependency cache (clones siblings + downloads SONiC debs)"
"$DRIVER" bootstrap

# --- Step 3: verify pure tests ---------------------------------------------
if [[ "$RUN_TESTS" == "1" ]]; then
  say "running pure unit tests (~40s)"
  "$DRIVER" pure
  say "DONE — environment verified. Entry points:"
else
  say "DONE — cache ready (skipped pure tests). Entry points:"
fi

cat <<'EOF'

  ./dev/run-tests.sh pure          # pure unit tests, ~40s
  ./dev/run-tests.sh integration   # full integration tests, ~20 min
  ./dev/run-tests.sh build         # produce sonic-gnmi_*.deb in dev/build-out/
  ./dev/run-tests.sh shell         # bash inside the container with all deps
  ./dev/run-tests.sh clean         # wipe the dependency cache (drastic)

  See dev/SETUP.md for the full step-by-step guide and troubleshooting.

EOF
