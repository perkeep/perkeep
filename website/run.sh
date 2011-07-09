#!/bin/bash

set -e

Bin=$(dirname $( readlink -f $0))
Port=8081

LOGDIR=$Bin/../logs
mkdir -p $LOGDIR

cd $Bin
echo "Running camweb in $Bin on port $Port"
../build.pl website && ./camweb --http=:$Port --root=$Bin --logdir=$LOGDIR