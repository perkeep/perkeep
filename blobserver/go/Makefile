main:
	echo "I've using gofr to build this now: http://bitbucket.org/kylelemons/gofr/wiki/Home"
	exit 999

include $(GOROOT)/src/Make.$(GOARCH)

TARG=camlistored
GOFILES=\
	camlistored.go\
	auth.go\
	blobref.go\
	enumerate.go\
	get.go\
	http_util.go\
	preupload.go\
	temp_testing.go\
	upload.go\

include $(GOROOT)/src/Make.cmd

