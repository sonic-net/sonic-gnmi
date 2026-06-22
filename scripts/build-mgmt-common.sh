#!/bin/sh
# Build sonic-mgmt-common (generates YANG bindings + cvl schema) — single source
# of truth for both ADO templates and dev/run-tests.sh.
#
# Uses `set -e` (fail fast) without `-x`: per-command echoing is intentionally
# omitted to keep this shared script's logs uncluttered. Callers that want
# command tracing can invoke it via `sh -x` or `bash -x`.
set -e
MGMT_COMMON_DIR="${1:-sonic-mgmt-common}"
echo "--- build sonic-mgmt-common ($MGMT_COMMON_DIR) ---"
( cd "$MGMT_COMMON_DIR" && NO_TEST_BINS=1 dpkg-buildpackage -rfakeroot -b -us -uc )
