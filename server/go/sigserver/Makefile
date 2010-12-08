include $(GOROOT)/src/Make.inc

TARG=camsigd
GOFILES=\
	camsigd.go\
	keys.go\
	sign.go\
	verify.go\

include $(GOROOT)/src/Make.cmd

test: camsigd
	prove test

