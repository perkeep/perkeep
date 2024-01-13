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
