include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobref.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver.a\

TARG=camli/blobserver/localdisk
GOFILES=\
	localdisk.go\
	util.go\

include $(GOROOT)/src/Make.pkg
