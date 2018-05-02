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

dockerbuild:
	docker build --tag=gcr.io/perkeep-containers/perkeep:latest .
