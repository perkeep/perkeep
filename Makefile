all:
	go install ./pkg/... ./server/... ./cmd/... ./third_party/...

full:
	go install --tags=with_sqlite ./pkg/... ./server/... ./cmd/... ./third_party/...

presubmitlite:
	SKIP_DEP_TESTS=1 go test -short ./pkg/... ./server/camlistored ./cmd/... && echo PASS

presubmit:
	SKIP_DEP_TESTS=1 go test --tags=with_sqlite -short ./pkg/... ./server/camlistored ./cmd/... && echo PASS

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui && genfileembed ./pkg/server
