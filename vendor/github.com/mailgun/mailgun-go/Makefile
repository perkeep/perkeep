.PHONY: all
.DEFAULT_GOAL := all

all:
# Only run these tests if secure credentials exist
ifeq ($(TRAVIS_SECURE_ENV_VARS),true)
	go get -t .
	go test .
endif
