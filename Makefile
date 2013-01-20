all:
	go install ./pkg/... ./server/... ./cmd/... ./third_party/...

full:
	go install --tags=with_sqlite ./pkg/... ./server/... ./cmd/... ./third_party/...

# Workaround Go bug where the $GOPATH/pkg cache doesn't know about tag changes.
# Useful when you accidentally run "make" and then "make presubmit" doesn't work.
# See https://code.google.com/p/go/issues/detail?id=4443
forcefull:
	go install -a --tags=with_sqlite ./pkg/... ./server/... ./cmd/... ./third_party/...

presubmitlite:
	SKIP_DEP_TESTS=1 go test -short ./pkg/... ./server/camlistored ./cmd/... && echo PASS

presubmit:
	SKIP_DEP_TESTS=1 go test --tags=with_sqlite -short ./pkg/... ./server/camlistored ./cmd/... && echo PASS

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui && genfileembed ./pkg/server
