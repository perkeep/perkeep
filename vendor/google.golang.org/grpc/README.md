#gRPC-Go experimental branch

This is an in-development experimental branch of https://github.com/grpc/grpc-go

This branch seeks to understand how much code be deleted and replaced with
Go's native HTTP/2 implementation. (Preliminary prototypes suggest most of it.)

Installation
------------

For development convenience (but not user convenience), the Go package path for this
repositor is unchanged. You can not fetch it with `go get`. You just `git clone` it to
`$GOPATH/src/google.golang.org/grpc` manually.

Prerequisites
-------------

This requires Go 1.7 or later.

Status
------
An experiment.

Bugs, discussion
----------------

Let's just use this issue tracker for now: https://github.com/bradfitz/grpc-go/issues
