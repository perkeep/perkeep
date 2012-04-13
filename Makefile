all:
	go install ./pkg/... ./server/... ./cmd/... ./third_party/...

presubmit:
	SKIP_DEP_TESTS=1 go test ./pkg/... ./server/camlistored ./cmd/...

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui
