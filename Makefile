# The normal way to build Perkeep is just "go run make.go", which
# doesn't require make. This file is mostly little convenient aliases
# and notes.

all:
	go run make.go

presubmit: fmt
	go install perkeep.org/dev/devcam
	devcam test -short

fmt:
	go fmt perkeep.org/cmd/... perkeep.org/dev/... perkeep.org/misc/... perkeep.org/pkg/... perkeep.org/server/... perkeep.org/internal/...

# TODO: merge staticcheck and staticcheckfull once the tree is clean and passes lintfull (via staticcheck.conf knobs)
staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck --checks=U1000,S1012,S1024 ./...
staticcheckfull:
	go run honnef.co/go/tools/cmd/staticcheck ./...

dockerbuild:
	docker build --tag=gcr.io/perkeep-containers/perkeep:latest .

dockerbuilddev:
	docker build --tag=gcr.io/perkeep-containers/perkeep-dev-$(USER):latest .

dockerpushdev: dockerbuilddev
	docker push gcr.io/perkeep-containers/perkeep-dev-$(USER):latest

webbuild:
	docker build -t registry.fly.io/perkeep-website -f Dockerfile.website .

web-push-prod:
	flyctl deploy -a perkeep-website

web-push-staging:
	flyctl deploy -a perkeep-staging

# iossign fetches an unsigned iOS device build (from
# clients/mobile/ios/build.sh device) from the credential-free build Mac,
# then signs it and installs it on a paired iPhone. Run it on a Mac with
# Apple credentials; see clients/mobile/README.md. Set IOS_DEVICE to a
# device name or UDID if more than one is paired.
IOS_BUILD_HOST ?= runner@10.2.201.85
IOS_SIGN_DIR ?= /tmp/perkeep-iossign
IOS_DEVICE ?=

iossign:
	rm -rf $(IOS_SIGN_DIR)
	mkdir -p $(IOS_SIGN_DIR)
	scp -r $(IOS_BUILD_HOST):perkeep/clients/mobile/ios/build/device/Perkeep.app $(IOS_SIGN_DIR)/
	go run ./dev/iossign install $(if $(IOS_DEVICE),-device "$(IOS_DEVICE)") $(IOS_SIGN_DIR)/Perkeep.app

# iosdevlog prints the paired iPhone's recent log messages about code
# signing and developer trust.
iosdevlog:
	go run ./dev/iossign devlog $(if $(IOS_DEVICE),-device "$(IOS_DEVICE)")

.PHONY: iossign iosdevlog
