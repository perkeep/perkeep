.PHONY: all clean

all: editor
	go vet
	go install
	make todo

editor:
	go fmt
	go test -i
	go test
	go build

todo:
	@grep -n ^[[:space:]]*_[[:space:]]*=[[:space:]][[:alpha:]][[:alnum:]]* *.go *.y || true
	@grep -n TODO *.go *.y || true
	@grep -n BUG *.go *.y || true
	@grep -n println *.go *.y || true

clean:
	@go clean
	rm -f y.output
