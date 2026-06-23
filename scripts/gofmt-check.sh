#!/usr/bin/env bash
# gofmt static-check gate — single source of truth for the ADO gofmt step.
# Bash (not POSIX sh) because it uses `mapfile`. Run from the repo root.
set -euo pipefail
mapfile -t files < <(find . -type f -name '*.go' \
    ! -path './vendor/*' ! -path './build/*' \
    ! -path './patches/*' ! -path './proto/*' \
    ! -path './swsscommon/*')
bad=$(gofmt -l "${files[@]}")
if [ -n "$bad" ]; then
  echo "::error::gofmt found unformatted Go file(s):"
  printf '  %s\n' $bad
  echo
  echo "----- gofmt diff (first 200 lines) -----"
  gofmt -d $bad | head -n 200
  echo "----------------------------------------"
  echo
  echo "Fix with: gofmt -w <file>, then commit the result."
  exit 1
fi
echo "All files properly formatted."
