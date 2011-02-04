include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobref.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/httprange.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/httputil.a\

TARG=camli/blobserver/handlers
GOFILES=\
	get.go\
	enumerate.go\
	remove.go\
	upload.go\
	preupload.go\

include $(GOROOT)/src/Make.pkg
