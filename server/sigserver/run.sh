#!/bin/sh

export CAMLI_PASSWORD=test
make && ./sigserver "$@"
