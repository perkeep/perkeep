include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/http.a
TARG=camli/client
GOFILES=\
	client.go\
	config.go\
	get.go\
	upload.go\

include $(GOROOT)/src/Make.pkg
