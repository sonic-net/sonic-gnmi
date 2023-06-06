#!/usr/bin/env bash

set -e

. $(dirname ${BASH_SOURCE})/../../../sonic-mgmt-common/tools/test/env.sh \
    --dest=${TOPDIR}/build/test \
    --dbconfig-in=${TOPDIR}/testdata/database_config.json \
