include $(GOROOT)/src/Make.inc

TARG=camsigd
GOFILES=\
	camsigd.go\
	sign.go\

include $(GOROOT)/src/Make.cmd

test: camsigd
	prove test

