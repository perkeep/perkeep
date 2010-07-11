#!/bin/sh

mkdir /tmp/camliroot
export CAMLI_PASSWORD=foo
make && ./camlistored


