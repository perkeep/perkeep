all:
	go fmt
	go test -i
	go test
	go build
	go vet
	make todo

todo:
	@grep -n ^[[:space:]]*_[[:space:]]*=[[:space:]][[:alpha:]][[:alnum:]]* *.go || true
	@grep -n TODO *.go || true
	@grep -n BUG *.go || true
	@grep -n println *.go || true

clean:
	@go clean
	rm -f *~ cov cov.html bad-dump good-dump lldb.test old.txt new.txt \
		test-acidfiler0-* db_bench.test

gocov:
	gocov test $(COV) | gocov-html > cov.html
