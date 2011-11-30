#!/bin/bash

set -e

Bin=$(dirname $( readlink -f $0))

LOGDIR=$Bin/../logs
mkdir -p $LOGDIR

cd $Bin
echo "Running camweb in $Bin"
../build.pl website && ./camweb --http=:8080 --https=:4430 --root=$Bin --logdir=$LOGDIR \
    --tlscert=$HOME/etc/ssl.crt \
    --tlskey=$HOME/etc/ssl.key \
    --gerrithost=ec2-107-22-182-135.compute-1.amazonaws.com
