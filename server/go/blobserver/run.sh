#!/bin/sh

Bin=$(dirname $( readlink -f $0))

echo "BIN is: [$Bin]"

ROOT=/tmp/camliroot
if [ ! -d $ROOT ]; then
    mkdir $ROOT
fi
export CAMLI_PASSWORD=foo

$Bin/../../../build.pl server/go/blobserver && $Bin/camlistored -root=$ROOT -listen=:3179 "$@"
