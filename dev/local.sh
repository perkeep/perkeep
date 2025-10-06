#!/bin/sh

export CAMLI_CONFENV_SECRET_RING=$(pk env srcroot)/pkg/jsonsign/testdata/test-secring.gpg
export CAMLI_CONFIG_DIR=$(pk env srcroot)/dev/config-dir-local

# Redundant, but:
export CAMLI_DEFAULT_SERVER=dev
