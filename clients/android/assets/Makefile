# To use this Makefile, first:
#
#   $ cd $GOROOT/src
#   $ GOOS=linux GOARCH=arm ./make.bash
#
# TODO: have make.go bootstrap that above when necessary, running "go env" to find the GOROOT and
# mirror it all into a separate writable GOROOT under $CAMROOT/tmp and bootstrap
# it with "GOOS=linux GOARCH=arm make.bash".
all:
	(cd ../../.. && go run make.go --os=linux --arch=arm --targets=camlistore.org/cmd/camput)
	cp -p ../../../bin/linux_arm/camput camput.arm
	../../../misc/gitversion > camput-version.txt
	mkdir -p ../gen/org/camlistore
	/bin/echo -n "package org.camlistore; public final class ChildProcessConfig { // " > ../gen/org/camlistore/ChildProcessConfig.java
	openssl sha1 camput.arm >> ../gen/org/camlistore/ChildProcessConfig.java
	/bin/echo "}" >> ../gen/org/camlistore/ChildProcessConfig.java
