all:
	go install --tags=with_sqlite ./pkg/... ./server/... ./cmd/... ./third_party/...

presubmit:
	SKIP_DEP_TESTS=1 go test --tags=with_sqlite -short ./pkg/... ./server/camlistored ./cmd/... && echo PASS

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui && genfileembed ./pkg/server
