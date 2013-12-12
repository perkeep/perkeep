#!/bin/sh

# Some hacks to make make demoing Camlistore less distracting but
# still permit using the dev-* scripts (which are normally slow and
# noisy)

go run make.go
go install camlistore.org/dev/devcam
export CAMLI_QUIET=1
export CAMLI_FAST_DEV=1
