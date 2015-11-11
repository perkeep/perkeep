#!/bin/bash

set -e

apt-get update
apt-get install -y --no-install-recommends curl git-core ca-certificates

curl --silent https://storage.googleapis.com/golang/go1.5.1.linux-amd64.tar.gz | tar -C /usr/local -zxv
mkdir -p /gopath/src
git clone --depth=1 https://camlistore.googlesource.com/camlistore /gopath/src/camlistore.org

export GOPATH=/gopath
export GOBIN=/usr/local/bin
export GO15VENDOREXPERIMENT=1
export CGO_ENABLED=0
/usr/local/go/bin/go install -v camlistore.org/server/camlistored

rm -rf /usr/local/go
apt-get remove --yes curl git-core
apt-get clean
rm -rf /var/cache/apt/
rm -fr /var/lib/apt/lists
