#!/bin/bash

set -e

Bin=$(dirname $( readlink -f $0))

LOGDIR=$Bin/../logs
mkdir -p $LOGDIR

cd $Bin
echo "Running camweb in $Bin"
../build.pl website && ./camweb --http=:8080 --root=$Bin --logdir=$LOGDIR

