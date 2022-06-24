#!/usr/bin/env bash

set -e

PATCH_DIR=$(dirname $(realpath ${BASH_SOURCE[0]}))

DEST_DIR=vendor
[ ! -z $1 ] && DEST_DIR=$1

if [ ! -d "${DEST_DIR}" ]; then
    echo "DEST_DIR \"${DEST_DIR}\" is not existing"
    exit 1
fi

# Copy some of the packages from go mod download directory into vendor directory.
# It is a workaround for 'go mod vendor' not copying all files

[ -z ${GO} ] && GO=go
[ -z ${GOPATH} ] && GOPATH=$(${GO} env GOPATH)
PKGPATH=$(echo ${GOPATH} | sed 's/:.*$//g')/pkg/mod

# Copy package files from GOPATH/pkg/mod to vendor
# $1 = package name, $2 = version, $3... = files
function copy() {
    for FILE in "${@:3}"; do
        rsync -r --chmod=u+w --exclude=testdata --exclude=*_test.go \
            ${PKGPATH}/$1@$2/${FILE}  ${DEST_DIR}/$1/
    done
}

set -x

copy github.com/openconfig/gnmi v0.0.0-20200617225440-d2b4e6a45802 .

