all:
	GOROOT=$(HOME)/armgo GOOS=linux GOARCH=arm CGO_ENABLED=0 $(HOME)/armgo/bin/go build -o camput.arm --ldflags="-X camlistore.org/pkg/buildinfo.GitInfo "`../../../misc/gitversion` camlistore.org/cmd/camput
	/bin/echo -n "package org.camlistore; public final class ChildProcessConfig { // " > ../gen/org/camlistore/ChildProcessConfig.java
	openssl sha1 camput.arm >> ../gen/org/camlistore/ChildProcessConfig.java
	/bin/echo "}" >> ../gen/org/camlistore/ChildProcessConfig.java
