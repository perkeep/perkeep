include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobref.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/misc/httprange.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/httputil.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/auth.a\

TARG=camli/blobserver/handlers
GOFILES=\
	get.go\
	enumerate.go\
	remove.go\
	upload.go\
	stat.go\

include $(GOROOT)/src/Make.pkg
