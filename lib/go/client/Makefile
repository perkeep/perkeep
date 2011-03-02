include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobref.a

TARG=camli/client
GOFILES=\
	client.go\
	config.go\
	enumerate.go\
	get.go\
	upload.go\

include $(GOROOT)/src/Make.pkg
