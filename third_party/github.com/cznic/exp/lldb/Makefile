all:
	go fmt
	go test -i
	go test -timeout 1h
	go build
	go vet
	go install
	make todo

todo:
	@grep -n ^[[:space:]]*_[[:space:]]*=[[:space:]][[:alpha:]][[:alnum:]]* *.go || true
	@grep -n TODO *.go || true
	@grep -n BUG *.go || true
	@grep -n println *.go || true

clean:
	@go clean
	rm -f *~ cov cov.html bad-dump good-dump lldb.test old.txt new.txt \
		test-acidfiler0-* _test.db _wal

gocov:
	gocov test $(COV) | gocov-html > cov.html
