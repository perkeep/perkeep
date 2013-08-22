all:
	go fmt
	go test -i
	go test
	go install
	go vet
	make todo

todo:
	@grep -n ^[[:space:]]*_[[:space:]]*=[[:space:]][[:alnum:]] *.go || true
	@grep -n TODO *.go || true
	@grep -n BUG *.go || true
	@grep -n println *.go || true

clean:
	rm -f *~ cov cov.html

gocov:
	gocov test $(COV) | gocov-html > cov.html

bench:
	go test -run NONE -bench B
