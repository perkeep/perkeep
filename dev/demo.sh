#!/bin/sh

# Some hacks to make make demoing Camlistore less distracting but
# still permit using the dev-* scripts (which are normally slow and
# noisy)

#go run make.go
#go install camlistore.org/dev/devcam
#export CAMLI_QUIET=1
#export CAMLI_FAST_DEV=1

# Or just:
# (This way is buggy in that the server selection doesn't let you also
# pick an identity)
# export CAMLI_DEFAULT_SERVER=dev

# Better:
export CAMLI_CONFIG_DIR=$HOME/src/camlistore.org/config/dev-client-dir-demo
