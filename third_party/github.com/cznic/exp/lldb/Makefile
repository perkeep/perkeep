.PHONY: all editor clean cover nuke

grep=--include=*.go

all: editor
	go build
	go vet
	go install
	make todo

clean:
	go clean
	rm -f *~ cov cov.html bad-dump good-dump lldb.test old.txt new.txt \
		test-acidfiler0-* _test.db _wal

cover:
	t=$(shell tempfile) ; go test -coverprofile $$t && go tool cover -html $$t && unlink $$t

editor:
	go fmt
	go test -i
	go test -timeout 1h

nuke: clean
	go clean -i

todo:
	@grep -nr $(grep) BUG * || true
	@grep -nr $(grep) LATER * || true
	@grep -nr $(grep) MAYBE * || true
	@grep -nr $(grep) TODO * || true
	@grep -nr $(grep) FIXME * || true
	@grep -nr $(grep) ^[[:space:]]*_[[:space:]]*=[[:space:]][[:alpha:]][[:alnum:]]* * || true
	@grep -nr $(grep) println * || true
