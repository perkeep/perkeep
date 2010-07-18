main:
	echo "I've using gofr to build this now: http://bitbucket.org/kylelemons/gofr/wiki/Home"
	exit 999

include $(GOROOT)/src/Make.$(GOARCH)

TARG=camlistored
GOFILES=\
	camlistored.go\
	blobref.go\

include $(GOROOT)/src/Make.cmd

