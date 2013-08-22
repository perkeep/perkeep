all:
	go build
	go vet
	go install
	make todo

editor:
	go fmt
	go test -i
	go test

todo:
	@grep -n ^[[:space:]]*_[[:space:]]*=[[:space:]][[:alnum:]] *.go || true
	@grep -n TODO *.go || true
	@grep -n BUG *.go || true

clean:
	rm -f *~ cov cov.html test.db

gocov:
	gocov test $(COV) | gocov-html > cov.html

bench:
	go test -run NONE -bench B
